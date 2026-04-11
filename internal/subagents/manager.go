package subagents

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"yak-go/internal/cli"
	"yak-go/internal/llm"
	"yak-go/internal/plugin"
	"yak-go/internal/tools"
	"yak-go/internal/types"
)

type ClientFactory func(def Definition) (llm.ChatClient, error)

type SpawnRequest struct {
	Agent     string
	Task      string
	Label     string
	Wait      bool
	TimeoutMS int
}

type SpawnResult struct {
	RunID   string
	Status  string
	Message string
	Result  string
}

type RunSnapshot struct {
	RunID       string
	Agent       string
	Label       string
	Task        string
	Status      string
	Result      string
	Error       string
	CreatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
}

type childRun struct {
	snapshot RunSnapshot
	done     chan struct{}
	cancel   context.CancelFunc
}

type Manager struct {
	clientFactory ClientFactory
	logDir        string
	builtinTools  []tools.Tool
	baseHooks     []tools.ToolHook
	plugins       map[string]RuntimePlugin
	definitions   map[string]Definition
	runs          map[string]*childRun
	nextID        uint64
	mu            sync.RWMutex
}

func NewManager(clientFactory ClientFactory, logDir string, defs []Definition, builtinTools []tools.Tool, baseHooks []tools.ToolHook, plugins []RuntimePlugin) (*Manager, error) {
	if clientFactory == nil {
		return nil, fmt.Errorf("client factory is required")
	}

	definitions := make(map[string]Definition, len(defs))
	for _, def := range defs {
		if _, ok := definitions[def.Name]; ok {
			return nil, fmt.Errorf("duplicate subagent %q", def.Name)
		}
		definitions[def.Name] = def
	}

	pluginMap := make(map[string]RuntimePlugin, len(plugins))
	for _, plugin := range plugins {
		pluginMap[plugin.Name] = plugin
	}

	manager := &Manager{
		clientFactory: clientFactory,
		logDir:        strings.TrimSpace(logDir),
		builtinTools:  append([]tools.Tool(nil), builtinTools...),
		baseHooks:     append([]tools.ToolHook(nil), baseHooks...),
		plugins:       pluginMap,
		definitions:   definitions,
		runs:          make(map[string]*childRun),
	}

	for _, def := range defs {
		_, _, _, _, _, err := manager.buildRuntime(def)
		if err != nil {
			return nil, fmt.Errorf("subagent %q: %w", def.Name, err)
		}
	}

	return manager, nil
}

func (m *Manager) Definitions() []Definition {
	names := make([]string, 0, len(m.definitions))
	for name := range m.definitions {
		names = append(names, name)
	}
	slices.Sort(names)

	out := make([]Definition, 0, len(names))
	for _, name := range names {
		out = append(out, m.definitions[name])
	}
	return out
}

func (m *Manager) Spawn(ctx context.Context, req SpawnRequest) (SpawnResult, error) {
	agent := strings.TrimSpace(req.Agent)
	if agent == "" {
		return SpawnResult{}, fmt.Errorf("agent is required")
	}
	task := strings.TrimSpace(req.Task)
	if task == "" {
		return SpawnResult{}, fmt.Errorf("task is required")
	}

	def, ok := m.definitions[agent]
	if !ok {
		return SpawnResult{}, fmt.Errorf("unknown subagent %q", agent)
	}

	run := m.newRun(def.Name, task, req.Label)
	go m.execute(run, req, def)

	if req.Wait {
		waitCtx := ctx
		cancel := func() {}
		if req.TimeoutMS > 0 {
			waitCtx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMS)*time.Millisecond)
		}
		defer cancel()

		snapshot, err := m.wait(waitCtx, run.snapshot.RunID)
		if err != nil {
			return SpawnResult{}, err
		}

		message := "subagent finished"
		if snapshot.Status == "error" {
			message = "subagent failed"
		}
		return SpawnResult{
			RunID:   snapshot.RunID,
			Status:  snapshot.Status,
			Message: message,
			Result:  coalesceResult(snapshot),
		}, nil
	}

	return SpawnResult{
		RunID:   run.snapshot.RunID,
		Status:  run.snapshot.Status,
		Message: "subagent started",
	}, nil
}

func (m *Manager) List() []RunSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]RunSnapshot, 0, len(m.runs))
	for _, run := range m.runs {
		out = append(out, run.snapshot)
	}
	return out
}

func (m *Manager) Wait(ctx context.Context, runID string) (RunSnapshot, error) {
	return m.wait(ctx, runID)
}

func (m *Manager) Kill(runID string) (RunSnapshot, error) {
	m.mu.Lock()
	run, ok := m.runs[runID]
	if !ok {
		m.mu.Unlock()
		return RunSnapshot{}, fmt.Errorf("unknown subagent %q", runID)
	}
	if run.snapshot.Status != "running" {
		snapshot := run.snapshot
		m.mu.Unlock()
		return snapshot, nil
	}

	run.snapshot.Status = "canceled"
	now := time.Now()
	run.snapshot.CompletedAt = &now
	cancel := run.cancel
	snapshot := run.snapshot
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return snapshot, nil
}

func (m *Manager) buildRuntime(def Definition) (*tools.Registry, []string, []plugin.AfterTurnHook, []plugin.AgentStartHook, []plugin.AgentEndHook, error) {
	allowedPlugins := make(map[string]RuntimePlugin, len(def.Plugins))
	for _, name := range def.Plugins {
		plugin, ok := m.plugins[name]
		if !ok {
			return nil, nil, nil, nil, nil, fmt.Errorf("unknown plugin %q", name)
		}
		allowedPlugins[name] = plugin
	}

	allTools := make(map[string]tools.Tool)
	for _, tool := range m.builtinTools {
		allTools[tool.Definition().Name] = tool
	}
	for _, name := range def.Plugins {
		for _, tool := range allowedPlugins[name].Tools {
			allTools[tool.Definition().Name] = tool
		}
	}

	selectedTools := make([]tools.Tool, 0, len(def.Tools))
	for _, name := range def.Tools {
		tool, ok := allTools[name]
		if !ok {
			return nil, nil, nil, nil, nil, fmt.Errorf("unknown tool %q", name)
		}
		selectedTools = append(selectedTools, tool)
	}

	registry := tools.NewRegistry(selectedTools...)
	for _, hook := range m.baseHooks {
		registry.AddHook(hook)
	}

	var prompts []string
	var afterTurnHooks []plugin.AfterTurnHook
	var agentStartHooks []plugin.AgentStartHook
	var agentEndHooks []plugin.AgentEndHook
	for _, name := range def.Plugins {
		plugin := allowedPlugins[name]
		for _, hook := range plugin.Hooks {
			registry.AddHook(hook)
		}
		if plugin.SystemPrompt != "" {
			prompts = append(prompts, plugin.SystemPrompt)
		}
		if plugin.AfterTurnHook != nil {
			afterTurnHooks = append(afterTurnHooks, plugin.AfterTurnHook)
		}
		if plugin.AgentStartHook != nil {
			agentStartHooks = append(agentStartHooks, plugin.AgentStartHook)
		}
		if plugin.AgentEndHook != nil {
			agentEndHooks = append(agentEndHooks, plugin.AgentEndHook)
		}
	}

	return registry, prompts, afterTurnHooks, agentStartHooks, agentEndHooks, nil
}

func (m *Manager) execute(run *childRun, req SpawnRequest, def Definition) {
	defer close(run.done)

	ctx := context.Background()
	if req.TimeoutMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMS)*time.Millisecond)
		run.cancel = cancel
		defer cancel()
	} else {
		ctx, run.cancel = context.WithCancel(context.Background())
		defer run.cancel()
	}

	startedAt := time.Now()
	m.update(run.snapshot.RunID, func(snapshot *RunSnapshot) {
		snapshot.StartedAt = &startedAt
	})

	client, err := m.clientFactory(def)
	if err != nil {
		completedAt := time.Now()
		m.update(run.snapshot.RunID, func(snapshot *RunSnapshot) {
			snapshot.CompletedAt = &completedAt
			snapshot.Status = "error"
			snapshot.Error = err.Error()
		})
		return
	}
	client, err = m.wrapLoggedClient(run.snapshot.RunID, def.Name, client)
	if err != nil {
		completedAt := time.Now()
		m.update(run.snapshot.RunID, func(snapshot *RunSnapshot) {
			snapshot.CompletedAt = &completedAt
			snapshot.Status = "error"
			snapshot.Error = err.Error()
		})
		return
	}

	registry, _, afterTurnHooks, agentStartHooks, agentEndHooks, err := m.buildRuntime(def)
	if err != nil {
		completedAt := time.Now()
		m.update(run.snapshot.RunID, func(snapshot *RunSnapshot) {
			snapshot.CompletedAt = &completedAt
			snapshot.Status = "error"
			snapshot.Error = err.Error()
		})
		return
	}

	runner := cli.Runner{
		Client:          client,
		Registry:        registry,
		Skills:          nil,
		AfterTurnHooks:  afterTurnHooks,
		AgentStartHooks: agentStartHooks,
		AgentEndHooks:   agentEndHooks,
		PluginPrompts:   nil,
		AgentID:         run.snapshot.RunID,
		AgentName:       def.Name,
	}

	messages := []types.Message{
		{Role: "system", Content: def.Prompt},
		{Role: "user", Content: buildDelegationMessage(def, req)},
	}

	finalText, _, err := runner.RunConversation(ctx, messages, registry.Schemas())
	completedAt := time.Now()
	m.update(run.snapshot.RunID, func(snapshot *RunSnapshot) {
		snapshot.CompletedAt = &completedAt
		if snapshot.Status == "canceled" {
			return
		}
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				snapshot.Status = "timeout"
				snapshot.Error = err.Error()
				return
			}
			if errors.Is(err, context.Canceled) {
				snapshot.Status = "canceled"
				snapshot.Error = err.Error()
				return
			}
			snapshot.Status = "error"
			snapshot.Error = err.Error()
			return
		}
		snapshot.Status = "completed"
		snapshot.Result = finalText
	})
}

func (m *Manager) newRun(agent, task, label string) *childRun {
	id := fmt.Sprintf("subagent-%d", atomic.AddUint64(&m.nextID, 1))
	run := &childRun{
		snapshot: RunSnapshot{
			RunID:     id,
			Agent:     agent,
			Label:     strings.TrimSpace(label),
			Task:      task,
			Status:    "running",
			CreatedAt: time.Now(),
		},
		done: make(chan struct{}),
	}

	m.mu.Lock()
	m.runs[id] = run
	m.mu.Unlock()
	return run
}

func (m *Manager) update(runID string, fn func(*RunSnapshot)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[runID]
	if !ok {
		return
	}
	fn(&run.snapshot)
}

func (m *Manager) wait(ctx context.Context, runID string) (RunSnapshot, error) {
	m.mu.RLock()
	run, ok := m.runs[runID]
	if !ok {
		m.mu.RUnlock()
		return RunSnapshot{}, fmt.Errorf("unknown subagent %q", runID)
	}
	done := run.done
	m.mu.RUnlock()

	select {
	case <-done:
	case <-ctx.Done():
		return RunSnapshot{}, ctx.Err()
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	run, ok = m.runs[runID]
	if !ok {
		return RunSnapshot{}, fmt.Errorf("unknown subagent %q", runID)
	}
	return run.snapshot, nil
}

func buildDelegationMessage(def Definition, req SpawnRequest) string {
	lines := []string{
		fmt.Sprintf("You are running as the %q sub-agent for the main Yak agent.", def.Name),
		"Return a concise result for the parent agent, not end-user-oriented prose.",
	}
	if strings.TrimSpace(req.Label) != "" {
		lines = append(lines, fmt.Sprintf("Task label: %s", strings.TrimSpace(req.Label)))
	}
	lines = append(lines, "", "Task:", req.Task)
	return strings.Join(lines, "\n")
}

func (m *Manager) wrapLoggedClient(runID, agent string, client llm.ChatClient) (llm.ChatClient, error) {
	if strings.TrimSpace(m.logDir) == "" {
		return client, nil
	}

	sessionDir := filepath.Join(m.logDir, "subagents", fmt.Sprintf("%s_%s", runID, agent))
	logged, err := llm.NewLoggingClient(client, sessionDir)
	if err != nil {
		return nil, fmt.Errorf("creating subagent log directory: %w", err)
	}
	return logged, nil
}

func coalesceResult(snapshot RunSnapshot) string {
	if snapshot.Result != "" {
		return snapshot.Result
	}
	if snapshot.Error != "" {
		return "error: " + snapshot.Error
	}
	return snapshot.Status
}
