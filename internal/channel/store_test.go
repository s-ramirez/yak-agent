package channel

import "testing"

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
