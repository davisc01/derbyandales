package timer

import (
	"testing"
	"time"
)

// Bad reads. The club's FastTrack sometimes reports nothing at all for a lane,
// or for a heat. Waiting for a result that is not coming is not an option, so
// resetting the start gate ends the heat — whichever comes first, every lane
// reporting or the gate going back up.

// stageAndStart takes a machine through arm, stage and release.
func stageAndStart(t *testing.T, m *Machine, clock *fakeClock) {
	t.Helper()
	m.Arm(0b1111)

	m.GateReading(true)
	clock.advance(MinGateTime + time.Millisecond)
	if settled, _ := m.GateReading(true); !settled {
		t.Fatal("the gate did not stage the cars")
	}
	m.GateReading(false)
	clock.advance(MinGateTime + time.Millisecond)
	if settled, _ := m.GateReading(false); !settled {
		t.Fatal("the gate did not release the cars")
	}
	if m.State() != StateRunning {
		t.Fatalf("state = %s, want running", m.State())
	}
}

// The rule: resetting the gate ends a heat that is still waiting for results.
func TestClosingTheGateEndsARunningHeat(t *testing.T) {
	m, clock := newTestMachine()
	stageAndStart(t, m, clock)

	// Two lanes come back; the other two never do.
	m.AddResult(LaneResult{Lane: 1, Time: 2.431})
	m.AddResult(LaneResult{Lane: 3, Time: 2.512})

	m.GateReading(true)
	clock.advance(MinGateTime + time.Millisecond)
	settled, endsHeat := m.GateReading(true)
	if !settled {
		t.Fatal("the gate close was not believed")
	}
	if !endsHeat {
		t.Fatal("resetting the gate did not end the heat")
	}

	lanes, missing := m.Finish()
	if len(lanes) != 4 {
		t.Fatalf("%d lanes in the result, want all 4", len(lanes))
	}
	byLane := map[int]float64{}
	for _, l := range lanes {
		byLane[l.Lane] = l.Time
	}
	if byLane[1] != 2.431 || byLane[3] != 2.512 {
		t.Errorf("the lanes that did report were not kept: %v", byLane)
	}
	// The ones the timer never mentioned are 9.999, not absent.
	if byLane[2] != DNFTime || byLane[4] != DNFTime {
		t.Errorf("lanes 2 and 4 came out as %v and %v, want %v", byLane[2], byLane[4], DNFTime)
	}
	if len(missing) != 2 || missing[0] != 2 || missing[1] != 4 {
		t.Errorf("missing lanes reported as %v, want [2 4]", missing)
	}

	// The fastest of the two that were timed still won.
	for _, l := range lanes {
		if l.Lane == 1 && l.TimerPlace != 1 {
			t.Errorf("lane 1 ran 2.431 and placed %d", l.TimerPlace)
		}
	}
}

// Staging must not end anything. The gate is closed before every heat, and if
// that counted the heat would finish before it began.
func TestClosingTheGateToStageDoesNotEndAnything(t *testing.T) {
	m, clock := newTestMachine()
	m.Arm(0b1111)

	m.GateReading(true)
	clock.advance(MinGateTime + time.Millisecond)
	settled, endsHeat := m.GateReading(true)
	if !settled {
		t.Fatal("the gate close was not believed")
	}
	if endsHeat {
		t.Fatal("staging the cars ended the heat")
	}
	if m.State() != StateSet {
		t.Errorf("state = %s, want set", m.State())
	}
}

// Whichever comes first. When every lane reports, the heat is over then and
// there — the gate going back up afterwards is just staging the next one.
func TestEveryLaneReportingEndsTheHeatWithoutTheGate(t *testing.T) {
	m, clock := newTestMachine()
	stageAndStart(t, m, clock)

	for lane, tm := range map[int]float64{1: 2.4, 2: 2.5, 3: 2.6, 4: 2.7} {
		complete, accepted := m.AddResult(LaneResult{Lane: lane, Time: tm})
		if !accepted {
			t.Fatalf("lane %d was refused", lane)
		}
		_ = complete
	}

	lanes, missing := m.Finish()
	if len(lanes) != 4 || len(missing) != 0 {
		t.Fatalf("%d lanes, %d missing — want 4 and 0", len(lanes), len(missing))
	}

	// And the gate closing now is staging, not a second finish.
	m.GateReading(true)
	clock.advance(MinGateTime + time.Millisecond)
	if _, endsHeat := m.GateReading(true); endsHeat {
		t.Error("the gate ended a heat that was already over")
	}
}

// A heat where the timer said nothing at all is still recorded — the cars did
// run — but every lane reads 9.999, which the anomaly check then flags.
func TestAHeatTheTimerSaidNothingAboutIsStillRecorded(t *testing.T) {
	m, clock := newTestMachine()
	stageAndStart(t, m, clock)

	m.GateReading(true)
	clock.advance(MinGateTime + time.Millisecond)
	if _, endsHeat := m.GateReading(true); !endsHeat {
		t.Fatal("the gate did not end the heat")
	}

	lanes, missing := m.Finish()
	if len(lanes) != 4 {
		t.Fatalf("%d lanes, want 4 — the cars raced even if nothing was timed", len(lanes))
	}
	if len(missing) != 4 {
		t.Errorf("%d missing lanes, want 4", len(missing))
	}
	for _, l := range lanes {
		if l.Time != DNFTime {
			t.Errorf("lane %d came out as %v, want %v", l.Lane, l.Time, DNFTime)
		}
	}
}
