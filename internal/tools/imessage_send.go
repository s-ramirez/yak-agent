package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	iMsgReBold          = regexp.MustCompile(`\*\*(.+?)\*\*|__(.+?)__`)
	iMsgReItalic        = regexp.MustCompile(`\*(.+?)\*|_(.+?)_`)
	iMsgReStrikethrough = regexp.MustCompile(`~~(.+?)~~`)
	iMsgReHeader        = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	iMsgReBlockquote    = regexp.MustCompile(`(?m)^>\s?`)
	iMsgReHR            = regexp.MustCompile(`(?m)^(\*{3,}|-{3,}|_{3,})\s*$`)
	iMsgReInlineCode    = regexp.MustCompile("`(.+?)`")
	iMsgReExtraNewlines = regexp.MustCompile(`\n{3,}`)
)

func iMsgStripMarkdown(s string) string {
	s = iMsgReHR.ReplaceAllString(s, "")
	s = iMsgReHeader.ReplaceAllString(s, "")
	s = iMsgReBlockquote.ReplaceAllString(s, "")
	s = iMsgReBold.ReplaceAllStringFunc(s, func(m string) string {
		g := iMsgReBold.FindStringSubmatch(m)
		if g[1] != "" {
			return g[1]
		}
		return g[2]
	})
	s = iMsgReItalic.ReplaceAllStringFunc(s, func(m string) string {
		g := iMsgReItalic.FindStringSubmatch(m)
		if g[1] != "" {
			return g[1]
		}
		return g[2]
	})
	s = iMsgReStrikethrough.ReplaceAllStringFunc(s, func(m string) string {
		return iMsgReStrikethrough.FindStringSubmatch(m)[1]
	})
	s = iMsgReInlineCode.ReplaceAllStringFunc(s, func(m string) string {
		return iMsgReInlineCode.FindStringSubmatch(m)[1]
	})
	s = iMsgReExtraNewlines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// IMessageSendConfig holds the BlueBubbles connection details needed to send
// a message. It mirrors the subset of imessage.Config required for sending.
type IMessageSendConfig struct {
	ServerURL string
	Password  string
}

// IMessageSendTool lets the agent proactively send an iMessage to any
// recipient reachable via the local BlueBubbles server.
type IMessageSendTool struct {
	cfg IMessageSendConfig
}

var iMessageSendDefinition = ToolDefinition{
	Name:        "imessage_send",
	Description: "Send an iMessage to a phone number, email address, or existing chat via BlueBubbles.",
	Guidelines: []string{
		"Use imessage_send to proactively notify the user or reply outside the current conversation thread.",
		"The 'to' field accepts a phone number (+15551234567), email address, or a full chatGuid (e.g. iMessage;-;+15551234567).",
		"Keep messages concise — iMessage does not render Markdown, so avoid using it.",
	},
	Parameters: JSONSchema{
		"type": "object",
		"properties": map[string]any{
			"to": map[string]any{
				"type":        "string",
				"description": "Recipient: phone number, email, or BlueBubbles chatGuid",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "Message text to send (plain text; Markdown will be stripped)",
			},
		},
		"required": []string{"to", "message"},
	},
}

// IMessageSendParams is the parameter struct for imessage_send.
type IMessageSendParams struct {
	To      string `json:"to"`
	Message string `json:"message"`
}

// NewIMessageSendTool returns a new IMessageSendTool wired to the given config.
func NewIMessageSendTool(cfg IMessageSendConfig) *IMessageSendTool {
	return &IMessageSendTool{cfg: cfg}
}

func (t *IMessageSendTool) Definition() ToolDefinition {
	return iMessageSendDefinition
}

func (t *IMessageSendTool) Execute(ctx context.Context, raw json.RawMessage) (ToolResult, error) {
	var params IMessageSendParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return errorResult("invalid JSON arguments"), nil
	}
	if strings.TrimSpace(params.To) == "" {
		return errorResult("'to' is required"), nil
	}
	if strings.TrimSpace(params.Message) == "" {
		return errorResult("'message' is required"), nil
	}

	text := iMsgStripMarkdown(params.Message)
	if text == "" {
		return errorResult("message is empty after stripping Markdown"), nil
	}


	chatGUID, err := resolveOrBuildChatGUID(ctx, t.cfg, params.To)
	if err != nil {
		return errorResultf("failed to resolve recipient: %v", err), nil
	}

	if err := bbSendMessage(ctx, t.cfg, chatGUID, text); err != nil {
		return errorResultf("send failed: %v", err), nil
	}

	return ToolResult{Output: fmt.Sprintf("Message sent to %s", params.To)}, nil
}

// resolveOrBuildChatGUID converts a user-supplied 'to' value into a chatGuid
// suitable for the BlueBubbles API. If the value already looks like a chatGuid
// it is returned as-is.
func resolveOrBuildChatGUID(ctx context.Context, cfg IMessageSendConfig, to string) (string, error) {
	// Already a full chatGuid.
	if strings.Contains(to, ";") {
		return to, nil
	}
	// Otherwise, construct a DM chatGuid directly.
	// BlueBubbles accepts "iMessage;-;<handle>" for DM creation.
	handle := strings.TrimSpace(to)
	return "iMessage;-;" + handle, nil
}

// bbSendMessage POSTs a text message to the BlueBubbles REST API.
func bbSendMessage(ctx context.Context, cfg IMessageSendConfig, chatGUID, text string) error {
	type payload struct {
		ChatGUID string `json:"chatGuid"`
		TempGUID string `json:"tempGuid"`
		Message  string `json:"message"`
	}
	p := payload{
		ChatGUID: chatGUID,
		TempGUID: fmt.Sprintf("tmp-%d", time.Now().UnixNano()),
		Message:  text,
	}
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}

	u := strings.TrimRight(cfg.ServerURL, "/") + "/api/v1/message/text?password=" + cfg.Password
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("bluebubbles: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("bluebubbles: status %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	return nil
}

