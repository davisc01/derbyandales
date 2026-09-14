package app

import (
	"context"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/davisc01/derbyandales/internal/store"
)

// Driving the anomaly check through a real race: schedule the heats, record a
// night's times, sabotage one heat, and see whether the software notices.

// runWholeRace records plausible times for every heat, adding penalty seconds
// to the heat numbered sabotage (0 for a clean race). It returns the race id.
func runWholeRace(t *testing.T, a *App, raceID int64, sabotage int, penalty float64) {
	t.Helper()
	ctx := context.Background()

	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatalf("GenerateSchedule: %v", err)
	}
	heats, err := a.DB.Heats(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}

	rng := rand.New(rand.NewSource(7))
	pace := map[int64]float64{}
	for _, h := range heats {
		times := map[int]float64{}
		for _, l := range h.Lanes {
			if l.EntryID == nil {
				continue
			}
			if _, ok := pace[*l.EntryID]; !ok {
				pace[*l.EntryID] = 2.30 + rng.Float64()*0.30
			}
			tm := pace[*l.EntryID] + rng.NormFloat64()*0.02
			if h.Number == sabotage {
				tm += penalty
			}
			times[l.Lane] = float64(int64(tm*1000+0.5)) / 1000
		}
		if err := a.DB.RecordHeatResults(ctx, h.ID, times); err != nil {
			t.Fatal(err)
		}
	}
}

// The case this exists for: something went wrong in one heat and every car in
// it paid for it.
func TestABrokenHeatIsFlaggedAtTheEndOfTheRace(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	// Heat 9 took almost half a second off everybody — a gate that stuck.
	runWholeRace(t, a, raceID, 9, 0.45)

	found, err := a.DB.HeatAnomalies(ctx, raceID)
	if err != nil {
		t.Fatalf("HeatAnomalies: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("the sabotaged heat was not flagged at all")
	}
	if found[0].Heat != 9 {
		t.Fatalf("flagged heat %d first, want 9: %+v", found[0].Heat, found)
	}
	if !found[0].Clear {
		t.Errorf("a heat where everyone lost 0.45 s was not reported as clear: %+v", found[0])
	}
	if found[0].HeatID == 0 {
		t.Error("the flagged heat cannot be re-run: no heat id")
	}
	if len(found[0].Entries) != found[0].Cars {
		t.Errorf("%d cars named, %d counted", len(found[0].Entries), found[0].Cars)
	}
}

// The check must be quiet on a night where nothing went wrong, or nobody will
// believe it on the night something did.
func TestACleanRaceRaisesNoClearFlag(t *testing.T) {
	a, raceID := raceFixture(t)

	runWholeRace(t, a, raceID, 0, 0)

	found, err := a.DB.HeatAnomalies(context.Background(), raceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		if f.Clear {
			t.Errorf("clean race flagged heat %d as a clear fault: margin %.3f s",
				f.Heat, f.Margin)
		}
	}
}

// Re-running is how a flagged heat gets put right, and it has to work at the
// end of the race — long after that heat's turn has passed.
func TestAFlaggedHeatCanBeReRunAfterTheRace(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	runWholeRace(t, a, raceID, 9, 0.45)

	found, _ := a.DB.HeatAnomalies(ctx, raceID)
	if len(found) == 0 {
		t.Fatal("nothing flagged")
	}
	target := found[0]

	if err := a.Race.ReRun(ctx, target.HeatID); err != nil {
		t.Fatalf("ReRun: %v", err)
	}
	t.Cleanup(a.Race.Stop)

	// Clearing its times makes it the next heat waiting to run, so the race
	// picks it up rather than needing to be wound back.
	next, err := a.DB.NextHeat(ctx, raceID)
	if err != nil {
		t.Fatalf("NextHeat after a re-run: %v", err)
	}
	if next.ID != target.HeatID {
		t.Errorf("next heat is %d, want the re-run heat %d", next.Number, target.Heat)
	}

	// And with the bad times gone, the flag goes with them.
	again, _ := a.DB.Heats(ctx, raceID)
	for _, h := range again {
		if h.ID != target.HeatID {
			continue
		}
		for _, l := range h.Lanes {
			if l.FinishTime != nil {
				t.Errorf("lane %d still has a time after a re-run", l.Lane)
			}
		}
	}
}

// "Re-run" on a heat that has not been run yet would really mean "skip ahead to
// it", quietly stepping over everything in between.
func TestAHeatThatHasNotRunCannotBeReRun(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	heats, _ := a.DB.Heats(ctx, raceID)

	err := a.Race.ReRun(ctx, heats[len(heats)-1].ID)
	if err == nil {
		t.Fatal("re-running a heat that has never run was allowed")
	}
	if !strings.Contains(err.Error(), "not been run") {
		t.Errorf("error = %q, want it to explain why", err)
	}
}

// The intermission is a hard stop. Re-running a heat is a way into the track
// that has to be closed too, or a tidy-up sends cars at people standing at the
// voting table.
func TestReRunningIsRefusedDuringTheIntermission(t *testing.T) {
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

	runHeatsUntil(t, a, raceID, func() bool { return a.Race.Intermission(ctx).Active }, 60*time.Second)

	// Heat 1 has certainly run by now, so the refusal can only come from the
	// intermission check.
	heats, _ := a.DB.Heats(ctx, raceID)
	err := a.Race.ReRun(ctx, heats[0].ID)
	if err == nil {
		t.Fatal("a heat was re-run during the intermission")
	}
	if !strings.Contains(err.Error(), "intermission") {
		t.Errorf("error = %q, want it to name the intermission", err)
	}

	// And the heat's times are still there: a refused re-run must not have
	// cleared them on the way to failing.
	again, _ := a.DB.Heat(ctx, heats[0].ID)
	for _, l := range again.Lanes {
		if l.EntryID != nil && l.FinishTime == nil {
			t.Error("a refused re-run cleared the heat's times anyway")
		}
	}
}
