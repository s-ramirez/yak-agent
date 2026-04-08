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
	Client         llm.ChatClient
	IO             IO
	Registry       *tools.Registry
	Skills         []skills.Skill
	AfterTurnHooks []plugin.AfterTurnHook
	PluginPrompts  []string
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
		Content: prompt.BuildSystemPrompt(availableTools, r.Skills, env, r.PluginPrompts),
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

		if err := r.agentLoop(ctx, &messages, toolSchemas); err != nil {
			if writeErr := r.IO.Write(fmt.Sprintf("error: %v\n", err)); writeErr != nil {
				return writeErr
			}
		}
	}
}

const maxEmptyRetries = 2

func (r Runner) agentLoop(ctx context.Context, messages *[]types.Message, toolSchemas []types.ChatRequestTool) error {
	hadToolCalls := false
	emptyRetries := 0

	for {
		resp, err := r.Client.Chat(ctx, *messages, toolSchemas)
		if err != nil {
			return err
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
					Content: "Present the tool results to the user. Do not call any tools.",
				})
				continue
			}

			if text == "" {
				text = "[no response]"
			}

			if err := r.IO.Write(text + "\n"); err != nil {
				return err
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
			return nil
		}

		hadToolCalls = true

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

			if reason := r.Registry.RunBeforeHooks(call.Function.Name, rawArgs); reason != "" {
				*messages = append(*messages, types.Message{
					Role:       "tool",
					ToolCallID: call.ID,
					Content:    fmt.Sprintf("error: blocked by hook: %s", reason),
				})
				continue
			}

			result, err := tool.Execute(ctx, rawArgs)
			r.Registry.RunAfterHooks(call.Function.Name, result, err)

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
