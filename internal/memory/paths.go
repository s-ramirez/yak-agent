package memory

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SandboxPath resolves rel against base and returns an absolute path
// guaranteed to live under base. Rejects absolute paths, empty input,
// and anything that escapes the base after cleaning.
func SandboxPath(base, rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("path must be relative to memory root")
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path must stay inside memory root")
	}

	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(absBase, clean)

	relCheck, err := filepath.Rel(absBase, joined)
	if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path must stay inside memory root")
	}
	return joined, nil
}
