package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCuratedMissing(t *testing.T) {
	store := NewStore(t.TempDir())
	curated, err := store.LoadCurated()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if curated != "" {
		t.Fatalf("expected empty curated, got %q", curated)
	}
}

func TestLoadCuratedTruncates(t *testing.T) {
	base := t.TempDir()
	big := strings.Repeat("a", CuratedMaxBytes+500)
	if err := os.WriteFile(filepath.Join(base, CuratedFilename), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	curated, err := NewStore(base).LoadCurated()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(curated, "[truncated:") {
		t.Fatalf("expected truncation marker, got %q", curated[:80])
	}
}

func TestStoreWriteCreatesLayout(t *testing.T) {
	base := t.TempDir()
	store := NewStore(base)
	if err := store.Write("sessions/2026-04-13-1422.md", []byte("hello"), false); err != nil {
		t.Fatal(err)
	}
	for _, sub := range append([]string{SessionsDir}, vaultPaths()...) {
		if _, err := os.Stat(filepath.Join(base, sub)); err != nil {
			t.Fatalf("expected %s to exist: %v", sub, err)
		}
	}
	data, err := store.Read("sessions/2026-04-13-1422.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("unexpected content: %q", data)
	}
}

func vaultPaths() []string {
	out := make([]string, 0, len(VaultSubdirs))
	for _, s := range VaultSubdirs {
		out = append(out, filepath.Join(VaultDir, s))
	}
	return out
}

func TestStoreWriteAppend(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Write("MEMORY.md", []byte("line1\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := store.Write("MEMORY.md", []byte("line2\n"), true); err != nil {
		t.Fatal(err)
	}
	data, err := store.Read("MEMORY.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "line1\nline2\n" {
		t.Fatalf("unexpected content: %q", data)
	}
}

func TestStoreWriteRejectsEscape(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Write("../escape.md", []byte("x"), false); err == nil {
		t.Fatal("expected sandbox violation")
	}
}

func TestStoreSearch(t *testing.T) {
	base := t.TempDir()
	store := NewStore(base)
	if err := store.Write("MEMORY.md", []byte("User timezone is Europe/Madrid\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := store.Write("sessions/2026-04-13.md", []byte("did Go work\nlearned about madrid\n"), false); err != nil {
		t.Fatal(err)
	}

	hits, err := store.Search("madrid", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits (case insensitive), got %d: %+v", len(hits), hits)
	}
}

func TestStoreSearchRespectsMax(t *testing.T) {
	base := t.TempDir()
	store := NewStore(base)
	lines := strings.Repeat("foo\n", 10)
	if err := store.Write("MEMORY.md", []byte(lines), false); err != nil {
		t.Fatal(err)
	}
	hits, err := store.Search("foo", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("expected 3 hits, got %d", len(hits))
	}
}

func TestStoreList(t *testing.T) {
	base := t.TempDir()
	store := NewStore(base)
	if err := store.Write("sessions/a.md", []byte("x"), false); err != nil {
		t.Fatal(err)
	}
	if err := store.Write("sessions/b.md", []byte("y"), false); err != nil {
		t.Fatal(err)
	}
	entries, err := store.List("sessions")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "a.md" || entries[1].Name != "b.md" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}

func TestStoreListMissingReturnsEmpty(t *testing.T) {
	store := NewStore(t.TempDir())
	entries, err := store.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty, got %+v", entries)
	}
}
