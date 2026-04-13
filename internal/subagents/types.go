package subagents

import (
	"yak-go/internal/plugin"
	"yak-go/internal/tools"
)

type Definition struct {
	Name        string
	Description string
	WhenToUse   string
	Model       string
	BaseURL     string
	APIKeyEnv   string
	ContextSize int
	Tools       []string
	Plugins     []string
	Prompt      string
	FilePath    string
}

type RuntimePlugin struct {
	Name           string
	Tools          []tools.Tool
	Hooks          []tools.ToolHook
	SystemPrompt   string
	AfterTurnHook  plugin.AfterTurnHook
	AgentStartHook plugin.AgentStartHook
	AgentEndHook   plugin.AgentEndHook
	UsageHook      plugin.UsageHook
}
