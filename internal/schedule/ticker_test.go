package schedule

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

func drainEvents(ch <-chan Event) []Event {
	var out []Event
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}

func TestSchedulerFiresAtJob(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{t: time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)}
	store.SetNowFn(clock.Now)

	at := clock.Now().Add(5 * time.Minute)
	added, err := store.Add(Job{
		Name:     "wakeup",
		Enabled:  true,
		Schedule: Schedule{Kind: KindAt, At: &at},
		Text:     "remind me",
	})
	if err != nil {
		t.Fatal(err)
	}

	sched := NewScheduler(store, 4)
	sched.nowFn = clock.Now

	sched.tick()
	if got := drainEvents(sched.Events()); len(got) != 0 {
		t.Fatalf("expected no events at t-5m, got %+v", got)
	}

	clock.Set(at)
	sched.tick()
	got := drainEvents(sched.Events())
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(got), got)
	}
	if got[0].JobID != added.ID || got[0].Text != "remind me" {
		t.Fatalf("unexpected event: %+v", got[0])
	}

	jobs := store.List()
	if jobs[0].Enabled {
		t.Fatal("expected at job to be disabled after firing")
	}
}

func TestSchedulerEveryJobReschedules(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	clock := &fakeClock{t: time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)}
	store.SetNowFn(clock.Now)

	anchor := clock.Now()
	added, err := store.Add(Job{
		Name:    "ping",
		Enabled: true,
		Schedule: Schedule{
			Kind:   KindEvery,
			Every:  Duration(15 * time.Minute),
			Anchor: &anchor,
		},
		Text: "ping",
	})
	if err != nil {
		t.Fatal(err)
	}

	sched := NewScheduler(store, 4)
	sched.nowFn = clock.Now

	sched.tick()
	if got := drainEvents(sched.Events()); len(got) != 0 {
		t.Fatalf("expected no events at anchor, got %+v", got)
	}

	clock.Set(anchor.Add(15 * time.Minute))
	sched.tick()
	got := drainEvents(sched.Events())
	if len(got) != 1 || got[0].JobID != added.ID {
		t.Fatalf("expected 1 event, got %+v", got)
	}

	jobs := store.List()
	want := anchor.Add(30 * time.Minute)
	if jobs[0].NextRunAt == nil || !jobs[0].NextRunAt.Equal(want) {
		t.Fatalf("expected NextRunAt=%v, got %v", want, jobs[0].NextRunAt)
	}

	clock.Set(anchor.Add(30 * time.Minute))
	sched.tick()
	got = drainEvents(sched.Events())
	if len(got) != 1 {
		t.Fatalf("expected second fire, got %+v", got)
	}
}

func TestSchedulerInjectSyntheticJob(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	clock := &fakeClock{t: time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)}

	sched := NewScheduler(store, 4)
	sched.nowFn = clock.Now

	anchor := clock.Now()
	sched.Inject(Job{
		Name:    "heartbeat",
		Enabled: true,
		Schedule: Schedule{
			Kind:   KindEvery,
			Every:  Duration(10 * time.Minute),
			Anchor: &anchor,
		},
		Text: "heartbeat tick",
	})

	sched.tick()
	if got := drainEvents(sched.Events()); len(got) != 0 {
		t.Fatalf("expected no events before first interval, got %+v", got)
	}

	clock.Set(anchor.Add(10 * time.Minute))
	sched.tick()
	got := drainEvents(sched.Events())
	if len(got) != 1 || got[0].Name != "heartbeat" {
		t.Fatalf("expected heartbeat event, got %+v", got)
	}

	if jobs := store.List(); len(jobs) != 0 {
		t.Fatalf("expected store to remain empty (synthetic jobs not persisted), got %+v", jobs)
	}

	clock.Set(anchor.Add(20 * time.Minute))
	sched.tick()
	got = drainEvents(sched.Events())
	if len(got) != 1 {
		t.Fatalf("expected second heartbeat, got %+v", got)
	}
}

func TestSchedulerStartStop(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	sched := NewScheduler(store, 4)
	sched.tickInterval = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sched.Start(ctx)
	time.Sleep(20 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		sched.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return within 1s")
	}
}

func TestSchedulerFireDropsWhenChannelFull(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	clock := &fakeClock{t: time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)}
	store.SetNowFn(clock.Now)

	at := clock.Now().Add(1 * time.Minute)
	if _, err := store.Add(Job{
		Name: "drop", Enabled: true,
		Schedule: Schedule{Kind: KindAt, At: &at}, Text: "x",
	}); err != nil {
		t.Fatal(err)
	}

	sched := NewScheduler(store, 1)
	sched.nowFn = clock.Now
	sched.events <- Event{JobID: "filler"} // saturate buffer

	clock.Set(at)
	sched.tick()

	// Job should not have been marked run because fire returned false.
	jobs := store.List()
	if !jobs[0].Enabled {
		t.Fatal("expected job to remain enabled when fire was dropped")
	}
	if jobs[0].LastRunAt != nil {
		t.Fatalf("expected LastRunAt nil, got %v", jobs[0].LastRunAt)
	}
}
