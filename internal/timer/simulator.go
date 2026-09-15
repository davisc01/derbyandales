package timer

import (
	"fmt"
	"io"
	"math/rand"
	"regexp"
	"strings"
	"sync"
	"time"
)

// SimulatorKey identifies the simulated timer.
const SimulatorKey = "simulator"

// The simulator speaks the real FastTrack protocol rather than faking the layer
// above it: it echoes commands, acknowledges with '*', answers RV and RF, holds
// a gate state, and emits a genuine result line.
//
// That is the point. A simulator that shortcut to "here are four times" would
// never exercise line assembly, detector excision, masking, or the gate
// debounce — which is exactly where the bugs are. This one does, so the whole
// driver can be developed and tested without the track, and the test bench can
// be demonstrated with no hardware present.

// SimulatorProfile is the FastTrack profile pointed at the simulated device.
func SimulatorProfile() *Profile {
	p := FastTrack()
	p.Name = "Simulated timer"
	p.Key = SimulatorKey
	return p
}

// SimOptions tunes the simulated hardware.
type SimOptions struct {
	Lanes int
	// Fastest and Slowest bound the generated times.
	Fastest, Slowest float64
	// DNFLanes are lanes that will never report a finish.
	DNFLanes []int
	// GateBounce makes the gate report a few spurious readings when it moves,
	// the way a real switch does.
	GateBounce bool
	// UnterminatedResults omits the newline after the result line, which real
	// FastTrack hardware does.
	UnterminatedResults bool
	// NoLaserReset makes RF report a timer that cannot be reset over serial.
	NoLaserReset bool
	// GateUnsupported makes RG answer "X".
	GateUnsupported bool
	// ResultDelay is how long after the gate opens results appear.
	ResultDelay time.Duration
	// Seed makes the generated times reproducible.
	Seed int64
}

// DefaultSimOptions describes a healthy four-lane FastTrack K3.
func DefaultSimOptions() SimOptions {
	return SimOptions{
		Lanes:               4,
		Fastest:             2.35,
		Slowest:             2.80,
		UnterminatedResults: true,
		ResultDelay:         600 * time.Millisecond,
		Seed:                1,
	}
}

// Simulator is a fake serial port that behaves like a FastTrack timer.
type Simulator struct {
	opts SimOptions
	rng  *rand.Rand

	mu         sync.Mutex
	out        []byte
	outCh      chan struct{}
	closed     bool
	masked     map[int]bool
	gateClosed bool
	running    bool

	// dropLanes are lanes whose next result is thrown away, and dropAll drops
	// the whole heat. This is how a bad read is rehearsed: the club's timer
	// does sometimes report nothing, and the recovery — resetting the gate to
	// end the heat — is worth practising before the night it matters.
	dropLanes map[int]bool
	dropAll   bool

	// Log records everything written to the device, for assertions.
	Log []string
}

// NewSimulator returns a simulated timer.
func NewSimulator(opts SimOptions) *Simulator {
	if opts.Lanes <= 0 {
		opts.Lanes = 4
	}
	if opts.Fastest <= 0 {
		opts.Fastest, opts.Slowest = 2.35, 2.80
	}
	return &Simulator{
		opts:   opts,
		rng:    rand.New(rand.NewSource(opts.Seed)),
		outCh:  make(chan struct{}, 1),
		masked: make(map[int]bool),
	}
}

var simCommandPattern = regexp.MustCompile(`^([A-Z])([A-Z0-9])$`)

// Write receives a command from the driver.
func (s *Simulator) Write(p []byte) (int, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	s.mu.Unlock()

	for _, cmd := range splitCommands(string(p)) {
		s.handle(cmd)
	}
	return len(p), nil
}

// splitCommands breaks a write into two-character commands. FastTrack commands
// have no terminator, so several can arrive in one write.
func splitCommands(s string) []string {
	s = strings.TrimSpace(s)
	var out []string
	for len(s) >= 2 {
		out = append(out, s[:2])
		s = s[2:]
	}
	return out
}

func (s *Simulator) handle(cmd string) {
	s.mu.Lock()
	s.Log = append(s.Log, cmd)
	s.mu.Unlock()

	if !simCommandPattern.MatchString(cmd) {
		return
	}

	// Real hardware echoes the command back before answering.
	s.emit(cmd + "\n")

	switch cmd {
	case ftReadVersion:
		s.emit("Copyright (c) Micro Wizard 2002-2005\n")
		s.emit("K3 Version 1.05A  Serial Number 15985\n")

	case ftReturnFeats:
		// Eight feature bits, written most-significant first, so the third
		// character is bit 6: "laser reset from computer". The last must be 1,
		// or the timer would not be sending serial race data at all.
		if s.opts.NoLaserReset {
			s.emit("1001 0111\n")
		} else {
			s.emit("1011 0111\n")
		}

	case ftResetElim, ftFormatNew, ftFormatEnh:
		// Accepted silently.

	case ftUnmaskLanes:
		s.mu.Lock()
		s.masked = make(map[int]bool)
		s.mu.Unlock()

	case ftReadGate:
		if s.opts.GateUnsupported {
			s.emit("X\n")
			return
		}
		s.mu.Lock()
		closed := s.gateClosed
		s.mu.Unlock()
		if closed {
			s.emit("RG1\n")
		} else {
			s.emit("RG0\n")
		}

	case ftResetLaser:
		s.emit("*\n")

	case ftPulseLaser:
		// Releases the cars on a track with an automatic gate.
		go s.OpenGate()

	default:
		// Mask a lane: "MA".."MF".
		if strings.HasPrefix(cmd, ftMaskPrefix) && len(cmd) == 2 &&
			cmd[1] >= 'A' && cmd[1] <= 'F' {
			s.mu.Lock()
			s.masked[int(cmd[1]-'A')+1] = true
			s.mu.Unlock()
			s.emit("*\n")
		}
	}
}

// CloseGate stages the cars.
func (s *Simulator) CloseGate() { s.setGate(true) }

// OpenGate drops the gate, starting the race and producing results.
func (s *Simulator) OpenGate() { s.setGate(false) }

func (s *Simulator) setGate(closed bool) {
	s.mu.Lock()
	if s.gateClosed == closed {
		s.mu.Unlock()
		return
	}
	s.gateClosed = closed
	start := !closed && !s.running
	if start {
		s.running = true
	}
	s.mu.Unlock()

	if start {
		go func() {
			time.Sleep(s.opts.ResultDelay)
			s.EmitResults()
		}()
	}
}

// DropNextResult makes the next heat report nothing at all, the way a bad read
// looks from the software's side.
func (s *Simulator) DropNextResult() {
	s.mu.Lock()
	s.dropAll = true
	s.mu.Unlock()
}

// DropNextLane makes the next heat report every lane but this one.
func (s *Simulator) DropNextLane(lane int) {
	s.mu.Lock()
	if s.dropLanes == nil {
		s.dropLanes = map[int]bool{}
	}
	s.dropLanes[lane] = true
	s.mu.Unlock()
}

// EmitResults sends a heat result line for the unmasked lanes.
func (s *Simulator) EmitResults() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if s.dropAll {
		// Nothing comes back at all. The heat ends when the gate is reset.
		s.dropAll = false
		s.running = false
		s.mu.Unlock()
		return
	}
	dnf := make(map[int]bool, len(s.opts.DNFLanes))
	for _, l := range s.opts.DNFLanes {
		dnf[l] = true
	}

	times := make(map[int]float64, s.opts.Lanes)
	for lane := 1; lane <= s.opts.Lanes; lane++ {
		if s.masked[lane] || dnf[lane] || s.dropLanes[lane] {
			continue
		}
		spread := s.opts.Slowest - s.opts.Fastest
		times[lane] = s.opts.Fastest + s.rng.Float64()*spread
	}
	s.dropLanes = nil
	s.running = false
	maxLanes := s.opts.Lanes
	s.mu.Unlock()

	// Places, so the place characters are consistent with the times.
	order := make([]int, 0, len(times))
	for lane := range times {
		order = append(order, lane)
	}
	for i := 0; i < len(order); i++ {
		for j := i + 1; j < len(order); j++ {
			if times[order[j]] < times[order[i]] {
				order[i], order[j] = order[j], order[i]
			}
		}
	}
	place := make(map[int]int, len(order))
	for i, lane := range order {
		place[lane] = i + 1
	}

	var b strings.Builder
	for lane := 1; lane <= maxLanes; lane++ {
		if lane > 1 {
			b.WriteString(" ")
		}
		t, ok := times[lane]
		if !ok {
			// A masked or non-finishing lane reads as zero, with no place.
			fmt.Fprintf(&b, "%c=0.000 ", 'A'+byte(lane-1))
			continue
		}
		fmt.Fprintf(&b, "%c=%.3f%c", 'A'+byte(lane-1), t, '!'+byte(place[lane]-1))
	}

	line := b.String()
	if !s.opts.UnterminatedResults {
		line += "\n"
	}
	s.emit(line)
}

// EmitSingleLane sends a result line where exactly one lane recorded a finish
// and every other lane reads as zero.
//
// This is one car rolled down one lane, which is how the lane-mapping check
// works: whichever input reports is the one that lane is actually wired to.
func (s *Simulator) EmitSingleLane(lane int, seconds float64) {
	s.mu.Lock()
	maxLanes := s.opts.Lanes
	unterminated := s.opts.UnterminatedResults
	s.mu.Unlock()

	var b strings.Builder
	for l := 1; l <= maxLanes; l++ {
		if l > 1 {
			b.WriteString(" ")
		}
		if l == lane {
			fmt.Fprintf(&b, "%c=%.3f!", 'A'+byte(l-1), seconds)
		} else {
			fmt.Fprintf(&b, "%c=0.000 ", 'A'+byte(l-1))
		}
	}
	line := b.String()
	if !unterminated {
		line += "\n"
	}
	s.emit(line)
}

// emit queues bytes for the driver to read.
func (s *Simulator) emit(text string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.out = append(s.out, text...)
	s.mu.Unlock()

	select {
	case s.outCh <- struct{}{}:
	default:
	}
}

// Read delivers queued bytes, blocking until there are some.
func (s *Simulator) Read(p []byte) (int, error) {
	for {
		s.mu.Lock()
		if len(s.out) > 0 {
			n := copy(p, s.out)
			s.out = s.out[n:]
			s.mu.Unlock()
			return n, nil
		}
		if s.closed {
			s.mu.Unlock()
			return 0, io.EOF
		}
		s.mu.Unlock()

		select {
		case <-s.outCh:
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Close shuts the simulated port.
func (s *Simulator) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	select {
	case s.outCh <- struct{}{}:
	default:
	}
	return nil
}

// Commands returns everything the driver has written, for assertions.
func (s *Simulator) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.Log...)
}

// MaskedLanes reports which lanes the driver masked off.
func (s *Simulator) MaskedLanes() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []int
	for lane, m := range s.masked {
		if m {
			out = append(out, lane)
		}
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
