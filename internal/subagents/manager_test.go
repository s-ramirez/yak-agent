package subagents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yak-go/internal/llm"
	"yak-go/internal/tools"
	"yak-go/internal/types"
)

type loggedClient struct {
	response string
}

func (c *loggedClient) Chat(_ context.Context, _ []types.Message, _ []types.ChatRequestTool) (*types.ChatResponse, error) {
	return &types.ChatResponse{
		Choices: []types.Choice{{
			Message: types.ResponseMessage{
				Role:    "assistant",
				Content: strPtr(c.response),
			},
		}},
	}, nil
}

func TestManagerLogsSubagentTranscripts(t *testing.T) {
	logDir := t.TempDir()
	manager, err := NewManager(
		func(def Definition) (llm.ChatClient, error) {
			return &loggedClient{response: "done"}, nil
		},
		logDir,
		[]Definition{{
			Name:   "scout",
			Model:  "small-model",
			Tools:  []string{"read"},
			Prompt: "You are Scout.",
		}},
		[]tools.Tool{testTool{name: "read"}},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	result, err := manager.Spawn(context.Background(), SpawnRequest{
		Agent: "scout",
		Task:  "inspect",
		Wait:  true,
	})
	if err != nil {
		t.Fatalf("Spawn returned error: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed status, got %q", result.Status)
	}

	subagentRoot := filepath.Join(logDir, "subagents")
	entries, err := os.ReadDir(subagentRoot)
	if err != nil {
		t.Fatalf("ReadDir returned error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one subagent log directory, got %d", len(entries))
	}
	if !strings.Contains(entries[0].Name(), "subagent-1_scout") {
		t.Fatalf("unexpected subagent log directory name: %q", entries[0].Name())
	}

	files, err := os.ReadDir(filepath.Join(subagentRoot, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadDir log session returned error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected request/response logs, got %d files", len(files))
	}
}
