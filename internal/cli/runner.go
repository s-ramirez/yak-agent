package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"yak-go/internal/compaction"
	"yak-go/internal/llm"
	"yak-go/internal/memory"
	"yak-go/internal/plugin"
	"yak-go/internal/prompt"
	"yak-go/internal/skills"
	"yak-go/internal/tools"
	"yak-go/internal/types"
)

type IO interface {
	Write(text string) error
	ReadLine(ctx context.Context) (string, error)
}

type Runner struct {
	Client          llm.ChatClient
	IO              IO
	Registry        *tools.Registry
	Skills          []skills.Skill
	AfterTurnHooks  []plugin.AfterTurnHook
	AgentStartHooks []plugin.AgentStartHook
	AgentEndHooks   []plugin.AgentEndHook
	PluginPrompts   []string
	AgentID         string // "main" or "subagent-N"
	AgentName       string // human-readable name
	Prompt          string // opening of the system prompt (from agent.md body)
	ContextSize     int    // model context window in tokens; 0 = unknown
	OnUsage         func(usage *types.Usage)
	UsageHooks      []plugin.UsageHook

	// MemoryStore, if set, provides persistent memory for the agent.
	// When non-nil, MEMORY.md is loaded and injected into the system prompt
	// and DistillMemory can be invoked to refresh MEMORY.md.
	MemoryStore *memory.Store

	// OnUserInput, if set, is invoked every time a non-empty user line is
	// received in the interactive REPL. Used to gate auto-distill.
	OnUserInput func()

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
}

func (r *Runner) Run(ctx context.Context) error {
	if r.Client == nil {
		return fmt.Errorf("client is required")
	}
	if r.IO == nil {
		return fmt.Errorf("io is required")
	}

	var availableTools []tools.Tool
	var toolSchemas []types.ChatRequestTool
	if r.Registry != nil {
		availableTools = r.Registry.List()
		toolSchemas = r.Registry.Schemas()
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
	messages := []types.Message{{
		Role:    "system",
		Content: prompt.BuildSystemPrompt(r.Prompt, availableTools, r.Skills, env, curated, r.PluginPrompts),
	}}

	for {
		if err := r.IO.Write("> "); err != nil {
			return err
		}

		line, err := r.IO.ReadLine(ctx)
		if err != nil {
			if err == io.EOF {
				return io.EOF
			}
			return err
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if r.OnUserInput != nil {
			r.OnUserInput()
		}

		expanded, err := r.expandSkillCommand(trimmed)
		if err != nil {
			if writeErr := r.IO.Write(fmt.Sprintf("error: %v\n", err)); writeErr != nil {
				return writeErr
			}
			continue
		}

		messages = append(messages, types.Message{
			Role:    "user",
			Content: expanded,
		})

		if _, err := r.agentLoop(ctx, &messages, toolSchemas, func(text string) error {
			return r.IO.Write(text + "\n")
		}); err != nil {
			if writeErr := r.IO.Write(fmt.Sprintf("error: %v\n", err)); writeErr != nil {
				return writeErr
			}
		}
	}
}

const maxEmptyRetries = 2
const emptyResponseRecoveryPrompt = "Your previous reply was empty. You must now produce a direct assistant response using the conversation and any tool results already returned. Do not call any more tools. Do not repeat completed work. If the task is complete, give the final answer now."

func (r *Runner) RunConversation(
	ctx context.Context,
	messages []types.Message,
	toolSchemas []types.ChatRequestTool,
) (string, []types.Message, error) {
	finalText, err := r.agentLoop(ctx, &messages, toolSchemas, nil)
	return finalText, messages, err
}

func (r *Runner) agentLoop(
	ctx context.Context,
	messages *[]types.Message,
	toolSchemas []types.ChatRequestTool,
	onFinalText func(string) error,
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
		if r.maybeCompact(ctx, messages) {
			hadToolCalls = false
			emptyRetries = 0
		}

		resp, err := r.Client.Chat(ctx, *messages, toolSchemas)
		if err != nil {
			return "", err
		}

		r.reportUsage(resp)
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

			if onFinalText != nil {
				if err := onFinalText(text); err != nil {
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
func (r *Runner) maybeCompact(ctx context.Context, messages *[]types.Message) bool {
	if !r.Compaction.Enabled || r.ContextSize <= 0 {
		return false
	}
	tokens := compaction.EstimateContextTokens(*messages, r.lastUsage, r.lastUsageIndex)
	if !compaction.ShouldCompact(tokens, r.ContextSize, r.Compaction) {
		return false
	}

	if r.IO != nil {
		_ = r.IO.Write(compaction.FormatTriggerLine(tokens, r.ContextSize, r.Compaction) + "\n")
	}

	res, err := compaction.Compact(ctx, r.Client, *messages, r.lastSummary, r.Compaction, tokens)
	if err != nil {
		if r.IO != nil {
			_ = r.IO.Write(fmt.Sprintf("[compaction failed: %v]\n", err))
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

	if r.IO != nil {
		after := compaction.EstimateContextTokens(*messages, nil, -1)
		_ = r.IO.Write(fmt.Sprintf("[compacted: %d → %d tokens]\n", tokens, after))
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

func (r *Runner) reportUsage(resp *types.ChatResponse) {
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
	if r.IO == nil {
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
	_ = r.IO.Write(line)
}

const skillPrefix = "/skill:"
const memoryDistillCommand = "/memory:distill"

// distillInstruction is the fixed prompt used by both the manual
// /memory:distill slash command and the auto-distill flow on session exit.
const distillInstruction = `Review this session and refresh long-term memory.

1. Call memory_read with path="MEMORY.md" to see current curated memory. A missing file is fine.
2. Call memory_list with dir="sessions" to see available session notes. Read recent ones that look relevant with memory_read.
3. Decide whether this session produced anything worth preserving long-term: user preferences, active priorities, hard-won lessons, durable facts. Skip anything already in the agent config, skills, or obvious from project files.
4. If there is something worth updating, call memory_write with path="MEMORY.md", mode="overwrite", and content containing a refreshed MEMORY.md (aim for under 3000 characters, plain Markdown, no frontmatter required). Then reply with one short sentence summarizing what changed.
5. If nothing needs updating, reply with exactly: NO_UPDATE

Do not ask the user questions — this is a background review.`

// expandSkillCommand checks if input starts with /skill:<name> or
// /memory:distill and expands it to an appropriate prompt. If the input is
// not a recognized command, it is returned unchanged.
func (r *Runner) expandSkillCommand(input string) (string, error) {
	if input == memoryDistillCommand {
		return distillInstruction, nil
	}
	if !strings.HasPrefix(input, skillPrefix) {
		return input, nil
	}

	rest := input[len(skillPrefix):]
	name := rest
	args := ""
	if idx := strings.IndexByte(rest, ' '); idx >= 0 {
		name = rest[:idx]
		args = strings.TrimSpace(rest[idx+1:])
	}

	for _, s := range r.Skills {
		if s.Name == name {
			content, err := os.ReadFile(s.FilePath)
			if err != nil {
				return "", fmt.Errorf("reading skill %q: %w", name, err)
			}
			result := string(content)
			if args != "" {
				result += "\n\n" + args
			}
			return result, nil
		}
	}

	return "", fmt.Errorf("unknown skill %q", name)
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

// DistillMemory runs a single non-interactive agent turn that asks the model
// to refresh MEMORY.md based on this session's work. Only the top-level
// runner (AgentID == "main") executes; subagents are no-ops. Intended to be
// called from the main entry point after the interactive REPL exits.
func (r *Runner) DistillMemory(ctx context.Context) error {
	if r.MemoryStore == nil || r.AgentID != "main" {
		return nil
	}
	if r.Client == nil {
		return fmt.Errorf("client is required")
	}

	var availableTools []tools.Tool
	var toolSchemas []types.ChatRequestTool
	if r.Registry != nil {
		availableTools = r.Registry.List()
		toolSchemas = r.Registry.Schemas()
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
	messages := []types.Message{
		{Role: "system", Content: prompt.BuildSystemPrompt(r.Prompt, availableTools, r.Skills, env, curated, r.PluginPrompts)},
		{Role: "user", Content: distillInstruction},
	}

	_, err := r.agentLoop(ctx, &messages, toolSchemas, nil)
	return err
}
