package schedule

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	storeFilename = "jobs.json"
	dirPerm       = 0o700
	filePerm      = 0o600
	storeVersion  = 1
)

// Store persists scheduled jobs to a JSON file. All operations are safe for
// concurrent use by multiple goroutines (the scheduler ticker and the tool
// invocation paths both touch the store).
//
// Synthetic (in-memory) jobs do not belong in the store — use
// Scheduler.Inject for those.
type Store struct {
	dir   string
	mu    sync.Mutex
	jobs  []Job
	nowFn func() time.Time
}

// NewStore opens (or creates) the store at the given directory. Existing
// jobs are loaded; the directory is created on demand at first save.
func NewStore(dir string) (*Store, error) {
	s := &Store{dir: dir, nowFn: time.Now}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// SetNowFn overrides the time source. Test-only.
func (s *Store) SetNowFn(fn func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nowFn = fn
}

// Now returns the current time as seen by the store. Tools that need to
// reason about "now" (e.g. computing wake delays) should route through here
// so a single time source is used in tests.
func (s *Store) Now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nowFn()
}

// Dir returns the directory backing the store.
func (s *Store) Dir() string { return s.dir }

func (s *Store) path() string { return filepath.Join(s.dir, storeFilename) }

type storeFile struct {
	Version int   `json:"version"`
	Jobs    []Job `json:"jobs"`
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path())
	if err != nil {
		if os.IsNotExist(err) {
			s.jobs = nil
			return nil
		}
		return err
	}
	var file storeFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("schedule: parse %s: %w", s.path(), err)
	}
	s.jobs = file.Jobs
	return nil
}

// save writes jobs to disk atomically. Caller must hold s.mu.
func (s *Store) save() error {
	if err := os.MkdirAll(s.dir, dirPerm); err != nil {
		return err
	}
	file := storeFile{Version: storeVersion, Jobs: s.jobs}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path() + ".tmp"
	if err := os.WriteFile(tmp, data, filePerm); err != nil {
		return err
	}
	return os.Rename(tmp, s.path())
}

// List returns a copy of all jobs sorted by NextRunAt then Name. Jobs with no
// NextRunAt sort to the end.
func (s *Store) List() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, len(s.jobs))
	copy(out, s.jobs)
	sort.Slice(out, func(i, j int) bool {
		ai, aj := out[i].NextRunAt, out[j].NextRunAt
		switch {
		case ai == nil && aj == nil:
			return out[i].Name < out[j].Name
		case ai == nil:
			return false
		case aj == nil:
			return true
		case ai.Equal(*aj):
			return out[i].Name < out[j].Name
		default:
			return ai.Before(*aj)
		}
	})
	return out
}

// Add inserts a new job. ID and CreatedAt are populated if zero. NextRunAt is
// computed from the current time.
func (s *Store) Add(job Job) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job.Synthetic {
		return Job{}, fmt.Errorf("schedule: synthetic jobs must not be persisted; use Scheduler.Inject")
	}
	if job.ID == "" {
		job.ID = newID()
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = s.nowFn()
	}
	if job.WakeMode == "" {
		job.WakeMode = WakeModeNow
	}
	if next, ok := NextRunAt(job, s.nowFn()); ok {
		job.NextRunAt = &next
	}
	s.jobs = append(s.jobs, job)
	if err := s.save(); err != nil {
		s.jobs = s.jobs[:len(s.jobs)-1]
		return Job{}, err
	}
	return job, nil
}

// Remove deletes a job by ID. Returns (false, nil) if not found, (true, nil)
// on success.
func (s *Store) Remove(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, j := range s.jobs {
		if j.ID == id {
			s.jobs = append(s.jobs[:i], s.jobs[i+1:]...)
			if err := s.save(); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}

// MarkRun records that a job fired at runAt and recomputes NextRunAt.
// One-shot at jobs are auto-disabled (Enabled=false, NextRunAt=nil).
func (s *Store) MarkRun(id string, runAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.jobs {
		if s.jobs[i].ID != id {
			continue
		}
		t := runAt
		s.jobs[i].LastRunAt = &t
		if s.jobs[i].Schedule.Kind == KindAt {
			s.jobs[i].Enabled = false
			s.jobs[i].NextRunAt = nil
		} else if next, ok := NextRunAt(s.jobs[i], runAt); ok {
			s.jobs[i].NextRunAt = &next
		} else {
			s.jobs[i].NextRunAt = nil
		}
		return s.save()
	}
	return fmt.Errorf("schedule: job %q not found", id)
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
