package timer

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// A Trace is a timestamped record of everything said to and by the timer.
//
// This is the difference between "the timer was being weird" and a diagnosable
// report. It is also how a new hardware quirk becomes a regression test: record
// the session, save it to testdata, and replay it.
type Trace struct {
	mu      sync.Mutex
	started time.Time
	lines   []TraceLine
	limit   int
}

// TraceLine is one entry.
type TraceLine struct {
	Offset time.Duration
	// Out is true for bytes we sent, false for bytes the timer sent.
	Out bool
	// Inferred marks a received line that was never newline-terminated and was
	// completed by the read timeout.
	Inferred bool
	Text     string
}

// DefaultTraceLimit caps how much is kept in memory. A race night is a few
// thousand lines; this is generous.
const DefaultTraceLimit = 20000

// NewTrace starts recording.
func NewTrace() *Trace {
	return &Trace{started: time.Now(), limit: DefaultTraceLimit}
}

// Sent records an outgoing command.
func (t *Trace) Sent(cmd string) { t.add(true, false, cmd) }

// Received records an incoming line.
func (t *Trace) Received(line string, inferred bool) { t.add(false, inferred, line) }

func (t *Trace) add(out, inferred bool, text string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.lines) >= t.limit {
		// Keep the most recent half rather than stopping: the interesting part
		// of a long session is nearly always the end.
		t.lines = append(t.lines[:0], t.lines[len(t.lines)/2:]...)
	}
	t.lines = append(t.lines, TraceLine{
		Offset:   time.Since(t.started),
		Out:      out,
		Inferred: inferred,
		Text:     text,
	})
}

// Lines returns a copy of the record.
func (t *Trace) Lines() []TraceLine {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]TraceLine(nil), t.lines...)
}

// Len reports how many entries are held.
func (t *Trace) Len() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.lines)
}

// String renders the trace for the diagnostic console.
func (t *Trace) String() string {
	var b strings.Builder
	for _, l := range t.Lines() {
		dir := "<<"
		if l.Out {
			dir = ">>"
		}
		note := ""
		if l.Inferred {
			note = "   (no newline; completed by timeout)"
		}
		fmt.Fprintf(&b, "%9.3f %s %s%s\n", l.Offset.Seconds(), dir, l.Text, note)
	}
	return b.String()
}

// Save writes the trace to a file.
func (t *Trace) Save(path string) error {
	header := fmt.Sprintf("# Derby and Ales timer trace\n# recorded %s\n# >> sent to timer, << received from timer\n\n",
		t.started.Format(time.RFC3339))
	if err := os.WriteFile(path, []byte(header+t.String()), 0o644); err != nil {
		return fmt.Errorf("timer: save trace: %w", err)
	}
	return nil
}
