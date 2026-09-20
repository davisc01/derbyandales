package timer

import (
	"io"
	"strings"
	"sync"
	"time"
)

// Port is the serial connection. The interface exists so the simulator and the
// tests can stand in for real hardware.
type Port interface {
	io.ReadWriteCloser
}

// Timing constants for reading from a real timer. These are not arbitrary —
// each one exists because of a way real hardware misbehaves.
const (
	// NewlineTimeout is how long to wait before treating a partial buffer as a
	// complete line. Several timers, FastTrack included, do not terminate their
	// final line, so without this the last result of a heat never arrives.
	NewlineTimeout = 200 * time.Millisecond

	// SilenceTimeout is how long with no bytes at all before the connection is
	// declared lost. A USB-serial adapter that has been unplugged looks exactly
	// like a timer with nothing to say, so only the clock can tell them apart.
	SilenceTimeout = 2 * time.Second

	// readChunk is the read buffer size. Timer output is tiny.
	readChunk = 256
)

// Line is one line of timer output, with the moment it completed.
type Line struct {
	Text string
	At   time.Time
	// Inferred reports that the line was never newline-terminated and was
	// completed by the timeout instead.
	Inferred bool
}

// Reader turns a byte stream into lines, coping with unterminated output.
type Reader struct {
	port Port
	now  func() time.Time

	lines  chan Line
	errs   chan error
	closed chan struct{}

	mu       sync.Mutex
	lastRecv time.Time
	partial  bool
}

// NewReader starts reading from port. Call Close to stop.
func NewReader(port Port) *Reader {
	return newReaderWithClock(port, time.Now)
}

func newReaderWithClock(port Port, now func() time.Time) *Reader {
	r := &Reader{
		port:     port,
		now:      now,
		lines:    make(chan Line, 64),
		errs:     make(chan error, 1),
		closed:   make(chan struct{}),
		lastRecv: now(),
	}
	go r.run()
	return r
}

// Lines yields completed lines.
func (r *Reader) Lines() <-chan Line { return r.lines }

// Err yields the first read error, including a lost connection.
func (r *Reader) Err() <-chan error { return r.errs }

// Pending reports whether a partial, unterminated line is being held.
//
// A FastTrack terminates nothing, so that line is still coming: it will be
// assembled by the timeout and land after whatever is asked next. Anyone who
// needs a clean exchange has to wait for it rather than treat it as an answer.
func (r *Reader) Pending() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.partial
}

// Silent reports how long it has been since any byte arrived.
func (r *Reader) Silent() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now().Sub(r.lastRecv)
}

// Close stops reading.
func (r *Reader) Close() error {
	select {
	case <-r.closed:
		return nil
	default:
		close(r.closed)
	}
	return r.port.Close()
}

// run reads bytes and assembles lines.
//
// Reads happen on their own goroutine so that the assembly loop can also wake
// on a timer: that is what lets an unterminated line be completed by elapsed
// time rather than by more input that may never come.
func (r *Reader) run() {
	defer close(r.lines)

	raw := make(chan []byte, 16)
	readErr := make(chan error, 1)

	go func() {
		buf := make([]byte, readChunk)
		for {
			n, err := r.port.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				select {
				case raw <- chunk:
				case <-r.closed:
					return
				}
			}
			if err != nil {
				select {
				case readErr <- err:
				default:
				}
				return
			}
		}
	}()

	var pending strings.Builder
	// idle fires when a partial line has been sitting long enough to be treated
	// as complete. It is stopped whenever there is nothing pending.
	idle := time.NewTimer(time.Hour)
	defer idle.Stop()
	stopIdle := func() {
		if !idle.Stop() {
			select {
			case <-idle.C:
			default:
			}
		}
	}
	stopIdle()

	for {
		select {
		case <-r.closed:
			return

		case err := <-readErr:
			if pending.Len() > 0 {
				r.emit(Line{Text: strings.TrimSpace(pending.String()), At: r.now(), Inferred: true})
			}
			if err != io.EOF {
				select {
				case r.errs <- err:
				default:
				}
			}
			return

		case chunk := <-raw:
			r.mu.Lock()
			r.lastRecv = r.now()
			r.mu.Unlock()

			pending.WriteString(string(chunk))
			text := pending.String()
			pending.Reset()

			// Timers vary between \r\n, \n and \r, so normalise first.
			text = strings.ReplaceAll(text, "\r\n", "\n")
			text = strings.ReplaceAll(text, "\r", "\n")

			for {
				i := strings.IndexByte(text, '\n')
				if i < 0 {
					break
				}
				if line := strings.TrimSpace(text[:i]); line != "" {
					r.emit(Line{Text: line, At: r.now()})
				}
				text = text[i+1:]
			}
			pending.WriteString(text)
			r.setPartial(pending.Len() > 0)

			stopIdle()
			if pending.Len() > 0 {
				idle.Reset(NewlineTimeout)
			}

		case <-idle.C:
			// Nothing more arrived, so whatever is buffered is a whole line.
			if line := strings.TrimSpace(pending.String()); line != "" {
				r.emit(Line{Text: line, At: r.now(), Inferred: true})
			}
			pending.Reset()
			r.setPartial(false)
		}
	}
}

func (r *Reader) setPartial(partial bool) {
	r.mu.Lock()
	r.partial = partial
	r.mu.Unlock()
}

func (r *Reader) emit(l Line) {
	select {
	case r.lines <- l:
	case <-r.closed:
	default:
		// Dropping a line is better than wedging the reader; the caller is not
		// keeping up and the state machine will time out on its own.
	}
}
