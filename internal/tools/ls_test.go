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

func TestLsToolListsDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "alpha.txt", "")
	writeFile(t, dir, "beta.txt", "")
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	tool := NewLsTool(OSFS{})
	raw, _ := json.Marshal(LsParams{Path: dir})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Output)
	}
	if !strings.Contains(result.Output, "alpha.txt") {
		t.Fatalf("expected alpha.txt in output, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "subdir/") {
		t.Fatalf("expected subdir/ in output, got %q", result.Output)
	}
}

func TestLsToolSortsAlphabetically(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "charlie.txt", "")
	writeFile(t, dir, "Alpha.txt", "")
	writeFile(t, dir, "bravo.txt", "")

	tool := NewLsTool(OSFS{})
	raw, _ := json.Marshal(LsParams{Path: dir})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(result.Output, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %q", result.Output)
	}
	if lines[0] != "Alpha.txt" || lines[1] != "bravo.txt" || lines[2] != "charlie.txt" {
		t.Fatalf("expected alphabetical order, got %q", result.Output)
	}
}

func TestLsToolEmptyDirectory(t *testing.T) {
	dir := t.TempDir()

	tool := NewLsTool(OSFS{})
	raw, _ := json.Marshal(LsParams{Path: dir})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "(empty directory)" {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}

func TestLsToolRespectsLimit(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		writeFile(t, dir, fmt.Sprintf("file%02d.txt", i), "")
	}

	tool := NewLsTool(OSFS{})
	raw, _ := json.Marshal(LsParams{Path: dir, Limit: 3})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "3 entries limit reached") {
		t.Fatalf("expected limit message, got %q", result.Output)
	}
}

func TestLsToolRejectsNonDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewLsTool(OSFS{})
	raw, _ := json.Marshal(LsParams{Path: path})

	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected error result, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "not a directory") {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}
