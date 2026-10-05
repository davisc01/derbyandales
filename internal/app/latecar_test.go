package app

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/store"
)

// checkInLate enters a car for a race whose schedule already exists, the way
// the check-in desk does when somebody walks in at heat 7.
func checkInLate(t *testing.T, a *App, raceID int64, number int, name string) model.Entry {
	t.Helper()
	ctx := context.Background()
	race, err := a.DB.Race(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	racer, err := a.DB.FindOrCreateRacer(ctx, race.SeasonID, "Late", "Arrival "+name)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	e, err := a.DB.CreateEntry(ctx, model.Entry{
		RaceID: raceID, RacerID: racer.ID, CarNumber: number, CarName: name, CheckedInAt: &now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// runHeats records plausible times for heats in running order until n have
// times, straight into the store so no intermission or advance gets in the way.
func runHeats(t *testing.T, a *App, raceID int64, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		h, err := a.DB.NextHeat(ctx, raceID)
		if err != nil {
			t.Fatalf("heat %d: %v", i+1, err)
		}
		times := map[int]float64{}
		for _, l := range h.Lanes {
			if l.EntryID != nil {
				times[l.Lane] = 2.4 + float64(*l.EntryID%17)*0.01 + float64(l.Lane)*0.003
			}
		}
		if err := a.DB.RecordHeatResults(ctx, h.ID, times); err != nil {
			t.Fatal(err)
		}
	}
}

// heatCars is a heat's cars by lane, for comparing heats before and after.
func heatCars(h store.HeatView) []any {
	out := []any{h.Number}
	for _, l := range h.Lanes {
		if l.EntryID == nil {
			out = append(out, nil)
		} else {
			out = append(out, *l.EntryID)
		}
	}
	return out
}

// The heats that cannot change must not: the ones raced, the one on the track
// and the one the impound official is already loading into a tray. A late car
// that reshuffles a loaded tray sends the wrong car down a lane.
func TestALateCarIsFittedWithoutTouchingRunOrLoadedHeats(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()
	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	runHeats(t, a, raceID, 5)
	if err := a.Race.SetRace(ctx, raceID); err != nil { // heat 6 on the track
		t.Fatal(err)
	}
	before, _ := a.DB.Heats(ctx, raceID)

	late := checkInLate(t, a, raceID, 199, "Latecomer")
	plan, err := a.Race.LateCars(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Cars) != 1 || plan.Cars[0].ID != late.ID {
		t.Fatalf("late cars = %+v, want just the latecomer", plan.Cars)
	}
	if plan.KeptThrough != 7 {
		t.Errorf("kept through heat %d, want 7 (5 run, 6 on the track, 7 loading)", plan.KeptThrough)
	}

	if _, err := a.Race.FitLateCars(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	after, _ := a.DB.Heats(ctx, raceID)
	for i := 0; i < 7; i++ {
		if !reflect.DeepEqual(heatCars(before[i]), heatCars(after[i])) {
			t.Errorf("heat %d changed: %v → %v", i+1, heatCars(before[i]), heatCars(after[i]))
		}
	}
	if len(after) != len(before)+1 {
		t.Errorf("%d heats after, want %d — one more, every lane filled", len(after), len(before)+1)
	}
	if again, _ := a.DB.UnscheduledEntries(ctx, raceID); len(again) != 0 {
		t.Errorf("still late after fitting in: %v", again)
	}

	var found bool
	for _, e := range mustAudit(t, a) {
		if e.Action == "race.latecar" && strings.Contains(e.Detail, "Latecomer") {
			found = true
		}
	}
	if !found {
		t.Error("fitting in a late car was not audited")
	}
}

func mustAudit(t *testing.T, a *App) []model.AuditEntry {
	t.Helper()
	entries, err := a.DB.RecentAudit(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// The point of all this: at the end of the night the late car has its four
// runs and an average like everybody else, and nobody else gained or lost a
// run to make room — an extra run would change the very averages the night is
// decided on.
func TestALateCarFinishesTheNightWithFourRuns(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()
	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	runHeats(t, a, raceID, 3)
	late := checkInLate(t, a, raceID, 198, "Running Late")
	if _, err := a.Race.FitLateCars(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	p, _ := a.DB.Progress(ctx, raceID)
	runHeats(t, a, raceID, p.Total-p.Completed)
	if err := a.Race.SetRace(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	if err := a.Race.ArmNext(ctx); err != nil { // nothing left: finishes the race
		t.Fatal(err)
	}

	standings, err := a.DB.Standings(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, s := range standings {
		if s.Heats != 4 {
			t.Errorf("%s ran %d times, want 4", s.Entry.CarName, s.Heats)
		}
		if s.EntryID == late.ID {
			seen = true
		}
	}
	if !seen {
		t.Error("the late car is not in the standings")
	}
	if frozen, _ := a.DB.RaceFrozen(ctx, raceID); !frozen {
		t.Error("the race should have finished once the added heats ran")
	}
}

// A car checked in after the last heat landed did not race, and reopening a
// finished race is the audited recompute, not a side effect of check-in. The
// coordinator is still told, rather than left to find the car missing.
func TestFittingIsRefusedOnceTheRaceIsFrozen(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()
	runWholeRace(t, a, raceID, 0, 0)
	if err := a.Race.SetRace(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	if err := a.Race.ArmNext(ctx); err != nil {
		t.Fatal(err)
	}
	checkInLate(t, a, raceID, 197, "Too Late")

	plan, err := a.Race.LateCars(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Cars) != 1 || plan.CanFit || plan.Reason == "" {
		t.Errorf("plan = %+v, want the car reported and fitting refused with a reason", plan)
	}
	if _, err := a.Race.FitLateCars(ctx, raceID); err == nil {
		t.Error("a finished race was reopened to fit a car in")
	}
}

// The intermission is when people walk in, so fitting a car in must work then —
// and must not arm anything, because the intermission is a hard stop with
// people standing at the table.
func TestFittingDuringTheIntermissionArmsNothing(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()
	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	if err := a.Race.SetRace(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	half := a.Race.IntermissionHeat(ctx, raceID)
	heats, _ := a.DB.Heats(ctx, raceID)
	for _, h := range heats[:half] {
		times := map[int]float64{}
		for _, l := range h.Lanes {
			if l.EntryID != nil {
				times[l.Lane] = 2.5 + float64(l.Lane)*0.01
			}
		}
		if err := a.Race.EnterTimes(ctx, h.ID, times, "test"); err != nil {
			t.Fatalf("heat %d: %v", h.Number, err)
		}
	}
	if !a.Race.Intermission(ctx).Active {
		t.Fatal("expected to be in the intermission")
	}

	checkInLate(t, a, raceID, 196, "Intermission Arrival")
	if _, err := a.Race.FitLateCars(ctx, raceID); err != nil {
		t.Fatalf("fitting in during the intermission: %v", err)
	}
	if !a.Race.Intermission(ctx).Active {
		t.Error("fitting a car in ended the intermission")
	}
	after, _ := a.DB.Heats(ctx, raceID)
	for _, h := range after {
		if h.Status == model.HeatArmed || h.Status == model.HeatRunning {
			if !anyRecorded(h) {
				t.Errorf("heat %d was armed during the intermission", h.Number)
			}
		}
	}
}
