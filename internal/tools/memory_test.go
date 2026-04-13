package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"yak-go/internal/memory"
)

func newTestStore(t *testing.T) *memory.Store {
	t.Helper()
	return memory.NewStore(t.TempDir())
}

func TestMemoryWriteAndRead(t *testing.T) {
	store := newTestStore(t)
	write := NewMemoryWriteTool(store)
	read := NewMemoryReadTool(store)

	raw, _ := json.Marshal(MemoryWriteParams{Path: "MEMORY.md", Content: "alpha\nbeta\n"})
	res, err := write.Execute(context.Background(), raw)
	if err != nil || res.IsError {
		t.Fatalf("write failed: %v / %s", err, res.Output)
	}

	raw, _ = json.Marshal(MemoryReadParams{Path: "MEMORY.md"})
	res, err = read.Execute(context.Background(), raw)
	if err != nil || res.IsError {
		t.Fatalf("read failed: %v / %s", err, res.Output)
	}
	if !strings.Contains(res.Output, "1\talpha") || !strings.Contains(res.Output, "2\tbeta") {
		t.Fatalf("unexpected output: %q", res.Output)
	}
}

func TestMemoryWriteAppendMode(t *testing.T) {
	store := newTestStore(t)
	write := NewMemoryWriteTool(store)

	raw, _ := json.Marshal(MemoryWriteParams{Path: "sessions/s.md", Content: "a\n"})
	if _, err := write.Execute(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(MemoryWriteParams{Path: "sessions/s.md", Content: "b\n", Mode: "append"})
	if _, err := write.Execute(context.Background(), raw); err != nil {
		t.Fatal(err)
	}

	data, err := store.Read("sessions/s.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "a\nb\n" {
		t.Fatalf("unexpected content: %q", data)
	}
}

func TestMemoryWriteRejectsEscape(t *testing.T) {
	store := newTestStore(t)
	write := NewMemoryWriteTool(store)
	raw, _ := json.Marshal(MemoryWriteParams{Path: "../escape.md", Content: "x"})
	res, err := write.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected sandbox error, got %q", res.Output)
	}
}

func TestMemoryWriteRejectsInvalidMode(t *testing.T) {
	store := newTestStore(t)
	write := NewMemoryWriteTool(store)
	raw, _ := json.Marshal(MemoryWriteParams{Path: "x.md", Content: "y", Mode: "delete"})
	res, err := write.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected mode error, got %q", res.Output)
	}
}

func TestMemoryReadMissing(t *testing.T) {
	store := newTestStore(t)
	read := NewMemoryReadTool(store)
	raw, _ := json.Marshal(MemoryReadParams{Path: "missing.md"})
	res, err := read.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected error, got %q", res.Output)
	}
}

func TestMemorySearchCaseInsensitive(t *testing.T) {
	store := newTestStore(t)
	if err := store.Write("MEMORY.md", []byte("User: Alice\nTimezone: UTC\n"), false); err != nil {
		t.Fatal(err)
	}
	search := NewMemorySearchTool(store)
	raw, _ := json.Marshal(MemorySearchParams{Query: "alice"})
	res, err := search.Execute(context.Background(), raw)
	if err != nil || res.IsError {
		t.Fatalf("search failed: %v / %s", err, res.Output)
	}
	if !strings.Contains(res.Output, "MEMORY.md:1") {
		t.Fatalf("expected match on line 1: %q", res.Output)
	}
}

func TestMemorySearchNoMatches(t *testing.T) {
	store := newTestStore(t)
	if err := store.Write("MEMORY.md", []byte("hello\n"), false); err != nil {
		t.Fatal(err)
	}
	search := NewMemorySearchTool(store)
	raw, _ := json.Marshal(MemorySearchParams{Query: "zzzz"})
	res, _ := search.Execute(context.Background(), raw)
	if res.Output != "no matches" {
		t.Fatalf("unexpected: %q", res.Output)
	}
}

func TestMemoryListReturnsSortedEntries(t *testing.T) {
	store := newTestStore(t)
	if err := store.Write("sessions/b.md", []byte("x"), false); err != nil {
		t.Fatal(err)
	}
	if err := store.Write("sessions/a.md", []byte("x"), false); err != nil {
		t.Fatal(err)
	}
	list := NewMemoryListTool(store)
	raw, _ := json.Marshal(MemoryListParams{Dir: "sessions"})
	res, err := list.Execute(context.Background(), raw)
	if err != nil || res.IsError {
		t.Fatalf("list failed: %v / %s", err, res.Output)
	}
	idxA := strings.Index(res.Output, "a.md")
	idxB := strings.Index(res.Output, "b.md")
	if idxA < 0 || idxB < 0 || idxA > idxB {
		t.Fatalf("expected a.md before b.md: %q", res.Output)
	}
}
