package imessage

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"yak-go/internal/channel"
)

func TestNormalizeHandle(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"+15551234567", "+15551234567"},
		{"imessage:+15551234567", "+15551234567"},
		{"sms:+15551234567", "+15551234567"},
		{"+1 (555) 123-4567", "+15551234567"},
		{"user@example.com", "user@example.com"},
		{"User@Example.COM", "user@example.com"},
		{"imessage:User@Example.COM", "user@example.com"},
	}
	for _, tt := range tests {
		got := normalizeHandle(tt.in)
		if got != tt.want {
			t.Errorf("normalizeHandle(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestWebhookHandler_AuthRejects(t *testing.T) {
	ch := New(Config{
		ServerURL:    "http://localhost",
		Password:     "secret",
		WebhookPath:  "/bb",
		WebhookPort:  0,
		OwnerHandles: []string{"+15551234567"},
	})
	out := make(chan channel.Inbound, 4)
	handler := ch.makeHandler(context.Background(), out)

	body := makeNewMessagePayload("+15551234567", "hello", "iMessage;-;+15551234567", false)
	req := httptest.NewRequest(http.MethodPost, "/bb", bytes.NewReader(body))
	// No password supplied.
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rr.Code)
	}
	if len(out) != 0 {
		t.Error("no message should be enqueued on auth failure")
	}
}

func TestWebhookHandler_NonOwnerDropped(t *testing.T) {
	ch := New(Config{
		ServerURL:    "http://localhost",
		Password:     "secret",
		OwnerHandles: []string{"+15550000000"},
	})
	out := make(chan channel.Inbound, 4)
	handler := ch.makeHandler(context.Background(), out)

	body := makeNewMessagePayload("+19999999999", "hello", "iMessage;-;+19999999999", false)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.URL.RawQuery = "password=secret"
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("want 200, got %d", rr.Code)
	}
	if len(out) != 0 {
		t.Error("non-owner message should be silently dropped")
	}
}

func TestWebhookHandler_OwnerDMForwarded(t *testing.T) {
	ch := New(Config{
		ServerURL:    "http://localhost",
		Password:     "secret",
		OwnerHandles: []string{"+15551234567"},
	})
	out := make(chan channel.Inbound, 4)
	handler := ch.makeHandler(context.Background(), out)

	body := makeNewMessagePayload("+15551234567", "ping", "iMessage;-;+15551234567", false)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.URL.RawQuery = "password=secret"
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	select {
	case msg := <-out:
		if msg.Content != "ping" {
			t.Errorf("content = %q, want %q", msg.Content, "ping")
		}
		if msg.Thread != "iMessage;-;+15551234567" {
			t.Errorf("thread = %q, want chatGuid", msg.Thread)
		}
		if msg.Channel != "imessage" {
			t.Errorf("channel = %q", msg.Channel)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for inbound message")
	}
}

func TestWebhookHandler_FromMeDropped(t *testing.T) {
	ch := New(Config{
		ServerURL:    "http://localhost",
		Password:     "secret",
		OwnerHandles: []string{"+15551234567"},
	})
	out := make(chan channel.Inbound, 4)
	handler := ch.makeHandler(context.Background(), out)

	body := makeNewMessagePayload("+15551234567", "echo", "iMessage;-;+15551234567", true)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.URL.RawQuery = "password=secret"
	rr := httptest.NewRecorder()
	handler(rr, req)

	if len(out) != 0 {
		t.Error("fromMe message should be dropped")
	}
}

func TestWebhookHandler_GroupRequiresTag(t *testing.T) {
	ch := New(Config{
		ServerURL:    "http://localhost",
		Password:     "secret",
		OwnerHandles: []string{"+15551234567"},
		GroupTag:     "@yak",
	})
	out := make(chan channel.Inbound, 4)
	handler := ch.makeHandler(context.Background(), out)

	// Group message without tag — should be dropped.
	body := makeNewMessagePayload("+15551234567", "hey everyone", "iMessage;+;groupchat123", false)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.URL.RawQuery = "password=secret"
	httptest.NewRecorder()
	rr := httptest.NewRecorder()
	handler(rr, req)
	if len(out) != 0 {
		t.Error("group message without tag should be dropped")
	}

	// Group message with tag — should be forwarded, tag stripped.
	body = makeNewMessagePayload("+15551234567", "@yak what time is it?", "iMessage;+;groupchat123", false)
	req = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.URL.RawQuery = "password=secret"
	rr = httptest.NewRecorder()
	handler(rr, req)

	select {
	case msg := <-out:
		if strings.Contains(msg.Content, "@yak") {
			t.Errorf("tag should be stripped from content, got %q", msg.Content)
		}
		if !strings.Contains(msg.Content, "what time is it?") {
			t.Errorf("content should retain question, got %q", msg.Content)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for group message")
	}
}

func TestWebhookHandler_NonPostIgnored(t *testing.T) {
	ch := New(Config{Password: "secret"})
	out := make(chan channel.Inbound, 4)
	handler := ch.makeHandler(context.Background(), out)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL.RawQuery = "password=secret"
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", rr.Code)
	}
}

// makeNewMessagePayload builds a minimal imessage-rs webhook JSON body.
// isGroup uses style=43 (imessage-rs group detection) when true.
func makeNewMessagePayload(handle, text, chatGUID string, fromMe bool) []byte {
	style := 0
	if strings.Contains(chatGUID, ";+;") {
		style = 43
	}
	type handleObj struct {
		Address string `json:"address"`
	}
	type chatObj struct {
		Guid  string `json:"guid"` // imessage-rs field name
		Style int    `json:"style"`
	}
	type data struct {
		Text     string    `json:"text"`
		IsFromMe bool      `json:"isFromMe"`
		Chats    []chatObj `json:"chats"`
		Handle   handleObj `json:"handle"`
	}
	type payload struct {
		Type string `json:"type"`
		Data data   `json:"data"`
	}
	p := payload{
		Type: "new-message",
		Data: data{
			Text:     text,
			IsFromMe: fromMe,
			Chats:    []chatObj{{Guid: chatGUID, Style: style}},
			Handle:   handleObj{Address: handle},
		},
	}
	b, _ := json.Marshal(p)
	return b
}
