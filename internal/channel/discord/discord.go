// Package discord implements a channel.Channel backed by a Discord bot.
// Incoming messages arrive via the Discord gateway (websocket); outgoing
// messages are sent via the Discord REST API.
//
// Only messages from the configured OwnerIDs are forwarded to the
// dispatcher. In guild channels an additional GuildTag (or a mention of
// the bot) must appear in the message; that marker is stripped before
// dispatch.
package discord

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/bwmarrin/discordgo"

	"yak-go/internal/channel"
)

const channelName = "discord"

// Config holds all runtime parameters for the Discord channel.
type Config struct {
	// Token is the Discord bot token (without the "Bot " prefix).
	Token string

	// OwnerIDs is the set of Discord user IDs (snowflakes) that are allowed
	// to trigger agent responses. Messages from any other user are dropped.
	OwnerIDs []string

	// GuildTag is a literal string that must appear in a guild-channel
	// message for the agent to respond (e.g. "@yak"). A mention of the bot
	// also satisfies this requirement. DMs bypass the check entirely.
	GuildTag string
}

// Channel implements channel.Channel for Discord.
type Channel struct {
	cfg     Config
	session *discordgo.Session
	botID   string
}

// New returns a new Channel with the given configuration.
func New(cfg Config) *Channel {
	return &Channel{cfg: cfg}
}

// ConfigFromEnv reads YAK_DISCORD_* environment variables and returns a
// populated Config along with a reason string when the channel should be
// disabled. Returns (nil, "", nil) if disabled without explanation (e.g.
// no token). Errors only when a value is present but malformed.
//
// The second return (disabledReason) is non-empty when the channel is
// explicitly disabled via YAK_DISCORD_ENABLED=false so the caller can log it.
func ConfigFromEnv(getenv func(string) string, parseBool func(name string, def bool) bool) (*Config, string, error) {
	if !parseBool("YAK_DISCORD_ENABLED", true) {
		return nil, "YAK_DISCORD_ENABLED=false", nil
	}
	token := strings.TrimSpace(getenv("YAK_DISCORD_TOKEN"))
	if token == "" {
		return nil, "", nil
	}
	var owners []string
	if raw := getenv("YAK_DISCORD_OWNER_IDS"); raw != "" {
		for _, id := range strings.Split(raw, ",") {
			if id = strings.TrimSpace(id); id != "" {
				owners = append(owners, id)
			}
		}
	}
	return &Config{
		Token:    token,
		OwnerIDs: owners,
		GuildTag: getenv("YAK_DISCORD_GUILD_TAG"),
	}, "", nil
}

func (c *Channel) Name() string { return channelName }

// Listen opens the Discord gateway connection and forwards qualifying
// messages onto out. It blocks until ctx is cancelled.
func (c *Channel) Listen(ctx context.Context, out chan<- channel.Inbound) error {
	s, err := discordgo.New("Bot " + c.cfg.Token)
	if err != nil {
		return fmt.Errorf("discord: new session: %w", err)
	}
	s.Identify.Intents = discordgo.IntentsGuildMessages |
		discordgo.IntentsDirectMessages |
		discordgo.IntentsMessageContent

	s.AddHandler(c.makeHandler(ctx, out))

	if err := s.Open(); err != nil {
		return fmt.Errorf("discord: open gateway: %w", err)
	}
	c.session = s
	if s.State != nil && s.State.User != nil {
		c.botID = s.State.User.ID
	}
	fmt.Fprintf(os.Stderr, "[discord] gateway connected as %s\n", c.botID)

	<-ctx.Done()
	_ = s.Close()
	return ctx.Err()
}

// Send delivers a reply via the Discord REST API. Runner meta-lines like
// "[tokens: ...]" are filtered before sending.
func (c *Channel) Send(ctx context.Context, msg channel.Outbound) error {
	if c.session == nil {
		return fmt.Errorf("discord: session not initialized")
	}
	text := dropMetaLines(msg.Content)
	if text == "" {
		return nil
	}
	for _, chunk := range chunkMessage(text, 2000) {
		if _, err := c.session.ChannelMessageSend(msg.Thread, chunk); err != nil {
			fmt.Fprintf(os.Stderr, "[discord] send error: %v\n", err)
			return err
		}
	}
	return nil
}

// makeHandler returns a discordgo MessageCreate handler that filters and
// forwards qualifying messages to the dispatcher.
func (c *Channel) makeHandler(ctx context.Context, out chan<- channel.Inbound) func(*discordgo.Session, *discordgo.MessageCreate) {
	log := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "[discord] "+format+"\n", args...)
	}

	return func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if m.Author == nil || m.Author.Bot {
			return
		}
		if s.State != nil && s.State.User != nil && m.Author.ID == s.State.User.ID {
			return
		}

		text := strings.TrimSpace(m.Content)
		if text == "" {
			return
		}

		if !c.isOwner(m.Author.ID) {
			log("dropped message from non-owner %s (channel=%s)", m.Author.ID, m.ChannelID)
			return
		}

		isDM := m.GuildID == ""
		if !isDM {
			mentioned := false
			botID := c.botID
			if botID == "" && s.State != nil && s.State.User != nil {
				botID = s.State.User.ID
			}
			for _, u := range m.Mentions {
				if u != nil && u.ID == botID {
					mentioned = true
					break
				}
			}
			tagPresent := c.cfg.GuildTag != "" && strings.Contains(text, c.cfg.GuildTag)
			if !mentioned && !tagPresent {
				log("dropped guild message from %s — missing mention/tag", m.Author.ID)
				return
			}
			if mentioned && botID != "" {
				text = strings.ReplaceAll(text, "<@"+botID+">", "")
				text = strings.ReplaceAll(text, "<@!"+botID+">", "")
			}
			if tagPresent {
				text = strings.ReplaceAll(text, c.cfg.GuildTag, "")
			}
			text = strings.TrimSpace(text)
			if text == "" {
				return
			}
		}

		log("accepted message from %s (channel=%s, dm=%v): %q", m.Author.ID, m.ChannelID, isDM, text)

		msg := channel.Inbound{
			Channel:    channelName,
			Thread:     m.ChannelID,
			Sender:     m.Author.ID,
			Content:    text,
			Kind:       channel.KindUser,
			ReceivedAt: m.Timestamp,
		}
		select {
		case <-ctx.Done():
		case out <- msg:
		}
	}
}

func (c *Channel) isOwner(id string) bool {
	for _, o := range c.cfg.OwnerIDs {
		if o == id {
			return true
		}
	}
	return false
}

// dropMetaLines removes lines that are purely runner/system annotations
// (surrounded by square brackets, e.g. "[tokens: ...]").
func dropMetaLines(s string) string {
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			continue
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// chunkMessage splits text into chunks no larger than limit bytes, trying
// to break on newlines. Discord's per-message limit is 2000 characters.
func chunkMessage(text string, limit int) []string {
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
