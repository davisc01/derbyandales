package timer

import (
	"testing"
	"time"
)

// The state machine holds no I/O, so the awkward parts — gate bounce, overdue
// results — are testable with a fake clock instead of real elapsed time.

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestMachine() (*Machine, *fakeClock) {
	clock := &fakeClock{t: time.Date(2027, 4, 24, 19, 0, 0, 0, time.UTC)}
	return newMachineWithClock(clock.now), clock
}

func TestMachineStartsIdle(t *testing.T) {
	m, _ := newTestMachine()
	if m.State() != StateIdle {
		t.Errorf("state = %s, want idle", m.State())
	}
	if m.Racing() {
		t.Error("a fresh machine is not racing")
	}
}

// Real start gates bounce. A single noisy reading must not start the race:
// the timer would report nothing and the heat would have to be re-run.
func TestGateBounceIsIgnored(t *testing.T) {
	m, clock := newTestMachine()
	m.Arm(0b1111)

	// A brief flicker to closed, well under the debounce window.
	m.GateReading(true)
	clock.advance(MinGateTime / 4)
	if settled, _ := m.GateReading(true); settled {
		t.Fatal("the gate changed state before the debounce window elapsed")
	}
	// It bounces back before the window is up.
	clock.advance(MinGateTime / 4)
	m.GateReading(false)

	if m.GateClosed() {
		t.Error("a bounce should not have changed the believed gate state")
	}
	if m.State() != StateMark {
		t.Errorf("state = %s, want mark — a bounce must not stage the cars", m.State())
	}
}

// A reading that persists is believed.
func TestSustainedGateCloseStagesTheCars(t *testing.T) {
	m, clock := newTestMachine()
	m.Arm(0b1111)

	m.GateReading(true) // first sighting, starts the clock
	clock.advance(MinGateTime + time.Millisecond)
	if settled, _ := m.GateReading(true); !settled {
		t.Fatal("a sustained reading should have been believed")
	}

	if !m.GateClosed() {
		t.Error("gate should read closed")
	}
	if m.State() != StateSet {
		t.Errorf("state = %s, want set", m.State())
	}
}

func TestGateOpenStartsTheRace(t *testing.T) {
	m, clock := newTestMachine()
	m.Arm(0b1111)

	// Stage.
	m.GateReading(true)
	clock.advance(MinGateTime + time.Millisecond)
	m.GateReading(true)

	// Drop.
	m.GateReading(false)
	clock.advance(MinGateTime + time.Millisecond)
	m.GateReading(false)

	if m.State() != StateRunning {
		t.Fatalf("state = %s, want running", m.State())
	}
	if m.GateClosed() {
		t.Error("gate should read open")
	}
}

// Transitions drive the displays, so every one has to be announced.
func TestTransitionsAreAnnounced(t *testing.T) {
	m, clock := newTestMachine()

	var seen []string
	m.OnTransition = func(from, to State) { seen = append(seen, string(from)+"->"+string(to)) }

	m.Arm(0b1111)
	m.GateReading(true)
	clock.advance(MinGateTime + time.Millisecond)
	m.GateReading(true)
	m.GateReading(false)
	clock.advance(MinGateTime + time.Millisecond)
	m.GateReading(false)

	want := []string{"idle->mark", "mark->set", "set->running"}
	if len(seen) != len(want) {
		t.Fatalf("transitions %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("transition %d = %q, want %q", i, seen[i], want[i])
		}
	}
}

// A heat completes when every lane in use has reported — not when every lane
// the timer can see has.
func TestHeatCompletesWhenEveryUsedLaneReports(t *testing.T) {
	m, _ := newTestMachine()
	m.Arm(0b1011) // lanes 1, 2 and 4; lane 3 is a bye

	for _, r := range []LaneResult{
		{Lane: 1, Time: 2.40},
		{Lane: 2, Time: 2.45},
	} {
		complete, accepted := m.AddResult(r)
		if !accepted {
			t.Fatalf("lane %d was refused", r.Lane)
		}
		if complete {
			t.Fatalf("heat reported complete after only lane %d", r.Lane)
		}
	}

	// A masked lane reporting anyway, which FastTrack does, must not complete
	// the heat on its own.
	if complete, _ := m.AddResult(LaneResult{Lane: 3, Time: 9.999}); complete {
		t.Error("a bye lane completed the heat; lane 4 has not reported yet")
	}

	complete, accepted := m.AddResult(LaneResult{Lane: 4, Time: 2.50})
	if !accepted || !complete {
		t.Errorf("final lane: complete=%v accepted=%v, want both true", complete, accepted)
	}
}

// Places are recomputed from the times; the bye lane must not appear at all.
func TestFinishExcludesByeLanes(t *testing.T) {
	m, _ := newTestMachine()
	m.Arm(0b1011)

	m.AddResult(LaneResult{Lane: 1, Time: 2.50})
	m.AddResult(LaneResult{Lane: 2, Time: 2.40})
	m.AddResult(LaneResult{Lane: 3, Time: 9.999}) // masked, reports anyway
	m.AddResult(LaneResult{Lane: 4, Time: 2.60})

	results, _ := m.Finish()
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3 — the bye lane should be dropped", len(results))
	}
	for _, r := range results {
		if r.Lane == 3 {
			t.Error("the bye lane appeared in the results")
		}
	}

	byLane := map[int]LaneResult{}
	for _, r := range results {
		byLane[r.Lane] = r
	}
	if byLane[2].TimerPlace != 1 || byLane[1].TimerPlace != 2 || byLane[4].TimerPlace != 3 {
		t.Errorf("places %d/%d/%d for lanes 2/1/4, want 1/2/3",
			byLane[2].TimerPlace, byLane[1].TimerPlace, byLane[4].TimerPlace)
	}
	if m.State() != StateIdle {
		t.Errorf("state after Finish = %s, want idle", m.State())
	}
}

func TestResultsRefusedWhenIdle(t *testing.T) {
	m, _ := newTestMachine()
	if _, accepted := m.AddResult(LaneResult{Lane: 1, Time: 2.4}); accepted {
		t.Error("a result must be refused when no heat is armed")
	}
}

// Some timers cannot report their gate. Results arriving are then the only
// evidence the race happened, and must be accepted.
func TestResultsWithoutAGateReadingStillCount(t *testing.T) {
	m, _ := newTestMachine()
	m.Arm(0b0011)
	m.GateNotSupported()

	if m.State() != StateMark {
		t.Fatalf("state = %s, want mark", m.State())
	}
	complete, accepted := m.AddResult(LaneResult{Lane: 1, Time: 2.4})
	if !accepted {
		t.Fatal("result refused despite a heat being armed")
	}
	if m.State() != StateRunning {
		t.Errorf("state = %s, want running — results imply the race happened", m.State())
	}
	if complete {
		t.Error("heat completed with only one of two lanes reported")
	}
}

// A car that stops on the track never breaks the finish beam, so the heat has
// to be given up on rather than waited for forever.
func TestOverdueResults(t *testing.T) {
	m, clock := newTestMachine()
	m.Arm(0b1111)

	m.GateReading(true)
	clock.advance(MinGateTime + time.Millisecond)
	m.GateReading(true)
	m.GateReading(false)
	clock.advance(MinGateTime + time.Millisecond)
	m.GateReading(false)

	if m.Overdue() {
		t.Error("a heat that just started is not overdue")
	}
	clock.advance(ResultsOverdue + time.Second)
	if !m.Overdue() {
		t.Error("a heat should be overdue after the timeout")
	}

	m.ReturnToMark()
	if m.State() != StateMark {
		t.Errorf("state = %s, want mark so the heat can be re-run", m.State())
	}
	if m.Overdue() {
		t.Error("a re-armed heat should not still be overdue")
	}
}

func TestOverdueOnlyAppliesWhileRunning(t *testing.T) {
	m, clock := newTestMachine()
	m.Arm(0b1111)
	clock.advance(ResultsOverdue * 10)
	if m.Overdue() {
		t.Error("a heat waiting to start is not overdue, however long the wait")
	}
}

func TestDisarmAbandonsTheHeat(t *testing.T) {
	m, _ := newTestMachine()
	m.Arm(0b1111)
	m.AddResult(LaneResult{Lane: 1, Time: 2.4})

	m.Disarm()
	if m.State() != StateIdle {
		t.Errorf("state = %s, want idle", m.State())
	}
	if _, accepted := m.AddResult(LaneResult{Lane: 2, Time: 2.5}); accepted {
		t.Error("results should be refused after disarming")
	}
}

// Every lane reading as a non-finish nearly always means the timer triggered
// with no cars on the track. Recording that as a heat would be worse than
// useless.
func TestAllZeroesIsDetectable(t *testing.T) {
	h := NewHeatResult(0b1111)
	for lane := 1; lane <= 4; lane++ {
		h.Add(LaneResult{Lane: lane, Time: 9.999})
	}
	if !h.AllZeroes(0b1111) {
		t.Error("a heat where nothing finished should be detectable")
	}

	h = NewHeatResult(0b1111)
	h.Add(LaneResult{Lane: 1, Time: 2.4})
	for lane := 2; lane <= 4; lane++ {
		h.Add(LaneResult{Lane: lane, Time: 9.999})
	}
	if h.AllZeroes(0b1111) {
		t.Error("one real finish means the heat happened")
	}
}
