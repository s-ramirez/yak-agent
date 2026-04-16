// Package imessage implements a channel.Channel backed by the BlueBubbles
// macOS app. Incoming messages arrive via an HTTP webhook that BlueBubbles
// POSTs to; outgoing messages are sent through the BlueBubbles REST API.
//
// Only messages from the configured OwnerHandles are forwarded to the
// dispatcher. In group chats an additional GroupTag must appear in the
// message body (e.g. "@yak"); that prefix is stripped before dispatch.
package imessage

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"yak-go/internal/channel"
)

const channelName = "imessage"

// Config holds all runtime parameters for the iMessage channel.
type Config struct {
	// ServerURL is the base URL of the BlueBubbles server, e.g.
	// "http://192.168.1.10:1234".
	ServerURL string

	// Password is the BlueBubbles API password used for both authenticating
	// incoming webhooks and signing outgoing API requests.
	Password string

	// WebhookPath is the URL path that BlueBubbles should POST events to.
	// Defaults to "/bluebubbles" if empty.
	WebhookPath string

	// WebhookPort is the local TCP port the webhook HTTP server listens on.
	WebhookPort int

	// OwnerHandles is the set of normalized phone numbers or email addresses
	// that are allowed to trigger agent responses. Messages from any other
	// sender are silently dropped.
	OwnerHandles []string

	// GroupTag is the string that must appear in a group-chat message for the
	// agent to respond. Leave empty to respond to all group messages from
	// owner handles (not recommended).
	GroupTag string

	// DebounceDelay overrides the default 10-second wait used to group
	// consecutive messages before dispatching. Zero uses the default.
	DebounceDelay time.Duration
}

// msgPart is a single inbound message fragment waiting to be debounced.
type msgPart struct {
	text   string
	sender string
}

// defaultDebounceDelay is how long to wait for follow-up messages before dispatching.
const defaultDebounceDelay = 10 * time.Second

// Channel implements channel.Channel for iMessage via BlueBubbles.
type Channel struct {
	cfg     Config
	seenMu  sync.Mutex
	seenIDs map[string]struct{} // message GUIDs already dispatched this session
	pendMu  sync.Mutex
	pending map[string]chan msgPart // per-thread debounce channels
}

// New returns a new Channel with the given configuration.
func New(cfg Config) *Channel {
	if cfg.WebhookPath == "" {
		cfg.WebhookPath = "/bluebubbles"
	}
	if cfg.WebhookPort == 0 {
		cfg.WebhookPort = 8421
	}
	// Normalize all owner handles once at startup.
	normalized := make([]string, 0, len(cfg.OwnerHandles))
	for _, h := range cfg.OwnerHandles {
		normalized = append(normalized, normalizeHandle(h))
	}
	cfg.OwnerHandles = normalized
	return &Channel{
		cfg:     cfg,
		seenIDs: make(map[string]struct{}),
		pending: make(map[string]chan msgPart),
	}
}

func (c *Channel) Name() string { return channelName }

// Listen starts the webhook HTTP server and forwards qualifying messages onto
// out. It blocks until ctx is cancelled.
func (c *Channel) Listen(ctx context.Context, out chan<- channel.Inbound) error {
	mux := http.NewServeMux()
	mux.HandleFunc(c.cfg.WebhookPath, c.makeHandler(ctx, out))

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", c.cfg.WebhookPort),
		Handler: mux,
	}

	errCh := make(chan error, 1)
	go func() {
		ln, err := net.Listen("tcp", srv.Addr)
		if err != nil {
			errCh <- fmt.Errorf("imessage webhook: listen: %w", err)
			return
		}
		errCh <- srv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

// Send delivers a reply via the imessage-rs REST API. The content is
// stripped of Markdown formatting before transmission. System-only lines
// (e.g. "[tokens: ...]", "[compacted: ...]") are silently dropped.
func (c *Channel) Send(ctx context.Context, msg channel.Outbound) error {
	text := stripMarkdown(msg.Content)
	text = dropMetaLines(text)
	if text == "" {
		return nil
	}
	if err := sendMessage(ctx, c.cfg, msg.Thread, text); err != nil {
		fmt.Fprintf(os.Stderr, "[imessage] send error: %v\n", err)
		return err
	}
	return nil
}

// dropMetaLines removes lines that are purely runner/system annotations
// (surrounded by square brackets, e.g. "[tokens: ...]", "[compacted: ...]").
// These are informational output intended for the CLI, not conversational
// replies to the user.
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

// makeHandler returns the http.HandlerFunc that processes imessage-rs/BlueBubbles
// webhook events.
func (c *Channel) makeHandler(ctx context.Context, out chan<- channel.Inbound) http.HandlerFunc {
	passwordBytes := []byte(c.cfg.Password)
	log := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "[imessage] "+format+"\n", args...)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Authenticate before reading the body.
		if !checkPassword(r, passwordBytes) {
			log("webhook: rejected request with wrong or missing password")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		var payload webhookPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			log("webhook: could not parse JSON: %v", err)
			w.WriteHeader(http.StatusOK)
			return
		}

		// Only handle new inbound messages.
		if payload.Type != "new-message" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if payload.fromMe() {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Deduplicate: BlueBubbles replays recent messages on reconnect.
		// Drop any message whose GUID we have already dispatched this session.
		if guid := payload.messageGUID(); guid != "" {
			c.seenMu.Lock()
			_, already := c.seenIDs[guid]
			if !already {
				c.seenIDs[guid] = struct{}{}
			}
			c.seenMu.Unlock()
			if already {
				log("webhook: dropped duplicate message guid=%s", guid)
				w.WriteHeader(http.StatusOK)
				return
			}
		}

		text := payload.text()
		if text == "" {
			log("webhook: dropped message with empty text")
			w.WriteHeader(http.StatusOK)
			return
		}

		sender := normalizeHandle(payload.senderHandle())
		if sender == "" {
			log("webhook: dropped message — could not determine sender handle")
			w.WriteHeader(http.StatusOK)
			return
		}

		chatGUID := payload.chatGUID()
		if chatGUID == "" {
			// imessage-rs can send an empty chats array for some messages.
			// For DMs, construct the chatGuid from the sender handle.
			raw := payload.senderHandle()
			if raw != "" {
				chatGUID = "iMessage;-;" + raw
				log("webhook: chats empty, constructed chatGuid: %s", chatGUID)
			}
		}
		if chatGUID == "" {
			log("webhook: dropped message — could not determine chatGuid")
			w.WriteHeader(http.StatusOK)
			return
		}

		isGroup := payload.isGroupChat()

		// Security gate: only owner handles may trigger the agent.
		if !c.isOwner(sender) {
			log("webhook: dropped message from non-owner %q (chat=%s)", sender, chatGUID)
			w.WriteHeader(http.StatusOK)
			return
		}

		// In group chats require the GroupTag.
		if isGroup && c.cfg.GroupTag != "" {
			if !strings.Contains(text, c.cfg.GroupTag) {
				log("webhook: dropped group message from %q — missing tag %q", sender, c.cfg.GroupTag)
				w.WriteHeader(http.StatusOK)
				return
			}
			// Strip the tag from the forwarded content.
			text = strings.TrimSpace(strings.ReplaceAll(text, c.cfg.GroupTag, ""))
			if text == "" {
				w.WriteHeader(http.StatusOK)
				return
			}
		}

		log("webhook: accepted message from %q (chat=%s, group=%v): %q", sender, chatGUID, isGroup, text)
		c.enqueue(ctx, chatGUID, sender, text, out)
		w.WriteHeader(http.StatusOK)
	}
}

// enqueue routes an inbound message through the per-thread debounce worker.
// Messages that arrive within debounceDelay of each other are accumulated and
// dispatched as a single combined message.
func (c *Channel) enqueue(ctx context.Context, chatGUID, sender, text string, out chan<- channel.Inbound) {
	c.pendMu.Lock()
	ch, exists := c.pending[chatGUID]
	if !exists {
		ch = make(chan msgPart, 16)
		c.pending[chatGUID] = ch
		go c.debounceWorker(ctx, chatGUID, ch, out)
	}
	c.pendMu.Unlock()

	select {
	case ch <- msgPart{text: text, sender: sender}:
	case <-ctx.Done():
	}
}

func (c *Channel) debounce() time.Duration {
	if c.cfg.DebounceDelay > 0 {
		return c.cfg.DebounceDelay
	}
	return defaultDebounceDelay
}

// debounceWorker accumulates message parts for chatGUID, then dispatches them
// as a single Inbound after the configured debounce delay of silence.
func (c *Channel) debounceWorker(ctx context.Context, chatGUID string, ch <-chan msgPart, out chan<- channel.Inbound) {
	defer func() {
		c.pendMu.Lock()
		if c.pending[chatGUID] == ch {
			delete(c.pending, chatGUID)
		}
		c.pendMu.Unlock()
	}()

	var parts []string
	var sender string
	var timer <-chan time.Time

	for {
		select {
		case part := <-ch:
			if sender == "" {
				sender = part.sender
			}
			parts = append(parts, part.text)
			timer = time.After(c.debounce())
		case <-timer:
			select {
			case <-ctx.Done():
			case out <- channel.Inbound{
				Channel:    channelName,
				Thread:     chatGUID,
				Sender:     sender,
				Content:    strings.Join(parts, "\n"),
				Kind:       channel.KindUser,
				ReceivedAt: time.Now(),
			}:
			}
			return
		case <-ctx.Done():
			return
		}
	}
}

func (c *Channel) isOwner(handle string) bool {
	for _, h := range c.cfg.OwnerHandles {
		if h == handle {
			return true
		}
	}
	return false
}

// checkPassword verifies the request carries the correct BlueBubbles password.
// It checks, in order: the "password" query param, "guid" query param, and
// the "x-password" / "x-guid" / "x-bluebubbles-guid" headers.
// The comparison is constant-time to prevent timing attacks.
func checkPassword(r *http.Request, want []byte) bool {
	candidates := []string{
		r.URL.Query().Get("password"),
		r.URL.Query().Get("guid"),
		r.Header.Get("x-password"),
		r.Header.Get("x-guid"),
		r.Header.Get("x-bluebubbles-guid"),
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(c), want) == 1 {
			return true
		}
	}
	return false
}

// webhookPayload is a loose representation of the JSON that BlueBubbles POSTs.
// Fields use raw json.RawMessage so we can handle both object and string forms.
type webhookPayload struct {
	Type   string          `json:"type"`
	Data   json.RawMessage `json:"data"`
	// Top-level fallbacks for servers that hoist fields.
	Text       string          `json:"text"`
	IsFromMe   bool            `json:"isFromMe"`
	FromMe     bool            `json:"from_me"`
	ChatGUID   string          `json:"chatGuid"`
	Handle     json.RawMessage `json:"handle"`
}

type messageData struct {
	GUID     string          `json:"guid"`
	Text     string          `json:"text"`
	IsFromMe bool            `json:"isFromMe"`
	FromMe   bool            `json:"from_me"`
	Outgoing bool            `json:"outgoing"`
	// BlueBubbles puts the chatGuid directly on the message.
	ChatGUID string    `json:"chatGuid"`
	Chat     *chatData `json:"chat"`
	// imessage-rs puts chat info in a "chats" array on the message.
	Chats  []chatData      `json:"chats"`
	Handle json.RawMessage `json:"handle"`
}

type chatData struct {
	// imessage-rs serializes the chat GUID as "guid"; BlueBubbles uses "chatGuid".
	Guid     string `json:"guid"`
	ChatGUID string `json:"chatGuid"`
	// Style 43 = group chat in imessage-rs / macOS chat.db.
	Style int `json:"style"`
}

func (p *webhookPayload) message() *messageData {
	if len(p.Data) == 0 {
		return nil
	}
	var m messageData
	if err := json.Unmarshal(p.Data, &m); err != nil {
		return nil
	}
	return &m
}

// messageGUID returns the unique identifier of the message, used for
// deduplication when BlueBubbles replays recent messages on reconnect.
func (p *webhookPayload) messageGUID() string {
	if m := p.message(); m != nil {
		return m.GUID
	}
	return ""
}

func (p *webhookPayload) fromMe() bool {
	if m := p.message(); m != nil {
		return m.IsFromMe || m.FromMe || m.Outgoing
	}
	return p.IsFromMe || p.FromMe
}

func (p *webhookPayload) text() string {
	if m := p.message(); m != nil {
		return strings.TrimSpace(m.Text)
	}
	return strings.TrimSpace(p.Text)
}

func (p *webhookPayload) chatGUID() string {
	if m := p.message(); m != nil {
		// BlueBubbles: chatGuid directly on the message.
		if m.ChatGUID != "" {
			return m.ChatGUID
		}
		// imessage-rs: chat info lives in the "chats" array; field is "guid".
		if len(m.Chats) > 0 {
			if m.Chats[0].Guid != "" {
				return m.Chats[0].Guid
			}
			if m.Chats[0].ChatGUID != "" {
				return m.Chats[0].ChatGUID
			}
		}
		if m.Chat != nil {
			if m.Chat.Guid != "" {
				return m.Chat.Guid
			}
			return m.Chat.ChatGUID
		}
	}
	return p.ChatGUID
}

// isGroupChat returns true when the chat is a group conversation.
// Checks the style field (imessage-rs: style=43) and the chatGuid separator
// (BlueBubbles: ";+;" indicates a group).
func (p *webhookPayload) isGroupChat() bool {
	if m := p.message(); m != nil {
		if len(m.Chats) > 0 && m.Chats[0].Style == 43 {
			return true
		}
		if m.Chat != nil && m.Chat.Style == 43 {
			return true
		}
	}
	// BlueBubbles group chatGuid contains ";+;".
	return strings.Contains(p.chatGUID(), ";+;")
}

func (p *webhookPayload) senderHandle() string {
	var raw json.RawMessage
	if m := p.message(); m != nil {
		raw = m.Handle
	} else {
		raw = p.Handle
	}
	if len(raw) == 0 {
		return ""
	}
	// Handle can be either a string or an object with an "address" field.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		Address string `json:"address"`
		Handle  string `json:"handle"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		if obj.Address != "" {
			return obj.Address
		}
		return obj.Handle
	}
	return ""
}

// sendMessage POSTs a text message to a BlueBubbles chat.
func sendMessage(ctx context.Context, cfg Config, chatGUID, text string) error {
	type sendPayload struct {
		ChatGUID string `json:"chatGuid"`
		TempGUID string `json:"tempGuid"`
		Message  string `json:"message"`
	}
	payload := sendPayload{
		ChatGUID: chatGUID,
		TempGUID: fmt.Sprintf("tmp-%d", time.Now().UnixNano()),
		Message:  text,
	}
	body, err := json.Marshal(payload)
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
		return fmt.Errorf("bluebubbles send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("bluebubbles send: status %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	return nil
}

// rePhone matches a string that looks like a phone number (digits, spaces,
// dashes, parens, optional leading +).
var rePhone = regexp.MustCompile(`^\+?[\d\s\-().]+$`)

// normalizeHandle strips service prefixes and normalizes the formatting of an
// iMessage handle so that comparisons with OwnerHandles are reliable.
func normalizeHandle(h string) string {
	// Strip known service prefixes.
	for _, prefix := range []string{"imessage:", "sms:", "iMessage:", "SMS:"} {
		h = strings.TrimPrefix(h, prefix)
	}
	h = strings.TrimSpace(h)

	// Emails: lowercase only.
	if strings.Contains(h, "@") {
		return strings.ToLower(h)
	}

	// Phone numbers: remove everything except digits and leading +.
	if rePhone.MatchString(h) {
		var b strings.Builder
		for i, r := range h {
			if r == '+' && i == 0 {
				b.WriteRune(r)
				continue
			}
			if r >= '0' && r <= '9' {
				b.WriteRune(r)
			}
		}
		return b.String()
	}

	return h
}
