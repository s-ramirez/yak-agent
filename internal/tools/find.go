package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const findDefaultLimit = 1000

type FindTool struct {
	definition ToolDefinition
}

type FindParams struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Limit   int    `json:"limit"`
}

func NewFindTool(extraGuidelines ...string) *FindTool {
	guidelines := []string{
		"Use find to locate files by name or extension pattern.",
		"Use path to narrow the search to a specific subdirectory.",
	}
	guidelines = append(guidelines, extraGuidelines...)

	return &FindTool{
		definition: ToolDefinition{
			Name:        "find",
			Description: "Search for files by glob pattern. Returns matching file paths relative to the search directory. Skips .git, node_modules, and other common noise directories. Output is limited to 1000 results by default.",
			Guidelines:  guidelines,
			Parameters: JSONSchema{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{"type": "string", "description": "Glob pattern to match files, e.g. '*.go', '**/*.json', or 'src/**/*.ts'"},
					"path":    map[string]any{"type": "string", "description": "Directory to search in (default: current directory)"},
					"limit":   map[string]any{"type": "number", "description": "Maximum number of results (default: 1000)"},
				},
				"required": []string{"pattern"},
			},
			SelectionRules: []SelectionRule{
				{Text: "Use find to locate files by name pattern instead of running find via bash."},
				{Text: "Prefer find over bash for any file search task. Only fall back to bash when you need features find does not support."},
			},
		},
	}
}

func (t *FindTool) Definition() ToolDefinition {
	return t.definition
}

func (t *FindTool) Execute(ctx context.Context, raw json.RawMessage) (ToolResult, error) {
	var params FindParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return errorResult("invalid JSON arguments"), nil
	}
	if params.Pattern == "" {
		return errorResult("pattern is required"), nil
	}

	searchPath := params.Path
	if searchPath == "" {
		var err error
		searchPath, err = os.Getwd()
		if err != nil {
			return errorResultf("failed to resolve working directory: %v", err), nil
		}
	}

	limit := params.Limit
	if limit <= 0 {
		limit = findDefaultLimit
	}

	info, err := os.Stat(searchPath)
	if err != nil {
		return errorResultf("path not found: %s", searchPath), nil
	}
	if !info.IsDir() {
		return errorResultf("not a directory: %s", searchPath), nil
	}

	var results []string
	limitReached := false

	_ = filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if info.IsDir() {
			if shouldSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, _ := filepath.Rel(searchPath, path)
		matched, _ := filepath.Match(params.Pattern, info.Name())
		if !matched {
			matched, _ = filepath.Match(params.Pattern, relPath)
		}
		if !matched {
			return nil
		}

		results = append(results, filepath.ToSlash(relPath))
		if len(results) >= limit {
			limitReached = true
			return fmt.Errorf("limit reached")
		}
		return nil
	})

	if len(results) == 0 {
		return ToolResult{Output: "No files found matching pattern"}, nil
	}

	output := strings.Join(results, "\n")
	if limitReached {
		output += fmt.Sprintf("\n\n[%d results limit reached. Use limit=%d for more, or refine pattern]", limit, limit*2)
	}

	return ToolResult{Output: output}, nil
}
