package store

import (
	"context"
	"testing"
)

func voteFixture(t *testing.T) (*DB, int64, []EntryView, []int64) {
	t.Helper()
	db, raceID, entries := fixture(t, 6)
	ctx := context.Background()

	if err := db.EnsureVoteCategories(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	categories, err := db.VoteCategories(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(categories))
	for _, c := range categories {
		ids = append(ids, c.ID)
	}
	return db, raceID, entries, ids
}

func TestVoteCategoriesAreCreatedOnce(t *testing.T) {
	db, raceID, _, ids := voteFixture(t)
	ctx := context.Background()

	if len(ids) != 2 {
		t.Fatalf("got %d categories, want 2", len(ids))
	}

	// Calling again must not duplicate them; it runs on every intermission.
	if err := db.EnsureVoteCategories(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	again, _ := db.VoteCategories(ctx, raceID)
	if len(again) != 2 {
		t.Errorf("got %d categories after a second call, want 2", len(again))
	}

	if again[0].Key != VoteTheme || again[1].Key != VoteDesign {
		t.Errorf("ballot order is %s then %s, want theme then design",
			again[0].Key, again[1].Key)
	}
}

// The pace car is not a competitor. Ineligible cars stay on the ballot: they
// are barred from the standings, not from having a nice paint job.
func TestBallotSkipsThePaceCarButKeepsIneligibleCars(t *testing.T) {
	db, raceID, entries, _ := voteFixture(t)
	ctx := context.Background()

	// fixture() makes the first entry the CONTROL car.
	excluded := entries[2]
	excluded.Excluded = true
	excluded.ExclusionReason = "raced in a previous championship"
	if err := db.UpdateEntry(ctx, excluded.Entry); err != nil {
		t.Fatal(err)
	}

	ballot, err := db.BallotEntries(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ballot) != len(entries)-1 {
		t.Errorf("ballot has %d cars, want %d", len(ballot), len(entries)-1)
	}
	for _, e := range ballot {
		if e.IsControl {
			t.Error("the pace car is on the ballot")
		}
	}
	found := false
	for _, e := range ballot {
		if e.ID == excluded.ID {
			found = true
		}
	}
	if !found {
		t.Error("an ineligible car should still be votable")
	}
}

func TestTallyCountsAndOrders(t *testing.T) {
	db, _, entries, ids := voteFixture(t)
	ctx := context.Background()
	theme := ids[0]

	// entries[0] is the pace car, so vote for the others.
	for i := 0; i < 3; i++ {
		if _, err := db.CastVote(ctx, theme, entries[1].ID); err != nil {
			t.Fatal(err)
		}
	}
	db.CastVote(ctx, theme, entries[2].ID)

	tally, err := db.Tally(ctx, theme)
	if err != nil {
		t.Fatal(err)
	}
	if tally[0].Entry.ID != entries[1].ID || tally[0].Votes != 3 {
		t.Errorf("top row is entry %d with %d votes, want %d with 3",
			tally[0].Entry.ID, tally[0].Votes, entries[1].ID)
	}
	if !tally[0].Leading || tally[1].Leading {
		t.Error("only the top count should be marked leading")
	}

	// Every eligible car is listed, including the ones with no votes, so the
	// coordinator sees the whole field.
	if len(tally) != len(entries)-1 {
		t.Errorf("tally has %d rows, want the whole ballot (%d)", len(tally), len(entries)-1)
	}

	count, _ := db.VoteCount(ctx, theme)
	if count != 4 {
		t.Errorf("vote count = %d, want 4", count)
	}
}

// A tie is the case the software must not resolve by itself.
func TestTiedLeadersAreBothMarked(t *testing.T) {
	db, _, entries, ids := voteFixture(t)
	ctx := context.Background()
	theme := ids[0]

	for i := 0; i < 2; i++ {
		db.CastVote(ctx, theme, entries[1].ID)
		db.CastVote(ctx, theme, entries[2].ID)
	}

	tally, err := db.Tally(ctx, theme)
	if err != nil {
		t.Fatal(err)
	}
	leaders := 0
	for _, row := range tally {
		if row.Leading {
			leaders++
		}
	}
	if leaders != 2 {
		t.Errorf("%d cars marked leading, want 2 — a tie needs a person", leaders)
	}
}

// A misclick is the common case, so undo is one action and names the car.
func TestUndoLastVote(t *testing.T) {
	db, _, entries, ids := voteFixture(t)
	ctx := context.Background()
	theme := ids[0]

	db.CastVote(ctx, theme, entries[1].ID)
	db.CastVote(ctx, theme, entries[2].ID)

	undone, err := db.UndoLastVote(ctx, theme)
	if err != nil {
		t.Fatal(err)
	}
	if undone.EntryID != entries[2].ID {
		t.Errorf("undid the vote for entry %d, want the most recent (%d)",
			undone.EntryID, entries[2].ID)
	}

	count, _ := db.VoteCount(ctx, theme)
	if count != 1 {
		t.Errorf("count = %d after an undo, want 1", count)
	}

	// Undoing with nothing left says so rather than failing silently.
	db.UndoLastVote(ctx, theme)
	if _, err := db.UndoLastVote(ctx, theme); err == nil {
		t.Error("undoing with no votes left should report an error")
	}
}

// Votes are voided rather than deleted, so a mistaken reset leaves evidence.
func TestResetVoidsRatherThanDeletes(t *testing.T) {
	db, _, entries, ids := voteFixture(t)
	ctx := context.Background()
	theme := ids[0]

	for i := 0; i < 3; i++ {
		db.CastVote(ctx, theme, entries[1].ID)
	}
	if err := db.ResetVoteCategory(ctx, theme); err != nil {
		t.Fatal(err)
	}

	if count, _ := db.VoteCount(ctx, theme); count != 0 {
		t.Errorf("live votes = %d after a reset, want 0", count)
	}
	var total int
	db.QueryRow(`SELECT COUNT(*) FROM vote WHERE category_id = ?`, theme).Scan(&total)
	if total != 3 {
		t.Errorf("%d vote rows remain, want 3 — reset voids, it does not delete", total)
	}
}

// Declaring writes the award the website expects, by name.
func TestDeclareWinnerWritesTheAward(t *testing.T) {
	db, raceID, entries, ids := voteFixture(t)
	ctx := context.Background()

	if err := db.DeclareVoteWinner(ctx, ids[0], entries[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeclareVoteWinner(ctx, ids[1], entries[2].ID); err != nil {
		t.Fatal(err)
	}

	awards, err := db.Awards(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(awards) != 2 {
		t.Fatalf("got %d awards, want 2", len(awards))
	}

	byName := map[string]AwardView{}
	for _, a := range awards {
		byName[a.Name] = a
	}
	// These strings go straight into the website's awards.csv and must match
	// what is already published there.
	for _, want := range []string{"Best Theme", "Best Design"} {
		a, ok := byName[want]
		if !ok {
			t.Fatalf("no award named %q; got %v", want, awards)
		}
		if a.AwardType != AwardTypeVoted {
			t.Errorf("%s type = %q, want %q", want, a.AwardType, AwardTypeVoted)
		}
		if a.Source != "vote" {
			t.Errorf("%s source = %q, want vote", want, a.Source)
		}
	}
}

// Changing your mind must not leave two winners on the website.
func TestRedeclaringReplacesTheAward(t *testing.T) {
	db, raceID, entries, ids := voteFixture(t)
	ctx := context.Background()

	db.DeclareVoteWinner(ctx, ids[0], entries[1].ID)
	db.DeclareVoteWinner(ctx, ids[0], entries[2].ID)

	awards, _ := db.Awards(ctx, raceID)
	if len(awards) != 1 {
		t.Fatalf("got %d awards after redeclaring, want 1", len(awards))
	}
	if awards[0].EntryID == nil || *awards[0].EntryID != entries[2].ID {
		t.Error("the award should belong to the car declared second")
	}

	if err := db.ClearVoteWinner(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	awards, _ = db.Awards(ctx, raceID)
	if len(awards) != 0 {
		t.Errorf("clearing left %d awards, want 0", len(awards))
	}
}

// The pace car races and is ranked, but it does not take trophies.
func TestPaceCarCannotWinATrophy(t *testing.T) {
	db, _, entries, ids := voteFixture(t)
	ctx := context.Background()

	if err := db.DeclareVoteWinner(ctx, ids[0], entries[0].ID); err == nil {
		t.Fatal("the pace car was declared a trophy winner")
	}
}

func TestDisablingACategoryKeepsItsVotes(t *testing.T) {
	db, raceID, entries, ids := voteFixture(t)
	ctx := context.Background()

	db.CastVote(ctx, ids[0], entries[1].ID)
	if err := db.SetVoteCategoryEnabled(ctx, ids[0], false); err != nil {
		t.Fatal(err)
	}

	categories, _ := db.VoteCategories(ctx, raceID)
	if categories[0].Enabled {
		t.Error("the category should be switched off")
	}
	if count, _ := db.VoteCount(ctx, ids[0]); count != 1 {
		t.Error("switching a question off should not discard its votes")
	}
}
