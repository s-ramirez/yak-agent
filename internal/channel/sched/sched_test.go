package sched

import (
	"strings"
	"testing"
	"time"

	"yak-go/internal/schedule"
)

func TestFormatEventEmitsScheduledEventXML(t *testing.T) {
	ev := schedule.Event{
		JobID: "abc123",
		Name:  "deploy reminder",
		Text:  "check on the staging deploy",
	}
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	got := FormatEvent(ev, now)

	if !strings.Contains(got, `<scheduled_event name="deploy reminder" fired_at="2026-04-13T10:00:00Z">`) {
		t.Fatalf("expected opening tag with attributes, got %q", got)
	}
	if !strings.Contains(got, "check on the staging deploy") {
		t.Fatalf("expected text payload, got %q", got)
	}
	if !strings.HasSuffix(got, "</scheduled_event>") {
		t.Fatalf("expected closing tag, got %q", got)
	}
}
