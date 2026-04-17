package channel

import (
	"sync"

	"yak-go/internal/types"
)

// Conversation holds the message history for a single (channel, thread)
// pair. The dispatcher serializes access per conversation, so handlers
// can assume exclusive access to Messages while HandleTurn runs.
type Conversation struct {
	Key      Key
	Messages []types.Message

	// ModelOverride, if non-empty, is set by the dispatcher from the
	// inbound message and cleared after the turn. The turn handler may
	// use it to switch models for a single turn (e.g. heartbeat runs).
	ModelOverride string

	// Compaction state scoped to this conversation. The runner reads and
	// writes these between turns so multi-channel traffic does not cross-
	// contaminate each other's token accounting.
	LastSummary    string
	LastUsage      *types.Usage
	LastUsageIndex int // -1 means "no authoritative prefix yet"
}

// Store is an in-memory map of conversations keyed by (channel, thread).
// It does not persist across process restarts.
type Store struct {
	mu    sync.Mutex
	convs map[Key]*Conversation
}

func NewStore() *Store {
	return &Store{convs: make(map[Key]*Conversation)}
}

// Get returns the conversation for key, creating an empty one if it does
// not already exist.
func (s *Store) Get(key Key) *Conversation {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, ok := s.convs[key]
	if !ok {
		conv = &Conversation{Key: key, LastUsageIndex: -1}
		s.convs[key] = conv
	}
	return conv
}
