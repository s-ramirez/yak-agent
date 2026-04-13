package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

type WriteTool struct {
	fs FS
}

var writeDefinition = ToolDefinition{
	Name:        "write",
	Description: "Create a new file or completely overwrite an existing file. Parent directories are created automatically.",
	Guidelines: []string{
		"Only use write to create new files or for complete rewrites.",
		"Do not use write to make small changes to existing files.",
	},
	Parameters: JSONSchema{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "Path to the file to write"},
			"content": map[string]any{"type": "string", "description": "Content to write to the file"},
		},
		"required": []string{"path", "content"},
	},
}

type WriteParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func NewWriteTool(fs FS) *WriteTool {
	return &WriteTool{fs: fs}
}

func (t *WriteTool) Definition() ToolDefinition {
	return writeDefinition
}

func (t *WriteTool) Execute(_ context.Context, raw json.RawMessage) (ToolResult, error) {
	var params WriteParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return errorResult("invalid JSON arguments"), nil
	}
	if params.Path == "" {
		return errorResult("path is required"), nil
	}

	dir := filepath.Dir(params.Path)
	if dir != "." && dir != "" {
		if err := t.fs.MkdirAll(dir, 0o755); err != nil {
			return errorResultf("failed to write file: %v", err), nil
		}
	}

	if err := t.fs.WriteFile(params.Path, []byte(params.Content), 0o644); err != nil {
		return errorResultf("failed to write file: %v", err), nil
	}

	lineCount := 0
	if params.Content != "" {
		lineCount = strings.Count(params.Content, "\n") + 1
	}

	return ToolResult{
		Output: fmt.Sprintf("wrote %d lines to %s", lineCount, params.Path),
	}, nil
}
