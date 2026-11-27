package notify

import (
	"errors"
	"strings"
	"testing"
)

type failing struct{}

func (failing) Notify(Notification) error { return errors.New("no notification center") }

func TestRecorderAndMulti(t *testing.T) {
	recorder := &Recorder{}
	multi := Multi{failing{}, recorder, Noop{}}
	err := multi.Notify(Notification{Title: "Input needed", Body: "Which database?", Kind: "input-required", Reference: "run-1"})
	if err == nil {
		t.Fatal("expected first error to surface")
	}
	sent := recorder.Sent()
	if len(sent) != 1 || sent[0].Reference != "run-1" {
		t.Fatalf("recorder did not capture notification: %+v", sent)
	}
}

func TestTruncateKeepsBodiesShort(t *testing.T) {
	long := strings.Repeat("x", 500)
	if got := truncate(long, 240); len([]rune(got)) != 240 || !strings.HasSuffix(got, "…") {
		t.Fatalf("unexpected truncation length %d", len(got))
	}
	if got := truncate("short", 240); got != "short" {
		t.Fatal("short values must pass through")
	}
}
