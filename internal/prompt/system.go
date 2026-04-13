package prompt

import (
	"fmt"
	"strings"

	"yak-go/internal/skills"
	"yak-go/internal/tools"
)

type Environment struct {
	OS        string
	Arch      string
	Workspace string
	Timezone  string
	Time      string
}

const defaultPrompt = "You are a coding assistant. You help the user by reading, writing, editing files, and executing commands when needed."

func BuildSystemPrompt(agentPrompt string, available []tools.Tool, loadedSkills []skills.Skill, env Environment, curatedMemory string, pluginSections []string) string {
	if strings.TrimSpace(agentPrompt) == "" {
		agentPrompt = defaultPrompt
	}
	sections := []string{agentPrompt}

	sections = append(sections, buildEnvironment(env))

	if s := buildCuratedMemorySection(curatedMemory, available); s != "" {
		sections = append(sections, s)
	}

	if len(available) > 0 {
		sections = append(sections, buildToolGuidelines(available))
		sections = append(sections, buildToolSelectionRules(available))
	}

	if s := buildSkillsSection(loadedSkills); s != "" {
		sections = append(sections, s)
	}

	for _, s := range pluginSections {
		if s != "" {
			sections = append(sections, s)
		}
	}

	return strings.Join(sections, "\n\n")
}

func buildCuratedMemorySection(curated string, available []tools.Tool) string {
	curated = strings.TrimSpace(curated)
	hasMemoryTools := false
	for _, t := range available {
		if strings.HasPrefix(t.Definition().Name, "memory_") {
			hasMemoryTools = true
			break
		}
	}
	if curated == "" && !hasMemoryTools {
		return ""
	}

	lines := []string{"# Memory"}
	if hasMemoryTools {
		lines = append(lines,
			"You have a persistent memory store at .yak/memory/ with three layers:",
			"- MEMORY.md — curated long-term facts (shown below). Only the /memory:distill flow should rewrite it.",
			"- sessions/YYYY-MM-DD-HHMM.md — durable session notes. Use memory_write to save anything worth remembering from this session.",
			"- vault/{Memory,Knowledge,Journal,Notes}/*.md — permanent reference notes. Use memory_read/memory_search/memory_list to recall.",
		)
	}
	if curated != "" {
		lines = append(lines, "", "<curated_memory>", curated, "</curated_memory>")
	}
	return strings.Join(lines, "\n")
}

func buildSkillsSection(loadedSkills []skills.Skill) string {
	var visible []skills.Skill
	for _, s := range loadedSkills {
		if !s.DisableModelInvocation {
			visible = append(visible, s)
		}
	}
	if len(visible) == 0 {
		return ""
	}

	lines := []string{
		"The following skills provide specialized instructions for specific tasks.",
		"Use the read tool to load a skill's file when the task matches its description.",
		"When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.",
		"",
		"<available_skills>",
	}
	for _, s := range visible {
		lines = append(lines, "  <skill>")
		lines = append(lines, fmt.Sprintf("    <name>%s</name>", s.Name))
		lines = append(lines, fmt.Sprintf("    <description>%s</description>", s.Description))
		lines = append(lines, fmt.Sprintf("    <location>%s</location>", s.FilePath))
		lines = append(lines, "  </skill>")
	}
	lines = append(lines, "</available_skills>")
	return strings.Join(lines, "\n")
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
	hasSessionsSpawn := false
	hasSubagents := false

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
		case "sessions_spawn":
			hasSessionsSpawn = true
		case "subagents":
			hasSubagents = true
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

	if hasSessionsSpawn {
		rules = append(rules,
			"- Use sessions_spawn when a task can be delegated to a focused helper agent.",
			"- Always provide the target subagent name and include the necessary context in the delegated task.",
			"- Default to wait=true unless parallel progress is important.",
		)
	}

	if hasSubagents {
		rules = append(rules, "- Use subagents to inspect, wait on, or cancel background child runs.")
	}

	return strings.Join(rules, "\n")
}
