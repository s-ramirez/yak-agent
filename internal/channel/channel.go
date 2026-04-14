package channel

import (
	"context"
	"time"
)

// Kind distinguishes user-originated messages from system-produced ones
// like scheduled events. The dispatcher uses it to decide whether a
// message counts as "user activity" for things like auto-distill.
type Kind int

const (
	KindUser Kind = iota
	KindEvent
)

// Key identifies a conversation. Channel is the name of the channel the
// inbound message belongs to; Thread is an opaque per-channel identifier
// that lets a single channel host multiple concurrent conversations.
type Key struct {
	Channel string
	Thread  string
}

// Inbound is a message entering the dispatcher. Channel + Thread form
// the conversation key; replies route back to the channel named Channel.
type Inbound struct {
	Channel    string
	Thread     string
	Sender     string
	Content    string
	Kind       Kind
	ReceivedAt time.Time
}

// Outbound is a message leaving the dispatcher, destined for the Send
// method of the channel named in Channel.
type Outbound struct {
	Channel string
	Thread  string
	Content string
}

// Channel is a bi-directional communication surface. Listen runs for
// the lifetime of the process, pushing Inbound messages onto the shared
// bus. Send delivers a reply to an existing recipient.
type Channel interface {
	Name() string
	Listen(ctx context.Context, out chan<- Inbound) error
	Send(ctx context.Context, msg Outbound) error
}
