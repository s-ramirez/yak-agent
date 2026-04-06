package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadToolReadsWithOffsetAndLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadTool(OSFS{})
	raw, _ := json.Marshal(ReadParams{Path: path, Offset: 2, Limit: 2})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Output)
	}
	if result.Output != "2\tb\n3\tc" {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}

func TestReadToolTruncatesLargeRequests(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.txt")

	var builder strings.Builder
	for i := 0; i < MaxReadLines+10; i++ {
		builder.WriteString("line\n")
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadTool(OSFS{})
	raw, _ := json.Marshal(ReadParams{Path: path, Offset: 1, Limit: MaxReadLines + 5})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "[truncated:") {
		t.Fatalf("expected truncation marker, got %q", result.Output)
	}
}

func TestReadToolRejectsMissingPath(t *testing.T) {
	tool := NewReadTool(OSFS{})
	raw, _ := json.Marshal(ReadParams{})

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
