package webui

import (
	"sync"
	"time"
)

type EventType string

const (
	EventAgentStart EventType = "agent_start"
	EventAgentEnd   EventType = "agent_end"
	EventToolStart  EventType = "tool_start"
	EventToolEnd    EventType = "tool_end"
	EventAgentSpawn EventType = "agent_spawn"
	EventAgentDone  EventType = "agent_done"
)

type Event struct {
	Type      EventType `json:"type"`
	Timestamp int64     `json:"ts"`
	AgentID   string    `json:"agent_id"`
	AgentName string    `json:"agent_name"`
	ToolName  string    `json:"tool,omitempty"`
	Status    string    `json:"status,omitempty"`
}

const (
	historySize    = 100
	clientChanSize = 64
)

type EventBus struct {
	mu      sync.RWMutex
	clients map[uint64]chan Event
	nextID  uint64
	history []Event
}

func NewEventBus() *EventBus {
	return &EventBus{
		clients: make(map[uint64]chan Event),
	}
}

func (b *EventBus) Publish(ev Event) {
	if ev.Timestamp == 0 {
		ev.Timestamp = time.Now().UnixMilli()
	}

	b.mu.Lock()
	b.history = append(b.history, ev)
	if len(b.history) > historySize {
		b.history = b.history[len(b.history)-historySize:]
	}
	clients := make([]chan Event, 0, len(b.clients))
	for _, ch := range b.clients {
		clients = append(clients, ch)
	}
	b.mu.Unlock()

	for _, ch := range clients {
		select {
		case ch <- ev:
		default:
			// Client can't keep up; drop the event.
		}
	}
}

func (b *EventBus) Subscribe() (uint64, <-chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	id := b.nextID
	b.nextID++
	ch := make(chan Event, clientChanSize)
	b.clients[id] = ch
	return id, ch
}

func (b *EventBus) Unsubscribe(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if ch, ok := b.clients[id]; ok {
		close(ch)
		delete(b.clients, id)
	}
}

func (b *EventBus) History() []Event {
	b.mu.RLock()
	defer b.mu.RUnlock()

	out := make([]Event, len(b.history))
	copy(out, b.history)
	return out
}
