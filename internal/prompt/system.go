package prompt

import (
	"fmt"
	"strings"

	"yak-go/internal/tools"
)

type Environment struct {
	OS        string
	Arch      string
	Workspace string
	Timezone  string
	Time      string
}

func BuildSystemPrompt(available []tools.Tool, env Environment) string {
	sections := []string{
		"You are a coding assistant. You help the user by reading, writing, editing files, and executing commands when needed.",
	}

	sections = append(sections, buildEnvironment(env))

	if len(available) > 0 {
		sections = append(sections, buildToolGuidelines(available))
		sections = append(sections, buildToolSelectionRules(available))
	}

	return strings.Join(sections, "\n\n")
}

func buildEnvironment(env Environment) string {
	lines := []string{"# Environment"}

	if env.OS != "" || env.Arch != "" {
		lines = append(lines, fmt.Sprintf("- Platform: %s/%s", env.OS, env.Arch))
	}
	if env.Workspace != "" {
		lines = append(lines, fmt.Sprintf("- Workspace: %s", env.Workspace))
	}
	if env.Time != "" {
		line := fmt.Sprintf("- Current time: %s", env.Time)
		if env.Timezone != "" {
			line += fmt.Sprintf(" (%s)", env.Timezone)
		}
		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

func buildToolGuidelines(available []tools.Tool) string {
	lines := []string{"# Tools"}

	for _, tool := range available {
		definition := tool.Definition()
		if len(definition.Guidelines) == 0 {
			continue
		}

		lines = append(lines, "")
		lines = append(lines, "## "+definition.Name)
		for _, guideline := range definition.Guidelines {
			lines = append(lines, "- "+guideline)
		}
	}

	return strings.Join(lines, "\n")
}

func buildToolSelectionRules(available []tools.Tool) string {
	hasRead := false
	hasEdit := false
	hasWrite := false
	hasBash := false
	hasGrep := false
	hasLs := false
	hasFind := false

	for _, tool := range available {
		switch tool.Definition().Name {
		case "read":
			hasRead = true
		case "edit":
			hasEdit = true
		case "write":
			hasWrite = true
		case "bash":
			hasBash = true
		case "grep":
			hasGrep = true
		case "ls":
			hasLs = true
		case "find":
			hasFind = true
		}
	}

	rules := []string{"# Tool selection"}

	if hasRead && hasEdit {
		rules = append(rules, "- Always read a file before editing it.")
	}

	if hasEdit && hasWrite {
		rules = append(rules,
			"- To modify part of an existing file, use edit. Only use write for creating new files or complete rewrites.",
			"- When the user says \"add\", \"change\", \"update\", \"fix\", \"remove\", or \"replace\", use edit, not write.",
		)
	}

	if hasRead {
		rules = append(rules, "- Read files to understand context before making changes.")
	}

	if hasBash {
		rules = append(rules,
			"- Use bash to run shell commands when you need command output, tests, or build results.",
			"- Prefer bash over guessing command results.",
		)
	}

	if hasGrep {
		rules = append(rules, "- Use grep to search file contents for patterns instead of running grep via bash.")
	}

	if hasLs {
		rules = append(rules, "- Use ls to list directory contents instead of running ls via bash.")
	}

	if hasFind {
		rules = append(rules,
			"- Use find to locate files by name pattern instead of running find via bash.",
			"- Prefer find over bash for any file search task. Only fall back to bash when you need features find does not support.",
		)
	}

	return strings.Join(rules, "\n")
}
