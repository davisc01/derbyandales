package timer

import (
	"sync"
	"time"
)

// State is where the timer is in the cycle of a heat.
type State string

const (
	// StateIdle: nothing is armed. Results arriving now are refused.
	StateIdle State = "idle"
	// StateMark: a heat is armed and we are waiting for cars to be staged.
	StateMark State = "mark"
	// StateSet: cars are staged behind a closed gate, ready to run.
	StateSet State = "set"
	// StateRunning: the gate is open and cars are on the track.
	StateRunning State = "running"
)

// Timing constants for the racing cycle.
const (
	// MinGateTime is how long a gate reading must persist before it is believed.
	//
	// Real start gates bounce. Without this, a single noisy reading starts the
	// race, the timer reports nothing, and the heat has to be re-run.
	MinGateTime = 500 * time.Millisecond

	// ResultsOverdue is how long after the start to give up waiting.
	// A car that stops on the track never breaks the finish beam.
	ResultsOverdue = 11 * time.Second

	// GatePollInterval is how often the gate is read while not racing.
	GatePollInterval = 250 * time.Millisecond
)

// Machine tracks the racing cycle and debounces the start gate.
//
// It holds no I/O: events go in, state transitions come out. That makes the
// awkward parts — gate bounce, overdue results — testable without hardware or
// real elapsed time.
type Machine struct {
	mu    sync.Mutex
	state State
	now   func() time.Time

	gateClosed      bool
	gateChangeSince time.Time
	gateKnowable    bool

	startedAt time.Time
	result    *HeatResult
	laneMask  uint

	// OnTransition is called for every state change, outside the lock.
	OnTransition func(from, to State)
}

// NewMachine returns an idle machine.
func NewMachine() *Machine { return newMachineWithClock(time.Now) }

func newMachineWithClock(now func() time.Time) *Machine {
	return &Machine{state: StateIdle, now: now, gateKnowable: true}
}

// State reports the current state.
func (m *Machine) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// GateClosed reports the debounced gate reading.
func (m *Machine) GateClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gateClosed
}

// GateKnowable reports whether the timer can tell us about the gate at all.
func (m *Machine) GateKnowable() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gateKnowable
}

// Arm prepares for a heat on the given lanes. laneMask has bit (lane-1) set for
// each lane in use.
func (m *Machine) Arm(laneMask uint) {
	m.mu.Lock()
	m.laneMask = laneMask
	m.result = NewHeatResult(laneMask)
	m.startedAt = time.Time{}
	from := m.state
	m.state = StateMark
	m.gateChangeSince = time.Time{}
	m.mu.Unlock()
	m.fire(from, StateMark)
}

// Disarm returns to idle, abandoning any heat in progress.
func (m *Machine) Disarm() {
	m.mu.Lock()
	from := m.state
	m.state = StateIdle
	m.result = nil
	m.laneMask = 0
	m.mu.Unlock()
	if from != StateIdle {
		m.fire(from, StateIdle)
	}
}

// GateReading feeds in one raw gate observation. It returns true if the
// debounced state changed as a result.
//
// A reading only counts once it has persisted for MinGateTime, so a bouncing
// switch cannot start a race.
func (m *Machine) GateReading(closed bool) (settled, endsHeat bool) {
	m.mu.Lock()

	if closed == m.gateClosed {
		// Agrees with what we believe; cancel any pending change.
		m.gateChangeSince = time.Time{}
		m.mu.Unlock()
		return false, false
	}

	now := m.now()
	if m.gateChangeSince.IsZero() {
		m.gateChangeSince = now
		m.mu.Unlock()
		return false, false
	}
	if now.Sub(m.gateChangeSince) < MinGateTime {
		m.mu.Unlock()
		return false, false
	}

	m.gateClosed = closed
	m.gateChangeSince = time.Time{}

	from := m.state
	to := from
	switch {
	case closed && m.state == StateMark:
		// Cars staged behind a closed gate.
		to = StateSet
	case !closed && m.state == StateSet:
		// Gate dropped: they are running.
		to = StateRunning
		m.startedAt = now
	case closed && m.state == StateRunning:
		// Resetting the gate for the next heat is the operator saying this one
		// is over. It is the club's rule, and it is the answer to a bad read:
		// when the timer reports nothing at all, waiting for it is waiting for
		// something that is not coming. Whichever happens first — every lane
		// reporting, or the gate going back up — ends the heat.
		//
		// The state is left as it is; the caller finishes the heat, which is
		// what moves it to idle.
		endsHeat = true
	}
	m.state = to
	m.mu.Unlock()

	if from != to {
		m.fire(from, to)
	}
	return true, endsHeat
}

// GateNotSupported records that this timer cannot report the gate.
//
// Racing then relies on results simply arriving, which works but gives no
// "ready to race" indication. The test bench says so rather than leaving the
// operator to wonder why the screen never changes.
func (m *Machine) GateNotSupported() {
	m.mu.Lock()
	m.gateKnowable = false
	m.mu.Unlock()
}

// AddResult records a lane result. It reports whether the heat is now complete,
// and whether the result was accepted at all.
//
// Results arriving while idle are refused. That is not pedantry: someone
// rolling a car down the track between heats would otherwise overwrite a
// finished result.
func (m *Machine) AddResult(r LaneResult) (complete, accepted bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.result == nil || m.state == StateIdle {
		return false, false
	}
	// Results can arrive without ever having seen the gate open, on a timer
	// whose gate we cannot read. Treat them as proof the race happened.
	if m.state == StateMark || m.state == StateSet {
		from := m.state
		m.state = StateRunning
		m.startedAt = m.now()
		defer m.fire(from, StateRunning)
	}
	return m.result.Add(r), true
}

// Finish completes the heat and returns the results, ordered by lane with
// places recomputed from the times.
// missing names the armed lanes the timer never reported, which is what a bad
// read looks like. Those lanes are in the results at 9.999; this says which
// ones they are, so the heat can be reported honestly rather than just
// recorded.
func (m *Machine) Finish() (lanes []LaneResult, missing []int) {
	m.mu.Lock()
	if m.result == nil {
		m.mu.Unlock()
		return nil, nil
	}
	lanes = m.result.Lanes(m.laneMask)
	missing = m.result.Missing(m.laneMask)
	from := m.state
	m.state = StateIdle
	m.result = nil
	m.mu.Unlock()

	if from != StateIdle {
		m.fire(from, StateIdle)
	}
	return lanes, missing
}

// Overdue reports whether a running heat has waited too long for results.
func (m *Machine) Overdue() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != StateRunning || m.startedAt.IsZero() {
		return false
	}
	return m.now().Sub(m.startedAt) > ResultsOverdue
}

// ReturnToMark puts an overdue heat back to armed so it can be re-run.
func (m *Machine) ReturnToMark() {
	m.mu.Lock()
	from := m.state
	m.state = StateMark
	m.startedAt = time.Time{}
	m.result = NewHeatResult(m.laneMask)
	m.mu.Unlock()
	if from != StateMark {
		m.fire(from, StateMark)
	}
}

// Racing reports whether a heat is armed or under way.
func (m *Machine) Racing() bool {
	s := m.State()
	return s == StateMark || s == StateSet || s == StateRunning
}

func (m *Machine) fire(from, to State) {
	if m.OnTransition != nil && from != to {
		m.OnTransition(from, to)
	}
}
