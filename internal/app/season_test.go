package app

import (
	"context"
	"testing"

	"github.com/davisc01/derbyandales/internal/model"
)

// These drive the season against a real database: five fabricated race nights,
// scored and frozen the way a real season accumulates.

// seasonFixture returns an app with a demo season already five races deep.
func seasonFixture(t *testing.T) (*App, int64) {
	t.Helper()
	a := testApp(t)
	ctx := context.Background()

	live, err := a.SeedDemoSeason(ctx, 2027)
	if err != nil {
		t.Fatalf("seed demo season: %v", err)
	}
	race, err := a.DB.Race(ctx, live.ID)
	if err != nil {
		t.Fatal(err)
	}
	return a, race.SeasonID
}

func TestASeasonAddsUpFromItsFinishedRaces(t *testing.T) {
	a, seasonID := seasonFixture(t)
	ctx := context.Background()

	o, err := a.Season.Season(ctx, seasonID)
	if err != nil {
		t.Fatalf("Season: %v", err)
	}

	frozen := 0
	for _, r := range o.Races {
		if r.Frozen {
			frozen++
			if r.Cars == 0 {
				t.Errorf("%s is frozen with no cars", r.Race.Name)
			}
			if r.FrozenAt.IsZero() {
				t.Errorf("%s is frozen but has no timestamp", r.Race.Name)
			}
		}
	}
	if frozen != demoPastRaces {
		t.Errorf("%d races recorded, want %d", frozen, demoPastRaces)
	}

	// Three auto-qualifiers per finished race, and the last night has not been
	// run — so the field is short of a full season, which is the normal state
	// of this screen.
	want := demoPastRaces * o.Season.AutoQualPlaces
	if len(o.Qualifiers) != want {
		t.Errorf("%d qualifiers, want %d", len(o.Qualifiers), want)
	}
	if o.Expected <= o.Entrants {
		t.Errorf("expected field %d should exceed the current %d with a race still to run",
			o.Expected, o.Entrants)
	}

	if len(o.Wildcard) == 0 {
		t.Fatal("no wildcard standings after five races")
	}
	for i := 1; i < len(o.Wildcard); i++ {
		a, b := o.Wildcard[i-1], o.Wildcard[i]
		if a.MaxedOut == b.MaxedOut && b.Total > a.Total {
			t.Errorf("wildcard standings are out of order at rank %d", b.Rank)
		}
		if a.MaxedOut && !b.MaxedOut {
			t.Errorf("a maxed-out racer is ranked above one still in contention at %d", b.Rank)
		}
	}
}

// Seeds are the championship's running order, so they must be dense, unique and
// start at one.
func TestQualifiersAreSeededInOrder(t *testing.T) {
	a, seasonID := seasonFixture(t)

	slots, err := a.DB.Qualifiers(context.Background(), seasonID)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range slots {
		if s.Seed != i+1 {
			t.Fatalf("slot %d has seed %d", i, s.Seed)
		}
	}
	// Race winners come first, fastest first; then everyone else by average.
	// A runner-up must never outseed a winner however quick they were. "Winner"
	// is the best qualifier from each race, which is the winner unless their
	// place passed down.
	seenNonWinner := false
	for _, s := range slots {
		if !s.RaceTop {
			seenNonWinner = true
		} else if seenNonWinner {
			t.Errorf("seed %d won a race but is seeded below a runner-up", s.Seed)
		}
	}
}

// The pace car is ranked in every race's standings, but there is no such racer.
// The club's own published 2026 table lists "Derby Ales" 36th on zero points.
func TestThePaceCarDriverIsNotInTheSeasonStandings(t *testing.T) {
	a, seasonID := seasonFixture(t)
	ctx := context.Background()

	rows, err := a.DB.Wildcard(ctx, seasonID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Name == "Derby Ales" {
			t.Fatalf("the pace car's driver is ranked %d in the wildcard standings", r.Rank)
		}
	}

	slots, _ := a.DB.Qualifiers(ctx, seasonID)
	for _, s := range slots {
		if s.IsControl {
			t.Errorf("the pace car holds championship seed %d", s.Seed)
		}
	}
}

// An excluded car races and is timed, but it is not in the standings, so it
// cannot earn points either.
func TestAnExcludedCarEarnsNoSeasonPoints(t *testing.T) {
	a, seasonID := seasonFixture(t)
	ctx := context.Background()

	finishes, err := a.DB.SeasonFinishes(ctx, seasonID)
	if err != nil {
		t.Fatal(err)
	}

	// The demo excludes car 62 from race 3. It should have no frozen result at
	// all rather than a zero-point one.
	found := false
	for _, f := range finishes {
		if f.RaceNumber == 3 && f.CarNum == 62 {
			found = true
		}
	}
	if found {
		t.Error("the excluded car has a season result")
	}

	// And its exclusion moved everyone behind it up, so the race still numbers
	// its places cleanly from one.
	places := map[int]bool{}
	for _, f := range finishes {
		if f.RaceNumber == 3 && f.Place > 0 {
			if places[f.Place] {
				continue // a genuine tie shares a place
			}
			places[f.Place] = true
		}
	}
	if !places[1] {
		t.Error("race 3 has no first place")
	}
}

// The whole point of freezing: a race that has been run is a fact, and changing
// a setting afterwards must not quietly rewrite it.
func TestChangingASettingDoesNotRewriteFinishedRaces(t *testing.T) {
	a, seasonID := seasonFixture(t)
	ctx := context.Background()

	before, err := a.DB.Wildcard(ctx, seasonID)
	if err != nil {
		t.Fatal(err)
	}

	s, _ := a.DB.Season(ctx, seasonID)
	s.PointsCountControl = true // the old, uncorrected behaviour
	if err := a.DB.UpdateSeason(ctx, s); err != nil {
		t.Fatal(err)
	}

	after, _ := a.DB.Wildcard(ctx, seasonID)
	if len(after) != len(before) {
		t.Fatalf("the standings changed size: %d rows, was %d", len(after), len(before))
	}
	for i := range before {
		if after[i].Total != before[i].Total {
			t.Fatalf("%s moved from %d points to %d without a recompute",
				before[i].Name, before[i].Total, after[i].Total)
		}
	}

	// Recomputing is how you ask for it, and then the points do move — by one
	// per race the racer scored in, because the pace car is back in the field.
	n, err := a.Season.Recompute(ctx, seasonID, "coordinator")
	if err != nil {
		t.Fatalf("Recompute: %v", err)
	}
	if n != demoPastRaces {
		t.Errorf("recomputed %d races, want %d", n, demoPastRaces)
	}

	recomputed, _ := a.DB.Wildcard(ctx, seasonID)
	moved := 0
	for _, row := range recomputed {
		for _, was := range before {
			if was.RacerID == row.RacerID && row.Total != was.Total {
				moved++
			}
		}
	}
	if moved == 0 {
		t.Error("recomputing after the setting change moved nobody")
	}

	// And it is on the record, because it changed published results.
	entries, _ := a.DB.RecentAudit(ctx, 20)
	found := false
	for _, e := range entries {
		if e.Action == "season.recompute" {
			found = true
		}
	}
	if !found {
		t.Error("a recompute should be recorded in the audit log")
	}
}

// Adjustments are the manual override, and the reason is what makes them
// defensible three months later.
func TestAnAdjustmentNeedsAReason(t *testing.T) {
	a, seasonID := seasonFixture(t)
	ctx := context.Background()

	rows, _ := a.DB.Wildcard(ctx, seasonID)
	if len(rows) == 0 {
		t.Fatal("no racers")
	}
	racer := rows[0]

	if err := a.Season.Adjust(ctx, seasonID, racer.RacerID, 5, "", "coordinator"); err == nil {
		t.Error("an adjustment without a reason was accepted")
	}
	if err := a.Season.Adjust(ctx, seasonID, racer.RacerID, 0, "nothing", "coordinator"); err == nil {
		t.Error("an adjustment of zero points was accepted")
	}

	if err := a.Season.Adjust(ctx, seasonID, racer.RacerID, -4,
		"ran an ineligible wheelbase in race 2", "coordinator"); err != nil {
		t.Fatalf("Adjust: %v", err)
	}

	after, _ := a.DB.Wildcard(ctx, seasonID)
	for _, row := range after {
		if row.RacerID != racer.RacerID {
			continue
		}
		if row.Adjusted != -4 {
			t.Errorf("adjustment recorded as %d, want -4", row.Adjusted)
		}
		if row.Total != row.Earned-4 {
			t.Errorf("total %d does not include the adjustment (earned %d)", row.Total, row.Earned)
		}
	}

	list, _ := a.DB.Adjustments(ctx, seasonID)
	if len(list) != 1 {
		t.Fatalf("%d adjustments recorded, want 1", len(list))
	}
	if err := a.Season.RemoveAdjustment(ctx, seasonID, list[0].ID, "coordinator"); err != nil {
		t.Fatalf("RemoveAdjustment: %v", err)
	}
	if list, _ := a.DB.Adjustments(ctx, seasonID); len(list) != 0 {
		t.Errorf("%d adjustments left after removing one", len(list))
	}
}

// The championship it feeds is not a points race and must not become one.
func TestTheChampionshipAwardsNoSeasonPoints(t *testing.T) {
	a, seasonID := seasonFixture(t)
	ctx := context.Background()

	champ, err := a.DB.CreateRace(ctx, model.Race{
		SeasonID: seasonID,
		Number:   1,
		Name:     "Championship",
		Kind:     model.RaceChampionship,
		Status:   model.StatusVoting,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.DB.FreezeRace(ctx, champ.ID); err == nil {
		t.Fatal("the championship was recorded as a points race")
	}

	// And a recompute skips it rather than failing on it.
	if _, err := a.Season.Recompute(ctx, seasonID, "coordinator"); err != nil {
		t.Errorf("Recompute: %v", err)
	}
}

// A season changes shape — a race cancelled, a wildcard spot added — and the
// software has to allow it without quietly rewriting nights already raced.
func TestSeasonSettingsChangeWithoutRewritingFinishedRaces(t *testing.T) {
	a, seasonID := seasonFixture(t)
	ctx := context.Background()

	before, _ := a.DB.Wildcard(ctx, seasonID)
	s, _ := a.DB.Season(ctx, seasonID)
	in := SeasonSettings{
		Name: s.Name, TrackLengthFt: s.TrackLengthFt, RaceCount: s.RaceCount,
		AutoQualPlaces: s.AutoQualPlaces, WildcardSpots: 8,
		MaxChampionshipEntry: s.MaxChampionshipEntry, PointsCountControl: true,
		BracketLaneA: 2, BracketLaneB: 3,
	}
	change, err := a.Season.UpdateSettings(ctx, seasonID, in, "test")
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if len(change.Changed) != 3 {
		t.Errorf("reported changes %v, want wildcards, pace car and lanes", change.Changed)
	}
	if !change.NeedsRecompute {
		t.Error("counting the pace car changes points, but no recompute was called for")
	}
	got, _ := a.DB.Season(ctx, seasonID)
	if got.WildcardSpots != 8 || !got.PointsCountControl || got.BracketLaneA != 2 || got.BracketLaneB != 3 {
		t.Errorf("settings not saved: %+v", got)
	}
	after, _ := a.DB.Wildcard(ctx, seasonID)
	for i := range before {
		if i < len(after) && before[i].Total != after[i].Total {
			t.Fatalf("saving a setting rewrote %s's points from %d to %d", before[i].Name, before[i].Total, after[i].Total)
		}
	}

	// Values that cannot be right are refused with a reason.
	for name, bad := range map[string]func(*SeasonSettings){
		"same lane twice":        func(x *SeasonSettings) { x.BracketLaneB = x.BracketLaneA },
		"lane off the track":     func(x *SeasonSettings) { x.BracketLaneA = 9 },
		"no races":               func(x *SeasonSettings) { x.RaceCount = 0 },
		"fewer than raced":       func(x *SeasonSettings) { x.RaceCount = 2 },
		"nobody qualifies":       func(x *SeasonSettings) { x.AutoQualPlaces = 0 },
		"negative wildcards":     func(x *SeasonSettings) { x.WildcardSpots = -1 },
		"a track with no length": func(x *SeasonSettings) { x.TrackLengthFt = 0 },
	} {
		x := in
		bad(&x)
		if _, err := a.Season.UpdateSettings(ctx, seasonID, x, "test"); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// A name typed two ways at check-in splits one person's season in two, which
// can cost them a wildcard spot. Renaming fixes the spelling; merging puts the
// season back together.
func TestARacerSplitByATypoIsMergedBackTogether(t *testing.T) {
	a, seasonID := seasonFixture(t)
	ctx := context.Background()

	racers, err := a.DB.CompetingRacers(ctx, seasonID)
	if err != nil || len(racers) < 2 {
		t.Fatalf("need two racers, have %d (%v)", len(racers), err)
	}
	keep, drop := racers[0], racers[1]

	// Renaming onto a name already in use is a merge in disguise, and refused.
	if err := a.Season.RenameRacer(ctx, drop.ID, keep.FirstName, keep.LastName, "test"); err == nil {
		t.Error("a racer was renamed onto another racer's name")
	}
	if err := a.Season.RenameRacer(ctx, drop.ID, "  Corrected ", "Spelling", "test"); err != nil {
		t.Fatalf("RenameRacer: %v", err)
	}
	if r, _ := a.DB.Racer(ctx, drop.ID); r.FullName() != "Corrected Spelling" {
		t.Errorf("renamed racer is %q", r.FullName())
	}

	entriesOf := func(id int64) int {
		var n int
		a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM entry WHERE racer_id = ?`, id).Scan(&n)
		return n
	}
	total := entriesOf(keep.ID) + entriesOf(drop.ID)

	if _, err := a.Season.MergeRacers(ctx, keep.ID, drop.ID, "test"); err != nil {
		t.Fatalf("MergeRacers: %v", err)
	}
	if got := entriesOf(keep.ID); got != total {
		t.Errorf("%d entries after merging, want all %d", got, total)
	}
	if _, err := a.DB.Racer(ctx, drop.ID); err == nil {
		t.Error("the duplicate racer still exists")
	}
	after, _ := a.DB.CompetingRacers(ctx, seasonID)
	if len(after) != len(racers)-1 {
		t.Errorf("%d racers after merging, want %d", len(after), len(racers)-1)
	}
	if _, err := a.Season.MergeRacers(ctx, keep.ID, keep.ID, "test"); err == nil {
		t.Error("a racer was merged into itself")
	}
}
