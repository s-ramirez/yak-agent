package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseFrontmatter(t *testing.T) {
	input := "---\nname: commit\ndescription: Makes commits\ndisable-model-invocation: true\n---\n# Instructions\nDo the thing."

	fm, body := parseFrontmatter(input)

	if fm["name"] != "commit" {
		t.Fatalf("expected name=commit, got %q", fm["name"])
	}
	if fm["description"] != "Makes commits" {
		t.Fatalf("expected description, got %q", fm["description"])
	}
	if fm["disable-model-invocation"] != "true" {
		t.Fatalf("expected disable-model-invocation=true, got %q", fm["disable-model-invocation"])
	}
	if !strings.Contains(body, "# Instructions") {
		t.Fatalf("expected body to contain instructions, got %q", body)
	}
}

func TestParseFrontmatterNoFrontmatter(t *testing.T) {
	input := "# Just a markdown file\nNo frontmatter here."
	fm, body := parseFrontmatter(input)

	if len(fm) != 0 {
		t.Fatalf("expected empty frontmatter, got %v", fm)
	}
	if body != input {
		t.Fatalf("expected body to be original input")
	}
}

func TestLoadSkillsFromDirectory(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "commit")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"),
		"---\nname: commit\ndescription: Creates git commits\n---\nFollow conventional commits.")

	skills, diags, err := LoadSkills(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Name != "commit" {
		t.Fatalf("expected name=commit, got %q", skills[0].Name)
	}
	if skills[0].Description != "Creates git commits" {
		t.Fatalf("expected description, got %q", skills[0].Description)
	}
	if skills[0].Content != "Follow conventional commits." {
		t.Fatalf("expected content, got %q", skills[0].Content)
	}
}

func TestLoadSkillsNameDefaultsToDir(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "review")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"),
		"---\ndescription: Reviews PRs\n---\nReview instructions.")

	skills, _, err := LoadSkills(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Name != "review" {
		t.Fatalf("expected name to default to dir name, got %q", skills[0].Name)
	}
}

func TestLoadSkillsMissingDescription(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "broken")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"),
		"---\nname: broken\n---\nNo description.")

	skills, diags, err := LoadSkills(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Fatalf("expected 0 skills (missing description), got %d", len(skills))
	}
	if len(diags) == 0 {
		t.Fatal("expected diagnostic about missing description")
	}
}

func TestLoadSkillsCollision(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	writeFile(t, filepath.Join(dir1, "commit", "SKILL.md"),
		"---\nname: commit\ndescription: First\n---\nFirst.")
	writeFile(t, filepath.Join(dir2, "commit", "SKILL.md"),
		"---\nname: commit\ndescription: Second\n---\nSecond.")

	skills, diags, err := LoadSkills(dir1, dir2)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill (first wins), got %d", len(skills))
	}
	if skills[0].Description != "First" {
		t.Fatalf("expected first skill to win, got %q", skills[0].Description)
	}
	if len(diags) == 0 {
		t.Fatal("expected collision diagnostic")
	}
}

func TestLoadSkillsDisableModelInvocation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hidden", "SKILL.md"),
		"---\nname: hidden\ndescription: A hidden skill\ndisable-model-invocation: true\n---\nHidden.")

	skills, _, err := LoadSkills(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if !skills[0].DisableModelInvocation {
		t.Fatal("expected DisableModelInvocation to be true")
	}
}

func TestLoadSkillsSkipsMissingDir(t *testing.T) {
	skills, _, err := LoadSkills("/nonexistent/path")
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Fatalf("expected 0 skills for missing dir, got %d", len(skills))
	}
}

func TestLoadSkillsInvalidName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"),
		"---\nname: INVALID_NAME!\ndescription: Bad name\n---\nContent.")

	skills, diags, err := LoadSkills(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Skill still loads but with diagnostics
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if len(diags) == 0 {
		t.Fatal("expected diagnostic about invalid name")
	}
}
