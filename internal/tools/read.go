package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const MaxReadLines = 2000
const scannerBufferSize = 1024 * 1024

type ReadTool struct {
	fs FS
}

var readDefinition = ToolDefinition{
	Name:        "read",
	Description: "Read the contents of a file. Supports an optional line offset and limit.",
	Guidelines: []string{
		"Always read a file before editing it.",
		"Use offset and limit for large files instead of reading the entire file.",
	},
	Parameters: JSONSchema{
		"type": "object",
		"properties": map[string]any{
			"path":   map[string]any{"type": "string", "description": "Path to the file to read"},
			"offset": map[string]any{"type": "number", "description": "Line number to start reading from (1-indexed)"},
			"limit":  map[string]any{"type": "number", "description": "Maximum number of lines to read"},
		},
		"required": []string{"path"},
	},
}

type ReadParams struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

func NewReadTool(fs FS) *ReadTool {
	return &ReadTool{fs: fs}
}

func (t *ReadTool) Definition() ToolDefinition {
	return readDefinition
}

func (t *ReadTool) Execute(_ context.Context, raw json.RawMessage) (ToolResult, error) {
	var params ReadParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return errorResult("invalid JSON arguments"), nil
	}
	if params.Path == "" {
		return errorResult("path is required"), nil
	}

	if _, err := t.fs.Stat(params.Path); err != nil {
		return errorResultf("file not found or not readable: %s", params.Path), nil
	}

	offset := params.Offset
	if offset < 1 {
		offset = 1
	}

	requested := params.Limit
	limit := requested
	if limit <= 0 || limit > MaxReadLines {
		limit = MaxReadLines
	}
	capByMax := requested <= 0 || requested > MaxReadLines

	file, err := t.fs.Open(params.Path)
	if err != nil {
		return errorResultf("failed to read file: %v", err), nil
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), scannerBufferSize)

	initialCap := limit
	if initialCap > 128 {
		initialCap = 128
	}
	lines := make([]string, 0, initialCap)
	lineNo := 0
	truncated := false

	for scanner.Scan() {
		lineNo++
		if lineNo < offset {
			continue
		}
		if len(lines) == limit {
			truncated = capByMax
			break
		}
		lines = append(lines, fmt.Sprintf("%d\t%s", lineNo, scanner.Text()))
	}

	if err := scanner.Err(); err != nil {
		return errorResultf("failed to read file: %v", err), nil
	}

	output := strings.Join(lines, "\n")
	if truncated {
		if output != "" {
			output += "\n\n"
		}
		if requested > MaxReadLines {
			output += fmt.Sprintf("[truncated: showing %d of %d requested lines]", MaxReadLines, requested)
		} else {
			output += fmt.Sprintf("[truncated: showing %d lines]", limit)
		}
	}

	return ToolResult{Output: output}, nil
}
