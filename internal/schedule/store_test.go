package schedule

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreAddAssignsIDAndNextRun(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(1 * time.Hour)
	added, err := store.Add(Job{
		Name:     "test",
		Enabled:  true,
		Schedule: Schedule{Kind: KindAt, At: &at},
		Text:     "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if added.ID == "" {
		t.Fatal("expected ID to be assigned")
	}
	if added.NextRunAt == nil || !added.NextRunAt.Equal(at) {
		t.Fatalf("expected NextRunAt=%v, got %v", at, added.NextRunAt)
	}
	if added.WakeMode != WakeModeNow {
		t.Fatalf("expected default WakeMode=now, got %q", added.WakeMode)
	}
}

func TestStorePersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	at := time.Now().Add(1 * time.Hour)
	if _, err := store.Add(Job{
		Name:     "p",
		Enabled:  true,
		Schedule: Schedule{Kind: KindAt, At: &at},
		Text:     "x",
	}); err != nil {
		t.Fatal(err)
	}

	store2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	jobs := store2.List()
	if len(jobs) != 1 || jobs[0].Name != "p" {
		t.Fatalf("expected reload to yield 1 job named p, got %+v", jobs)
	}
	if jobs[0].Schedule.At == nil || !jobs[0].Schedule.At.Equal(at) {
		t.Fatalf("expected At=%v, got %v", at, jobs[0].Schedule.At)
	}
}

func TestStoreRemoveIdempotent(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	at := time.Now().Add(1 * time.Hour)
	added, _ := store.Add(Job{
		Name:     "rm",
		Enabled:  true,
		Schedule: Schedule{Kind: KindAt, At: &at},
		Text:     "x",
	})

	ok, err := store.Remove(added.ID)
	if err != nil || !ok {
		t.Fatalf("first remove: ok=%v err=%v", ok, err)
	}
	if jobs := store.List(); len(jobs) != 0 {
		t.Fatalf("expected empty, got %+v", jobs)
	}
	ok, err = store.Remove(added.ID)
	if err != nil || ok {
		t.Fatalf("second remove: expected ok=false err=nil, got ok=%v err=%v", ok, err)
	}
}

func TestStoreMarkRunDisablesAtJob(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	at := time.Now().Add(1 * time.Hour)
	added, _ := store.Add(Job{
		Name:     "once",
		Enabled:  true,
		Schedule: Schedule{Kind: KindAt, At: &at},
		Text:     "x",
	})

	if err := store.MarkRun(added.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	jobs := store.List()
	if jobs[0].Enabled {
		t.Fatal("expected at job to be disabled after firing")
	}
	if jobs[0].NextRunAt != nil {
		t.Fatalf("expected nil NextRunAt, got %v", jobs[0].NextRunAt)
	}
	if jobs[0].LastRunAt == nil {
		t.Fatal("expected LastRunAt to be set")
	}
}

func TestStoreMarkRunReschedulesEveryJob(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	anchor := time.Now()
	added, _ := store.Add(Job{
		Name:    "tick",
		Enabled: true,
		Schedule: Schedule{
			Kind:   KindEvery,
			Every:  Duration(15 * time.Minute),
			Anchor: &anchor,
		},
		Text: "x",
	})
	runAt := anchor.Add(15 * time.Minute)
	if err := store.MarkRun(added.ID, runAt); err != nil {
		t.Fatal(err)
	}
	jobs := store.List()
	if !jobs[0].Enabled {
		t.Fatal("expected every job to remain enabled")
	}
	want := anchor.Add(30 * time.Minute)
	if jobs[0].NextRunAt == nil || !jobs[0].NextRunAt.Equal(want) {
		t.Fatalf("expected NextRunAt=%v, got %v", want, jobs[0].NextRunAt)
	}
}

func TestStoreLoadEmptyDir(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if jobs := store.List(); len(jobs) != 0 {
		t.Fatalf("expected empty list, got %+v", jobs)
	}
}

func TestStoreFilePermissions(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	at := time.Now().Add(1 * time.Hour)
	if _, err := store.Add(Job{
		Name:     "p",
		Enabled:  true,
		Schedule: Schedule{Kind: KindAt, At: &at},
		Text:     "x",
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, storeFilename))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != filePerm {
		t.Fatalf("expected perms 0%o, got 0%o", filePerm, perm)
	}
}

func TestStoreRejectsSyntheticInsertion(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	at := time.Now().Add(time.Hour)
	_, err := store.Add(Job{
		Name:      "h",
		Enabled:   true,
		Schedule:  Schedule{Kind: KindAt, At: &at},
		Synthetic: true,
	})
	if err == nil {
		t.Fatal("expected synthetic insertion to be rejected")
	}
}

func TestDurationJSONRoundTrip(t *testing.T) {
	d := Duration(15 * time.Minute)
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `"15m0s"` {
		t.Fatalf("unexpected marshal: %s", data)
	}
	var d2 Duration
	if err := json.Unmarshal(data, &d2); err != nil {
		t.Fatal(err)
	}
	if d2 != d {
		t.Fatalf("round-trip mismatch: %v != %v", d, d2)
	}
}
