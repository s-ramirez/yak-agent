package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditToolAppliesSingleEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditTool(OSFS{})
	raw, _ := json.Marshal(EditParams{
		Path: path,
		Edits: []EditRequest{
			{OldText: "world", NewText: "gopher"},
		},
	})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Output)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello gopher\n" {
		t.Fatalf("unexpected file contents: %q", string(got))
	}
}

func TestEditToolRejectsDuplicateMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("x\ny\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditTool(OSFS{})
	raw, _ := json.Marshal(EditParams{
		Path: path,
		Edits: []EditRequest{
			{OldText: "x", NewText: "z"},
		},
	})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected error result, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "matches multiple locations") {
		t.Fatalf("unexpected error output: %q", result.Output)
	}
}

func TestEditToolRejectsMissingEdits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditTool(OSFS{})
	raw, _ := json.Marshal(EditParams{Path: path})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected error result, got %q", result.Output)
	}
	if result.Output != "error: edits must be a non-empty array" {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}
