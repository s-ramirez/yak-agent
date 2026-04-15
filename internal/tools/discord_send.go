package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// DiscordSendConfig holds the bot token used for REST sends.
type DiscordSendConfig struct {
	Token string
}

// DiscordSendTool lets the agent proactively send a Discord message to a
// channel or user.
type DiscordSendTool struct {
	cfg     DiscordSendConfig
	session *discordgo.Session
}

var discordSendDefinition = ToolDefinition{
	Name:        "discord_send",
	Description: "Send a message to a Discord channel or user via the configured bot.",
	Guidelines: []string{
		"Use discord_send to proactively notify the user or post to a channel outside the current conversation thread.",
		"Set kind='channel' (default) to post to a guild or DM channel by ID. Set kind='user' to send a DM to a user by ID — a DM channel is opened automatically.",
		"Discord's per-message limit is 2000 characters; longer messages are split automatically.",
	},
	Parameters: JSONSchema{
		"type": "object",
		"properties": map[string]any{
			"to": map[string]any{
				"type":        "string",
				"description": "Discord snowflake ID — either a channel ID or a user ID depending on 'kind'.",
			},
			"kind": map[string]any{
				"type":        "string",
				"enum":        []string{"channel", "user"},
				"description": "Whether 'to' is a channel ID or a user ID. Defaults to 'channel'.",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "Message text to send.",
			},
		},
		"required": []string{"to", "message"},
	},
}

// DiscordSendParams is the parameter struct for discord_send.
type DiscordSendParams struct {
	To      string `json:"to"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// NewDiscordSendTool returns a new DiscordSendTool. The returned tool lazily
// constructs a REST-only discordgo session on first use.
func NewDiscordSendTool(cfg DiscordSendConfig) *DiscordSendTool {
	return &DiscordSendTool{cfg: cfg}
}

func (t *DiscordSendTool) Definition() ToolDefinition {
	return discordSendDefinition
}

func (t *DiscordSendTool) Execute(ctx context.Context, raw json.RawMessage) (ToolResult, error) {
	var params DiscordSendParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return errorResult("invalid JSON arguments"), nil
	}
	if strings.TrimSpace(params.To) == "" {
		return errorResult("'to' is required"), nil
	}
	if strings.TrimSpace(params.Message) == "" {
		return errorResult("'message' is required"), nil
	}
	kind := params.Kind
	if kind == "" {
		kind = "channel"
	}
	if kind != "channel" && kind != "user" {
		return errorResultf("invalid kind %q (must be 'channel' or 'user')", kind), nil
	}

	if t.session == nil {
		s, err := discordgo.New("Bot " + t.cfg.Token)
		if err != nil {
			return errorResultf("discord session init: %v", err), nil
		}
		t.session = s
	}

	channelID := params.To
	if kind == "user" {
		ch, err := t.session.UserChannelCreate(params.To, discordgo.WithContext(ctx))
		if err != nil {
			return errorResultf("failed to open DM with user %s: %v", params.To, err), nil
		}
		channelID = ch.ID
	}

	for _, chunk := range discordChunk(params.Message, 2000) {
		if _, err := t.session.ChannelMessageSend(channelID, chunk, discordgo.WithContext(ctx)); err != nil {
			return errorResultf("send failed: %v", err), nil
		}
	}
	return ToolResult{Output: fmt.Sprintf("Message sent to %s %s", kind, params.To)}, nil
}

// discordChunk splits text into chunks no larger than limit bytes, trying
// to break on newlines.
func discordChunk(text string, limit int) []string {
	if len(text) <= limit {
		return []string{text}
	}
	var chunks []string
	for len(text) > limit {
		cut := strings.LastIndex(text[:limit], "\n")
		if cut <= 0 {
			cut = limit
		}
		chunks = append(chunks, strings.TrimRight(text[:cut], "\n"))
		text = strings.TrimLeft(text[cut:], "\n")
	}
	if text != "" {
		chunks = append(chunks, text)
	}
	return chunks
}
