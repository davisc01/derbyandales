package app

import (
	"context"
	"testing"
	"time"

	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/store"
)

// The demo data can be made and deleted from the settings page, which means the
// delete runs in the same database as the club's real seasons. These are about
// where that line is.

func TestClearingDemoDataLeavesTheClubsSeasonAlone(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	real, err := a.DB.CreateSeason(ctx, store.DefaultSeason(2027))
	if err != nil {
		t.Fatal(err)
	}
	realRace, err := a.DB.CreateRace(ctx, model.Race{
		SeasonID: real.ID, Number: 1, Name: "Race 1", Date: time.Now(),
		Kind: model.RacePoints, Status: model.StatusCheckin,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddDemoRace(ctx); err != nil {
		t.Fatalf("add demo race: %v", err)
	}

	if _, err := a.ClearDemoData(ctx); err != nil {
		t.Fatalf("clear: %v", err)
	}

	if _, err := a.DB.Season(ctx, real.ID); err != nil {
		t.Errorf("the club's season went with the demo data: %v", err)
	}
	if _, err := a.DB.Race(ctx, realRace.ID); err != nil {
		t.Errorf("the club's race went with the demo data: %v", err)
	}
	if a.HasDemoData(ctx) {
		t.Error("demo data survived being cleared")
	}
}

// Nothing is deleted without a way back: the demo season is fabricated, but the
// evening spent rehearsing on it is not.
func TestClearingDemoDataTakesASnapshotFirst(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	if _, err := a.AddDemoRace(ctx); err != nil {
		t.Fatal(err)
	}
	before := len(ListBackups(a.Paths.Backups))
	if _, err := a.ClearDemoData(ctx); err != nil {
		t.Fatal(err)
	}

	backups := ListBackups(a.Paths.Backups)
	if len(backups) <= before {
		t.Fatalf("no snapshot taken: %d backups before, %d after", before, len(backups))
	}
	var found bool
	for _, b := range backups {
		if b.Reason == BackupDemoClear {
			found = true
		}
	}
	if !found {
		t.Error("the snapshot is not labelled as the demo clear-out")
	}
}

// Every screen reads the race through the controller. Deleting the race it is
// pointing at without telling it would have the displays asking for a row that
// is not there any more, on every redraw, for the rest of the night.
func TestClearingTheLoadedDemoRaceUnloadsIt(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	race, err := a.AddDemoRace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if a.Race.CurrentRaceID() != race.ID {
		t.Fatalf("a new demo race should be the loaded one, got %d", a.Race.CurrentRaceID())
	}

	if _, err := a.ClearDemoData(ctx); err != nil {
		t.Fatal(err)
	}
	if id := a.Race.CurrentRaceID(); id != 0 {
		t.Errorf("controller still points at race %d after it was deleted", id)
	}
	if state := a.Race.State(ctx); state.RaceName != "" {
		t.Errorf("the screens still name a deleted race: %q", state.RaceName)
	}
}

// The club's own seasons come back to the screens once the demo is gone, so
// clearing it mid-season does not leave a dark TV.
func TestClearingDemoDataReloadsTheClubsRace(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	real, err := a.DB.CreateSeason(ctx, store.DefaultSeason(2027))
	if err != nil {
		t.Fatal(err)
	}
	realRace, err := a.DB.CreateRace(ctx, model.Race{
		SeasonID: real.ID, Number: 1, Name: "Race 1", Date: time.Now(),
		Kind: model.RacePoints, Status: model.StatusCheckin,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddDemoRace(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := a.ClearDemoData(ctx); err != nil {
		t.Fatal(err)
	}
	if id := a.Race.CurrentRaceID(); id != realRace.ID {
		t.Errorf("loaded race = %d, want the club's race %d", id, realRace.ID)
	}
}

// Pressing the button twice is a second night in the same season, not a second
// season: the pass-down and wildcard rules are all season-scoped, and two demo
// seasons would quietly split them.
func TestASecondDemoRaceJoinsTheSameSeason(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	first, err := a.AddDemoRace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.AddDemoRace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.SeasonID != second.SeasonID {
		t.Errorf("second demo race is in season %d, first in %d", second.SeasonID, first.SeasonID)
	}
	if second.Number != first.Number+1 {
		t.Errorf("race numbers = %d then %d", first.Number, second.Number)
	}

	seasons, err := a.DB.Seasons(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(seasons) != 1 {
		t.Errorf("got %d seasons, want 1", len(seasons))
	}
}

// The field is checked in already: the point of the button is a race that can
// be scheduled and run without twenty people at a table.
func TestADemoRaceArrivesWithItsFieldCheckedIn(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	race, err := a.AddDemoRace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := a.DB.Entries(ctx, race.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(demoRacers) {
		t.Fatalf("got %d entries, want %d", len(entries), len(demoRacers))
	}
	for _, e := range entries {
		if e.CheckedInAt == nil {
			t.Errorf("car %d is not checked in", e.CarNumber)
		}
	}
}

// There is nothing to clear on a fresh database, and the error is read by
// somebody in a brewery.
func TestClearingNothingSaysSoPlainly(t *testing.T) {
	a := testApp(t)
	_, err := a.ClearDemoData(context.Background())
	if err == nil {
		t.Fatal("clearing an empty database should say there is nothing to clear")
	}
	if got := err.Error(); got != "there is no demo data to clear" {
		t.Errorf("error = %q", got)
	}
}

// Race numbers are unique within a season, so the next demo night is one past
// the highest one there rather than one past the count. Deleting a race in the
// middle of a rehearsal used to make the next one collide with a night that was
// still on the schedule, and the collision reached the screen as an index name.
func TestADemoRaceNumbersPastTheHighestNightThere(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	first, err := a.AddDemoRace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.AddDemoRace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	third, err := a.AddDemoRace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The middle one goes, the way a race is dropped mid-rehearsal. There is
	// no delete-a-race API on purpose, so this reaches for the row directly.
	if _, err := a.DB.ExecContext(ctx, `DELETE FROM race WHERE id = ?`, second.ID); err != nil {
		t.Fatalf("delete the middle race: %v", err)
	}

	next, err := a.AddDemoRace(ctx)
	if err != nil {
		t.Fatalf("a fourth demo race after deleting one: %v", err)
	}
	if next.Number != third.Number+1 {
		t.Errorf("next race number = %d, want %d (after %d and %d)",
			next.Number, third.Number+1, first.Number, third.Number)
	}
}
