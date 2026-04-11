package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"yak-go/internal/types"
)

type ChatClient interface {
	Chat(ctx context.Context, messages []types.Message, tools []types.ChatRequestTool) (*types.ChatResponse, error)
}

type Client struct {
	baseURL    string
	model      string
	apiKey     string
	httpClient *http.Client
}

type Options struct {
	Timeout time.Duration
	APIKey  string
}

type ClientError struct {
	Status int
	Body   string
}

func (e *ClientError) Error() string {
	return fmt.Sprintf("API error (%d): %s", e.Status, e.Body)
}

func NewClient(baseURL, model string, opts *Options) *Client {
	timeout := 300 * time.Second
	if opts != nil && opts.Timeout > 0 {
		timeout = opts.Timeout
	}

	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		apiKey:  strings.TrimSpace(optsAPIKey(opts)),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func optsAPIKey(opts *Options) string {
	if opts == nil {
		return ""
	}
	return opts.APIKey
}

func (c *Client) Chat(ctx context.Context, messages []types.Message, tools []types.ChatRequestTool) (*types.ChatResponse, error) {
	requestBody := types.ChatRequest{
		Model:    c.model,
		Messages: messages,
		Tools:    tools,
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, &ClientError{
			Status: resp.StatusCode,
			Body:   string(raw),
		}
	}

	var decoded types.ChatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&decoded); err != nil {
		return nil, err
	}

	return &decoded, nil
}
