package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/davisc01/derbyandales/internal/store"
	"github.com/davisc01/derbyandales/internal/timer"
)

// These drive a whole race night against the simulator: schedule, arm, stage,
// release, record, advance. Nothing here needs the track.

// raceFixture returns an app with the simulated timer connected and a seeded
// race, ready to run.
func raceFixture(t *testing.T) (*App, int64) {
	t.Helper()
	a := testApp(t)
	ctx := context.Background()

	race, err := a.SeedDemoRace(ctx, 2027)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := a.Timer.Connect(ctx, "", timer.SimulatorKey); err != nil {
		t.Fatalf("connect simulator: %v", err)
	}
	if _, err := a.Timer.RunBench(ctx); err != nil {
		t.Fatalf("bench: %v", err)
	}
	return a, race.ID
}

func TestGenerateScheduleFollowsTheClubsRule(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	s, err := a.Race.GenerateSchedule(ctx, raceID)
	if err != nil {
		t.Fatalf("GenerateSchedule: %v", err)
	}

	entries, _ := a.DB.RacingEntries(ctx, raceID)
	if s.Cars != len(entries) {
		t.Errorf("scheduled %d cars, checked in %d", s.Cars, len(entries))
	}
	if s.RunsPerCar() != 4 {
		t.Errorf("runs per car = %d, want 4", s.RunsPerCar())
	}
	if !s.Perfect() {
		t.Errorf("a %d-car field should schedule perfectly, got %d repeated meetings",
			s.Cars, s.RepeatedMeetings)
	}

	// Closing check-in takes a snapshot, because it is the point where losing
	// the night's work would hurt most.
	found := false
	for _, b := range ListBackups(a.Paths.Backups) {
		if b.Reason == BackupCheckinClose {
			found = true
		}
	}
	if !found {
		t.Error("closing check-in should have taken a snapshot")
	}
}

// Regenerating over a race in progress would discard results.
func TestGenerateScheduleRefusesAfterRacingHasStarted(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	heat, err := a.DB.NextHeat(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	times := map[int]float64{}
	for _, l := range heat.Lanes {
		if l.EntryID != nil {
			times[l.Lane] = 2.5
		}
	}
	if err := a.DB.RecordHeatResults(ctx, heat.ID, times); err != nil {
		t.Fatal(err)
	}

	_, err = a.Race.GenerateSchedule(ctx, raceID)
	if err == nil {
		t.Fatal("regenerating over recorded results should be refused")
	}
	if !strings.Contains(err.Error(), "already been run") {
		t.Errorf("error = %q, want it to explain why", err)
	}
}

// Racing without a passing timer test is exactly the situation the bench exists
// to prevent.
func TestStartRefusesWithoutAPassingTimerTest(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	race, err := a.SeedDemoRace(ctx, 2027)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Timer.Connect(ctx, "", timer.SimulatorKey); err != nil {
		t.Fatal(err)
	}
	// Deliberately skip the bench.
	if _, err := a.Race.GenerateSchedule(ctx, race.ID); err != nil {
		t.Fatal(err)
	}

	err = a.Race.Start(ctx, race.ID)
	if err == nil {
		t.Fatal("racing should be refused until the timer test has passed")
	}
	if !strings.Contains(err.Error(), "timer test") {
		t.Errorf("error = %q, want it to name the timer test", err)
	}
}

// A failed test can be overridden, because a jammed gate switch must never stop
// a race from happening.
func TestOverriddenTimerTestAllowsRacing(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	race, _ := a.SeedDemoRace(ctx, 2027)
	if err := a.Timer.Connect(ctx, "", timer.SimulatorKey); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Timer.RunBench(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Race.GenerateSchedule(ctx, race.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.Timer.Override(ctx, "coordinator", "gate switch jammed"); err != nil {
		t.Fatal(err)
	}

	if err := a.Race.Start(ctx, race.ID); err != nil {
		t.Fatalf("racing should be allowed after an override: %v", err)
	}
	a.Race.Stop()
}

// The whole race, hands-off: every heat armed, run and recorded automatically.
func TestFullRaceRunsToCompletion(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	if err := a.DB.SetSetting(ctx, store.KeyAutoAdvanceSecs, "0"); err != nil {
		t.Fatal(err)
	}
	s, err := a.Race.GenerateSchedule(ctx, raceID)
	if err != nil {
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

	// Play the part of the person at the track for every heat — including the
	// coordinator, who has to end the intermission halfway through. Racing
	// stops there on purpose and nothing but a person restarts it.
	resumed := false
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		p, err := a.DB.Progress(ctx, raceID)
		if err != nil {
			t.Fatal(err)
		}
		if p.Completed >= p.Total {
			break
		}
		if a.Race.Intermission(ctx).Active {
			if err := a.Race.ResumeRacing(ctx); err != nil {
				t.Fatalf("resuming after the intermission: %v", err)
			}
			resumed = true
			continue
		}
		if a.Timer.Device().State() == timer.StateMark {
			sim.CloseGate()
			time.Sleep(MinGateSettle)
			sim.OpenGate()
		}
		time.Sleep(120 * time.Millisecond)
	}
	if !resumed {
		t.Error("the race never paused for its intermission")
	}

	p, _ := a.DB.Progress(ctx, raceID)
	if p.Completed != p.Total {
		t.Fatalf("ran %d of %d heats", p.Completed, p.Total)
	}
	if p.Total != len(s.Heats) {
		t.Errorf("stored %d heats, scheduled %d", p.Total, len(s.Heats))
	}

	// Every car should have four recorded runs.
	runs, err := a.DB.RunsByEntry(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	for entryID, rs := range runs {
		if len(rs) != 4 {
			t.Errorf("entry %d has %d runs, want 4", entryID, len(rs))
		}
	}

	standings, err := a.DB.Standings(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(standings) == 0 {
		t.Fatal("no standings after a completed race")
	}
	if standings[0].Place != 1 {
		t.Errorf("first row placed %d, want 1", standings[0].Place)
	}
	for i := 1; i < len(standings); i++ {
		if standings[i].Average < standings[i-1].Average {
			t.Errorf("standings are out of order at row %d", i)
		}
	}

	// The demo data has one ineligible entry; it races but must not be scored.
	entries, _ := a.DB.Entries(ctx, raceID)
	for _, e := range entries {
		if !e.Excluded {
			continue
		}
		for _, st := range standings {
			if st.Entry.ID == e.ID {
				t.Errorf("excluded car %d appears in the standings", e.CarNumber)
			}
		}
	}

	// Finishing a race takes a snapshot and moves it on to voting.
	race, _ := a.DB.Race(ctx, raceID)
	if race.Status == "racing" {
		t.Error("a completed race should have moved past racing")
	}
}

// MinGateSettle is how long the simulated gate is held closed so the real
// debounce accepts it. The debounce exists because physical switches bounce.
const MinGateSettle = 700 * time.Millisecond

// A re-run clears the old times and puts the heat back in the queue.
func TestReRunClearsAndRearms(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	heat, _ := a.DB.NextHeat(ctx, raceID)
	times := map[int]float64{}
	for _, l := range heat.Lanes {
		if l.EntryID != nil {
			times[l.Lane] = 2.5
		}
	}
	a.DB.RecordHeatResults(ctx, heat.ID, times)

	if err := a.Race.ReRun(ctx, heat.ID); err != nil {
		t.Fatalf("ReRun: %v", err)
	}
	t.Cleanup(a.Race.Stop)

	again, err := a.DB.Heat(ctx, heat.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range again.Lanes {
		if l.FinishTime != nil {
			t.Errorf("lane %d still has a time after a re-run", l.Lane)
		}
	}

	// And it is audited, so "why is heat 7 different" is answerable later.
	entries, _ := a.DB.RecentAudit(ctx, 20)
	found := false
	for _, e := range entries {
		if e.Action == "race.rerun" {
			found = true
		}
	}
	if !found {
		t.Error("a re-run should be recorded in the audit log")
	}
}

// The displays need a race without anyone choosing one.
func TestLoadMostRecentRacePicksTheLiveRace(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	if a.Race.CurrentRaceID() != 0 {
		t.Fatal("nothing should be loaded on an empty database")
	}

	race, err := a.SeedDemoRace(ctx, 2027)
	if err != nil {
		t.Fatal(err)
	}
	a.Race.LoadMostRecentRace(ctx)

	if got := a.Race.CurrentRaceID(); got != race.ID {
		t.Errorf("loaded race %d, want %d", got, race.ID)
	}
}

func TestDemoDataIsNotCreatedTwice(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	if a.HasDemoData(ctx) {
		t.Fatal("a fresh database has no demo data")
	}
	if _, err := a.SeedDemoRace(ctx, 2027); err != nil {
		t.Fatal(err)
	}
	if !a.HasDemoData(ctx) {
		t.Error("demo data should be detected after seeding")
	}
}

// The demo season is named so nobody mistakes it for real results.
func TestDemoSeasonIsLabelled(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	if _, err := a.SeedDemoRace(ctx, 2027); err != nil {
		t.Fatal(err)
	}
	seasons, _ := a.DB.Seasons(ctx)
	if len(seasons) != 1 {
		t.Fatalf("got %d seasons, want 1", len(seasons))
	}
	if !strings.Contains(seasons[0].Name, "demo") {
		t.Errorf("season name %q should say it is demo data", seasons[0].Name)
	}
}
