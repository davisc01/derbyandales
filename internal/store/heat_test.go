package store

import (
	"context"
	"testing"
	"time"

	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/schedule"
)

// fixture builds a season, a race and a field of cars.
func fixture(t *testing.T, cars int) (*DB, int64, []EntryView) {
	t.Helper()
	db := testDB(t)
	ctx := context.Background()

	season, err := db.CreateSeason(ctx, DefaultSeason(2027))
	if err != nil {
		t.Fatal(err)
	}
	race, err := db.CreateRace(ctx, model.Race{
		SeasonID: season.ID, Number: 1, Name: "Race 1", Date: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < cars; i++ {
		racer, err := db.FindOrCreateRacer(ctx, season.ID, "Driver", string(rune('A'+i)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.CreateEntry(ctx, model.Entry{
			RaceID:    race.ID,
			RacerID:   racer.ID,
			CarNumber: 10 + i,
			CarName:   "Car " + string(rune('A'+i)),
			IsControl: i == 0,
		}); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := db.RacingEntries(ctx, race.ID)
	if err != nil {
		t.Fatal(err)
	}
	return db, race.ID, entries
}

func TestSaveScheduleStoresEveryLane(t *testing.T) {
	db, raceID, entries := fixture(t, 8)
	ctx := context.Background()

	s, err := schedule.Generate(len(entries), 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSchedule(ctx, raceID, s, entries); err != nil {
		t.Fatal(err)
	}

	heats, err := db.Heats(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(heats) != len(s.Heats) {
		t.Fatalf("stored %d heats, generated %d", len(heats), len(s.Heats))
	}

	// The club's rule has to survive the round trip through SQLite, not just
	// hold in memory.
	perLane := map[int64]map[int]int{}
	for _, h := range heats {
		if len(h.Lanes) != 4 {
			t.Fatalf("heat %d stored %d lanes, want 4", h.Number, len(h.Lanes))
		}
		for _, l := range h.Lanes {
			if l.EntryID == nil {
				continue
			}
			if perLane[*l.EntryID] == nil {
				perLane[*l.EntryID] = map[int]int{}
			}
			perLane[*l.EntryID][l.Lane]++
		}
	}
	for _, e := range entries {
		lanes := perLane[e.ID]
		if len(lanes) != 4 {
			t.Errorf("car %d runs %d different lanes, want 4", e.CarNumber, len(lanes))
		}
		for lane, n := range lanes {
			if n != 1 {
				t.Errorf("car %d runs lane %d %d times, want once", e.CarNumber, lane, n)
			}
		}
	}
}

// A schedule that breaks the club's rule must never reach the database.
func TestSaveScheduleRejectsAnInvalidSchedule(t *testing.T) {
	db, raceID, entries := fixture(t, 6)
	ctx := context.Background()

	bad := &schedule.Schedule{
		Lanes: 4,
		Cars:  6,
		// Car 0 twice in one heat: a racer called to two lanes at once.
		Heats: []schedule.Heat{{0, 0, 1, 2}},
	}
	if err := db.SaveSchedule(ctx, raceID, bad, entries); err == nil {
		t.Fatal("an invalid schedule was accepted")
	}

	heats, _ := db.Heats(ctx, raceID)
	if len(heats) != 0 {
		t.Errorf("%d heats were written despite the rejection", len(heats))
	}
}

func TestNextHeatFollowsTheResults(t *testing.T) {
	db, raceID, entries := fixture(t, 8)
	ctx := context.Background()

	s, _ := schedule.Generate(len(entries), 4)
	if err := db.SaveSchedule(ctx, raceID, s, entries); err != nil {
		t.Fatal(err)
	}

	first, err := db.NextHeat(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Number != 1 {
		t.Errorf("first heat is %d, want 1", first.Number)
	}

	times := map[int]float64{}
	for _, l := range first.Lanes {
		if l.EntryID != nil {
			times[l.Lane] = 2.4 + float64(l.Lane)/100
		}
	}
	if err := db.RecordHeatResults(ctx, first.ID, times); err != nil {
		t.Fatal(err)
	}

	second, err := db.NextHeat(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Number != 2 {
		t.Errorf("after running heat 1, next is %d, want 2", second.Number)
	}
}

// Clearing a heat must put it back in the queue, or a re-run would be skipped.
func TestClearedHeatIsRunAgain(t *testing.T) {
	db, raceID, entries := fixture(t, 8)
	ctx := context.Background()

	s, _ := schedule.Generate(len(entries), 4)
	db.SaveSchedule(ctx, raceID, s, entries)

	h, _ := db.NextHeat(ctx, raceID)
	times := map[int]float64{}
	for _, l := range h.Lanes {
		if l.EntryID != nil {
			times[l.Lane] = 2.5
		}
	}
	db.RecordHeatResults(ctx, h.ID, times)

	if err := db.ClearHeatResults(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	again, err := db.NextHeat(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != h.ID {
		t.Errorf("next heat is %d, want the cleared heat %d", again.Number, h.Number)
	}
	for _, l := range again.Lanes {
		if l.FinishTime != nil {
			t.Errorf("lane %d still has a time after clearing", l.Lane)
		}
	}
}

// Places come from the times, so they are consistent with what gets published.
func TestRecordHeatResultsDerivesPlaces(t *testing.T) {
	db, raceID, entries := fixture(t, 8)
	ctx := context.Background()

	s, _ := schedule.Generate(len(entries), 4)
	db.SaveSchedule(ctx, raceID, s, entries)
	h, _ := db.NextHeat(ctx, raceID)

	// Deliberately out of order: the fastest is in the last lane.
	times := map[int]float64{1: 2.60, 2: 2.50, 3: 2.55, 4: 2.40}
	if err := db.RecordHeatResults(ctx, h.ID, times); err != nil {
		t.Fatal(err)
	}

	got, _ := db.Heat(ctx, h.ID)
	want := map[int]int{4: 1, 2: 2, 3: 3, 1: 4}
	for _, l := range got.Lanes {
		if l.FinishPlace == nil {
			t.Errorf("lane %d has no place", l.Lane)
			continue
		}
		if *l.FinishPlace != want[l.Lane] {
			t.Errorf("lane %d placed %d, want %d", l.Lane, *l.FinishPlace, want[l.Lane])
		}
	}
	if !got.Complete() {
		t.Error("heat should read as complete")
	}
}

func TestProgressCountsRunHeats(t *testing.T) {
	db, raceID, entries := fixture(t, 8)
	ctx := context.Background()

	s, _ := schedule.Generate(len(entries), 4)
	db.SaveSchedule(ctx, raceID, s, entries)

	p, err := db.Progress(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != len(s.Heats) || p.Completed != 0 {
		t.Errorf("progress = %d/%d, want 0/%d", p.Completed, p.Total, len(s.Heats))
	}

	for i := 0; i < 3; i++ {
		h, err := db.NextHeat(ctx, raceID)
		if err != nil {
			t.Fatal(err)
		}
		times := map[int]float64{}
		for _, l := range h.Lanes {
			if l.EntryID != nil {
				times[l.Lane] = 2.5
			}
		}
		db.RecordHeatResults(ctx, h.ID, times)
	}

	p, _ = db.Progress(ctx, raceID)
	if p.Completed != 3 {
		t.Errorf("completed = %d, want 3", p.Completed)
	}
}

// An excluded car races but is not in the standings. This is the flag that
// decided a real race, so it gets a test at the storage layer too.
func TestExcludedEntriesRaceButDoNotScore(t *testing.T) {
	db, raceID, entries := fixture(t, 8)
	ctx := context.Background()

	// Mark one car ineligible after check-in.
	excluded := entries[3]
	excluded.Excluded = true
	excluded.ExclusionReason = "raced in a previous championship"
	if err := db.UpdateEntry(ctx, excluded.Entry); err != nil {
		t.Fatal(err)
	}

	s, _ := schedule.Generate(len(entries), 4)
	db.SaveSchedule(ctx, raceID, s, entries)

	// Run every heat.
	for {
		h, err := db.NextHeat(ctx, raceID)
		if err != nil {
			break
		}
		times := map[int]float64{}
		for _, l := range h.Lanes {
			if l.EntryID != nil {
				// Make the excluded car the fastest, so leaving it in would be
				// obvious: it would win.
				if *l.EntryID == excluded.ID {
					times[l.Lane] = 2.0
				} else {
					times[l.Lane] = 2.5
				}
			}
		}
		db.RecordHeatResults(ctx, h.ID, times)
	}

	// It raced.
	runs, err := db.RunsByEntry(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, scored := runs[excluded.ID]; scored {
		t.Error("the excluded car's runs must not be scored")
	}

	standings, err := db.Standings(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range standings {
		if st.Entry.ID == excluded.ID {
			t.Fatalf("the excluded car placed %d; it should not be in the standings", st.Place)
		}
	}
	if len(standings) != len(entries)-1 {
		t.Errorf("%d cars in the standings, want %d", len(standings), len(entries)-1)
	}
}

// A struck-out run — a false start — is excluded from scoring but stays visible.
func TestIgnoredLaneIsExcludedFromScoring(t *testing.T) {
	db, raceID, entries := fixture(t, 8)
	ctx := context.Background()

	s, _ := schedule.Generate(len(entries), 4)
	db.SaveSchedule(ctx, raceID, s, entries)

	h, _ := db.NextHeat(ctx, raceID)
	times := map[int]float64{}
	var target int64
	for _, l := range h.Lanes {
		if l.EntryID != nil {
			times[l.Lane] = 2.5
			if target == 0 {
				target = *l.EntryID
			}
		}
	}
	db.RecordHeatResults(ctx, h.ID, times)

	var lane int64
	for _, l := range h.Lanes {
		if l.EntryID != nil && *l.EntryID == target {
			lane = int64(l.Lane)
		}
	}
	if err := db.SetLaneIgnored(ctx, h.ID, lane, true); err != nil {
		t.Fatal(err)
	}

	runs, _ := db.RunsByEntry(ctx, raceID)
	found := false
	for _, run := range runs[target] {
		if run.Ignored {
			found = true
		}
	}
	if !found {
		t.Error("the struck-out run should still be present, marked ignored")
	}
}

func TestLaneMaskSkipsByes(t *testing.T) {
	db, raceID, entries := fixture(t, 3) // fewer cars than lanes, so byes exist
	ctx := context.Background()

	s, err := schedule.Generate(len(entries), 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSchedule(ctx, raceID, s, entries); err != nil {
		t.Fatal(err)
	}

	heats, _ := db.Heats(ctx, raceID)
	for _, h := range heats {
		mask := h.LaneMask()
		for _, l := range h.Lanes {
			bit := mask&(1<<uint(l.Lane-1)) != 0
			if l.Bye() && bit {
				t.Errorf("heat %d lane %d is a bye but is in the mask", h.Number, l.Lane)
			}
			if !l.Bye() && !bit {
				t.Errorf("heat %d lane %d has a car but is masked out", h.Number, l.Lane)
			}
		}
	}
}
