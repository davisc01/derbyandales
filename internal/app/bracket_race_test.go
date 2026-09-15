package app

import (
	"context"
	"testing"
	"time"

	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/store"
	"github.com/davisc01/derbyandales/internal/timer"
)

// A bracket championship raced the way a night is actually run: from race
// control, one heat after another, with nobody opening the championship page to
// say who won. The bracket tests elsewhere declare winners directly, which
// proves the bracket but not that racing drives it.

// bracketReady builds a seeded, generated 24-car bracket with a timer
// connected, and returns the championship's id and each car's seed.
func bracketReady(t *testing.T) (*App, int64, map[int64]int) {
	t.Helper()
	a, seasonID := fullSeason(t)
	ctx := context.Background()

	if err := a.Timer.Connect(ctx, "", timer.SimulatorKey); err != nil {
		t.Fatalf("connect simulator: %v", err)
	}
	if _, err := a.Timer.RunBench(ctx); err != nil {
		t.Fatalf("bench: %v", err)
	}

	champID := championshipWithField(t, a, seasonID, nil)
	proposals, err := a.Bracket.Seed(ctx, seasonID, champID)
	if err != nil {
		t.Fatal(err)
	}
	st, err := a.Bracket.Generate(ctx, champID, proposals, "test")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	seedOf := map[int64]int{}
	for _, s := range st.Seeds {
		seedOf[s.Entry.ID] = s.Seed
	}
	return a, champID, seedOf
}

// lanesOf reads which lane each car is on in a heat.
func lanesOf(h store.HeatView) map[int64]int {
	out := map[int64]int{}
	for _, l := range h.Lanes {
		if l.EntryID != nil {
			out[*l.EntryID] = l.Lane
		}
	}
	return out
}

// The whole championship from race control, times typed in the way a night
// with no timer would be. The better seed is always faster, so the outcome is
// known in advance.
func TestABracketIsRacedFromRaceControl(t *testing.T) {
	a, champID, seedOf := bracketReady(t)
	ctx := context.Background()

	if err := a.Race.Start(ctx, champID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(a.Race.Stop)

	heats := 0
	for i := 0; i < 40; i++ {
		st := a.Race.State(ctx)
		if st.Heat == nil {
			t.Fatalf("after %d heats nothing is armed", heats)
		}
		times := map[int]float64{}
		lanes := lanesOf(*st.Heat)
		if len(lanes) != 2 {
			t.Fatalf("heat %d carries %d cars, want the two in a matchup", st.Heat.Number, len(lanes))
		}
		for entry, lane := range lanes {
			times[lane] = 2.4 + float64(seedOf[entry])*0.01
		}
		if err := a.Race.EnterTimes(ctx, st.Heat.ID, times, "test"); err != nil {
			t.Fatalf("entering heat %d: %v", st.Heat.Number, err)
		}
		heats++

		if _, err := a.DB.Champion(ctx, champID); err == nil {
			break
		}
		if err := a.Race.ArmNext(ctx); err != nil {
			t.Fatalf("arming after heat %d: %v", heats, err)
		}
	}

	champ, err := a.DB.Champion(ctx, champID)
	if err != nil {
		t.Fatalf("no champion after %d heats", heats)
	}
	if seedOf[champ.ID] != 1 {
		t.Errorf("seed %d won, but the top seed was fastest every time", seedOf[champ.ID])
	}
	// One race per car eliminated. A heat more than that is a matchup raced
	// twice; fewer is one skipped.
	if heats != 23 {
		t.Errorf("%d heats raced, want 23", heats)
	}

	race, _ := a.DB.Race(ctx, champID)
	if race.Status != model.StatusComplete {
		t.Errorf("the championship is %q after the final, want complete", race.Status)
	}
	// Pressing "next" once it is over must not reopen anything.
	_ = a.Race.ArmNext(ctx)
	if race, _ := a.DB.Race(ctx, champID); race.Status != model.StatusComplete {
		t.Errorf("arming after the final moved the championship to %q", race.Status)
	}
}

// The live path: the timer reports, the matchup is decided, and the next one
// arms on its own. Nobody touches the championship page.
func TestTheTimerDecidesAMatchupAndTheNextOneArms(t *testing.T) {
	a, champID, _ := bracketReady(t)
	ctx := context.Background()

	if err := a.DB.SetSetting(ctx, store.KeyAutoAdvanceSecs, "0"); err != nil {
		t.Fatal(err)
	}
	if err := a.Race.Start(ctx, champID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(a.Race.Stop)

	first := a.Race.State(ctx).Heat
	if first == nil || first.BracketMatchupID == nil {
		t.Fatal("starting the championship did not arm a matchup")
	}

	sim := a.Timer.Simulator()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if h := a.Race.State(ctx).Heat; h != nil && h.ID != first.ID {
			break
		}
		if a.Timer.Device().State() == timer.StateMark {
			sim.CloseGate()
			time.Sleep(MinGateSettle)
			sim.OpenGate()
		}
		time.Sleep(120 * time.Millisecond)
	}

	m, err := a.DB.Matchup(ctx, *first.BracketMatchupID)
	if err != nil {
		t.Fatal(err)
	}
	if m.WinnerEntryID == nil {
		// The simulator can produce a dead heat, but two cars landing on the
		// same thousandth is rare enough that this is the software, not luck.
		t.Fatal("the heat was timed but the matchup was not decided")
	}
	next := a.Race.State(ctx).Heat
	if next == nil || next.ID == first.ID {
		t.Fatal("the next matchup did not arm")
	}
	if next.BracketMatchupID == nil || *next.BracketMatchupID == m.ID {
		t.Error("the heat armed next is not a different matchup")
	}
}

// A dead heat has no second criterion, so the matchup is run again rather than
// given to either car. Nobody has a decision to make, so arming next does it.
func TestADeadHeatInTheBracketIsRunAgain(t *testing.T) {
	a, champID, _ := bracketReady(t)
	ctx := context.Background()

	if err := a.Race.Start(ctx, champID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(a.Race.Stop)

	h := a.Race.State(ctx).Heat
	times := map[int]float64{}
	for _, lane := range lanesOf(*h) {
		times[lane] = 2.5
	}
	if err := a.Race.EnterTimes(ctx, h.ID, times, "test"); err != nil {
		t.Fatalf("entering a dead heat: %v", err)
	}
	m, _ := a.DB.Matchup(ctx, *h.BracketMatchupID)
	if m.WinnerEntryID != nil {
		t.Fatal("a dead heat was given to one of the cars")
	}

	if err := a.Race.ArmNext(ctx); err != nil {
		t.Fatalf("ArmNext after a dead heat: %v", err)
	}
	again := a.Race.State(ctx).Heat
	if again == nil || again.BracketMatchupID == nil || *again.BracketMatchupID != m.ID {
		t.Fatal("the next heat armed is not the matchup that finished level")
	}
	for _, l := range again.Lanes {
		if l.FinishTime != nil {
			t.Fatal("the re-run still carries the dead heat's times")
		}
	}
}

// A crash in a matchup is re-run like any other heat — until the winner has
// raced again, after which undoing it would rewrite a later result.
func TestAMatchupCanBeReRunUntilItsWinnerRacesAgain(t *testing.T) {
	a, champID, seedOf := bracketReady(t)
	ctx := context.Background()

	if err := a.Race.Start(ctx, champID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(a.Race.Stop)

	race := func() store.HeatView {
		t.Helper()
		h := a.Race.State(ctx).Heat
		times := map[int]float64{}
		for entry, lane := range lanesOf(*h) {
			times[lane] = 2.4 + float64(seedOf[entry])*0.01
		}
		if err := a.Race.EnterTimes(ctx, h.ID, times, "test"); err != nil {
			t.Fatalf("entering heat %d: %v", h.Number, err)
		}
		return *h
	}

	first := race()
	if err := a.Race.ReRun(ctx, first.ID); err != nil {
		t.Fatalf("re-running a matchup whose winner has not raced again: %v", err)
	}
	m, _ := a.DB.Matchup(ctx, *first.BracketMatchupID)
	if m.WinnerEntryID != nil {
		t.Error("re-running the matchup left its old winner in place")
	}
	// The winner must also come back out of the round above, or they would be
	// waiting there for a race they have not won.
	for _, other := range mustMatchups(t, a, champID) {
		if other.Round == m.Round+1 && (other.TopEntryID != nil && lanesOf(first)[*other.TopEntryID] != 0 ||
			other.BottomEntryID != nil && lanesOf(first)[*other.BottomEntryID] != 0) {
			t.Error("the re-run matchup's cars are still waiting in the next round")
		}
	}

	// Race it properly, then carry on until one of its cars has raced again.
	race()
	winner, _ := a.DB.Matchup(ctx, *first.BracketMatchupID)
	for i := 0; i < 30; i++ {
		if err := a.Race.ArmNext(ctx); err != nil {
			t.Fatal(err)
		}
		h := race()
		if _, ok := lanesOf(h)[*winner.WinnerEntryID]; ok {
			break
		}
	}
	if err := a.Race.ReRun(ctx, first.ID); err == nil {
		t.Error("a matchup was re-run after its winner had already raced the next round")
	}
}

func mustMatchups(t *testing.T, a *App, champID int64) []store.MatchupView {
	t.Helper()
	ms, err := a.DB.Matchups(context.Background(), champID)
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

// Closing check-in builds a round-robin schedule. On a bracket that would put
// every car on the track four times in a race meant to eliminate them.
func TestABracketIsNotGivenARoundRobinSchedule(t *testing.T) {
	a, champID, _ := bracketReady(t)
	if _, err := a.Race.GenerateSchedule(context.Background(), champID); err == nil {
		t.Error("a bracket championship was given a round-robin schedule")
	}
}

// A standard championship — how the club ran 2023 to 2025 — has no voting, so
// it must not stop halfway for an intermission nobody is voting in.
func TestAChampionshipHasNoIntermission(t *testing.T) {
	a, seasonID := fullSeason(t)
	ctx := context.Background()
	champID := championshipWithField(t, a, seasonID, nil)
	if err := a.DB.SetRaceFormat(ctx, champID, model.FormatStandard); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Race.GenerateSchedule(ctx, champID); err != nil {
		t.Fatal(err)
	}
	half := int64(a.Race.IntermissionHeat(ctx, champID))
	if a.Race.shouldPauseAfter(ctx, champID, half) {
		t.Error("the championship paused for an intermission")
	}
}

// The finishing order of a bracket is how far each car got. Nobody who went
// out in the same round raced each other, so they share the place.
func TestABracketIsRankedByHowFarEachCarGot(t *testing.T) {
	a, champID, seedOf := bracketReady(t)
	ctx := context.Background()
	if err := a.Race.Start(ctx, champID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Race.Stop)

	for i := 0; i < 40; i++ {
		h := a.Race.State(ctx).Heat
		times := map[int]float64{}
		for entry, lane := range lanesOf(*h) {
			times[lane] = 2.4 + float64(seedOf[entry])*0.01
		}
		if err := a.Race.EnterTimes(ctx, h.ID, times, "test"); err != nil {
			t.Fatal(err)
		}
		if _, err := a.DB.Champion(ctx, champID); err == nil {
			break
		}
		if err := a.Race.ArmNext(ctx); err != nil {
			t.Fatal(err)
		}
	}

	standings, err := a.DB.Standings(ctx, champID)
	if err != nil {
		t.Fatal(err)
	}
	// 24 cars in a 32 bracket: the champion, the runner-up, two semi-final
	// losers, four quarter-final, eight in round two and the eight who lost in
	// round one.
	count := map[int]int{}
	for _, st := range standings {
		count[st.Place]++
	}
	want := map[int]int{1: 1, 2: 1, 3: 2, 5: 4, 9: 8, 17: 8}
	for place, n := range want {
		if count[place] != n {
			t.Errorf("%d cars placed %d, want %d", count[place], place, n)
		}
	}
	if len(standings) != 24 || standings[0].Place != 1 || seedOf[standings[0].Entry.ID] != 1 {
		t.Errorf("the standings do not start with the top seed as champion")
	}
	// Seeds 1 and 2 were fastest every time, so they met in the final.
	if seedOf[standings[1].Entry.ID] != 2 {
		t.Errorf("seed %d is runner-up, want 2", seedOf[standings[1].Entry.ID])
	}

	// A shared 3rd is not a tie to run off, and the championship decides one
	// trophy only: the season's.
	if ties, _ := a.DB.UnsettledTies(ctx, champID); len(ties) != 0 {
		t.Errorf("the semi-final losers were offered as a tie to run off")
	}
	awards, err := a.DB.SpeedAwards(ctx, champID)
	if err != nil {
		t.Fatal(err)
	}
	if len(awards) != 1 || awards[0].Entry.ID != standings[0].Entry.ID {
		t.Errorf("%d trophies for the championship, want the champion's alone", len(awards))
	}
}

// -demo-championship is how anybody rehearses championship night, so it has to
// arrive at a field that seeds completely and builds.
func TestTheDemoChampionshipIsReadyToBuild(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	champ, err := a.SeedDemoChampionship(ctx, 2027)
	if err != nil {
		t.Fatalf("SeedDemoChampionship: %v", err)
	}
	if !champ.Bracket() {
		t.Error("the demo championship is not a bracket")
	}
	proposals, err := a.Bracket.Seed(ctx, champ.SeasonID, champ.ID)
	if err != nil {
		t.Fatal(err)
	}
	cars := map[int64]int{}
	for _, p := range proposals {
		if !p.Matched() {
			t.Errorf("seed %d (%s) has no car: %s", p.Seed, p.Driver, p.Why)
		}
		cars[p.EntryID]++
	}
	// A car that has qualified does not race again, so no one car can hold two
	// places in the bracket.
	for id, n := range cars {
		if n > 1 {
			t.Errorf("entry %d is seeded %d times", id, n)
		}
	}
	slots, _ := a.DB.Qualifiers(ctx, champ.SeasonID)
	for _, sl := range slots {
		if sl.Entries > 3 {
			t.Errorf("%s holds %d places, over the cap", sl.Driver, sl.Entries)
		}
	}
	st, err := a.Bracket.Generate(ctx, champ.ID, proposals, "test")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if st.Entrants != 24 {
		t.Errorf("%d cars in the demo bracket, want 24", st.Entrants)
	}
}

// The championship decides the season trophy and nothing else, so a tie for 2nd
// there stands as a tie rather than being run off.
func TestAChampionshipOnlyRunsOffATieForFirst(t *testing.T) {
	a, seasonID := fullSeason(t)
	ctx := context.Background()
	champID := championshipWithField(t, a, seasonID, nil)
	if err := a.DB.SetRaceFormat(ctx, champID, model.FormatStandard); err != nil {
		t.Fatal(err)
	}
	tiedRace(t, a, champID, 2, 2)

	ties, err := a.DB.UnsettledTies(ctx, champID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ties) != 0 {
		t.Errorf("a tie for 2nd at the championship was offered as a run-off: %+v", ties)
	}
	awards, err := a.DB.SpeedAwards(ctx, champID)
	if err != nil {
		t.Fatalf("SpeedAwards: %v", err)
	}
	if len(awards) != 1 {
		t.Errorf("%d trophies at the championship, want 1", len(awards))
	}
}
