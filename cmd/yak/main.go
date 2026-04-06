package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"yak-go/internal/cli"
	"yak-go/internal/llm"
	"yak-go/internal/tools"
)

type stdio struct {
	reader *bufio.Reader
	writer io.Writer
}

func (s *stdio) Write(text string) error {
	_, err := io.WriteString(s.writer, text)
	return err
}

func (s *stdio) ReadLine(ctx context.Context) (string, error) {
	_ = ctx

	line, err := s.reader.ReadString('\n')
	if err != nil {
		if err == io.EOF && len(line) > 0 {
			return strings.TrimRight(line, "\r\n"), nil
		}
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func main() {
	baseURL := os.Getenv("YAK_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:1234"
	}

	model := os.Getenv("YAK_MODEL")
	if model == "" {
		model = "default"
	}

	client := llm.NewClient(baseURL, model, &llm.Options{
		Timeout: 60 * time.Second,
	})

	registry := tools.NewRegistry(
		tools.NewReadTool(tools.OSFS{}),
		tools.NewWriteTool(tools.OSFS{}),
		tools.NewEditTool(tools.OSFS{}),
		tools.NewBashTool(),
		tools.NewGrepTool(),
		tools.NewLsTool(tools.OSFS{}),
		tools.NewFindTool(),
	)

	registry.AddHook(&logHook{writer: os.Stderr})

	runner := cli.Runner{
		Client:   client,
		IO:       &stdio{reader: bufio.NewReader(os.Stdin), writer: os.Stdout},
		Registry: registry,
	}

	if err := runner.Run(context.Background()); err != nil && err != io.EOF {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

type logHook struct {
	writer io.Writer
}

func (h *logHook) BeforeToolCall(name string, _ json.RawMessage) string {
	fmt.Fprintf(h.writer, "[START] %s tool\n", strings.ToUpper(name))
	return ""
}

func (h *logHook) AfterToolCall(name string, result tools.ToolResult, err error) {
	status := "OK"
	if err != nil || result.IsError {
		status = "ERROR"
	}
	fmt.Fprintf(h.writer, "[DONE]  %s tool (%s)\n", strings.ToUpper(name), status)
}
