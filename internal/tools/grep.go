package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const grepDefaultLimit = 100
const grepMaxLineLength = 500

type GrepTool struct {
	definition ToolDefinition
}

type GrepParams struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path"`
	Glob       string `json:"glob"`
	IgnoreCase bool   `json:"ignoreCase"`
	Literal    bool   `json:"literal"`
	Limit      int    `json:"limit"`
}

func NewGrepTool(extraGuidelines ...string) *GrepTool {
	guidelines := []string{
		"Use grep to search for patterns across files in the project.",
		"Use the glob parameter to narrow the search to specific file types.",
		"Use ignoreCase for case-insensitive searches.",
	}
	guidelines = append(guidelines, extraGuidelines...)

	return &GrepTool{
		definition: ToolDefinition{
			Name:        "grep",
			Description: "Search file contents for a pattern. Returns matching lines with file paths and line numbers. Respects .gitignore when possible. Output is limited to 100 matches by default.",
			Guidelines:  guidelines,
			Parameters: JSONSchema{
				"type": "object",
				"properties": map[string]any{
					"pattern":    map[string]any{"type": "string", "description": "Search pattern (regex or literal string)"},
					"path":       map[string]any{"type": "string", "description": "Directory or file to search (default: current directory)"},
					"glob":       map[string]any{"type": "string", "description": "Filter files by extension, e.g. '*.go' or '*.ts'"},
					"ignoreCase": map[string]any{"type": "boolean", "description": "Case-insensitive search (default: false)"},
					"literal":    map[string]any{"type": "boolean", "description": "Treat pattern as a literal string instead of regex (default: false)"},
					"limit":      map[string]any{"type": "number", "description": "Maximum number of matches to return (default: 100)"},
				},
				"required": []string{"pattern"},
			},
		},
	}
}

func (t *GrepTool) Definition() ToolDefinition {
	return t.definition
}

func (t *GrepTool) Execute(ctx context.Context, raw json.RawMessage) (ToolResult, error) {
	var params GrepParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return errorResult("error: invalid JSON arguments"), nil
	}
	if params.Pattern == "" {
		return errorResult("error: pattern is required"), nil
	}

	searchPath := params.Path
	if searchPath == "" {
		var err error
		searchPath, err = os.Getwd()
		if err != nil {
			return errorResultf("error: failed to resolve working directory: %v", err), nil
		}
	}

	limit := params.Limit
	if limit <= 0 {
		limit = grepDefaultLimit
	}

	patternStr := params.Pattern
	if params.Literal {
		patternStr = regexp.QuoteMeta(patternStr)
	}
	if params.IgnoreCase {
		patternStr = "(?i)" + patternStr
	}
	re, err := regexp.Compile(patternStr)
	if err != nil {
		return errorResultf("error: invalid regex pattern: %v", err), nil
	}

	info, err := os.Stat(searchPath)
	if err != nil {
		return errorResultf("error: path not found: %s", searchPath), nil
	}

	var matches []string
	limitReached := false

	if !info.IsDir() {
		matches, limitReached = grepFile(searchPath, "", re, limit)
	} else {
		matches, limitReached = grepDirectory(ctx, searchPath, re, params.Glob, limit)
	}

	if len(matches) == 0 {
		return ToolResult{Output: "No matches found"}, nil
	}

	output := strings.Join(matches, "\n")
	if limitReached {
		output += fmt.Sprintf("\n\n[%d matches limit reached. Use limit=%d for more, or refine pattern]", limit, limit*2)
	}

	return ToolResult{Output: output}, nil
}

func grepDirectory(ctx context.Context, root string, re *regexp.Regexp, globPattern string, limit int) ([]string, bool) {
	var matches []string
	limitReached := false

	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if info.IsDir() {
			base := info.Name()
			if base == ".git" || base == "node_modules" || base == "__pycache__" || base == ".cache" {
				return filepath.SkipDir
			}
			return nil
		}

		if globPattern != "" {
			matched, _ := filepath.Match(globPattern, info.Name())
			if !matched {
				return nil
			}
		}

		relPath, _ := filepath.Rel(root, path)
		fileMatches, fileLimitReached := grepFile(path, relPath, re, limit-len(matches))
		matches = append(matches, fileMatches...)
		if fileLimitReached || len(matches) >= limit {
			limitReached = true
			return fmt.Errorf("limit reached")
		}
		return nil
	})

	return matches, limitReached
}

func grepFile(filePath string, displayPath string, re *regexp.Regexp, remaining int) ([]string, bool) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	if displayPath == "" {
		displayPath = filepath.Base(filePath)
	}

	var matches []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0

	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if re.MatchString(line) {
			display := line
			if len(display) > grepMaxLineLength {
				display = display[:grepMaxLineLength] + "... [truncated]"
			}
			matches = append(matches, fmt.Sprintf("%s:%d: %s", displayPath, lineNo, display))
			if len(matches) >= remaining {
				return matches, true
			}
		}
	}

	return matches, false
}
