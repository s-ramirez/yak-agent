package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"yak-go/internal/cli"
	"yak-go/internal/llm"
	"yak-go/internal/plugin"
	"yak-go/internal/plugin/tilldone"
	"yak-go/internal/skills"
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

	apiKey := os.Getenv("YAK_API_KEY")

	client := llm.NewClient(baseURL, model, &llm.Options{
		Timeout: 60 * time.Second,
		APIKey:  apiKey,
	})

	var chatClient llm.ChatClient = client
	logDir := os.Getenv("YAK_LOG_DIR")
	if logDir != "" {
		sessionDir := filepath.Join(logDir, time.Now().Format("20060102-150405"))
		lc, err := llm.NewLoggingClient(client, sessionDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to set up logging: %v\n", err)
		} else {
			chatClient = lc
			fmt.Fprintf(os.Stderr, "Logging to %s\n", sessionDir)
		}
	}

	// Initialize plugins.
	plugins := []plugin.Plugin{
		tilldone.New(),
	}

	api := plugin.API{
		Log: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "[plugin] "+format+"\n", args...)
		},
	}
	for _, p := range plugins {
		p.Init(api)
	}

	// Collect built-in + plugin tools.
	builtinTools := []tools.Tool{
		tools.NewReadTool(tools.OSFS{}),
		tools.NewWriteTool(tools.OSFS{}),
		tools.NewEditTool(tools.OSFS{}),
		tools.NewBashTool(),
		tools.NewGrepTool(),
		tools.NewLsTool(tools.OSFS{}),
		tools.NewFindTool(),
	}
	for _, p := range plugins {
		builtinTools = append(builtinTools, p.Tools()...)
	}

	registry := tools.NewRegistry(builtinTools...)
	registry.AddHook(&logHook{writer: os.Stderr})
	for _, p := range plugins {
		for _, h := range p.Hooks() {
			registry.AddHook(h)
		}
	}

	// Collect after-turn hooks and prompt sections from plugins.
	var afterTurnHooks []plugin.AfterTurnHook
	var pluginPrompts []string
	for _, p := range plugins {
		if ath, ok := p.(plugin.AfterTurnHook); ok {
			afterTurnHooks = append(afterTurnHooks, ath)
		}
		if s := p.SystemPromptSection(); s != "" {
			pluginPrompts = append(pluginPrompts, s)
		}
	}

	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	skillDirs := []string{
		filepath.Join(home, ".yak", "skills"),
		filepath.Join(cwd, ".yak", "skills"),
	}
	loadedSkills, diags, err := skills.LoadSkills(skillDirs...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: loading skills: %v\n", err)
	}
	for _, d := range diags {
		fmt.Fprintf(os.Stderr, "warning: %s\n", d)
	}

	runner := cli.Runner{
		Client:         chatClient,
		IO:             &stdio{reader: bufio.NewReader(os.Stdin), writer: os.Stdout},
		Registry:       registry,
		Skills:         loadedSkills,
		AfterTurnHooks: afterTurnHooks,
		PluginPrompts:  pluginPrompts,
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
