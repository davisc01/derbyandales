package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/store"
)

// A whole championship, end to end: finish the season, check the field in, seed
// it, build the bracket, and race it to a champion.

// fullSeason takes the demo season's last race through to a frozen result, so
// all six nights are in and the championship field is complete.
func fullSeason(t *testing.T) (*App, int64) {
	t.Helper()
	a, seasonID := seasonFixture(t)
	ctx := context.Background()

	races, err := a.DB.Races(ctx, seasonID)
	if err != nil {
		t.Fatal(err)
	}
	var live model.Race
	for _, r := range races {
		if r.Status == model.StatusCheckin {
			live = r
		}
	}
	if live.ID == 0 {
		t.Fatal("the demo season has no race left to run")
	}

	runWholeRace(t, a, live.ID, 0, 0)
	if err := a.DB.SetRaceStatus(ctx, live.ID, model.StatusVoting); err != nil {
		t.Fatal(err)
	}
	if err := a.Season.FreezeRace(ctx, live.ID); err != nil {
		t.Fatalf("freezing the last race: %v", err)
	}
	return a, seasonID
}

// championshipWithField creates the championship and checks in a car for every
// place in the field, using the car that qualified where there is one.
func championshipWithField(t *testing.T, a *App, seasonID int64, skip map[string]bool) int64 {
	t.Helper()
	ctx := context.Background()

	champ, err := a.DB.CreateRace(ctx, model.Race{
		SeasonID: seasonID,
		Number:   1,
		Name:     "Championship",
		Date:     time.Now(),
		Venue:    "The Testing Room",
		Kind:     model.RaceChampionship,
		Status:   model.StatusCheckin,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Who is in the field, before anybody has checked in.
	field, err := a.DB.ProposeSeeding(ctx, seasonID, champ.ID)
	if err != nil {
		t.Fatalf("ProposeSeeding: %v", err)
	}
	if len(field) == 0 {
		t.Fatal("the championship field is empty")
	}

	number := 100
	for _, c := range field {
		if skip[c.Driver] {
			continue
		}
		number++
		carName := c.CarName
		if carName == "" {
			carName = "Wildcard Entry " + c.Driver
		}
		now := time.Now()
		if _, err := a.DB.CreateEntry(ctx, model.Entry{
			RaceID:      champ.ID,
			RacerID:     c.RacerID,
			CarNumber:   number,
			CarName:     carName,
			CheckedInAt: &now,
		}); err != nil {
			t.Fatalf("checking in %s: %v", c.Driver, err)
		}
	}
	return champ.ID
}

func TestTheChampionshipFieldIsBuiltFromTheSeason(t *testing.T) {
	a, seasonID := fullSeason(t)
	ctx := context.Background()

	champID := championshipWithField(t, a, seasonID, nil)
	proposals, err := a.Bracket.Seed(ctx, seasonID, champID)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}

	s, _ := a.DB.Season(ctx, seasonID)
	want := s.RaceCount*s.AutoQualPlaces + s.WildcardSpots
	if len(proposals) != want {
		t.Fatalf("%d places in the field, want %d", len(proposals), want)
	}

	for _, p := range proposals {
		if !p.Matched() {
			t.Errorf("seed %d (%s) was not matched to a car: %s", p.Seed, p.Driver, p.Why)
		}
	}
	// Every qualifier brought the car that qualified, so every one of those is
	// an exact match and needs no checking by a person.
	for _, p := range proposals {
		if p.Origin == "qualifier" && !p.Exact {
			t.Errorf("seed %d matched loosely (%s) though the car qualified", p.Seed, p.Why)
		}
	}

	// Seeds are contiguous from one, and no car holds two of them.
	seen := map[int64]bool{}
	for i, p := range proposals {
		if p.Seed != i+1 {
			t.Fatalf("place %d has seed %d", i, p.Seed)
		}
		if seen[p.EntryID] {
			t.Fatalf("car %d is seeded twice", p.CarNumber)
		}
		seen[p.EntryID] = true
	}
}

// The whole point: a season's results become a bracket with no seeds typed in
// by hand.
func TestAChampionshipRunsToAChampion(t *testing.T) {
	a, seasonID := fullSeason(t)
	ctx := context.Background()

	champID := championshipWithField(t, a, seasonID, nil)
	proposals, _ := a.Bracket.Seed(ctx, seasonID, champID)

	st, err := a.Bracket.Generate(ctx, champID, proposals, "coordinator")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if st.Entrants != 24 || st.Capacity != 32 || st.Byes != 8 || st.Rounds != 5 {
		t.Fatalf("a 24-car field gave %d entrants, %d capacity, %d byes, %d rounds",
			st.Entrants, st.Capacity, st.Byes, st.Rounds)
	}

	// The byes are already through: a bye is not a race and must never look
	// like one waiting to happen.
	round2Filled := 0
	for _, m := range st.Matchups {
		if m.Round == 2 && (m.TopEntryID != nil || m.BottomEntryID != nil) {
			round2Filled++
		}
	}
	if round2Filled != 8 {
		t.Errorf("%d round-two slots filled by byes, want 8", round2Filled)
	}

	// Race it. The lower seed wins every time, so the outcome is known and any
	// structural mistake shows up as the wrong champion.
	seedOf := map[int64]int{}
	for _, s := range st.Seeds {
		seedOf[s.Entry.ID] = s.Seed
	}
	raced := 0
	for i := 0; i < 100; i++ {
		m, err := a.DB.NextMatchup(ctx, champID)
		if err != nil {
			break
		}
		winner := *m.TopEntryID
		if seedOf[*m.BottomEntryID] < seedOf[winner] {
			winner = *m.BottomEntryID
		}
		if err := a.Bracket.Declare(ctx, champID, m.ID, winner, "test", "scripted"); err != nil {
			t.Fatalf("declaring round %d position %d: %v", m.Round, m.Position, err)
		}
		raced++
	}

	// Every car but the champion has to lose once, so a 24-car field is 23
	// races however many byes there are. The byes only move where they happen:
	// eight round-one races instead of sixteen, and then eight in round two
	// rather than four.
	if raced != 23 {
		t.Errorf("%d matchups raced, want 23 — one per car eliminated", raced)
	}

	champ, err := a.DB.Champion(ctx, champID)
	if err != nil {
		t.Fatalf("no champion after a completed bracket: %v", err)
	}
	if seedOf[champ.ID] != 1 {
		t.Errorf("seed %d won with the better seed always winning, want seed 1", seedOf[champ.ID])
	}

	race, _ := a.DB.Race(ctx, champID)
	if race.Status != model.StatusComplete {
		t.Errorf("the championship is %q after a champion was decided", race.Status)
	}
}

// Somebody always drops out. The field should shrink and the bracket adjust,
// rather than leaving a hole that acts as a bye nobody earned.
func TestANoShowShrinksTheFieldAndAddsABye(t *testing.T) {
	a, seasonID := fullSeason(t)
	ctx := context.Background()

	full := championshipWithField(t, a, seasonID, nil)
	proposals, _ := a.Bracket.Seed(ctx, seasonID, full)
	missing := proposals[len(proposals)-1].Driver

	// A second championship, this time without that racer.
	a2, seasonID2 := fullSeason(t)
	champID := championshipWithField(t, a2, seasonID2, map[string]bool{missing: true})

	proposals2, err := a2.Bracket.Seed(ctx, seasonID2, champID)
	if err != nil {
		t.Fatal(err)
	}
	unmatched := 0
	for _, p := range proposals2 {
		if !p.Matched() {
			unmatched++
			if !strings.Contains(p.Why, "checked in") {
				t.Errorf("seed %d is unmatched but says %q", p.Seed, p.Why)
			}
		}
	}
	if unmatched == 0 {
		t.Fatal("the absent racer was matched to a car anyway")
	}

	st, err := a2.Bracket.Generate(ctx, champID, proposals2, "coordinator")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if st.Entrants != 24-unmatched {
		t.Errorf("%d entrants with %d absent, want %d", st.Entrants, unmatched, 24-unmatched)
	}
	if st.Byes != st.Capacity-st.Entrants {
		t.Errorf("%d byes for %d entrants in a %d bracket", st.Byes, st.Entrants, st.Capacity)
	}
	// Seeds stay contiguous: a gap would be a bye in the middle of the field.
	for i, s := range st.Seeds {
		if s.Seed != i+1 {
			t.Fatalf("seed %d at position %d — the field has a hole in it", s.Seed, i)
		}
	}
}

// A bracket that has started cannot be rebuilt, because rebuilding it would
// throw away results people watched happen.
func TestTheBracketCannotBeRebuiltOnceItHasStarted(t *testing.T) {
	a, seasonID := fullSeason(t)
	ctx := context.Background()

	champID := championshipWithField(t, a, seasonID, nil)
	proposals, _ := a.Bracket.Seed(ctx, seasonID, champID)
	if _, err := a.Bracket.Generate(ctx, champID, proposals, "coordinator"); err != nil {
		t.Fatal(err)
	}

	m, err := a.DB.NextMatchup(ctx, champID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.CreateMatchupHeat(ctx, champID, m.ID); err != nil {
		t.Fatal(err)
	}
	heat, _ := a.DB.Heat(ctx, *mustMatchup(t, a, m.ID).HeatID)
	times := map[int]float64{}
	for _, l := range heat.Lanes {
		if l.EntryID != nil {
			times[l.Lane] = 2.4
		}
	}
	if err := a.DB.RecordHeatResults(ctx, heat.ID, times); err != nil {
		t.Fatal(err)
	}

	_, err = a.DB.GenerateBracket(ctx, champID)
	if err == nil {
		t.Fatal("the bracket was rebuilt over a result")
	}
	if !strings.Contains(err.Error(), "already started") {
		t.Errorf("error = %q, want it to explain why", err)
	}
}

// A matchup is decided by which car got down the track first, not by how the
// times compare to anybody else's.
func TestAMatchupIsDecidedByTheFasterCar(t *testing.T) {
	a, seasonID := fullSeason(t)
	ctx := context.Background()

	champID := championshipWithField(t, a, seasonID, nil)
	proposals, _ := a.Bracket.Seed(ctx, seasonID, champID)
	if _, err := a.Bracket.Generate(ctx, champID, proposals, "coordinator"); err != nil {
		t.Fatal(err)
	}

	m, _ := a.DB.NextMatchup(ctx, champID)
	heat, err := a.DB.CreateMatchupHeat(ctx, champID, m.ID)
	if err != nil {
		t.Fatal(err)
	}

	// The bottom car wins, whatever the seeding says.
	slower, faster := *m.TopEntryID, *m.BottomEntryID
	times := map[int]float64{}
	for _, l := range heat.Lanes {
		if l.EntryID == nil {
			continue
		}
		if *l.EntryID == faster {
			times[l.Lane] = 2.301
		} else {
			times[l.Lane] = 2.402
		}
	}
	if err := a.DB.RecordHeatResults(ctx, heat.ID, times); err != nil {
		t.Fatal(err)
	}
	if err := a.Bracket.RecordResult(ctx, champID, m.ID); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}

	after := mustMatchup(t, a, m.ID)
	if after.WinnerEntryID == nil || *after.WinnerEntryID != faster {
		t.Errorf("the slower car went through")
	}
	_ = slower
}

// A dead heat has no second criterion to fall back on, and inventing one would
// be worse than asking someone to run it again.
func TestADeadHeatIsNotDecidedBySoftware(t *testing.T) {
	a, seasonID := fullSeason(t)
	ctx := context.Background()

	champID := championshipWithField(t, a, seasonID, nil)
	proposals, _ := a.Bracket.Seed(ctx, seasonID, champID)
	a.Bracket.Generate(ctx, champID, proposals, "coordinator")

	m, _ := a.DB.NextMatchup(ctx, champID)
	heat, _ := a.DB.CreateMatchupHeat(ctx, champID, m.ID)
	times := map[int]float64{}
	for _, l := range heat.Lanes {
		if l.EntryID != nil {
			times[l.Lane] = 2.345
		}
	}
	a.DB.RecordHeatResults(ctx, heat.ID, times)

	err := a.Bracket.RecordResult(ctx, champID, m.ID)
	if err == nil {
		t.Fatal("a dead heat was decided automatically")
	}
	if !strings.Contains(err.Error(), "again") {
		t.Errorf("error = %q, want it to say to run it again", err)
	}
}

// Racers ask to swap lanes, and it costs nothing to allow before the matchup
// has been run.
func TestLanesCanBeSwappedBeforeAMatchupRuns(t *testing.T) {
	a, seasonID := fullSeason(t)
	ctx := context.Background()

	champID := championshipWithField(t, a, seasonID, nil)
	proposals, _ := a.Bracket.Seed(ctx, seasonID, champID)
	a.Bracket.Generate(ctx, champID, proposals, "coordinator")

	m, _ := a.DB.NextMatchup(ctx, champID)
	top, bottom := *m.TopEntryID, *m.BottomEntryID

	if err := a.Bracket.SwapLanes(ctx, m.ID, "coordinator"); err != nil {
		t.Fatalf("SwapLanes: %v", err)
	}
	after := mustMatchup(t, a, m.ID)
	if *after.TopEntryID != bottom || *after.BottomEntryID != top {
		t.Error("the cars did not swap")
	}

	// And once it has been raced, swapping is refused rather than silently
	// rewriting who was in which lane.
	heat, _ := a.DB.CreateMatchupHeat(ctx, champID, m.ID)
	times := map[int]float64{}
	for _, l := range heat.Lanes {
		if l.EntryID != nil {
			times[l.Lane] = 2.4
		}
	}
	a.DB.RecordHeatResults(ctx, heat.ID, times)
	if err := a.Bracket.SwapLanes(ctx, m.ID, "coordinator"); err == nil {
		t.Error("lanes were swapped after the matchup had run")
	}
}

// Bracket heats run on the configured pair of lanes, and the rest run empty.
func TestBracketHeatsUseTheConfiguredLanes(t *testing.T) {
	a, seasonID := fullSeason(t)
	ctx := context.Background()

	s, _ := a.DB.Season(ctx, seasonID)
	s.BracketLaneA, s.BracketLaneB = 2, 3
	if err := a.DB.UpdateSeason(ctx, s); err != nil {
		t.Fatal(err)
	}

	champID := championshipWithField(t, a, seasonID, nil)
	proposals, _ := a.Bracket.Seed(ctx, seasonID, champID)
	a.Bracket.Generate(ctx, champID, proposals, "coordinator")

	m, _ := a.DB.NextMatchup(ctx, champID)
	heat, err := a.DB.CreateMatchupHeat(ctx, champID, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range heat.Lanes {
		occupied := l.EntryID != nil
		want := l.Lane == 2 || l.Lane == 3
		if occupied != want {
			t.Errorf("lane %d occupied = %v, want %v", l.Lane, occupied, want)
		}
	}
}

func mustMatchup(t *testing.T, a *App, id int64) model.BracketMatchup {
	t.Helper()
	m, err := a.DB.Matchup(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

var _ = store.SeedProposal{}
