package app

import (
	"context"
	"strings"
	"testing"

	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/season"
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
	// A runner-up must never outseed a winner however quick they were.
	seenNonWinner := false
	for _, s := range slots {
		if s.Place != 1 {
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

// Substitution moves a championship place, so the refusals matter more than the
// success case — and they have to read like an explanation, not a constraint.
func TestSubstitutionIsRefusedWhenItWouldBeWrong(t *testing.T) {
	a, seasonID := seasonFixture(t)
	ctx := context.Background()

	slots, err := a.DB.Qualifiers(ctx, seasonID)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) < 2 {
		t.Fatal("not enough qualifiers to test with")
	}

	// A slot that is not over the limit cannot be given away. Find one: the
	// demo season deliberately produces a racer above the cap as well.
	var within int64
	for _, s := range slots {
		if !s.OverLimit {
			within = s.EntryID
			break
		}
	}
	if within == 0 {
		t.Fatal("every qualifier is over the limit; the fixture cannot test this")
	}

	err = a.Season.Substitute(ctx, seasonID, within, slots[1].EntryID, "coordinator")
	if err == nil {
		t.Fatal("substituting a slot that is within the limit was allowed")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error = %q, want it to explain why", err)
	}

	// Neither can a car that never raced that night.
	if err := a.DB.Substitute(ctx, seasonID, within, 999999); err == nil {
		t.Error("substituting in a car that did not exist was allowed")
	}

	if err := a.Season.UndoSubstitution(ctx, seasonID, within, "coordinator"); err == nil {
		t.Error("undoing a substitution that never happened was allowed")
	}
}

// The success path: a racer over the cap gives a slot back to someone from the
// same race. This is the workflow the club actually performed in 2026.
func TestSubstitutingAnOverLimitSlotKeepsTheFieldTheSameSize(t *testing.T) {
	a, seasonID := seasonFixture(t)
	ctx := context.Background()

	slots, err := a.DB.Qualifiers(ctx, seasonID)
	if err != nil {
		t.Fatal(err)
	}

	// The lowest-seeded slot held by an over-limit racer: the one they would
	// give up.
	var give *season.Slot
	for i := range slots {
		if slots[i].OverLimit {
			give = &slots[i]
		}
	}
	if give == nil {
		t.Fatal("the demo season no longer produces a racer over the entry cap")
	}

	candidates, err := a.DB.SubstituteCandidates(ctx, seasonID, give.RaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) == 0 {
		t.Fatal("nobody in that race could take the slot")
	}
	take := candidates[0]
	if take.Place <= 3 {
		t.Errorf("the offered substitute finished %d, which already qualifies", take.Place)
	}

	if err := a.Season.Substitute(ctx, seasonID, give.EntryID, take.EntryID, "coordinator"); err != nil {
		t.Fatalf("Substitute: %v", err)
	}

	after, _ := a.DB.Qualifiers(ctx, seasonID)
	if len(after) != len(slots) {
		t.Fatalf("the field changed size: %d slots, was %d", len(after), len(slots))
	}
	var found bool
	for _, s := range after {
		if s.EntryID == give.EntryID {
			t.Errorf("the substituted-out car still holds seed %d", s.Seed)
		}
		if s.EntryID == take.EntryID {
			found = true
			if s.SubstitutedFor == nil {
				t.Error("the slot does not record who it replaced")
			}
		}
	}
	if !found {
		t.Error("the substitute is not in the field")
	}

	// And it is reversible, because the season is not over.
	if err := a.Season.UndoSubstitution(ctx, seasonID, give.EntryID, "coordinator"); err != nil {
		t.Fatalf("UndoSubstitution: %v", err)
	}
	restored, _ := a.DB.Qualifiers(ctx, seasonID)
	for _, s := range restored {
		if s.EntryID == give.EntryID {
			return
		}
	}
	t.Error("undoing the substitution did not put the original slot back")
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
