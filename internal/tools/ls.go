package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const lsDefaultLimit = 500

type LsTool struct {
	fs FS
}

var lsDefinition = ToolDefinition{
	Name:        "ls",
	Description: "List directory contents. Returns entries sorted alphabetically, with '/' suffix for directories. Includes dotfiles. Output is limited to 500 entries by default.",
	Guidelines: []string{
		"Use ls to explore directory structure.",
		"Use limit to control how many entries are returned for large directories.",
	},
	Parameters: JSONSchema{
		"type": "object",
		"properties": map[string]any{
			"path":  map[string]any{"type": "string", "description": "Directory to list (default: current directory)"},
			"limit": map[string]any{"type": "number", "description": "Maximum number of entries to return (default: 500)"},
		},
	},
}

type LsParams struct {
	Path  string `json:"path"`
	Limit int    `json:"limit"`
}

func NewLsTool(fs FS) *LsTool {
	return &LsTool{fs: fs}
}

func (t *LsTool) Definition() ToolDefinition {
	return lsDefinition
}

func (t *LsTool) Execute(ctx context.Context, raw json.RawMessage) (ToolResult, error) {
	_ = ctx

	var params LsParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return errorResult("error: invalid JSON arguments"), nil
	}

	dirPath := params.Path
	if dirPath == "" {
		dirPath = "."
	}

	limit := params.Limit
	if limit <= 0 {
		limit = lsDefaultLimit
	}

	info, err := t.fs.Stat(dirPath)
	if err != nil {
		return errorResultf("error: path not found: %s", dirPath), nil
	}
	if !info.IsDir() {
		return errorResultf("error: not a directory: %s", dirPath), nil
	}

	dir, err := t.fs.Open(dirPath)
	if err != nil {
		return errorResultf("error: cannot read directory: %v", err), nil
	}
	defer dir.Close()

	entries, err := dir.Readdirnames(-1)
	if err != nil {
		return errorResultf("error: cannot read directory: %v", err), nil
	}

	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i]) < strings.ToLower(entries[j])
	})

	var results []string
	limitReached := false
	for _, name := range entries {
		if len(results) >= limit {
			limitReached = true
			break
		}
		fullPath := dirPath + "/" + name
		info, err := t.fs.Stat(fullPath)
		if err != nil {
			continue
		}
		if info.IsDir() {
			results = append(results, name+"/")
		} else {
			results = append(results, name)
		}
	}

	if len(results) == 0 {
		return ToolResult{Output: "(empty directory)"}, nil
	}

	output := strings.Join(results, "\n")
	if limitReached {
		output += fmt.Sprintf("\n\n[%d entries limit reached. Use limit=%d for more]", limit, limit*2)
	}

	return ToolResult{Output: output}, nil
}
