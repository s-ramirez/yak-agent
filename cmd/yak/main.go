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
	"strings"
	"time"

	"strconv"

	"github.com/joho/godotenv"

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
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
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
		port, _ := strconv.Atoi(portStr)
		if port == 0 {
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
	if agentCfg != nil {
		allowed := make(map[string]struct{}, len(agentCfg.Tools))
		for _, name := range agentCfg.Tools {
			allowed[name] = struct{}{}
		}
		filtered := builtinTools[:0]
		for _, t := range builtinTools {
			if _, ok := allowed[t.Definition().Name]; ok {
				filtered = append(filtered, t)
			}
		}
		builtinTools = filtered
	}
	allTools := append([]tools.Tool(nil), builtinTools...)
	var baseHooks []tools.ToolHook
	baseHooks = append(baseHooks, &logHook{writer: os.Stderr})
	var runtimePlugins []subagents.RuntimePlugin

	var allowedPlugins map[string]struct{}
	if agentCfg != nil && len(agentCfg.Plugins) > 0 {
		allowedPlugins = make(map[string]struct{}, len(agentCfg.Plugins))
		for _, name := range agentCfg.Plugins {
			allowedPlugins[name] = struct{}{}
		}
	}
	var allowedTools map[string]struct{}
	if agentCfg != nil {
		allowedTools = make(map[string]struct{}, len(agentCfg.Tools))
		for _, name := range agentCfg.Tools {
			allowedTools[name] = struct{}{}
		}
	}

	// Collect after-turn hooks and prompt sections from plugins.
	var afterTurnHooks []plugin.AfterTurnHook
	var agentStartHooks []plugin.AgentStartHook
	var agentEndHooks []plugin.AgentEndHook
	var pluginPrompts []string
	for _, p := range plugins {
		if allowedPlugins != nil {
			if _, ok := allowedPlugins[p.Name()]; !ok {
				continue
			}
		}
		pluginTools := p.Tools()
		if allowedTools != nil {
			filtered := pluginTools[:0]
			for _, t := range pluginTools {
				if _, ok := allowedTools[t.Definition().Name]; ok {
					filtered = append(filtered, t)
				}
			}
			pluginTools = filtered
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
	if agentCfg != nil {
		agentPrompt = agentCfg.Prompt
	}

	runner := cli.Runner{
		Client:          chatClient,
		IO:              &stdio{reader: bufio.NewReader(os.Stdin), writer: os.Stdout},
		Registry:        registry,
		Skills:          loadedSkills,
		AfterTurnHooks:  afterTurnHooks,
		AgentStartHooks: agentStartHooks,
		AgentEndHooks:   agentEndHooks,
		PluginPrompts:   pluginPrompts,
		AgentID:         "main",
		AgentName:       "orchestrator",
		Prompt:          agentPrompt,
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
