// Package clock gives times that order the events of one process. The
// wall clock Go reads on Windows advances in steps of up to milliseconds,
// so two calls in a row can return the same instant: identifiers made from
// it collide, and things stamped with it cannot be told apart in order.
package clock

import (
	"sync"
	"time"
)

var (
	mu   sync.Mutex
	last time.Time
)

// Now is the current time in UTC, a nanosecond after the last time it
// returned when the clock has not moved since.
func Now() time.Time {
	mu.Lock()
	defer mu.Unlock()
	now := time.Now().UTC().Round(0)
	if !now.After(last) {
		now = last.Add(time.Nanosecond)
	}
	last = now
	return now
}
