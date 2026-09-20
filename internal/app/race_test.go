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

	// Finishing a race takes a snapshot and moves it on to voting. That happens
	// on the controller's own tick rather than with the last heat, so it is
	// waited for: reading the status the instant the heats run out is a race
	// between this loop and that one.
	waitFor(t, 3*time.Second, func() bool {
		race, err := a.DB.Race(ctx, raceID)
		return err == nil && race.Status != "racing"
	}, "a completed race should have moved past racing")
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

// The three answers the timer can give, and the fact that each needs a
// different thing from the coordinator: record it, go and find the times, or
// run the heat again. Getting the wording wrong here sends somebody to wait
// for results that are never coming.
func TestForceResultsDescribesEachAnswer(t *testing.T) {
	tc := testApp(t).Timer

	recorded := tc.describeForced(ForceResultsOutcome{Lanes: 4})
	if !recorded.Reported || !strings.Contains(recorded.Detail, "recorded") {
		t.Errorf("lanes reported should read as recorded: %+v", recorded)
	}

	// The timer had the times but nothing was armed to catch them. They are
	// not lost — they are in the log — and saying so is the difference between
	// a re-run and a shrug.
	dropped := tc.describeForced(ForceResultsOutcome{Dropped: 4})
	if !dropped.Reported {
		t.Errorf("the timer did report; it was this end that dropped them: %+v", dropped)
	}
	if !strings.Contains(dropped.Detail, "log") {
		t.Errorf("detail %q should say where the times went", dropped.Detail)
	}

	// The heat completed while we listened, without this call counting the
	// lanes itself. It still happened, and must not read as "never started".
	finished := tc.describeForced(ForceResultsOutcome{Finished: true})
	if !finished.Reported || strings.Contains(finished.Detail, "never started") {
		t.Errorf("a finished heat must not read as one that never ran: %+v", finished)
	}

	nothing := tc.describeForced(ForceResultsOutcome{})
	if nothing.Reported {
		t.Errorf("nothing came back: %+v", nothing)
	}
	if !strings.Contains(nothing.Detail, "never started") {
		t.Errorf("detail %q should name the cause", nothing.Detail)
	}
	if !strings.Contains(nothing.Detail, "lane lights") {
		t.Errorf("detail %q should point at the tell that catches this live", nothing.Detail)
	}
}

// Silence is the other answer, and it means something different: the timer
// never started this race, so the heat has to be run again rather than waited
// for. Saying so is the whole point of the button.
func TestForceResultsSaysSoWhenTheTimerNeverStarted(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	if err := a.Timer.Connect(ctx, "", timer.SimulatorKey); err != nil {
		t.Fatal(err)
	}

	out, err := a.Timer.ForceResults(ctx)
	if err != nil {
		t.Fatalf("ForceResults: %v", err)
	}
	if out.Reported {
		t.Fatalf("nothing has run; the timer cannot have reported: %+v", out)
	}
	if !strings.Contains(out.Detail, "never started") {
		t.Errorf("detail %q should say the race never started", out.Detail)
	}
	if !strings.Contains(out.Detail, "again") {
		t.Errorf("detail %q should tell the coordinator what to do next", out.Detail)
	}
}

// On a timer whose gate cannot be read, a heat that never started looks exactly
// like one still being staged. The coordinator gets asked, once, and only after
// long enough that ordinary staging does not trip it.
func TestQuietHeatAsksOnceAndOnlyWhenTheGateIsUnreadable(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()
	dev := a.Timer.Device()

	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	if err := a.Race.Start(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Race.Stop)
	waitFor(t, 5*time.Second, func() bool {
		return dev.State() == timer.StateMark
	}, "the first heat never armed")

	// The simulated timer reports its gate, so silence is not ambiguous and
	// nobody should be bothered.
	if a.Race.claimQuietHeat(dev) {
		t.Error("a timer that reports its gate must not raise this")
	}

	// The club's K1 cannot, which is the case this exists for.
	dev.GateUnreadable()
	a.Race.mu.Lock()
	a.Race.armedAt = time.Now().Add(-SilentHeatPrompt - time.Second)
	a.Race.mu.Unlock()

	if !a.Race.claimQuietHeat(dev) {
		t.Fatal("a heat armed long ago with nothing heard should be raised")
	}
	if a.Race.claimQuietHeat(dev) {
		t.Error("it must be raised once, not on every tick")
	}
}

// Staging four cars in a bar is not quick. A prompt that fires during normal
// staging is one that gets ignored by the second race of the season.
func TestQuietHeatStaysSilentWhileTheHeatIsStillYoung(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()
	dev := a.Timer.Device()

	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	if err := a.Race.Start(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Race.Stop)
	waitFor(t, 5*time.Second, func() bool {
		return dev.State() == timer.StateMark
	}, "the first heat never armed")

	dev.GateUnreadable()
	if a.Race.claimQuietHeat(dev) {
		t.Error("a heat armed moments ago is being staged, not lost")
	}
}

// The timer's setup is re-asserted on every heat, not just on connect.
//
// A timer that browns out and restarts mid-night comes back in its power-on
// mode — eliminator mode on, the old result format — and everything it sends
// after that is unparseable. At a venue running a TV and a Pi off one
// extension cord, that is not a hypothetical.
func TestArmingReassertsTheTimerSetup(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()
	sim := a.Timer.Simulator()

	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}

	count := func(cmd string) int {
		n := 0
		for _, c := range sim.Commands() {
			if c == cmd {
				n++
			}
		}
		return n
	}
	before := count("RE")

	if err := a.Race.Start(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Race.Stop)
	waitFor(t, 5*time.Second, func() bool {
		return a.Timer.Device().State() == timer.StateMark
	}, "the first heat never armed")

	if got := count("RE"); got <= before {
		t.Errorf("eliminator mode was reset %d times before arming and %d after; "+
			"arming must re-assert it", before, got)
	}
	if count("N1") == 0 {
		t.Error("the result format was never set")
	}
}
