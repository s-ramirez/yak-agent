package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"yak-go/internal/memory"
)

// Memory tools are thin adapters over *memory.Store. All paths in tool
// params are relative to .yak/memory/. The store enforces sandboxing.

// ---------- memory_read ----------

type MemoryReadTool struct {
	store *memory.Store
}

var memoryReadDefinition = ToolDefinition{
	Name: "memory_read",
	Description: "Read a file from the agent's persistent memory store (.yak/memory/). " +
		"Paths are relative to the memory root (e.g. \"MEMORY.md\", \"sessions/2026-04-13-1422.md\", \"vault/Knowledge/foo.md\").",
	Guidelines: []string{
		"Use memory_read to recall prior context before answering questions about past sessions, user preferences, or durable facts.",
		"Paths must be relative to the memory root — never pass absolute paths.",
	},
	Parameters: JSONSchema{
		"type": "object",
		"properties": map[string]any{
			"path":   map[string]any{"type": "string", "description": "Path relative to .yak/memory/"},
			"offset": map[string]any{"type": "number", "description": "1-indexed line to start at (optional)"},
			"limit":  map[string]any{"type": "number", "description": "Maximum number of lines to return (optional)"},
		},
		"required": []string{"path"},
	},
}

type MemoryReadParams struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

func NewMemoryReadTool(store *memory.Store) *MemoryReadTool { return &MemoryReadTool{store: store} }

func (t *MemoryReadTool) Definition() ToolDefinition { return memoryReadDefinition }

func (t *MemoryReadTool) Execute(_ context.Context, raw json.RawMessage) (ToolResult, error) {
	var params MemoryReadParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return errorResult("invalid JSON arguments"), nil
	}
	data, err := t.store.Read(params.Path)
	if err != nil {
		return errorResultf("memory file not found or unreadable: %s", params.Path), nil
	}

	offset := params.Offset
	if offset < 1 {
		offset = 1
	}
	limit := params.Limit
	if limit <= 0 || limit > MaxReadLines {
		limit = MaxReadLines
	}

	lines := strings.Split(string(data), "\n")
	var out []string
	for i, line := range lines {
		lineNo := i + 1
		if lineNo < offset {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, fmt.Sprintf("%d\t%s", lineNo, line))
	}
	if len(out) == 0 {
		return ToolResult{Output: "(empty)"}, nil
	}
	return ToolResult{Output: strings.Join(out, "\n")}, nil
}

// ---------- memory_write ----------

type MemoryWriteTool struct {
	store *memory.Store
}

var memoryWriteDefinition = ToolDefinition{
	Name: "memory_write",
	Description: "Write or append to a file in the agent's persistent memory store (.yak/memory/). " +
		"Use this to save durable session notes, vault reference pages, or to update MEMORY.md during a distill flow. " +
		"Paths are relative to the memory root.",
	Guidelines: []string{
		"Save session notes worth keeping as sessions/YYYY-MM-DD-HHMM.md.",
		"Put permanent reference material under vault/Memory, vault/Knowledge, vault/Journal, or vault/Notes.",
		"Only overwrite MEMORY.md from an explicit distill flow — do not treat it as a scratchpad.",
		"Parent directories are created automatically.",
	},
	Parameters: JSONSchema{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "Path relative to .yak/memory/"},
			"content": map[string]any{"type": "string", "description": "Content to write"},
			"mode": map[string]any{
				"type":        "string",
				"description": "\"overwrite\" (default) or \"append\"",
				"enum":        []string{"overwrite", "append"},
			},
		},
		"required": []string{"path", "content"},
	},
}

type MemoryWriteParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    string `json:"mode"`
}

func NewMemoryWriteTool(store *memory.Store) *MemoryWriteTool {
	return &MemoryWriteTool{store: store}
}

func (t *MemoryWriteTool) Definition() ToolDefinition { return memoryWriteDefinition }

func (t *MemoryWriteTool) Execute(_ context.Context, raw json.RawMessage) (ToolResult, error) {
	var params MemoryWriteParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return errorResult("invalid JSON arguments"), nil
	}
	appendMode := false
	switch params.Mode {
	case "", "overwrite":
		appendMode = false
	case "append":
		appendMode = true
	default:
		return errorResultf("mode must be \"overwrite\" or \"append\", got %q", params.Mode), nil
	}
	if err := t.store.Write(params.Path, []byte(params.Content), appendMode); err != nil {
		return errorResultf("%v", err), nil
	}
	verb := "wrote"
	if appendMode {
		verb = "appended"
	}
	lineCount := 0
	if params.Content != "" {
		lineCount = strings.Count(params.Content, "\n") + 1
	}
	return ToolResult{Output: fmt.Sprintf("%s %d lines to memory/%s", verb, lineCount, params.Path)}, nil
}

// ---------- memory_search ----------

type MemorySearchTool struct {
	store *memory.Store
}

var memorySearchDefinition = ToolDefinition{
	Name:        "memory_search",
	Description: "Case-insensitive literal substring search across all markdown files in .yak/memory/. Returns path:line hits with the matching line as context.",
	Guidelines: []string{
		"Use memory_search to find prior mentions of a topic across MEMORY.md, sessions, and the vault.",
		"This is literal substring matching — not semantic search. Pick distinctive keywords.",
	},
	Parameters: JSONSchema{
		"type": "object",
		"properties": map[string]any{
			"query":       map[string]any{"type": "string", "description": "Substring to search for"},
			"max_results": map[string]any{"type": "number", "description": "Maximum number of hits (default 20)"},
		},
		"required": []string{"query"},
	},
}

type MemorySearchParams struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

func NewMemorySearchTool(store *memory.Store) *MemorySearchTool {
	return &MemorySearchTool{store: store}
}

func (t *MemorySearchTool) Definition() ToolDefinition { return memorySearchDefinition }

func (t *MemorySearchTool) Execute(_ context.Context, raw json.RawMessage) (ToolResult, error) {
	var params MemorySearchParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return errorResult("invalid JSON arguments"), nil
	}
	hits, err := t.store.Search(params.Query, params.MaxResults)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	if len(hits) == 0 {
		return ToolResult{Output: "no matches"}, nil
	}
	lines := make([]string, 0, len(hits))
	for _, h := range hits {
		lines = append(lines, fmt.Sprintf("%s:%d: %s", h.Path, h.Line, h.Snippet))
	}
	return ToolResult{Output: strings.Join(lines, "\n")}, nil
}

// ---------- memory_list ----------

type MemoryListTool struct {
	store *memory.Store
}

var memoryListDefinition = ToolDefinition{
	Name:        "memory_list",
	Description: "List entries in a directory under .yak/memory/. Pass an empty path (or omit it) to list the memory root.",
	Guidelines: []string{
		"Use memory_list to discover what session notes and vault files already exist before writing new ones.",
	},
	Parameters: JSONSchema{
		"type": "object",
		"properties": map[string]any{
			"dir": map[string]any{"type": "string", "description": "Directory relative to .yak/memory/ (empty = root)"},
		},
	},
}

type MemoryListParams struct {
	Dir string `json:"dir"`
}

func NewMemoryListTool(store *memory.Store) *MemoryListTool { return &MemoryListTool{store: store} }

func (t *MemoryListTool) Definition() ToolDefinition { return memoryListDefinition }

func (t *MemoryListTool) Execute(_ context.Context, raw json.RawMessage) (ToolResult, error) {
	var params MemoryListParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return errorResult("invalid JSON arguments"), nil
	}
	entries, err := t.store.List(params.Dir)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	if len(entries) == 0 {
		return ToolResult{Output: "(empty)"}, nil
	}
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		kind := "f"
		if e.IsDir {
			kind = "d"
		}
		lines = append(lines, fmt.Sprintf("%s %8d  %s  %s", kind, e.Size, e.Mtime.UTC().Format(time.RFC3339), e.Name))
	}
	return ToolResult{Output: strings.Join(lines, "\n")}, nil
}
