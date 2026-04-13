package memory

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSandboxPathAcceptsRelativeChildren(t *testing.T) {
	base := t.TempDir()
	cases := []string{
		"MEMORY.md",
		"sessions/2026-04-13-1422.md",
		"vault/Knowledge/go.md",
		"./MEMORY.md",
	}
	for _, rel := range cases {
		got, err := SandboxPath(base, rel)
		if err != nil {
			t.Fatalf("SandboxPath(%q) unexpected error: %v", rel, err)
		}
		if !strings.HasPrefix(got, base) {
			t.Fatalf("SandboxPath(%q) = %q, expected prefix %q", rel, got, base)
		}
	}
}

func TestSandboxPathRejectsEscapes(t *testing.T) {
	base := t.TempDir()
	cases := []string{
		"",
		"..",
		"../etc/passwd",
		"sessions/../../secret",
		filepath.Join(base, "MEMORY.md"), // absolute path
		"/etc/passwd",
	}
	for _, rel := range cases {
		if _, err := SandboxPath(base, rel); err == nil {
			t.Fatalf("SandboxPath(%q) should have errored", rel)
		}
	}
}
