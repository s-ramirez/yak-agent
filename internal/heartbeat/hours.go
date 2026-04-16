package heartbeat

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// IsWithinActiveHours reports whether now falls within the [start, end)
// window. start and end are "HH:MM" strings in 24-hour format. If either
// is empty the window is considered unbounded and the function returns true.
// A zero-width window (start == end) always returns false. Midnight wrap is
// supported: when end < start (e.g. "23:00"–"02:00") the window spans
// midnight.
func IsWithinActiveHours(start, end, timezone string, now time.Time) bool {
	if start == "" || end == "" {
		return true
	}
	startMin, err := parseHHMM(start)
	if err != nil {
		return true // misconfigured → fail open
	}
	endMin, err := parseHHMM(end)
	if err != nil {
		return true
	}
	if startMin == endMin {
		return false // zero-width window → disabled
	}

	loc := time.Local
	if timezone != "" {
		if l, err := time.LoadLocation(timezone); err == nil {
			loc = l
		}
	}
	t := now.In(loc)
	cur := t.Hour()*60 + t.Minute()

	if endMin > startMin {
		// Non-wrapping window: e.g. 09:00–22:00
		return cur >= startMin && cur < endMin
	}
	// Wrapping window: e.g. 23:00–02:00 spans midnight
	return cur >= startMin || cur < endMin
}

func parseHHMM(s string) (int, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid HH:MM %q", s)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, fmt.Errorf("invalid hour in %q", s)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, fmt.Errorf("invalid minute in %q", s)
	}
	return h*60 + m, nil
}
