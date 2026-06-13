package channel

import (
	"testing"

	"yak-go/internal/types"
)

func TestStoreGetCreatesOnce(t *testing.T) {
	s := NewStore()
	key := Key{Channel: "cli", Thread: "default"}

	first := s.Get(key)
	if first == nil {
		t.Fatal("expected non-nil conversation")
	}
	if first.Key != key {
		t.Fatalf("expected key %+v on conv, got %+v", key, first.Key)
	}

	second := s.Get(key)
	if first != second {
		t.Fatal("expected repeated Get to return the same conversation pointer")
	}
}

func TestStoreGetIsolatesByKey(t *testing.T) {
	s := NewStore()
	a := s.Get(Key{Channel: "cli", Thread: "alice"})
	b := s.Get(Key{Channel: "cli", Thread: "bob"})
	if a == b {
		t.Fatal("expected distinct conversations for different threads")
	}

	c := s.Get(Key{Channel: "webui", Thread: "alice"})
	if c == a {
		t.Fatal("expected distinct conversations for different channels")
	}
}

func TestPersistentStoreRoundTripsMemoryReviewCounter(t *testing.T) {
	dir := t.TempDir()
	key := Key{Channel: "cli", Thread: "default"}
	store := NewPersistentStore(dir, nil)
	conv := store.Get(key)
	conv.Messages = []types.Message{{Role: "user", Content: "hello"}}
	conv.UserTurnsSinceMemoryReview = 7
	if err := store.Save(key); err != nil {
		t.Fatal(err)
	}

	reloaded := NewPersistentStore(dir, nil).Get(key)
	if reloaded.UserTurnsSinceMemoryReview != 7 {
		t.Fatalf("expected review counter 7, got %d", reloaded.UserTurnsSinceMemoryReview)
	}
}

func TestStoreIsolatesByTopic(t *testing.T) {
	s := NewStore()
	a := s.Get(Key{Channel: "telegram", Thread: "-1001", Topic: "exercise"})
	b := s.Get(Key{Channel: "telegram", Thread: "-1001", Topic: "nutrition"})
	if a == b {
		t.Fatal("expected distinct conversations for different topics")
	}
}

func TestPersistentStoreUsesTopicScopedHistoryPath(t *testing.T) {
	dir := t.TempDir()
	key := Key{Channel: "telegram", Thread: "-1001", Topic: "exercise"}
	store := NewPersistentStore(dir, nil)
	conv := store.Get(key)
	conv.Messages = []types.Message{{Role: "user", Content: "planche"}}
	if err := store.Save(key); err != nil {
		t.Fatal(err)
	}

	reloaded := NewPersistentStore(dir, nil).Get(key)
	if len(reloaded.Messages) != 1 || reloaded.Messages[0].Content != "planche" {
		t.Fatalf("expected topic-scoped history to round trip, got %+v", reloaded.Messages)
	}

	other := NewPersistentStore(dir, nil).Get(Key{Channel: "telegram", Thread: "-1001", Topic: "nutrition"})
	if len(other.Messages) != 0 {
		t.Fatalf("expected different topic history to stay isolated, got %+v", other.Messages)
	}
}
