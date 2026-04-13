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

	"yak-go/internal/llm"
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
}

func (r Runner) Run(ctx context.Context) error {
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

	messages := []types.Message{{
		Role:    "system",
		Content: prompt.BuildSystemPrompt(r.Prompt, availableTools, r.Skills, env, r.PluginPrompts),
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

func (r Runner) RunConversation(
	ctx context.Context,
	messages []types.Message,
	toolSchemas []types.ChatRequestTool,
) (string, []types.Message, error) {
	finalText, err := r.agentLoop(ctx, &messages, toolSchemas, nil)
	return finalText, messages, err
}

func (r Runner) agentLoop(
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

	for {
		resp, err := r.Client.Chat(ctx, *messages, toolSchemas)
		if err != nil {
			return "", err
		}

		r.reportUsage(resp)

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
		*messages = append(*messages, types.Message{
			Role:      "assistant",
			Content:   nullableContent(assistantMsg.Content),
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

func hasSuccessfulRecentToolResult(messages []types.Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role == "assistant" || msg.Role == "user" {
			break
		}
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
		if strings.HasPrefix(content, "error:") {
			continue
		}
		return true
	}
	return false
}

func (r Runner) reportUsage(resp *types.ChatResponse) {
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

func nullableContent(content *string) any {
	if content == nil {
		return nil
	}
	return *content
}

const skillPrefix = "/skill:"

// expandSkillCommand checks if input starts with /skill:<name> and expands it
// to the skill's file content plus any trailing arguments. If the input is not
// a skill command, it is returned unchanged.
func (r Runner) expandSkillCommand(input string) (string, error) {
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
