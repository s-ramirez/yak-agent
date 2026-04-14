package schedule

import (
	"testing"
	"time"
)

func TestNextRunAtAtFuture(t *testing.T) {
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	at := now.Add(1 * time.Hour)
	job := Job{Schedule: Schedule{Kind: KindAt, At: &at}}

	got, ok := NextRunAt(job, now)
	if !ok || !got.Equal(at) {
		t.Fatalf("expected at=%v ok=true, got %v ok=%v", at, got, ok)
	}
}

func TestNextRunAtAtPastFiresImmediately(t *testing.T) {
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	at := now.Add(-1 * time.Hour)
	job := Job{Schedule: Schedule{Kind: KindAt, At: &at}}

	got, ok := NextRunAt(job, now)
	if !ok || !got.Equal(at) {
		t.Fatalf("expected past at to still schedule, got %v ok=%v", got, ok)
	}
}

func TestNextRunAtAtAlreadyRun(t *testing.T) {
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	at := now.Add(1 * time.Hour)
	last := now.Add(-1 * time.Minute)
	job := Job{Schedule: Schedule{Kind: KindAt, At: &at}, LastRunAt: &last}

	if _, ok := NextRunAt(job, now); ok {
		t.Fatal("expected ok=false for already-run at job")
	}
}

func TestNextRunAtAtNilTime(t *testing.T) {
	if _, ok := NextRunAt(Job{Schedule: Schedule{Kind: KindAt}}, time.Now()); ok {
		t.Fatal("expected ok=false for nil At")
	}
}

func TestNextRunAtEveryBeforeAnchor(t *testing.T) {
	anchor := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	now := anchor.Add(-2 * time.Hour)
	job := Job{Schedule: Schedule{
		Kind:   KindEvery,
		Every:  Duration(15 * time.Minute),
		Anchor: &anchor,
	}}

	got, ok := NextRunAt(job, now)
	if !ok || !got.Equal(anchor) {
		t.Fatalf("expected first fire at anchor=%v, got %v ok=%v", anchor, got, ok)
	}
}

func TestNextRunAtEveryAtAnchor(t *testing.T) {
	anchor := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	job := Job{Schedule: Schedule{
		Kind:   KindEvery,
		Every:  Duration(15 * time.Minute),
		Anchor: &anchor,
	}}

	want := anchor.Add(15 * time.Minute)
	got, ok := NextRunAt(job, anchor)
	if !ok || !got.Equal(want) {
		t.Fatalf("expected first fire at anchor+every=%v, got %v ok=%v", want, got, ok)
	}
}

func TestNextRunAtEverySkipsMissed(t *testing.T) {
	anchor := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	now := anchor.Add(47 * time.Minute) // ticks at +15, +30, +45 missed
	job := Job{Schedule: Schedule{
		Kind:   KindEvery,
		Every:  Duration(15 * time.Minute),
		Anchor: &anchor,
	}}

	want := anchor.Add(60 * time.Minute)
	got, ok := NextRunAt(job, now)
	if !ok || !got.Equal(want) {
		t.Fatalf("expected next future tick %v, got %v ok=%v", want, got, ok)
	}
}

func TestNextRunAtEveryAfterLastRun(t *testing.T) {
	anchor := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	last := anchor.Add(15 * time.Minute)
	now := last.Add(1 * time.Minute)
	job := Job{
		Schedule: Schedule{
			Kind:   KindEvery,
			Every:  Duration(15 * time.Minute),
			Anchor: &anchor,
		},
		LastRunAt: &last,
	}

	want := anchor.Add(30 * time.Minute)
	got, ok := NextRunAt(job, now)
	if !ok || !got.Equal(want) {
		t.Fatalf("expected next tick %v, got %v ok=%v", want, got, ok)
	}
}

func TestNextRunAtEveryWayBehind(t *testing.T) {
	anchor := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	last := anchor.Add(15 * time.Minute)
	now := last.Add(2 * time.Hour)
	job := Job{
		Schedule: Schedule{
			Kind:   KindEvery,
			Every:  Duration(15 * time.Minute),
			Anchor: &anchor,
		},
		LastRunAt: &last,
	}

	// next tick strictly after now, anchor-aligned
	want := anchor.Add((15 + 9*15) * time.Minute) // 15 + 135 = 150m → next 150+15=165m? hmm
	// rethink: now = anchor + 135m. elapsed = 135m, k = 135/15 = 9, candidate = anchor + 10*15m = anchor+150m
	want = anchor.Add(150 * time.Minute)
	got, ok := NextRunAt(job, now)
	if !ok || !got.Equal(want) {
		t.Fatalf("expected %v, got %v ok=%v", want, got, ok)
	}
}

func TestNextRunAtEveryNoAnchor(t *testing.T) {
	job := Job{Schedule: Schedule{Kind: KindEvery, Every: Duration(15 * time.Minute)}}
	if _, ok := NextRunAt(job, time.Now()); ok {
		t.Fatal("expected false for missing anchor")
	}
}

func TestNextRunAtEveryZeroInterval(t *testing.T) {
	now := time.Now()
	job := Job{Schedule: Schedule{Kind: KindEvery, Anchor: &now}}
	if _, ok := NextRunAt(job, now); ok {
		t.Fatal("expected false for zero interval")
	}
}

func TestNextRunAtUnknownKind(t *testing.T) {
	if _, ok := NextRunAt(Job{Schedule: Schedule{Kind: "weird"}}, time.Now()); ok {
		t.Fatal("expected false for unknown kind")
	}
}

func TestNextRunAtCronWeekday9am(t *testing.T) {
	// Sunday 2026-04-12 10:00 UTC — next weekday 9am should be Mon 2026-04-13 09:00.
	now := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)
	job := Job{Schedule: Schedule{Kind: KindCron, Cron: "0 9 * * 1-5"}}

	got, ok := NextRunAt(job, now)
	if !ok {
		t.Fatal("expected cron job to schedule")
	}
	want := time.Date(2026, 4, 13, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestNextRunAtCronAfterLastRun(t *testing.T) {
	// Every hour at :00. last run at 10:00, now at 10:30 → next should be 11:00.
	last := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	now := last.Add(30 * time.Minute)
	job := Job{
		Schedule:  Schedule{Kind: KindCron, Cron: "0 * * * *"},
		LastRunAt: &last,
	}

	got, ok := NextRunAt(job, now)
	if !ok {
		t.Fatal("expected cron to schedule")
	}
	want := time.Date(2026, 4, 13, 11, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestNextRunAtCronInvalidExpression(t *testing.T) {
	job := Job{Schedule: Schedule{Kind: KindCron, Cron: "not a cron"}}
	if _, ok := NextRunAt(job, time.Now()); ok {
		t.Fatal("expected false for invalid cron expression")
	}
}

func TestNextRunAtCronEmpty(t *testing.T) {
	if _, ok := NextRunAt(Job{Schedule: Schedule{Kind: KindCron}}, time.Now()); ok {
		t.Fatal("expected false for empty cron")
	}
}
