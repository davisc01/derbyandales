package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/davisc01/derbyandales/internal/scoring"
	"github.com/davisc01/derbyandales/internal/store"
	"github.com/davisc01/derbyandales/internal/timer"
)

// Entering times by hand. It is how the whole night runs with no timer — which
// is how anybody learns this software — and it is how a bad reading is put
// right afterwards. Both are the same thing.

func TestAWholeHeatCanBeEnteredByHand(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	heats, _ := a.DB.Heats(ctx, raceID)
	heat := heats[0]

	times := map[int]float64{}
	for i, l := range heat.Lanes {
		if l.EntryID != nil {
			times[l.Lane] = 2.400 + float64(i)*0.05
		}
	}
	if err := a.Race.EnterTimes(ctx, heat.ID, times, "coordinator"); err != nil {
		t.Fatalf("EnterTimes: %v", err)
	}

	after, _ := a.DB.Heat(ctx, heat.ID)
	if !after.Complete() {
		t.Fatal("the heat is not complete after being entered by hand")
	}
	// Places are derived here, not taken on trust, so a typed heat and a timed
	// one are the same kind of thing afterwards.
	for _, l := range after.Lanes {
		if l.EntryID == nil {
			continue
		}
		if l.FinishPlace == nil {
			t.Errorf("lane %d has a time but no place", l.Lane)
		}
	}
	first := 0
	for _, l := range after.Lanes {
		if l.FinishPlace != nil && *l.FinishPlace == 1 {
			first = l.Lane
		}
	}
	if first == 0 {
		t.Error("nobody was placed first")
	}

	// And it is on the record, because a hand-entered time is a decision.
	entries, _ := a.DB.RecentAudit(ctx, 20)
	found := false
	for _, e := range entries {
		if e.Action == "race.manual" {
			found = true
		}
	}
	if !found {
		t.Error("entering times by hand was not recorded in the audit log")
	}
}

// A car that did not finish must not read as a very fast one. The timer sends
// 0.000 for that and it becomes 9.999; a person typing should get the same.
func TestAZeroEnteredByHandBecomesANonFinish(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()
	a.Race.GenerateSchedule(ctx, raceID)
	heats, _ := a.DB.Heats(ctx, raceID)
	heat := heats[0]

	times := map[int]float64{}
	for i, l := range heat.Lanes {
		if l.EntryID == nil {
			continue
		}
		if i == 0 {
			times[l.Lane] = 0 // "it never crossed the line"
		} else {
			times[l.Lane] = 2.500
		}
	}
	if err := a.Race.EnterTimes(ctx, heat.ID, times, "coordinator"); err != nil {
		t.Fatal(err)
	}

	after, _ := a.DB.Heat(ctx, heat.ID)
	for _, l := range after.Lanes {
		if l.FinishTime == nil {
			continue
		}
		if *l.FinishTime == 0 {
			t.Errorf("lane %d was recorded as 0.000, which sorts as the fastest run", l.Lane)
		}
	}
	// It sorts last, which is the point.
	var dnfLane, dnfPlace int
	for _, l := range after.Lanes {
		if l.FinishTime != nil && *l.FinishTime == scoring.DNF {
			dnfLane = l.Lane
			if l.FinishPlace != nil {
				dnfPlace = *l.FinishPlace
			}
		}
	}
	if dnfLane == 0 {
		t.Fatal("the zero was not turned into a non-finish")
	}
	if dnfPlace == 1 {
		t.Error("the car that did not finish was placed first")
	}
}

// The things that would quietly corrupt a result.
func TestHandEnteredTimesAreChecked(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()
	a.Race.GenerateSchedule(ctx, raceID)
	heats, _ := a.DB.Heats(ctx, raceID)
	heat := heats[0]

	cases := []struct {
		name  string
		times map[int]float64
		want  string
	}{
		{"nothing at all", map[int]float64{}, "no times"},
		{"a lane with no car in it", map[int]float64{99: 2.4}, "no car"},
		{"a time too fast to be real", map[int]float64{1: 0.2}, "too fast"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := a.Race.EnterTimes(ctx, heat.ID, c.times, "coordinator")
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to mention %q", err, c.want)
			}
		})
	}
}

// The intermission is a hard stop, and every door into the track has to be shut.
func TestEnteringTimesIsRefusedDuringTheIntermission(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	a.DB.SetSetting(ctx, store.KeyAutoAdvanceSecs, "0")
	a.Race.GenerateSchedule(ctx, raceID)
	if err := a.Race.Start(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Race.Stop)
	runHeatsUntil(t, a, raceID, func() bool { return a.Race.Intermission(ctx).Active }, 60*time.Second)

	heats, _ := a.DB.Heats(ctx, raceID)
	err := a.Race.EnterTimes(ctx, heats[0].ID, map[int]float64{1: 2.4}, "coordinator")
	if err == nil {
		t.Fatal("times were entered during the intermission")
	}
	if !strings.Contains(err.Error(), "intermission") {
		t.Errorf("error = %q, want it to name the intermission", err)
	}
}

// A correction: the same path, over a heat that already has times.
func TestAWrongTimeCanBeCorrected(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()
	a.Race.GenerateSchedule(ctx, raceID)
	heats, _ := a.DB.Heats(ctx, raceID)
	heat := heats[0]

	first := map[int]float64{}
	for i, l := range heat.Lanes {
		if l.EntryID != nil {
			first[l.Lane] = 2.400 + float64(i)*0.05
		}
	}
	a.Race.EnterTimes(ctx, heat.ID, first, "coordinator")

	// Lane 1 was read wrong; it was actually the fastest.
	if err := a.Race.EnterTimes(ctx, heat.ID, map[int]float64{1: 2.100}, "coordinator"); err != nil {
		t.Fatalf("correcting one lane: %v", err)
	}

	after, _ := a.DB.Heat(ctx, heat.ID)
	for _, l := range after.Lanes {
		if l.Lane == 1 {
			if l.FinishTime == nil || *l.FinishTime != 2.100 {
				t.Errorf("lane 1 was not corrected")
			}
			if l.FinishPlace == nil || *l.FinishPlace != 1 {
				t.Error("the places were not recomputed after the correction")
			}
		}
	}
}

// A bad read: the timer says nothing about a lane, the operator resets the gate
// for the next heat, and that ends the heat. The silent lane is recorded at
// 9.999, not dropped.
func TestABadReadIsRecordedRatherThanLost(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	a.DB.SetSetting(ctx, store.KeyAutoAdvanceSecs, "0")
	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	if err := a.Race.Start(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Race.Stop)

	sim := a.Timer.Simulator()
	if sim == nil {
		t.Fatal("expected the simulated timer")
	}

	// Stage and release, with the timer set to report nothing at all — which is
	// exactly what a bad read looks like from here.
	waitFor(t, 5*time.Second, func() bool {
		return a.Timer.Device().State() == timer.StateMark
	}, "the heat never armed")

	sim.DropNextResult()

	// Wait for each transition rather than assuming a sleep is long enough.
	// The gate is polled and debounced, so under load a fixed pause is a flake
	// waiting to happen — which is exactly how this test first behaved.
	sim.CloseGate()
	waitFor(t, 5*time.Second, func() bool {
		return a.Timer.Device().State() == timer.StateSet
	}, "the cars never staged")

	sim.OpenGate()
	waitFor(t, 5*time.Second, func() bool {
		return a.Timer.Device().State() == timer.StateRunning
	}, "the cars never started")

	// Nothing arrives. The heat is still open.
	time.Sleep(600 * time.Millisecond)
	if p, _ := a.DB.Progress(ctx, raceID); p.Completed != 0 {
		t.Fatal("a heat completed with no result at all")
	}

	// The operator resets the gate to stage the next heat. That ends this one.
	sim.CloseGate()

	waitFor(t, 8*time.Second, func() bool {
		p, err := a.DB.Progress(ctx, raceID)
		return err == nil && p.Completed >= 1
	}, "resetting the gate did not end the heat")

	heats, _ := a.DB.Heats(ctx, raceID)
	run := heats[0]
	for _, l := range run.Lanes {
		if l.EntryID == nil {
			continue
		}
		if l.FinishTime == nil {
			t.Errorf("lane %d has no time at all; a bad read must not drop a car", l.Lane)
			continue
		}
		if *l.FinishTime != scoring.DNF {
			t.Errorf("lane %d recorded %v, want %v", l.Lane, *l.FinishTime, scoring.DNF)
		}
	}

	// Racing carried on rather than stopping dead: a heat nobody timed is a
	// heat to re-run, not a reason to halt the night.
	if !a.Race.State(ctx).Running {
		t.Error("racing stopped after a bad read")
	}

	// And it is counted against the race for the wrap-up, lanes and all, so
	// the end of the night can say which lanes the timer lost.
	events, err := a.DB.HeatEvents(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != store.HeatBadRead || events[0].Heat != run.Number ||
		len(events[0].Lanes) == 0 {
		t.Errorf("heat events = %+v, want one bad read naming its lanes", events)
	}
}

// waitFor polls until done() or the deadline.
func waitFor(t *testing.T, limit time.Duration, done func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(60 * time.Millisecond)
	}
	t.Fatal(msg)
}
