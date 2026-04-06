package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type EditTool struct {
	fs FS
}

var editDefinition = ToolDefinition{
	Name: "edit",
	Description: "Edit a file by replacing exact text matches. Each edit's oldText must match a unique, non-overlapping region of the file. " +
		"All edits are matched against the original file content, not incrementally.",
	Guidelines: []string{
		"Use edit for precise, targeted changes to existing files.",
		"Keep oldText as small as possible while still being unique in the file.",
		"When changing multiple locations in one file, use one edit call with multiple entries in the edits array.",
	},
	Parameters: JSONSchema{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "Path to the file to edit"},
			"edits": map[string]any{
				"type":        "array",
				"description": "One or more targeted replacements. Each oldText must be unique in the file.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"oldText": map[string]any{"type": "string", "description": "Exact text to find (must be unique in the file)"},
						"newText": map[string]any{"type": "string", "description": "Replacement text"},
					},
					"required": []string{"oldText", "newText"},
				},
			},
		},
		"required": []string{"path", "edits"},
	},
}

type EditParams struct {
	Path  string        `json:"path"`
	Edits []EditRequest `json:"edits"`
}

type EditRequest struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

type matchedEdit struct {
	editIndex   int
	matchIndex  int
	matchLength int
	newText     string
}

func NewEditTool(fs FS) *EditTool {
	return &EditTool{fs: fs}
}

func (t *EditTool) Definition() ToolDefinition {
	return editDefinition
}

func (t *EditTool) Execute(ctx context.Context, raw json.RawMessage) (ToolResult, error) {
	_ = ctx

	var params EditParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return errorResult("error: invalid JSON arguments"), nil
	}
	if params.Path == "" {
		return errorResult("error: path is required"), nil
	}
	if len(params.Edits) == 0 {
		return errorResult("error: edits must be a non-empty array"), nil
	}

	for i, edit := range params.Edits {
		if edit.OldText == "" {
			return errorResultf("error: edits[%d].oldText must not be empty", i), nil
		}
	}

	if _, err := t.fs.Stat(params.Path); err != nil {
		return errorResultf("error: file not found or not readable: %s", params.Path), nil
	}

	rawFile, err := t.fs.ReadFile(params.Path)
	if err != nil {
		return errorResultf("error: failed to read file: %v", err), nil
	}
	content := string(rawFile)

	matched := make([]matchedEdit, 0, len(params.Edits))
	for i, edit := range params.Edits {
		index := strings.Index(content, edit.OldText)
		if index == -1 {
			return errorResultf("error: edits[%d].oldText not found in %s", i, params.Path), nil
		}

		secondIndex := strings.Index(content[index+1:], edit.OldText)
		if secondIndex != -1 {
			return errorResultf("error: edits[%d].oldText matches multiple locations in %s. Provide more context to make it unique.", i, params.Path), nil
		}

		matched = append(matched, matchedEdit{
			editIndex:   i,
			matchIndex:  index,
			matchLength: len(edit.OldText),
			newText:     edit.NewText,
		})
	}

	sort.Slice(matched, func(i, j int) bool {
		return matched[i].matchIndex < matched[j].matchIndex
	})

	for i := 1; i < len(matched); i++ {
		prev := matched[i-1]
		curr := matched[i]
		if prev.matchIndex+prev.matchLength > curr.matchIndex {
			return errorResultf("error: edits[%d] and edits[%d] overlap. Merge them into one edit.", prev.editIndex, curr.editIndex), nil
		}
	}

	result := content
	for i := len(matched) - 1; i >= 0; i-- {
		edit := matched[i]
		result = result[:edit.matchIndex] + edit.newText + result[edit.matchIndex+edit.matchLength:]
	}

	if result == content {
		return errorResultf("error: no changes made to %s", params.Path), nil
	}

	if err := t.fs.WriteFile(params.Path, []byte(result), 0o644); err != nil {
		return errorResultf("error: failed to write file: %v", err), nil
	}

	count := len(matched)
	suffix := ""
	if count != 1 {
		suffix = "s"
	}

	return ToolResult{
		Output: fmt.Sprintf("applied %d edit%s to %s", count, suffix, params.Path),
	}, nil
}
