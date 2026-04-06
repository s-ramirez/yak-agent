package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"yak-go/internal/tools"
	"yak-go/internal/types"
)

type fakeIO struct {
	lines   []string
	writes  []string
	readErr error
	index   int
}

func (f *fakeIO) Write(text string) error {
	f.writes = append(f.writes, text)
	return nil
}

func (f *fakeIO) ReadLine(ctx context.Context) (string, error) {
	_ = ctx
	if f.index >= len(f.lines) {
		if f.readErr != nil {
			return "", f.readErr
		}
		return "", io.EOF
	}
	line := f.lines[f.index]
	f.index++
	return line, nil
}

type stubClient struct {
	responses []*types.ChatResponse
	errors    []error
	calls     int
}

func (s *stubClient) Chat(ctx context.Context, messages []types.Message, tools []types.ChatRequestTool) (*types.ChatResponse, error) {
	_ = ctx
	_ = messages
	_ = tools
	if s.calls < len(s.errors) && s.errors[s.calls] != nil {
		err := s.errors[s.calls]
		s.calls++
		return nil, err
	}
	if s.calls >= len(s.responses) {
		return nil, errors.New("unexpected chat call")
	}
	resp := s.responses[s.calls]
	s.calls++
	return resp, nil
}

func strPtr(value string) *string {
	return &value
}

func TestRunnerRunPrintsAssistantText(t *testing.T) {
	ioStub := &fakeIO{lines: []string{"hello"}}
	client := &stubClient{
		responses: []*types.ChatResponse{
			{
				Choices: []types.Choice{{
					Message: types.ResponseMessage{
						Role:    "assistant",
						Content: strPtr("hi there"),
					},
				}},
			},
		},
	}

	runner := Runner{
		Client:   client,
		IO:       ioStub,
		Registry: tools.NewRegistry(),
	}

	err := runner.Run(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}

	got := strings.Join(ioStub.writes, "")
	if got != "> hi there\n> " {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestRunnerRunExecutesToolCalls(t *testing.T) {
	ioStub := &fakeIO{lines: []string{"read file"}}
	args, _ := json.Marshal(map[string]any{
		"path":    "/tmp/example.txt",
		"content": "hello\nworld",
	})

	client := &stubClient{
		responses: []*types.ChatResponse{
			{
				Choices: []types.Choice{{
					Message: types.ResponseMessage{
						Role:    "assistant",
						Content: strPtr(""),
						ToolCalls: []types.ToolCall{
							{
								ID:   "call-1",
								Type: "function",
								Function: types.ToolCallFunction{
									Name:      "write",
									Arguments: string(args),
								},
							},
						},
					},
				}},
			},
			{
				Choices: []types.Choice{{
					Message: types.ResponseMessage{
						Role:    "assistant",
						Content: strPtr("done"),
					},
				}},
			},
		},
	}

	runner := Runner{
		Client: client,
		IO:     ioStub,
		Registry: tools.NewRegistry(
			tools.NewWriteTool(tools.OSFS{}),
		),
	}

	tempDir := t.TempDir()
	targetPath := tempDir + "/example.txt"
	args, _ = json.Marshal(map[string]any{
		"path":    targetPath,
		"content": "hello\nworld",
	})
	client.responses[0].Choices[0].Message.ToolCalls[0].Function.Arguments = string(args)

	err := runner.Run(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}

	got := strings.Join(ioStub.writes, "")
	if got != "> done\n> " {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestRunnerRunPrintsClientErrorsAndContinues(t *testing.T) {
	ioStub := &fakeIO{lines: []string{"first", "second"}}
	client := &stubClient{
		errors: []error{
			errors.New("boom"),
			nil,
		},
		responses: []*types.ChatResponse{
			nil,
			{
				Choices: []types.Choice{{
					Message: types.ResponseMessage{
						Role:    "assistant",
						Content: strPtr("second response"),
					},
				}},
			},
		},
	}

	runner := Runner{
		Client:   client,
		IO:       ioStub,
		Registry: tools.NewRegistry(),
	}

	err := runner.Run(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}

	got := strings.Join(ioStub.writes, "")
	if got != "> error: boom\n> second response\n> " {
		t.Fatalf("unexpected output: %q", got)
	}
}
