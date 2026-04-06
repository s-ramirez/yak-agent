package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepToolFindsPattern(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n\nfunc hello() {}\nfunc world() {}\n")

	tool := NewGrepTool()
	raw, _ := json.Marshal(GrepParams{Pattern: "func", Path: dir})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Output)
	}
	if !strings.Contains(result.Output, "main.go:3: func hello()") {
		t.Fatalf("expected match on line 3, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "main.go:4: func world()") {
		t.Fatalf("expected match on line 4, got %q", result.Output)
	}
}

func TestGrepToolFiltersByGlob(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")
	writeFile(t, dir, "readme.md", "package docs\n")

	tool := NewGrepTool()
	raw, _ := json.Marshal(GrepParams{Pattern: "package", Path: dir, Glob: "*.go"})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Output, "readme.md") {
		t.Fatalf("expected glob to exclude .md files, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "main.go") {
		t.Fatalf("expected match in main.go, got %q", result.Output)
	}
}

func TestGrepToolCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "test.txt", "Hello World\nhello world\n")

	tool := NewGrepTool()
	raw, _ := json.Marshal(GrepParams{Pattern: "HELLO", Path: dir, IgnoreCase: true})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "test.txt:1:") || !strings.Contains(result.Output, "test.txt:2:") {
		t.Fatalf("expected case-insensitive matches on both lines, got %q", result.Output)
	}
}

func TestGrepToolLiteralMode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "test.txt", "a.b\na*b\n")

	tool := NewGrepTool()
	raw, _ := json.Marshal(GrepParams{Pattern: "a.b", Path: dir, Literal: true})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "test.txt:1:") {
		t.Fatalf("expected literal match on line 1, got %q", result.Output)
	}
	if strings.Contains(result.Output, "test.txt:2:") {
		t.Fatalf("expected literal mode to not match 'a*b', got %q", result.Output)
	}
}

func TestGrepToolRespectsLimit(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "match")
	}
	writeFile(t, dir, "test.txt", strings.Join(lines, "\n"))

	tool := NewGrepTool()
	raw, _ := json.Marshal(GrepParams{Pattern: "match", Path: dir, Limit: 5})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "5 matches limit reached") {
		t.Fatalf("expected limit message, got %q", result.Output)
	}
}

func TestGrepToolNoMatchesFound(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "test.txt", "hello world\n")

	tool := NewGrepTool()
	raw, _ := json.Marshal(GrepParams{Pattern: "nonexistent", Path: dir})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "No matches found" {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}

func TestGrepToolRejectsMissingPattern(t *testing.T) {
	tool := NewGrepTool()
	raw, _ := json.Marshal(GrepParams{})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected error result, got %q", result.Output)
	}
	if result.Output != "error: pattern is required" {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}

func TestGrepToolSkipsGitDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "findme\n")
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, gitDir, "config", "findme\n")

	tool := NewGrepTool()
	raw, _ := json.Marshal(GrepParams{Pattern: "findme", Path: dir})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Output, ".git") {
		t.Fatalf("expected .git to be skipped, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "main.go") {
		t.Fatalf("expected match in main.go, got %q", result.Output)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
