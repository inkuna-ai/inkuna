package clock

import (
	"sync"
	"testing"
	"time"
)

func TestNowNeverRepeatsEvenWhenCalledAtOnce(t *testing.T) {
	const callers, calls = 8, 1000
	var group sync.WaitGroup
	times := make(chan time.Time, callers*calls)
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			previous := time.Time{}
			for range calls {
				now := Now()
				if !now.After(previous) {
					t.Errorf("Now went from %v to %v", previous, now)
				}
				previous = now
				times <- now
			}
		}()
	}
	group.Wait()
	close(times)
	seen := make(map[time.Time]bool, callers*calls)
	for now := range times {
		if seen[now] {
			t.Fatalf("Now returned %v twice", now)
		}
		seen[now] = true
		if now.Location() != time.UTC {
			t.Fatalf("Now is in %v, want UTC", now.Location())
		}
	}
}
