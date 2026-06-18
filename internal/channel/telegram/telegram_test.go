package telegram

import (
	"testing"

	"yak-go/internal/channel"
)

func TestRouteKeepsUnconfiguredTopicsUnchanged(t *testing.T) {
	ch := New(Config{}, nil)
	in := channel.Inbound{Channel: Name, Thread: "group-1", Topic: "exercise", Content: "hi"}
	route := ch.Route(in)
	if route.Key.Thread != "group-1" || route.Key.Topic != "exercise" {
		t.Fatalf("unexpected route: %+v", route)
	}
	if route.AgentName != "" {
		t.Fatalf("unexpected agent override: %+v", route)
	}
}

func TestRouteMapsTopicToSubagentScopedThread(t *testing.T) {
	routes := channel.NewRouteRegistry()
	ch := New(Config{Topics: map[string]TopicConfig{
		"exercise": {Subagent: "Rocky"},
	}}, routes)
	in := channel.Inbound{Channel: Name, Thread: "group-1", Topic: "exercise", Content: "planche"}
	route := ch.Route(in)
	if route.Key.Thread != "group-1::agent=rocky" {
		t.Fatalf("thread = %q", route.Key.Thread)
	}
	if route.AgentName != "rocky" {
		t.Fatalf("agent = %q", route.AgentName)
	}
	if agent, ok := routes.Lookup(route.Key); !ok || agent != "rocky" {
		t.Fatalf("route registry = %q, %v", agent, ok)
	}
}
