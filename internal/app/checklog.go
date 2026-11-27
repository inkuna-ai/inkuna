package app

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

// longestPendingLine bounds a line still waiting for its newline; past it,
// the line is kept as it is so far, so a command that never writes a
// newline cannot grow memory without bound.
const longestPendingLine = 64 << 10

// checkNotifyEvery spaces the notices a running check sends, so a command
// that writes fast does not flood the view.
const checkNotifyEvery = 250 * time.Millisecond

// shortestHiddenSecret is the shortest declared secret value a log hides;
// replacing shorter values everywhere would garble ordinary output.
const shortestHiddenSecret = 6

var redacted = []byte("[REDACTED]")

// checkLog keeps what a check's command writes, with secrets redacted, as a
// head and a tail within a cap. Nothing is left out until the log is over
// its cap; then a quarter of it holds the first lines and the rest the
// latest, since a failure's summary comes last, and what falls between them
// is counted, not kept. It serves the log while the command runs and the
// text stored once it ends.
type checkLog struct {
	mu       sync.Mutex
	capBytes int
	headCap  int
	head     []byte
	headFull bool
	// tail[tailStart:] is the kept tail; the bytes before tailStart are
	// dropped ones not yet compacted away.
	tail      []byte
	tailStart int
	// dropped counts the bytes left out between head and tail; written
	// counts everything the command wrote.
	dropped int64
	written int64
	// pending is output not yet kept: an unfinished line, and anything at
	// its end that could still turn out to be the start of a secret.
	pending    []byte
	secrets    [][]byte
	notify     func()
	lastNotice time.Time
}

func newCheckLog(capBytes int, secrets []string, notify func()) *checkLog {
	log := &checkLog{capBytes: capBytes, headCap: capBytes / 4, notify: notify}
	for _, value := range secrets {
		if len(value) >= shortestHiddenSecret {
			log.secrets = append(log.secrets, []byte(value))
		}
	}
	return log
}

// Write takes the command's output; it never fails, so a command is never
// stopped by its log.
func (log *checkLog) Write(data []byte) (int, error) {
	log.mu.Lock()
	log.written += int64(len(data))
	log.pending = append(log.pending, data...)
	log.drain(false)
	notify := log.notify != nil && time.Since(log.lastNotice) >= checkNotifyEvery
	if notify {
		log.lastNotice = time.Now()
	}
	log.mu.Unlock()
	if notify {
		log.notify()
	}
	return len(data), nil
}

// Close keeps everything still pending, which may not end in a newline.
func (log *checkLog) Close() {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.drain(true)
}

// drain keeps the pending output that is ready. Declared secrets are hidden
// across the whole of it first, so one that spans lines, or a cut in a long
// line, is still found whole. Whatever could be the start of a secret the
// command has not finished writing is held back until it is known; whole
// lines before it are kept, each with credential values redacted, and a
// line that has grown too long is kept as far as it can be.
func (log *checkLog) drain(final bool) {
	for _, value := range log.secrets {
		if bytes.Contains(log.pending, value) {
			log.pending = bytes.ReplaceAll(log.pending, value, redacted)
		}
	}
	ready := len(log.pending)
	if !final {
		ready -= log.heldBack()
	}
	for {
		end := bytes.IndexByte(log.pending[:ready], '\n')
		if end < 0 {
			break
		}
		log.keep(log.pending[:end+1])
		log.pending = log.pending[end+1:]
		ready -= end + 1
	}
	if ready > 0 && (final || ready > longestPendingLine) {
		log.keep(log.pending[:ready])
		log.pending = log.pending[ready:]
	}
	if len(log.pending) == 0 {
		log.pending = nil
	}
}

// heldBack is how much of the end of pending begins a declared secret:
// those bytes wait for the rest of the output to show whether they are one.
func (log *checkLog) heldBack() int {
	held := 0
	for _, value := range log.secrets {
		for length := min(len(value)-1, len(log.pending)); length > held; length-- {
			if bytes.HasSuffix(log.pending, value[:length]) {
				held = length
				break
			}
		}
	}
	return held
}

// keep adds output, credential values redacted: to the head while it fits
// there, else to the tail, which drops its oldest lines only once the log is
// over its cap. A first line too long for the head keeps its beginning
// there, so the log still opens where the command's output did.
func (log *checkLog) keep(line []byte) {
	text := []byte(agentruntime.Redact(string(line)))
	if !log.headFull {
		if len(log.head)+len(text) <= log.headCap {
			log.head = append(log.head, text...)
			return
		}
		log.headFull = true
		if len(log.head) == 0 {
			cut := log.headCap
			for cut > 0 && !utf8.RuneStart(text[cut]) {
				cut--
			}
			log.head = append(log.head, text[:cut]...)
			text = text[cut:]
		}
	}
	log.tail = append(log.tail, text...)
	if over := len(log.tail) - log.tailStart - (log.capBytes - len(log.head)); over > 0 {
		// The tail starts at a line, so the log never opens mid-line; inside
		// a line too long for that, it starts at a character.
		start := log.tailStart + over
		if next := bytes.IndexByte(log.tail[start:], '\n'); next >= 0 && start+next+1 < len(log.tail) {
			start += next + 1
		} else {
			for start < len(log.tail) && !utf8.RuneStart(log.tail[start]) {
				start++
			}
		}
		log.dropped += int64(start - log.tailStart)
		log.tailStart = start
		// Compacting only once the dropped part outgrows the kept one keeps
		// each byte's cost constant however long the command writes.
		if log.tailStart > log.capBytes {
			log.tail = append([]byte(nil), log.tail[log.tailStart:]...)
			log.tailStart = 0
		}
	}
}

// snapshot is the log as kept so far: the head, a marker for anything left
// out, and the tail.
func (log *checkLog) snapshot() (text string, cut bool, written int64) {
	log.mu.Lock()
	defer log.mu.Unlock()
	tail := log.tail[log.tailStart:]
	var builder strings.Builder
	builder.Grow(len(log.head) + len(tail) + 128)
	builder.Write(log.head)
	if log.dropped > 0 {
		if len(log.head) > 0 && log.head[len(log.head)-1] != '\n' {
			builder.WriteByte('\n')
		}
		fmt.Fprintf(&builder, "[… Inkuna left out %d bytes of output here; the log keeps the first and last of it …]\n", log.dropped)
	}
	builder.Write(tail)
	return builder.String(), log.dropped > 0, log.written
}
