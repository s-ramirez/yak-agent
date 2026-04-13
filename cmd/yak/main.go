package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"yak-go/internal/cli"
	"yak-go/internal/llm"
	"yak-go/internal/plugin"
	"yak-go/internal/plugin/webui"
	"yak-go/internal/skills"
	"yak-go/internal/subagents"
	"yak-go/internal/tools"
)

type stdio struct {
	reader *bufio.Reader
	writer io.Writer
}

func (s *stdio) Write(text string) error {
	_, err := io.WriteString(s.writer, text)
	return err
}

func (s *stdio) ReadLine(ctx context.Context) (string, error) {
	_ = ctx

	line, err := s.reader.ReadString('\n')
	if err != nil {
		if err == io.EOF && len(line) > 0 {
			return strings.TrimRight(line, "\r\n"), nil
		}
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func main() {
	if err := loadDotenv(".env"); err != nil {
		fmt.Fprintf(os.Stderr, "warning: loading .env: %v\n", err)
	}

	baseURL := os.Getenv("YAK_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:1234"
	}

	model := os.Getenv("YAK_MODEL")
	if model == "" {
		model = "default"
	}

	apiKey := os.Getenv("YAK_API_KEY")

	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	agentDirs := []string{
		filepath.Join(home, ".yak"),
		filepath.Join(cwd, ".yak"),
	}
	agentCfg, err := subagents.LoadAgentConfig(agentDirs...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: loading agent config: %v\n", err)
		os.Exit(1)
	}
	if agentCfg != nil {
		baseURL = agentCfg.BaseURL
		if baseURL == "" {
			baseURL = os.Getenv("YAK_BASE_URL")
			if baseURL == "" {
				baseURL = "http://localhost:1234"
			}
		}
		model = agentCfg.Model
		if agentCfg.APIKeyEnv != "" {
			apiKey = os.Getenv(agentCfg.APIKeyEnv)
		}
	}

	client := llm.NewClient(baseURL, model, &llm.Options{
		Timeout: 300 * time.Second,
		APIKey:  apiKey,
	})

	var chatClient llm.ChatClient = client
	var sessionDir string
	logDir := os.Getenv("YAK_LOG_DIR")
	if logDir != "" {
		sessionDir = filepath.Join(logDir, time.Now().Format("20060102-150405"))
		lc, err := llm.NewLoggingClient(client, sessionDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to set up logging: %v\n", err)
		} else {
			chatClient = lc
			fmt.Fprintf(os.Stderr, "Logging to %s\n", sessionDir)
		}
	}

	// Initialize plugins.
	var plugins []plugin.Plugin
	if portStr := os.Getenv("YAK_WEBUI_PORT"); portStr != "" {
		port, err := strconv.Atoi(portStr)
		if err != nil || port <= 0 {
			fmt.Fprintf(os.Stderr, "warning: invalid YAK_WEBUI_PORT %q, using 8420\n", portStr)
			port = 8420
		}
		plugins = append(plugins, webui.New(port))
	}

	api := plugin.API{
		Log: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "[plugin] "+format+"\n", args...)
		},
	}
	for _, p := range plugins {
		p.Init(api)
	}

	subagentDirs := []string{
		filepath.Join(home, ".yak", "subagents"),
		filepath.Join(cwd, ".yak", "subagents"),
	}
	defs, subagentDiags, err := subagents.LoadDefinitions(subagentDirs...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: loading subagents: %v\n", err)
	}
	for _, d := range subagentDiags {
		fmt.Fprintf(os.Stderr, "warning: %s\n", d)
	}
	searchDelegationGuidelines := subagents.SearchDelegationGuidelines(defs)

	builtinTools := []tools.Tool{
		tools.NewReadTool(tools.OSFS{}),
		tools.NewWriteTool(tools.OSFS{}),
		tools.NewEditTool(tools.OSFS{}),
		tools.NewBashTool(),
		tools.NewGrepTool(searchDelegationGuidelines...),
		tools.NewLsTool(tools.OSFS{}),
		tools.NewFindTool(searchDelegationGuidelines...),
	}
	var allowedTools, allowedPlugins map[string]struct{}
	if agentCfg != nil {
		allowedTools = nameSet(agentCfg.Tools)
		if len(agentCfg.Plugins) > 0 {
			allowedPlugins = nameSet(agentCfg.Plugins)
		}
		builtinTools = filterTools(builtinTools, allowedTools)
	}
	allTools := append([]tools.Tool(nil), builtinTools...)
	var baseHooks []tools.ToolHook
	baseHooks = append(baseHooks, &logHook{writer: os.Stderr})
	var runtimePlugins []subagents.RuntimePlugin

	// Collect after-turn hooks and prompt sections from plugins.
	var afterTurnHooks []plugin.AfterTurnHook
	var agentStartHooks []plugin.AgentStartHook
	var agentEndHooks []plugin.AgentEndHook
	var usageHooks []plugin.UsageHook
	var pluginPrompts []string
	for _, p := range plugins {
		if allowedPlugins != nil {
			if _, ok := allowedPlugins[p.Name()]; !ok {
				continue
			}
		}
		pluginTools := p.Tools()
		if allowedTools != nil {
			pluginTools = filterTools(pluginTools, allowedTools)
		}
		rp := subagents.RuntimePlugin{
			Name:         p.Name(),
			Tools:        pluginTools,
			Hooks:        p.Hooks(),
			SystemPrompt: p.SystemPromptSection(),
		}
		allTools = append(allTools, rp.Tools...)
		runtimePlugins = append(runtimePlugins, rp)
		for _, h := range p.Hooks() {
			baseHooks = append(baseHooks, h)
		}
		if ath, ok := p.(plugin.AfterTurnHook); ok {
			afterTurnHooks = append(afterTurnHooks, ath)
			runtimePlugins[len(runtimePlugins)-1].AfterTurnHook = ath
		}
		if ash, ok := p.(plugin.AgentStartHook); ok {
			agentStartHooks = append(agentStartHooks, ash)
			runtimePlugins[len(runtimePlugins)-1].AgentStartHook = ash
		}
		if aeh, ok := p.(plugin.AgentEndHook); ok {
			agentEndHooks = append(agentEndHooks, aeh)
			runtimePlugins[len(runtimePlugins)-1].AgentEndHook = aeh
		}
		if uh, ok := p.(plugin.UsageHook); ok {
			usageHooks = append(usageHooks, uh)
			runtimePlugins[len(runtimePlugins)-1].UsageHook = uh
		}
		if s := p.SystemPromptSection(); s != "" {
			pluginPrompts = append(pluginPrompts, s)
		}
	}

	registry := tools.NewRegistry(allTools...)
	for _, hook := range baseHooks {
		registry.AddHook(hook)
	}

	skillDirs := []string{
		filepath.Join(home, ".yak", "skills"),
		filepath.Join(cwd, ".yak", "skills"),
	}
	loadedSkills, diags, err := skills.LoadSkills(skillDirs...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: loading skills: %v\n", err)
	}
	for _, d := range diags {
		fmt.Fprintf(os.Stderr, "warning: %s\n", d)
	}

	if section := subagents.BuildPromptSection(defs); section != "" {
		pluginPrompts = append(pluginPrompts, section)
	}

	var agentPrompt string
	var contextSize int
	if agentCfg != nil {
		agentPrompt = agentCfg.Prompt
		contextSize = agentCfg.ContextSize
	}

	runner := cli.Runner{
		Client:          chatClient,
		IO:              &stdio{reader: bufio.NewReader(os.Stdin), writer: os.Stdout},
		Registry:        registry,
		Skills:          loadedSkills,
		AfterTurnHooks:  afterTurnHooks,
		AgentStartHooks: agentStartHooks,
		AgentEndHooks:   agentEndHooks,
		UsageHooks:      usageHooks,
		PluginPrompts:   pluginPrompts,
		AgentID:         "main",
		AgentName:       "orchestrator",
		Prompt:          agentPrompt,
		ContextSize:     contextSize,
	}

	subagentManager, err := subagents.NewManager(
		func(def subagents.Definition) (llm.ChatClient, error) {
			u := def.BaseURL
			if u == "" {
				u = baseURL
			}
			key := apiKey
			if def.APIKeyEnv != "" {
				key = os.Getenv(def.APIKeyEnv)
			}
			return llm.NewClient(u, def.Model, &llm.Options{
				Timeout: 300 * time.Second,
				APIKey:  key,
			}), nil
		},
		sessionDir,
		defs,
		builtinTools,
		baseHooks,
		runtimePlugins,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: subagents disabled: %v\n", err)
	} else {
		subagentManager.SetBaseObservers(agentStartHooks, agentEndHooks, usageHooks)
		registry = tools.NewRegistry(append(allTools,
			subagents.NewSpawnTool(subagentManager),
			subagents.NewControlTool(subagentManager),
		)...)
		for _, hook := range baseHooks {
			registry.AddHook(hook)
		}
		runner.Registry = registry
	}

	if err := runner.Run(context.Background()); err != nil && err != io.EOF {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

type logHook struct {
	writer io.Writer
}

func (h *logHook) BeforeToolCall(_ tools.HookContext, name string, params json.RawMessage) string {
	fmt.Fprintf(h.writer, "%s [STARTED]\n", formatToolCall(name, params))
	return ""
}

func (h *logHook) AfterToolCall(_ tools.HookContext, name string, params json.RawMessage, result tools.ToolResult, err error) {
	status := "DONE"
	if err != nil || result.IsError {
		status = "ERROR"
	}
	fmt.Fprintf(h.writer, "%s [%s]\n", formatToolCall(name, params), status)
}

func formatToolCall(name string, params json.RawMessage) string {
	return fmt.Sprintf("%s(%s)", name, formatParams(params))
}

func nameSet(names []string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

func filterTools(in []tools.Tool, allowed map[string]struct{}) []tools.Tool {
	filtered := in[:0]
	for _, t := range in {
		if _, ok := allowed[t.Definition().Name]; ok {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

func loadDotenv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(line[len("export "):])
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if n := len(val); n >= 2 {
			if (val[0] == '"' && val[n-1] == '"') || (val[0] == '\'' && val[n-1] == '\'') {
				val = val[1 : n-1]
			}
		}
		if _, ok := os.LookupEnv(key); ok {
			continue
		}
		os.Setenv(key, val)
	}
	return scanner.Err()
}

func formatParams(params json.RawMessage) string {
	trimmed := strings.TrimSpace(string(params))
	if trimmed == "" || trimmed == "null" {
		return ""
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, params); err != nil {
		if len(trimmed) > 160 {
			return trimmed[:160] + "..."
		}
		return trimmed
	}

	formatted := compact.String()
	if len(formatted) > 160 {
		return formatted[:160] + "..."
	}
	return formatted
}
