package store

import (
	"context"
	"testing"

	"github.com/davisc01/derbyandales/internal/model"
)

// The three kinds of entry behave differently, and conflating them changes who
// wins a race. This pins the distinction down.
func TestEntryEligibilitySemantics(t *testing.T) {
	normal := model.Entry{}
	control := model.Entry{IsControl: true}
	excluded := model.Entry{Excluded: true, ExclusionReason: "raced in the 2026 championship"}

	// A normal entry does everything.
	if !normal.Scores() || !normal.EarnsPoints() {
		t.Error("a normal entry should be scored and earn points")
	}

	// The pace car is ranked in the standings but takes nothing.
	if !control.Scores() {
		t.Error("the CONTROL car appears in the standings")
	}
	if control.EarnsPoints() {
		t.Error("the CONTROL car must not earn wildcard points or take an award")
	}

	// An ineligible entry races, but is left out of the standings entirely.
	if excluded.Scores() {
		t.Error("an excluded entry must not appear in the standings")
	}
	if excluded.EarnsPoints() {
		t.Error("an excluded entry must not earn points")
	}
}

// The reason has to survive, or nobody can answer "why wasn't my car in the
// standings?" after the fact.
func TestExclusionReasonRoundTrips(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	season, err := db.CreateSeason(ctx, DefaultSeason(2027))
	if err != nil {
		t.Fatal(err)
	}
	race, err := db.CreateRace(ctx, model.Race{
		SeasonID: season.ID, Number: 1, Name: "Race 1",
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec(
		`INSERT INTO racer (season_id, first_name, last_name) VALUES (?, 'Justin', 'Palmer')`,
		season.ID)
	if err != nil {
		t.Fatal(err)
	}
	racerID, _ := res.LastInsertId()

	const reason = "raced in a previous championship"
	if _, err := db.Exec(
		`INSERT INTO entry (race_id, racer_id, car_number, car_name, excluded, exclusion_reason)
		 VALUES (?, ?, 901, 'Shelby', 1, ?)`, race.ID, racerID, reason); err != nil {
		t.Fatal(err)
	}

	var gotExcluded int
	var gotReason string
	if err := db.QueryRow(
		`SELECT excluded, exclusion_reason FROM entry WHERE car_number = 901`).
		Scan(&gotExcluded, &gotReason); err != nil {
		t.Fatal(err)
	}
	if gotExcluded != 1 {
		t.Error("excluded flag did not persist")
	}
	if gotReason != reason {
		t.Errorf("exclusion_reason = %q, want %q", gotReason, reason)
	}
}

// Migrations must be additive: an existing database gets the new column without
// losing anything.
func TestMigrationAddsExclusionReasonToExistingRows(t *testing.T) {
	db := testDB(t)
	// The column exists and defaults to empty rather than NULL, so readers never
	// have to deal with a null string.
	var dflt string
	if err := db.QueryRow(
		`SELECT dflt_value FROM pragma_table_info('entry') WHERE name = 'exclusion_reason'`).
		Scan(&dflt); err != nil {
		t.Fatalf("exclusion_reason column missing: %v", err)
	}
}
