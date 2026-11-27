package app

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestACheckLogKeepsWholeLinesAtItsHeadAndTail(t *testing.T) {
	log := newCheckLog(400, nil, nil)
	for line := 1; line <= 100; line++ {
		_, _ = fmt.Fprintf(log, "line %03d\n", line)
	}
	_, _ = log.Write([]byte("no newline"))
	log.Close()
	text, cut, written := log.snapshot()
	if !cut || written != 100*9+10 {
		t.Fatalf("cut %v written %d", cut, written)
	}
	head, tail, found := strings.Cut(text, "[… Inkuna left out ")
	if !found || !strings.HasPrefix(head, "line 001\n") || !strings.HasSuffix(head, "\n") {
		t.Fatalf("head %q", head)
	}
	tail = tail[strings.IndexByte(tail, '\n')+1:]
	if !strings.HasPrefix(tail, "line ") || !strings.HasSuffix(tail, "line 100\nno newline") {
		t.Fatalf("tail %q", tail)
	}
	if len(head) > 100 || len(head)+len(tail) > 400 {
		t.Fatalf("head %d and tail %d bytes, over a quarter and the whole of 400", len(head), len(tail))
	}
}

func TestACheckLogUnderItsCapIsKeptWhole(t *testing.T) {
	log := newCheckLog(1<<20, nil, nil)
	_, _ = log.Write([]byte("one\ntw"))
	_, _ = log.Write([]byte("o\nthree"))
	if text, _, _ := log.snapshot(); text != "one\ntwo\n" {
		t.Fatalf("an unfinished line waits for its newline: %q", text)
	}
	log.Close()
	if text, cut, _ := log.snapshot(); cut || text != "one\ntwo\nthree" {
		t.Fatalf("text %q cut %v", text, cut)
	}
}

func TestNothingIsLeftOutUntilTheLogIsOverItsCap(t *testing.T) {
	log := newCheckLog(4096, nil, nil)
	line := strings.Repeat("a", 3499) + "\n"
	_, _ = log.Write([]byte(line))
	if text, cut, _ := log.snapshot(); cut || text != line {
		t.Fatalf("a 3500-byte line under a 4096-byte cap is kept whole: cut %v, %d bytes", cut, len(text))
	}

	long := newCheckLog(4096, nil, nil)
	_, _ = long.Write([]byte("BEGIN" + strings.Repeat("x", 10_000) + "END\n"))
	text, cut, _ := long.snapshot()
	if !cut || !strings.HasPrefix(text, "BEGIN") || !strings.HasSuffix(text, "END\n") || len(text) > 4096+200 {
		t.Fatalf("a line longer than the cap keeps its beginning and end: cut %v, %d bytes", cut, len(text))
	}
}

func TestALineLongerThanTheCapIsCutBetweenCharacters(t *testing.T) {
	log := newCheckLog(4096, nil, nil)
	_, _ = log.Write([]byte(strings.Repeat("é", 5000) + "\n"))
	log.Close()
	text, cut, _ := log.snapshot()
	if !cut || !utf8.ValidString(text) || !strings.HasPrefix(text, "éé") || !strings.HasSuffix(text, "é\n") {
		t.Fatalf("cut %v, valid UTF-8 %v", cut, utf8.ValidString(text))
	}
}

func TestALongLogCostsLinearTime(t *testing.T) {
	log := newCheckLog(1<<20, nil, nil)
	line := []byte(strings.Repeat("x", 99) + "\n")
	for range 500_000 {
		_, _ = log.Write(line)
	}
	if text, cut, written := log.snapshot(); !cut || written != 50_000_000 || len(text) > 1<<20+256 {
		t.Fatalf("cut %v written %d kept %d", cut, written, len(text))
	}
}

func TestACheckLogHidesSecretsWhereverTheyFall(t *testing.T) {
	secret := "hunter2-very-secret"
	pem := "-----BEGIN KEY-----\nMIIBOgIBAAJBAK\n-----END KEY-----"
	log := newCheckLog(1<<20, []string{secret, pem, "abc"}, nil)
	_, _ = log.Write([]byte("abc stays; using hunter2-ve"))
	_, _ = log.Write([]byte("ry-secret now\n"))
	_, _ = log.Write([]byte("key:\n" + pem[:25]))
	_, _ = log.Write([]byte(pem[25:] + "\npassword: swordfish\n"))
	// A line too long to wait for is kept in parts, and a secret across
	// the cut between them is still hidden whole.
	_, _ = log.Write([]byte(strings.Repeat("y", longestPendingLine) + "hunter2-"))
	_, _ = log.Write([]byte("very-secret tail\n"))
	log.Close()
	text, _, _ := log.snapshot()
	for _, leaked := range []string{"hunter2", "MIIBOgIBAAJBAK", "BEGIN KEY", "swordfish", "very-secret"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("%q leaked into the log", leaked)
		}
	}
	if !strings.HasPrefix(text, "abc stays; using [REDACTED] now\nkey:\n[REDACTED]\npassword: [REDACTED]\n") || !strings.HasSuffix(text, "y[REDACTED] tail\n") {
		t.Fatalf("redacted log %q", text)
	}
}

func TestOutputThatCannotStartASecretIsNotHeldBack(t *testing.T) {
	log := newCheckLog(1<<20, []string{"hunter2-very-secret"}, nil)
	_, _ = log.Write([]byte("compiling\n"))
	if text, _, _ := log.snapshot(); text != "compiling\n" {
		t.Fatalf("a finished line is shown at once: %q", text)
	}
	_, _ = log.Write([]byte("ends with hunt"))
	_, _ = log.Write([]byte("\n"))
	if text, _, _ := log.snapshot(); text != "compiling\nends with hunt\n" {
		t.Fatalf("a line is held only while its end could begin a secret: %q", text)
	}
	_, _ = log.Write([]byte("hunter2-v"))
	log.Close()
	if text, _, _ := log.snapshot(); !strings.HasSuffix(text, "hunter2-v") {
		t.Fatalf("what was held back is kept once the command ends: %q", text)
	}
}
