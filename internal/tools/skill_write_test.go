package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runSkillWrite(t *testing.T, tool *SkillWriteTool, p SkillWriteParams) ToolResult {
	t.Helper()
	raw, _ := json.Marshal(p)
	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return result
}

func TestSkillWriteCreatesFileAndReloads(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "logs", "skill_writes.log")
	reloaded := 0
	tool := NewSkillWriteTool(filepath.Join(dir, "skills"), logPath, func() error {
		reloaded++
		return nil
	})

	result := runSkillWrite(t, tool, SkillWriteParams{
		Name:        "calorie-log",
		Description: "Log meals to food/YYYY-MM-DD.md with calorie and protein totals.",
		Body:        "# Calorie log\n\nAppend each meal...",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Output)
	}
	if reloaded != 1 {
		t.Fatalf("expected reload to fire once, got %d", reloaded)
	}

	data, err := os.ReadFile(filepath.Join(dir, "skills", "calorie-log", "SKILL.md"))
	if err != nil {
		t.Fatalf("reading written skill: %v", err)
	}
	got := string(data)
	if !strings.HasPrefix(got, "---\nname: calorie-log\n") {
		t.Fatalf("missing frontmatter header: %q", got)
	}
	if !strings.Contains(got, "description: Log meals") {
		t.Fatalf("missing description: %q", got)
	}
	if !strings.Contains(got, "# Calorie log") {
		t.Fatalf("missing body: %q", got)
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading log: %v", err)
	}
	if !strings.Contains(string(logData), "wrote skill=calorie-log") {
		t.Fatalf("log missing entry: %q", string(logData))
	}
}

func TestSkillWriteRejectsExistingWithoutOverwrite(t *testing.T) {
	dir := t.TempDir()
	tool := NewSkillWriteTool(filepath.Join(dir, "skills"), "", nil)

	first := runSkillWrite(t, tool, SkillWriteParams{
		Name: "note", Description: "desc", Body: "body",
	})
	if first.IsError {
		t.Fatalf("first write failed: %s", first.Output)
	}

	second := runSkillWrite(t, tool, SkillWriteParams{
		Name: "note", Description: "desc", Body: "new body",
	})
	if !second.IsError {
		t.Fatalf("expected error on duplicate, got: %s", second.Output)
	}

	overwrite := runSkillWrite(t, tool, SkillWriteParams{
		Name: "note", Description: "desc", Body: "new body", Overwrite: true,
	})
	if overwrite.IsError {
		t.Fatalf("overwrite failed: %s", overwrite.Output)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "skills", "note", "SKILL.md"))
	if !strings.Contains(string(data), "new body") {
		t.Fatalf("overwrite did not replace body: %q", string(data))
	}
}

func TestSkillWriteValidatesInput(t *testing.T) {
	tool := NewSkillWriteTool(t.TempDir(), "", nil)
	cases := []struct {
		name   string
		params SkillWriteParams
		want   string
	}{
		{"empty name", SkillWriteParams{Description: "d", Body: "b"}, "name is required"},
		{"bad name", SkillWriteParams{Name: "Bad_Name", Description: "d", Body: "b"}, "lowercase alphanumeric"},
		{"empty description", SkillWriteParams{Name: "ok", Body: "b"}, "description is required"},
		{"multiline description", SkillWriteParams{Name: "ok", Description: "a\nb", Body: "b"}, "single line"},
		{"empty body", SkillWriteParams{Name: "ok", Description: "d"}, "body is required"},
		{"body with frontmatter", SkillWriteParams{Name: "ok", Description: "d", Body: "---\nname: x\n---\nbody"}, "must not contain YAML frontmatter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := runSkillWrite(t, tool, tc.params)
			if !result.IsError {
				t.Fatalf("expected error, got: %s", result.Output)
			}
			if !strings.Contains(result.Output, tc.want) {
				t.Fatalf("expected error to mention %q, got: %s", tc.want, result.Output)
			}
		})
	}
}

func TestSkillWriteDisableModelInvocation(t *testing.T) {
	dir := t.TempDir()
	tool := NewSkillWriteTool(filepath.Join(dir, "skills"), "", nil)
	result := runSkillWrite(t, tool, SkillWriteParams{
		Name: "hidden", Description: "d", Body: "b", DisableModelInvocation: true,
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Output)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "skills", "hidden", "SKILL.md"))
	if !strings.Contains(string(data), "disable-model-invocation: true") {
		t.Fatalf("expected disable-model-invocation line: %q", string(data))
	}
}
