package channel

import (
	"context"
	"testing"
)

type recordHandler struct {
	calls int
}

func (h *recordHandler) HandleTurn(_ context.Context, _ *Conversation, _ string, _ ReplyFunc) error {
	h.calls++
	return nil
}

func TestDelegatingHandlerFallsBackToMainWithoutRoute(t *testing.T) {
	mainHandler := &recordHandler{}
	h := &DelegatingHandler{Main: mainHandler}
	conv := &Conversation{Key: Key{Channel: "telegram", Thread: "g", Topic: "x"}}
	if err := h.HandleTurn(context.Background(), conv, "hi", func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if mainHandler.calls != 1 {
		t.Fatalf("main calls = %d", mainHandler.calls)
	}
}

func TestDelegatingHandlerUsesSubagentRouteWhenPresent(t *testing.T) {
	mainHandler := &recordHandler{}
	subHandler := &recordHandler{}
	routes := NewRouteRegistry()
	key := Key{Channel: "telegram", Thread: "g::agent=rocky", Topic: "exercise"}
	routes.Set(key, "rocky")
	h := &DelegatingHandler{
		Main:        mainHandler,
		Subagents:   map[string]TurnHandler{"rocky": subHandler},
		RouteAgents: routes,
	}
	conv := &Conversation{Key: key}
	if err := h.HandleTurn(context.Background(), conv, "hi", func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if subHandler.calls != 1 {
		t.Fatalf("subagent calls = %d", subHandler.calls)
	}
	if mainHandler.calls != 0 {
		t.Fatalf("main calls = %d", mainHandler.calls)
	}
}
