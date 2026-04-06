package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindToolMatchesByExtension(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "")
	writeFile(t, dir, "util.go", "")
	writeFile(t, dir, "readme.md", "")

	tool := NewFindTool()
	raw, _ := json.Marshal(FindParams{Pattern: "*.go", Path: dir})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Output)
	}
	if !strings.Contains(result.Output, "main.go") {
		t.Fatalf("expected main.go in output, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "util.go") {
		t.Fatalf("expected util.go in output, got %q", result.Output)
	}
	if strings.Contains(result.Output, "readme.md") {
		t.Fatalf("expected readme.md to be excluded, got %q", result.Output)
	}
}

func TestFindToolMatchesInSubdirectories(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "root.go", "")
	sub := filepath.Join(dir, "sub")
	writeFile(t, sub, "nested.go", "")

	tool := NewFindTool()
	raw, _ := json.Marshal(FindParams{Pattern: "*.go", Path: dir})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "root.go") {
		t.Fatalf("expected root.go in output, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "sub/nested.go") {
		t.Fatalf("expected sub/nested.go in output, got %q", result.Output)
	}
}

func TestFindToolSkipsGitDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "")
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, gitDir, "config.go", "")

	tool := NewFindTool()
	raw, _ := json.Marshal(FindParams{Pattern: "*.go", Path: dir})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Output, ".git") {
		t.Fatalf("expected .git to be skipped, got %q", result.Output)
	}
}

func TestFindToolRespectsLimit(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		writeFile(t, dir, fmt.Sprintf("file%02d.go", i), "")
	}

	tool := NewFindTool()
	raw, _ := json.Marshal(FindParams{Pattern: "*.go", Path: dir, Limit: 3})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "3 results limit reached") {
		t.Fatalf("expected limit message, got %q", result.Output)
	}
}

func TestFindToolNoResults(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "")

	tool := NewFindTool()
	raw, _ := json.Marshal(FindParams{Pattern: "*.rs", Path: dir})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "No files found matching pattern" {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}

func TestFindToolRejectsMissingPattern(t *testing.T) {
	tool := NewFindTool()
	raw, _ := json.Marshal(FindParams{})

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
