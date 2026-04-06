package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteToolCreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "file.txt")

	tool := NewWriteTool(OSFS{})
	raw, _ := json.Marshal(WriteParams{Path: path, Content: "hello\nworld"})

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
	if string(got) != "hello\nworld" {
		t.Fatalf("unexpected file contents: %q", string(got))
	}
}

func TestWriteToolRejectsMissingPath(t *testing.T) {
	tool := NewWriteTool(OSFS{})
	raw, _ := json.Marshal(WriteParams{Content: "hello"})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected error result, got %q", result.Output)
	}
	if result.Output != "error: path is required" {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}
