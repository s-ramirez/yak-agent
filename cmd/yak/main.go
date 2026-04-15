package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"yak-go/internal/channel"
	clichannel "yak-go/internal/channel/cli"
	discordchannel "yak-go/internal/channel/discord"
	imessagechannel "yak-go/internal/channel/imessage"
	"yak-go/internal/channel/sched"
	"yak-go/internal/cli"
	"yak-go/internal/compaction"
	"yak-go/internal/llm"
	"yak-go/internal/memory"
	"yak-go/internal/plugin"
	"yak-go/internal/plugin/webui"
	"yak-go/internal/prompt"
	"yak-go/internal/schedule"
	"yak-go/internal/skills"
	"yak-go/internal/subagents"
	"yak-go/internal/tools"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "init" {
		if err := runInit(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "init failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

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

	memoryStore := memory.NewStore(filepath.Join(cwd, ".yak", "memory"))

	scheduleStore, err := schedule.NewStore(filepath.Join(cwd, ".yak", "schedule"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: loading schedule store: %v\n", err)
	}
	var scheduler *schedule.Scheduler
	if scheduleStore != nil {
		scheduler = schedule.NewScheduler(scheduleStore, 16)
		if interval := os.Getenv("YAK_HEARTBEAT_INTERVAL"); interval != "" {
			d, err := time.ParseDuration(interval)
			if err != nil || d <= 0 {
				fmt.Fprintf(os.Stderr, "warning: invalid YAK_HEARTBEAT_INTERVAL %q, heartbeat disabled\n", interval)
			} else {
				now := time.Now()
				scheduler.Inject(schedule.Job{
					Name:    "heartbeat",
					Enabled: true,
					Schedule: schedule.Schedule{
						Kind:   schedule.KindEvery,
						Every:  schedule.Duration(d),
						Anchor: &now,
					},
					Text: "Heartbeat tick: no user input. Continue any pending work, or wait for input if there is nothing to do.",
				})
			}
		}
	}

	// Parse iMessage config early so the send tool can be included in builtinTools.
	var imsgCfg *imessagechannel.Config
	if !envEnabled("YAK_IMESSAGE_ENABLED") {
		fmt.Fprintln(os.Stderr, "iMessage channel disabled via YAK_IMESSAGE_ENABLED")
	} else if imsgURL := os.Getenv("YAK_IMESSAGE_SERVER_URL"); imsgURL != "" {
		imsgPassword := os.Getenv("YAK_IMESSAGE_PASSWORD")
		if imsgPassword == "" {
			fmt.Fprintf(os.Stderr, "warning: YAK_IMESSAGE_SERVER_URL set but YAK_IMESSAGE_PASSWORD is empty; iMessage channel disabled\n")
		} else {
			imsgPort := 8421
			if p := os.Getenv("YAK_IMESSAGE_WEBHOOK_PORT"); p != "" {
				if n, err := strconv.Atoi(p); err == nil && n > 0 {
					imsgPort = n
				} else {
					fmt.Fprintf(os.Stderr, "warning: invalid YAK_IMESSAGE_WEBHOOK_PORT %q, using %d\n", p, imsgPort)
				}
			}
			var imsgOwners []string
			if raw := os.Getenv("YAK_IMESSAGE_OWNER_HANDLES"); raw != "" {
				for _, h := range strings.Split(raw, ",") {
					if h = strings.TrimSpace(h); h != "" {
						imsgOwners = append(imsgOwners, h)
					}
				}
			}
			cfg := imessagechannel.Config{
				ServerURL:    imsgURL,
				Password:     imsgPassword,
				WebhookPath:  os.Getenv("YAK_IMESSAGE_WEBHOOK_PATH"),
				WebhookPort:  imsgPort,
				OwnerHandles: imsgOwners,
				GroupTag:     os.Getenv("YAK_IMESSAGE_GROUP_TAG"),
			}
			imsgCfg = &cfg
		}
	}

	// Parse Discord config early so the send tool can be included in builtinTools.
	var discordCfg *discordchannel.Config
	if !envEnabled("YAK_DISCORD_ENABLED") {
		fmt.Fprintln(os.Stderr, "Discord channel disabled via YAK_DISCORD_ENABLED")
	} else if token := os.Getenv("YAK_DISCORD_TOKEN"); token != "" {
		var owners []string
		if raw := os.Getenv("YAK_DISCORD_OWNER_IDS"); raw != "" {
			for _, id := range strings.Split(raw, ",") {
				if id = strings.TrimSpace(id); id != "" {
					owners = append(owners, id)
				}
			}
		}
		cfg := discordchannel.Config{
			Token:    token,
			OwnerIDs: owners,
			GuildTag: os.Getenv("YAK_DISCORD_GUILD_TAG"),
		}
		discordCfg = &cfg
	}

	builtinTools := []tools.Tool{
		tools.NewReadTool(tools.OSFS{}),
		tools.NewWriteTool(tools.OSFS{}),
		tools.NewEditTool(tools.OSFS{}),
		tools.NewBashTool(),
		tools.NewGrepTool(searchDelegationGuidelines...),
		tools.NewLsTool(tools.OSFS{}),
		tools.NewFindTool(searchDelegationGuidelines...),
		tools.NewWebFetchTool(),
		tools.NewWebSearchTool(),
		tools.NewMemoryReadTool(memoryStore),
		tools.NewMemoryWriteTool(memoryStore),
		tools.NewMemorySearchTool(memoryStore),
		tools.NewMemoryListTool(memoryStore),
	}
	if scheduleStore != nil {
		builtinTools = append(builtinTools, tools.NewScheduleTool(scheduleStore))
	}
	if imsgCfg != nil {
		builtinTools = append(builtinTools, tools.NewIMessageSendTool(tools.IMessageSendConfig{
			ServerURL: imsgCfg.ServerURL,
			Password:  imsgCfg.Password,
		}))
	}
	if discordCfg != nil {
		builtinTools = append(builtinTools, tools.NewDiscordSendTool(tools.DiscordSendConfig{
			Token: discordCfg.Token,
		}))
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

	// Load IDENTITY.md and USER.md from agentDirs; last found wins (project overrides home).
	contextFiles := loadContextFiles(agentDirs, "IDENTITY.md", "USER.md")

	var userActivity bool
	runner := cli.Runner{
		Client:          chatClient,
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
		ContextFiles:    contextFiles,
		MemoryStore:     memoryStore,
		Scheduler:       scheduler,
		Compaction:      compaction.DefaultSettings,
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

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	if scheduler != nil {
		scheduler.Start(runCtx)
		defer scheduler.Stop()
	}

	channels := channel.NewRegistry()
	channels.Register(clichannel.NewStdio(os.Stdin, os.Stdout))
	if scheduler != nil {
		channels.Register(&sched.Channel{
			Scheduler: scheduler,
			Target:    channel.Key{Channel: clichannel.Name, Thread: clichannel.DefaultThread},
		})
	}

	// iMessage via BlueBubbles — register channel if config was parsed successfully.
	if imsgCfg != nil {
		channels.Register(imessagechannel.New(*imsgCfg))
		webhookPath := imsgCfg.WebhookPath
		if webhookPath == "" {
			webhookPath = "/bluebubbles"
		}
		fmt.Fprintf(os.Stderr, "iMessage channel enabled (webhook :%d%s)\n",
			imsgCfg.WebhookPort, webhookPath)
	}

	// Discord — register channel if a token was provided.
	if discordCfg != nil {
		channels.Register(discordchannel.New(*discordCfg))
		fmt.Fprintf(os.Stderr, "Discord channel enabled (owners=%d, tag=%q)\n",
			len(discordCfg.OwnerIDs), discordCfg.GuildTag)
	}

	dispatcher := &channel.Dispatcher{
		Channels:    channels,
		Store:       channel.NewStore(),
		Commands:    &channel.CommandExpander{Skills: loadedSkills},
		Handler:     &runner,
		OnUserInput: func() { userActivity = true },
		Logger: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "[dispatcher] "+format+"\n", args...)
		},
	}

	runErr := dispatcher.Run(runCtx)
	if userActivity {
		if err := runner.DistillMemory(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "warning: memory distill failed: %v\n", err)
		}
	}
	if runErr != nil && !errors.Is(runErr, io.EOF) && !errors.Is(runErr, context.Canceled) {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", runErr)
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

// envEnabled returns true unless the env var is explicitly set to a falsy
// value (0/false/no/off, case-insensitive). Empty or unset = enabled.
func envEnabled(name string) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return true
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return true
	}
	return b
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

// loadContextFiles reads the named files from each dir in order; last found
// wins (project-level overrides home-level). Files that don't exist are skipped.
func loadContextFiles(dirs []string, names ...string) []prompt.ContextFile {
	latest := make(map[string]prompt.ContextFile, len(names))
	for _, dir := range dirs {
		for _, name := range names {
			path := filepath.Join(dir, name)
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			latest[name] = prompt.ContextFile{Path: path, Content: string(data)}
		}
	}
	result := make([]prompt.ContextFile, 0, len(names))
	for _, name := range names {
		if cf, ok := latest[name]; ok {
			result = append(result, cf)
		}
	}
	return result
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
