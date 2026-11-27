package storage

import "time"

// timestampLayout is how every timestamp is stored: UTC with all nine
// fractional digits. Queries order and compare timestamps as text, which
// is time order only when every value has the same width. RFC3339Nano drops
// trailing zeros, so "10.48711Z" would sort after "10.487110001Z", a
// nanosecond later, which is the step clock.Now takes when Windows' clock
// has not moved. time.RFC3339Nano still parses this form.
const timestampLayout = "2006-01-02T15:04:05.000000000Z07:00"

// timestamp is t as storage writes it (timestampLayout). Every timestamp
// written to a table goes through it.
func timestamp(t time.Time) string {
	return t.UTC().Format(timestampLayout)
}
