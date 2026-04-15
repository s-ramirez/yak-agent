package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"yak-go/internal/channel"
	"yak-go/internal/compaction"
	"yak-go/internal/llm"
	"yak-go/internal/memory"
	"yak-go/internal/plugin"
	"yak-go/internal/prompt"
	"yak-go/internal/schedule"
	"yak-go/internal/skills"
	"yak-go/internal/tools"
	"yak-go/internal/types"
)

// Runner holds the long-lived agent configuration. It exposes three
// entry points:
//
//   - HandleTurn: processes one dispatcher turn against a channel
//     Conversation. Used by the channel dispatcher as the TurnHandler.
//   - RunConversation: runs a fresh conversation to completion for
//     callers that own the full message slice (subagents).
//   - DistillMemory: a silent single-turn run that asks the model to
//     refresh MEMORY.md at session exit.
type Runner struct {
	Client          llm.ChatClient
	Registry        *tools.Registry
	Skills          []skills.Skill
	AfterTurnHooks  []plugin.AfterTurnHook
	AgentStartHooks []plugin.AgentStartHook
	AgentEndHooks   []plugin.AgentEndHook
	PluginPrompts   []string
	AgentID         string // "main" or "subagent-N"
	AgentName       string // human-readable name
	Prompt          string // opening of the system prompt (from AGENTS.md body)
	// ContextFiles are embedded verbatim at the top of the system prompt.
	// Typically IDENTITY.md and USER.md from the .yak/ workspace.
	ContextFiles []prompt.ContextFile
	ContextSize     int    // model context window in tokens; 0 = unknown
	OnUsage         func(usage *types.Usage)
	UsageHooks      []plugin.UsageHook

	// MemoryStore, if set, provides persistent memory for the agent.
	// When non-nil, MEMORY.md is loaded and injected into the system prompt
	// and DistillMemory can be invoked to refresh MEMORY.md.
	MemoryStore *memory.Store

	// Compaction, if enabled, automatically trims old messages once the
	// estimated context usage crosses the threshold. Only fires when
	// ContextSize > 0.
	Compaction compaction.Settings

	// lastSummary carries the previous compaction summary forward so the
	// next compaction can use the UPDATE_SUMMARIZATION_PROMPT variant.
	lastSummary string

	// lastUsage tracks the most recent authoritative token count returned
	// by the LLM so compaction can use it across turns.
	lastUsage      *types.Usage
	lastUsageIndex int

	// Scheduler, if set, is consulted when building the system prompt so
	// the model sees currently-enabled user-managed jobs at startup. The
	// scheduler's event stream is handled by the sched channel adapter,
	// not by the Runner directly.
	Scheduler *schedule.Scheduler
}

const maxEmptyRetries = 2
const emptyResponseRecoveryPrompt = "Your previous reply was empty. You must now produce a direct assistant response using the conversation and any tool results already returned. Do not call any more tools. Do not repeat completed work. If the task is complete, give the final answer now."

// HandleTurn implements channel.TurnHandler. The dispatcher serializes
// turns per conversation, so this method has exclusive access to
// conv.Messages for its duration.
//
// The first turn on a fresh conversation lazily builds and prepends the
// system prompt so every surface (CLI, scheduled event, future webui)
// shares the same initialization path.
func (r *Runner) HandleTurn(ctx context.Context, conv *channel.Conversation, userContent string, reply channel.ReplyFunc) error {
	if r.Client == nil {
		return fmt.Errorf("client is required")
	}

	if len(conv.Messages) == 0 {
		conv.Messages = append(conv.Messages, types.Message{
			Role:    "system",
			Content: r.buildSystemPrompt(),
		})
	}

	conv.Messages = append(conv.Messages, types.Message{
		Role:    "user",
		Content: userContent,
	})

	var toolSchemas []types.ChatRequestTool
	if r.Registry != nil {
		toolSchemas = r.Registry.Schemas()
	}

	_, err := r.agentLoop(ctx, &conv.Messages, toolSchemas, reply)
	return err
}

// buildSystemPrompt assembles the system prompt from the runner's
// current configuration. It is invoked once per conversation, when the
// conversation is first seen by HandleTurn.
func (r *Runner) buildSystemPrompt() string {
	var availableTools []tools.Tool
	if r.Registry != nil {
		availableTools = r.Registry.List()
	}

	now := time.Now()
	tz, _ := now.Zone()
	cwd, _ := os.Getwd()
	env := prompt.Environment{
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		Workspace: cwd,
		Timezone:  tz,
		Time:      now.Format(time.RFC3339),
	}

	curated := r.loadCuratedMemory()
	pluginPrompts := r.composePluginPrompts()
	return prompt.BuildSystemPrompt(r.Prompt, availableTools, r.Skills, env, curated, pluginPrompts, r.ContextFiles...)
}

// composePluginPrompts returns plugin sections plus a synthesized
// <scheduled_tasks> block describing currently-enabled user jobs.
func (r *Runner) composePluginPrompts() []string {
	sections := append([]string(nil), r.PluginPrompts...)
	if r.Scheduler == nil {
		return sections
	}
	if s := buildScheduledTasksSection(r.Scheduler.Store().List()); s != "" {
		sections = append(sections, s)
	}
	return sections
}

// buildScheduledTasksSection renders enabled jobs as an XML block for the
// system prompt. The tool's own guidelines already cover <scheduled_event>
// semantics, so this section is pure state — a snapshot of what is pending
// at session start.
func buildScheduledTasksSection(jobs []schedule.Job) string {
	var enabled []schedule.Job
	for _, j := range jobs {
		if j.Enabled {
			enabled = append(enabled, j)
		}
	}
	if len(enabled) == 0 {
		return ""
	}
	lines := []string{
		"# Scheduled tasks",
		"",
		"These jobs are currently persisted and will fire as <scheduled_event> user messages.",
		"Use the schedule tool to inspect, add, or remove jobs.",
		"",
		"<scheduled_tasks>",
	}
	for _, j := range enabled {
		sched := describeSchedule(j.Schedule)
		next := "(none)"
		if j.NextRunAt != nil {
			next = j.NextRunAt.UTC().Format(time.RFC3339)
		}
		lines = append(lines,
			"  <task>",
			fmt.Sprintf("    <id>%s</id>", j.ID),
			fmt.Sprintf("    <name>%s</name>", j.Name),
			fmt.Sprintf("    <schedule>%s</schedule>", sched),
			fmt.Sprintf("    <next-run>%s</next-run>", next),
			fmt.Sprintf("    <text>%s</text>", j.Text),
			"  </task>",
		)
	}
	lines = append(lines, "</scheduled_tasks>")
	return strings.Join(lines, "\n")
}

func describeSchedule(s schedule.Schedule) string {
	switch s.Kind {
	case schedule.KindAt:
		if s.At == nil {
			return "at (unset)"
		}
		return "at " + s.At.UTC().Format(time.RFC3339)
	case schedule.KindEvery:
		return "every " + time.Duration(s.Every).String()
	case schedule.KindCron:
		return "cron " + s.Cron
	default:
		return string(s.Kind)
	}
}

func (r *Runner) RunConversation(
	ctx context.Context,
	messages []types.Message,
	toolSchemas []types.ChatRequestTool,
) (string, []types.Message, error) {
	finalText, err := r.agentLoop(ctx, &messages, toolSchemas, nil)
	return finalText, messages, err
}

// agentLoop is the core LLM + tool-dispatch cycle. emit, if non-nil,
// receives user-visible output: the final assistant text, usage
// summaries, and compaction status lines. Pass nil for silent runs
// (subagents, DistillMemory).
func (r *Runner) agentLoop(
	ctx context.Context,
	messages *[]types.Message,
	toolSchemas []types.ChatRequestTool,
	emit channel.ReplyFunc,
) (finalText string, err error) {
	lifecycleCtx := plugin.AgentLifecycleContext{
		AgentID:   r.AgentID,
		AgentName: r.AgentName,
	}
	for _, h := range r.AgentStartHooks {
		h.OnAgentStart(lifecycleCtx)
	}
	defer func() {
		for _, h := range r.AgentEndHooks {
			h.OnAgentEnd(lifecycleCtx, finalText, err)
		}
	}()

	hadToolCalls := false
	emptyRetries := 0
	if r.lastUsageIndex == 0 {
		r.lastUsageIndex = -1
	}

	for {
		if r.maybeCompact(ctx, messages, emit) {
			hadToolCalls = false
			emptyRetries = 0
		}

		resp, err := r.Client.Chat(ctx, *messages, toolSchemas)
		if err != nil {
			return "", err
		}

		r.reportUsage(resp, emit)
		if resp.Usage != nil {
			r.lastUsage = resp.Usage
			r.lastUsageIndex = len(*messages) - 1
		}

		toolCalls := types.GetToolCalls(resp)
		if len(toolCalls) == 0 {
			text := types.GetResponseText(resp)

			if text == "" && hadToolCalls && emptyRetries < maxEmptyRetries {
				emptyRetries++
				*messages = append(*messages, types.Message{
					Role:    "assistant",
					Content: "",
				})
				*messages = append(*messages, types.Message{
					Role:    "user",
					Content: emptyResponseRecoveryPrompt,
				})
				continue
			}

			if text == "" {
				text = fallbackNoResponseMessage(*messages, hadToolCalls)
			}

			if emit != nil {
				if err := emit(text + "\n"); err != nil {
					return "", err
				}
			}

			*messages = append(*messages, types.Message{
				Role:    "assistant",
				Content: text,
			})

			// Check after-turn hooks — plugins can inject a follow-up message.
			injected := false
			for _, h := range r.AfterTurnHooks {
				if msg := h.AfterTurn(text); msg != "" {
					*messages = append(*messages, types.Message{
						Role:    "user",
						Content: msg,
					})
					hadToolCalls = false
					emptyRetries = 0
					injected = true
					break
				}
			}
			if injected {
				continue
			}
			return text, nil
		}

		hadToolCalls = true
		emptyRetries = 0

		assistantMsg := resp.Choices[0].Message
		var content any
		if assistantMsg.Content != nil {
			content = *assistantMsg.Content
		}
		*messages = append(*messages, types.Message{
			Role:      "assistant",
			Content:   content,
			ToolCalls: assistantMsg.ToolCalls,
		})

		for _, call := range toolCalls {
			if r.Registry == nil {
				*messages = append(*messages, types.Message{
					Role:       "tool",
					ToolCallID: call.ID,
					Content:    fmt.Sprintf("error: unknown tool %q", call.Function.Name),
				})
				continue
			}

			tool, ok := r.Registry.Get(call.Function.Name)
			if !ok {
				*messages = append(*messages, types.Message{
					Role:       "tool",
					ToolCallID: call.ID,
					Content:    fmt.Sprintf("error: unknown tool %q", call.Function.Name),
				})
				continue
			}

			rawArgs := json.RawMessage(call.Function.Arguments)
			if !json.Valid(rawArgs) {
				*messages = append(*messages, types.Message{
					Role:       "tool",
					ToolCallID: call.ID,
					Content:    "error: invalid JSON arguments",
				})
				continue
			}

			hctx := tools.HookContext{AgentID: r.AgentID, AgentName: r.AgentName}
			if reason := r.Registry.RunBeforeHooks(hctx, call.Function.Name, rawArgs); reason != "" {
				*messages = append(*messages, types.Message{
					Role:       "tool",
					ToolCallID: call.ID,
					Content:    fmt.Sprintf("error: blocked by hook: %s", reason),
				})
				continue
			}

			result, err := tool.Execute(ctx, rawArgs)
			r.Registry.RunAfterHooks(hctx, call.Function.Name, rawArgs, result, err)

			if err != nil {
				*messages = append(*messages, types.Message{
					Role:       "tool",
					ToolCallID: call.ID,
					Content:    fmt.Sprintf("error: %v", err),
				})
				continue
			}

			*messages = append(*messages, types.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    result.Output,
			})
		}
	}
}

// maybeCompact checks the budget and, if triggered, replaces *messages
// with a compacted version in place. Returns true when compaction ran
// successfully so callers can reset per-turn state.
func (r *Runner) maybeCompact(ctx context.Context, messages *[]types.Message, emit channel.ReplyFunc) bool {
	if !r.Compaction.Enabled || r.ContextSize <= 0 {
		return false
	}
	tokens := compaction.EstimateContextTokens(*messages, r.lastUsage, r.lastUsageIndex)
	if !compaction.ShouldCompact(tokens, r.ContextSize, r.Compaction) {
		return false
	}

	if emit != nil {
		_ = emit(compaction.FormatTriggerLine(tokens, r.ContextSize, r.Compaction) + "\n")
	}

	res, err := compaction.Compact(ctx, r.Client, *messages, r.lastSummary, r.Compaction, tokens)
	if err != nil {
		if emit != nil {
			_ = emit(fmt.Sprintf("[compaction failed: %v]\n", err))
		}
		return false
	}
	if res.Summary == "" {
		return false
	}

	*messages = res.Messages
	r.lastSummary = res.Summary
	r.lastUsage = nil
	r.lastUsageIndex = -1

	if emit != nil {
		after := compaction.EstimateContextTokens(*messages, nil, -1)
		_ = emit(fmt.Sprintf("[compacted: %d → %d tokens]\n", tokens, after))
	}
	return true
}

func fallbackNoResponseMessage(messages []types.Message, hadToolCalls bool) string {
	if !hadToolCalls {
		return "[no response]"
	}

	var recent []string
	for i := len(messages) - 1; i >= 0 && len(recent) < 3; i-- {
		msg := messages[i]
		if msg.Role != "tool" {
			continue
		}
		content, ok := msg.Content.(string)
		if !ok {
			continue
		}
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		recent = append([]string{content}, recent...)
	}

	if len(recent) == 0 {
		return "[no response after tool calls]"
	}

	return "[no response after tool calls]\nRecent tool results:\n- " + strings.Join(recent, "\n- ")
}

func (r *Runner) reportUsage(resp *types.ChatResponse, emit channel.ReplyFunc) {
	if resp == nil || resp.Usage == nil {
		return
	}
	for _, h := range r.UsageHooks {
		if h == nil {
			continue
		}
		h.OnUsage(plugin.AgentLifecycleContext{
			AgentID:   r.AgentID,
			AgentName: r.AgentName,
		}, resp.Usage, r.ContextSize)
	}
	if r.OnUsage != nil {
		r.OnUsage(resp.Usage)
		return
	}
	if emit == nil {
		return
	}
	u := resp.Usage
	var line string
	if r.ContextSize > 0 {
		remaining := r.ContextSize - u.TotalTokens
		pct := float64(u.TotalTokens) / float64(r.ContextSize) * 100
		line = fmt.Sprintf("[tokens: prompt=%d completion=%d total=%d / ctx=%d (%.1f%%) left=%d]\n",
			u.PromptTokens, u.CompletionTokens, u.TotalTokens, r.ContextSize, pct, remaining)
	} else {
		line = fmt.Sprintf("[tokens: prompt=%d completion=%d total=%d]\n",
			u.PromptTokens, u.CompletionTokens, u.TotalTokens)
	}
	_ = emit(line)
}

// loadCuratedMemory reads MEMORY.md if a store is configured. Returns "" on
// any error or missing file — memory is best-effort, never fatal.
func (r *Runner) loadCuratedMemory() string {
	if r.MemoryStore == nil {
		return ""
	}
	curated, err := r.MemoryStore.LoadCurated()
	if err != nil {
		return ""
	}
	return curated
}

// DistillMemory runs a single non-interactive agent turn that asks the
// model to refresh MEMORY.md based on this session's work. Only the
// top-level runner (AgentID == "main") executes; subagents are no-ops.
// Intended to be called from the main entry point after the channel
// dispatcher exits.
func (r *Runner) DistillMemory(ctx context.Context) error {
	if r.MemoryStore == nil || r.AgentID != "main" {
		return nil
	}
	if r.Client == nil {
		return fmt.Errorf("client is required")
	}

	var toolSchemas []types.ChatRequestTool
	if r.Registry != nil {
		toolSchemas = r.Registry.Schemas()
	}

	messages := []types.Message{
		{Role: "system", Content: r.buildSystemPrompt()},
		{Role: "user", Content: channel.DistillInstruction},
	}

	_, err := r.agentLoop(ctx, &messages, toolSchemas, nil)
	return err
}
