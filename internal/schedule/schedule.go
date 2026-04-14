package schedule

import (
	"time"

	"github.com/robfig/cron/v3"
)

// ParseCron parses a 5-field standard cron expression. Exposed so the
// schedule tool can validate user input before persisting.
func ParseCron(spec string) (cron.Schedule, error) {
	return cron.ParseStandard(spec)
}

// NextRunAt computes the next time job should fire, given current time `now`.
// Returns (zero, false) if the job has no future fire time — e.g. a one-shot
// at job that already ran, or a recurring job with an unparseable schedule.
//
// Semantics:
//   - kind=at: returns the absolute At time. If LastRunAt is set, the job is
//     considered done and the function returns false.
//   - kind=every: anchor sets the phase. If anchor is in the future and the
//     job has never run, the first fire is at anchor itself. Otherwise the
//     next fire is the smallest anchor + k*every that is strictly after
//     max(now, LastRunAt). Missed ticks are skipped (no catchup).
//   - kind=cron: parses the cron expression and returns the next activation
//     strictly after max(now, LastRunAt). Missed ticks are skipped — the
//     library's Next() never returns past times.
func NextRunAt(job Job, now time.Time) (time.Time, bool) {
	switch job.Schedule.Kind {
	case KindAt:
		if job.Schedule.At == nil {
			return time.Time{}, false
		}
		if job.LastRunAt != nil {
			return time.Time{}, false
		}
		return *job.Schedule.At, true

	case KindEvery:
		if job.Schedule.Anchor == nil || job.Schedule.Every == 0 {
			return time.Time{}, false
		}
		every := time.Duration(job.Schedule.Every)
		anchor := *job.Schedule.Anchor

		if job.LastRunAt == nil && now.Before(anchor) {
			return anchor, true
		}

		base := now
		if job.LastRunAt != nil && job.LastRunAt.After(base) {
			base = *job.LastRunAt
		}
		if base.Before(anchor) {
			base = anchor
		}
		elapsed := base.Sub(anchor)
		k := int64(elapsed / every)
		return anchor.Add(time.Duration(k+1) * every), true

	case KindCron:
		if job.Schedule.Cron == "" {
			return time.Time{}, false
		}
		parsed, err := ParseCron(job.Schedule.Cron)
		if err != nil {
			return time.Time{}, false
		}
		base := now
		if job.LastRunAt != nil && job.LastRunAt.After(base) {
			base = *job.LastRunAt
		}
		next := parsed.Next(base)
		if next.IsZero() {
			return time.Time{}, false
		}
		return next, true
	}
	return time.Time{}, false
}
