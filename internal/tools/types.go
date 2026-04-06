package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"yak-go/internal/types"
)

type JSONSchema map[string]any

type ToolDefinition struct {
	Name        string
	Description string
	Guidelines  []string
	Parameters  JSONSchema
}

type ToolResult struct {
	Output  string
	IsError bool
}

type Tool interface {
	Definition() ToolDefinition
	Execute(ctx context.Context, params json.RawMessage) (ToolResult, error)
}

func errorResult(message string) ToolResult {
	return ToolResult{
		Output:  message,
		IsError: true,
	}
}

func errorResultf(format string, args ...any) ToolResult {
	return errorResult(fmt.Sprintf(format, args...))
}

// ToolHook receives notifications before and after tool execution.
// Both methods are optional — return nil from either to take no action.
type ToolHook interface {
	// BeforeToolCall is invoked before a tool executes.
	// Return a non-empty string to block execution (the string is used as the error message).
	// Return "" to allow execution to proceed.
	BeforeToolCall(name string, params json.RawMessage) string

	// AfterToolCall is invoked after a tool finishes executing.
	AfterToolCall(name string, result ToolResult, err error)
}

type Registry struct {
	tools map[string]Tool
	list  []Tool
	hooks []ToolHook
}

func NewRegistry(available ...Tool) *Registry {
	byName := make(map[string]Tool, len(available))
	for _, tool := range available {
		byName[tool.Definition().Name] = tool
	}

	return &Registry{
		tools: byName,
		list:  available,
	}
}

// AddHook registers a hook that is notified before and after tool executions.
func (r *Registry) AddHook(hook ToolHook) {
	r.hooks = append(r.hooks, hook)
}

// RunBeforeHooks calls BeforeToolCall on all registered hooks.
// Returns a non-empty block reason if any hook blocks execution.
func (r *Registry) RunBeforeHooks(name string, params json.RawMessage) string {
	for _, hook := range r.hooks {
		if reason := hook.BeforeToolCall(name, params); reason != "" {
			return reason
		}
	}
	return ""
}

// RunAfterHooks calls AfterToolCall on all registered hooks.
func (r *Registry) RunAfterHooks(name string, result ToolResult, err error) {
	for _, hook := range r.hooks {
		hook.AfterToolCall(name, result, err)
	}
}

func (r *Registry) Get(name string) (Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *Registry) List() []Tool {
	out := make([]Tool, len(r.list))
	copy(out, r.list)
	return out
}

func (r *Registry) Schemas() []types.ChatRequestTool {
	schemas := make([]types.ChatRequestTool, 0, len(r.list))
	for _, tool := range r.list {
		definition := tool.Definition()
		schemas = append(schemas, types.ChatRequestTool{
			Type: "function",
			Function: types.ChatRequestFunction{
				Name:        definition.Name,
				Description: definition.Description,
				Parameters:  definition.Parameters,
			},
		})
	}
	return schemas
}
