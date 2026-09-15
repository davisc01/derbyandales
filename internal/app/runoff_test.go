package app

import (
	"context"
	"strings"
	"testing"

	"github.com/davisc01/derbyandales/internal/scoring"
	"github.com/davisc01/derbyandales/internal/store"
)

// A tie for a trophy is settled on the track. A tie anywhere else stands.

// tiedRace runs a whole race in which the cars at the given places all record
// identical averages. Places are 1-based and refer to the finishing order the
// times produce.
func tiedRace(t *testing.T, a *App, raceID int64, tieAt int, howMany int) []int64 {
	t.Helper()
	ctx := context.Background()

	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	entries, _ := a.DB.Entries(ctx, raceID)

	// Give every eligible car a distinct pace, then make howMany of them share
	// the pace that lands at tieAt.
	var eligible []store.EntryView
	for _, e := range entries {
		if e.EarnsPoints() {
			eligible = append(eligible, e)
		}
	}
	if len(eligible) < tieAt+howMany {
		t.Fatalf("only %d eligible cars; cannot tie %d at place %d", len(eligible), howMany, tieAt)
	}

	pace := map[int64]float64{}
	for i, e := range eligible {
		pace[e.ID] = 2.000 + float64(i)*0.100
	}
	var tied []int64
	shared := 2.000 + float64(tieAt-1)*0.100
	for i := tieAt - 1; i < tieAt-1+howMany; i++ {
		pace[eligible[i].ID] = shared
		tied = append(tied, eligible[i].ID)
	}
	// The pace car and any excluded car go at the back, out of the way.
	for _, e := range entries {
		if _, ok := pace[e.ID]; !ok {
			pace[e.ID] = 9.0
		}
	}

	heats, _ := a.DB.Heats(ctx, raceID)
	for _, h := range heats {
		times := map[int]float64{}
		for _, l := range h.Lanes {
			if l.EntryID != nil {
				times[l.Lane] = pace[*l.EntryID]
			}
		}
		if err := a.DB.RecordHeatResults(ctx, h.ID, times); err != nil {
			t.Fatal(err)
		}
	}
	return tied
}

// A tie below the top three stands. Two cars that ran the same average are the
// same speed, and there is no trophy to argue over.
func TestATieBelowThePodiumIsLeftAlone(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	tied := tiedRace(t, a, raceID, 5, 2)

	ties, err := a.DB.UnsettledTies(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ties) != 0 {
		t.Fatalf("a tie for 5th was flagged for a run-off: %+v", ties)
	}

	// And it is still published as a tie: shared place, next place skipped.
	standings, _ := a.DB.Standings(ctx, raceID)
	places := map[int64]int{}
	for _, st := range standings {
		places[st.Entry.ID] = st.Place
	}
	if places[tied[0]] != 5 || places[tied[1]] != 5 {
		t.Errorf("tied cars placed %d and %d, want both 5th", places[tied[0]], places[tied[1]])
	}
	for _, st := range standings {
		if st.Place == 6 {
			t.Error("6th was awarded after a two-way tie for 5th; it should skip to 7th")
		}
	}

	// The trophies can be set without anybody running anything extra.
	if _, err := a.DB.SpeedAwards(ctx, raceID); err != nil {
		t.Errorf("a tie for 5th blocked the trophies: %v", err)
	}
}

// A tie for a trophy is not the software's to settle.
func TestATieForATrophyBlocksTheAwardsUntilItIsRunOff(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	tiedRace(t, a, raceID, 1, 2)

	ties, err := a.DB.UnsettledTies(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ties) != 1 {
		t.Fatalf("%d ties need settling, want 1", len(ties))
	}
	if ties[0].Place != 1 || len(ties[0].Entries) != 2 {
		t.Errorf("tie is at place %d with %d cars", ties[0].Place, len(ties[0].Entries))
	}
	if ties[0].Settled {
		t.Error("an unrun tie reports itself settled")
	}

	_, err = a.DB.SpeedAwards(ctx, raceID)
	if err == nil {
		t.Fatal("the trophies were set while two cars were tied for first")
	}
	// The message has to name the cars: somebody is about to announce this.
	for _, want := range []string{"#", "tied for 1st", "run that off"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

// A two-way tie for third still decides third, even though the loser takes
// fourth either way.
func TestATieForThirdIsRunOffEvenThoughOneOfThemFinishesFourth(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	tiedRace(t, a, raceID, 3, 2)

	ties, _ := a.DB.UnsettledTies(ctx, raceID)
	if len(ties) != 1 {
		t.Fatalf("%d ties need settling, want 1", len(ties))
	}
	if got := ties[0].Describe(); got != "tied for 3rd and 4th" {
		t.Errorf("described as %q", got)
	}
}

// The whole point: run the run-off, and the tie resolves into two places.
func TestARunOffSettlesTheOrderWithoutChangingTheAverages(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	tied := tiedRace(t, a, raceID, 1, 2)

	before, _ := a.DB.Standings(ctx, raceID)
	averages := map[int64]float64{}
	heatsRun := map[int64]int{}
	for _, st := range before {
		averages[st.Entry.ID] = st.Average
		heatsRun[st.Entry.ID] = st.Heats
	}

	heat, err := a.DB.CreateRunOff(ctx, raceID, 1)
	if err != nil {
		t.Fatalf("CreateRunOff: %v", err)
	}
	if !heat.RunOff() {
		t.Error("the run-off heat is not marked as one")
	}

	// The second car home wins it.
	winner, loser := tied[1], tied[0]
	times := map[int]float64{}
	for _, l := range heat.Lanes {
		if l.EntryID == nil {
			continue
		}
		if *l.EntryID == winner {
			times[l.Lane] = 2.001
		} else {
			times[l.Lane] = 2.050
		}
	}
	if err := a.DB.RecordHeatResults(ctx, heat.ID, times); err != nil {
		t.Fatal(err)
	}

	after, _ := a.DB.Standings(ctx, raceID)
	places := map[int64]int{}
	tiedFlag := map[int64]bool{}
	for _, st := range after {
		places[st.Entry.ID] = st.Place
		tiedFlag[st.Entry.ID] = st.Tied
	}
	if places[winner] != 1 || places[loser] != 2 {
		t.Errorf("after the run-off the winner is %d and the loser %d, want 1 and 2",
			places[winner], places[loser])
	}
	if tiedFlag[winner] || tiedFlag[loser] {
		t.Error("the cars are still marked as tied after the run-off")
	}

	// The run-off must not have touched anybody's average or run count. Four
	// runs, one per lane, drop the slowest — a fifth run for two cars would
	// rewrite the very averages that tied.
	for _, st := range after {
		if st.Average != averages[st.Entry.ID] {
			t.Errorf("car %d's average moved from %.4f to %.4f",
				st.Entry.CarNumber, averages[st.Entry.ID], st.Average)
		}
		if st.Heats != heatsRun[st.Entry.ID] {
			t.Errorf("car %d now has %d runs, had %d",
				st.Entry.CarNumber, st.Heats, heatsRun[st.Entry.ID])
		}
	}

	// And the trophies can now be set, to the right cars.
	awards, err := a.DB.SpeedAwards(ctx, raceID)
	if err != nil {
		t.Fatalf("the trophies are still blocked after the run-off: %v", err)
	}
	if awards[0].Entry.ID != winner {
		t.Errorf("1st went to car %d, not the run-off winner", awards[0].Entry.CarNumber)
	}
	if awards[1].Entry.ID != loser {
		t.Errorf("2nd went to car %d, not the run-off loser", awards[1].Entry.CarNumber)
	}
}

// A run-off that itself finishes level has settled nothing, and the software
// must not pick — that is the thing it exists to avoid.
func TestARunOffThatDeadHeatsHasToBeRunAgain(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	tiedRace(t, a, raceID, 1, 2)
	heat, err := a.DB.CreateRunOff(ctx, raceID, 1)
	if err != nil {
		t.Fatal(err)
	}

	times := map[int]float64{}
	for _, l := range heat.Lanes {
		if l.EntryID != nil {
			times[l.Lane] = 2.222
		}
	}
	if err := a.DB.RecordHeatResults(ctx, heat.ID, times); err != nil {
		t.Fatal(err)
	}

	dead, err := a.DB.DeadHeatRunOffs(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(dead) != 1 || dead[0] != 1 {
		t.Fatalf("a level run-off was not reported: %v", dead)
	}

	// Still tied, and the trophies are still blocked.
	standings, _ := a.DB.Standings(ctx, raceID)
	if standings[0].Place != 1 || !standings[0].Tied {
		t.Error("a level run-off separated the cars anyway")
	}
	if _, err := a.DB.SpeedAwards(ctx, raceID); err == nil {
		t.Error("the trophies were set off a level run-off")
	}
}

// Three cars tied for first is one run-off, not three.
func TestAThreeWayTieIsOneRunOff(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	tied := tiedRace(t, a, raceID, 1, 3)

	ties, _ := a.DB.UnsettledTies(ctx, raceID)
	if len(ties) != 1 {
		t.Fatalf("%d run-offs needed, want 1", len(ties))
	}
	if len(ties[0].Entries) != 3 {
		t.Fatalf("%d cars in the run-off, want 3", len(ties[0].Entries))
	}
	if got := ties[0].Describe(); got != "tied for 1st through 3rd" {
		t.Errorf("described as %q", got)
	}

	heat, err := a.DB.CreateRunOff(ctx, raceID, 1)
	if err != nil {
		t.Fatal(err)
	}
	placed := 0
	for _, l := range heat.Lanes {
		if l.EntryID != nil {
			placed++
		}
	}
	if placed != 3 {
		t.Errorf("%d cars on the track for the run-off, want 3", placed)
	}

	// Reverse their finishing order and check all three places follow.
	times := map[int]float64{}
	for _, l := range heat.Lanes {
		if l.EntryID == nil {
			continue
		}
		switch *l.EntryID {
		case tied[0]:
			times[l.Lane] = 2.300
		case tied[1]:
			times[l.Lane] = 2.200
		case tied[2]:
			times[l.Lane] = 2.100
		}
	}
	a.DB.RecordHeatResults(ctx, heat.ID, times)

	standings, _ := a.DB.Standings(ctx, raceID)
	places := map[int64]int{}
	for _, st := range standings {
		places[st.Entry.ID] = st.Place
	}
	if places[tied[2]] != 1 || places[tied[1]] != 2 || places[tied[0]] != 3 {
		t.Errorf("places came out %d/%d/%d, want the run-off order 1/2/3",
			places[tied[2]], places[tied[1]], places[tied[0]])
	}
}

// The run-off's times must not reach the season either.
func TestARunOffDoesNotChangeSeasonPoints(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	tied := tiedRace(t, a, raceID, 1, 2)
	heat, _ := a.DB.CreateRunOff(ctx, raceID, 1)
	times := map[int]float64{}
	for _, l := range heat.Lanes {
		if l.EntryID == nil {
			continue
		}
		if *l.EntryID == tied[1] {
			times[l.Lane] = 2.001
		} else {
			times[l.Lane] = 2.050
		}
	}
	a.DB.RecordHeatResults(ctx, heat.ID, times)

	// Four runs each, still, once the season has the race frozen.
	runs, err := a.DB.RunsByEntry(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range tied {
		if len(runs[id]) != 4 {
			t.Errorf("car %d has %d scoring runs after a run-off, want 4", id, len(runs[id]))
		}
	}
}

// The anomaly check looks at a car's runs across the night. A run-off is a
// different kind of thing and must not be evidence about a heat.
func TestARunOffIsNotEvidenceForTheAnomalyCheck(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	tied := tiedRace(t, a, raceID, 1, 2)
	heat, _ := a.DB.CreateRunOff(ctx, raceID, 1)
	times := map[int]float64{}
	for _, l := range heat.Lanes {
		if l.EntryID == nil {
			continue
		}
		if *l.EntryID == tied[1] {
			times[l.Lane] = 2.001
		} else {
			times[l.Lane] = 2.050
		}
	}
	a.DB.RecordHeatResults(ctx, heat.ID, times)

	found, err := a.DB.HeatAnomalies(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		if f.Heat == heat.Number {
			t.Errorf("the run-off heat %d was flagged as an anomaly", f.Heat)
		}
	}
}

var _ = scoring.PodiumPlaces

// The race is recorded into the season when its last heat lands, and the run-off
// comes after that. The season has to end up with the settled order, or a tie
// for 3rd would stay a tie in the championship places.
func TestTheSeasonRecordsTheOrderARunOffSettled(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	tied := tiedRace(t, a, raceID, 3, 2)
	if err := a.Season.FreezeRace(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	if err := a.Race.ArmRunOff(ctx, raceID, 3); err != nil {
		t.Fatalf("ArmRunOff: %v", err)
	}
	heat := a.Race.State(ctx).Heat
	times := map[int]float64{}
	for _, l := range heat.Lanes {
		if l.EntryID == nil {
			continue
		}
		if *l.EntryID == tied[1] {
			times[l.Lane] = 2.001
		} else {
			times[l.Lane] = 2.050
		}
	}
	if err := a.Race.EnterTimes(ctx, heat.ID, times, "test"); err != nil {
		t.Fatal(err)
	}

	race, _ := a.DB.Race(ctx, raceID)
	finishes, _ := a.DB.SeasonFinishes(ctx, race.SeasonID)
	place := map[int64]int{}
	for _, f := range finishes {
		place[f.EntryID] = f.Place
	}
	if place[tied[1]] != 3 || place[tied[0]] != 4 {
		t.Errorf("the season has the run-off winner %d and loser %d, want 3 and 4",
			place[tied[1]], place[tied[0]])
	}
}

// A place that passes down can land on two cars that finished level. Only one
// of them can have it, so that tie is run off like a tie for a trophy — even
// though it is for 4th.
func TestATieForAPassedDownPlaceIsRunOff(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()
	race, _ := a.DB.Race(ctx, raceID)
	sn, _ := a.DB.Season(ctx, race.SeasonID)

	// One place per racer, so a racer's second car cannot take one.
	sn.MaxChampionshipEntry = 1
	if err := a.DB.UpdateSeason(ctx, sn); err != nil {
		t.Fatal(err)
	}

	tied := tiedRace(t, a, raceID, 4, 2)

	// Give the 2nd-placed car to the winner's driver: it finishes 2nd but that
	// racer already has their one place, so 3rd and 4th qualify — and 4th is
	// shared.
	standings, _ := a.DB.Standings(ctx, raceID)
	var winner, second store.Standing
	for _, st := range standings {
		switch st.Place {
		case 1:
			winner = st
		case 2:
			second = st
		}
	}
	if _, err := a.DB.ExecContext(ctx, `UPDATE entry SET racer_id = ? WHERE id = ?`,
		winner.Entry.RacerID, second.Entry.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.Season.FreezeRace(ctx, raceID); err != nil {
		t.Fatal(err)
	}

	ties, err := a.DB.UnsettledTies(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	var found *store.TieView
	for i := range ties {
		if ties[i].Place == 4 {
			found = &ties[i]
		}
	}
	if found == nil {
		t.Fatalf("the tie for the passed-down 4th place is not offered as a run-off: %+v", ties)
	}
	if !found.ForPlace {
		t.Error("the tie is not marked as being for a championship place")
	}
	if len(found.Entries) != len(tied) {
		t.Errorf("%d cars in the tie, want %d", len(found.Entries), len(tied))
	}
	// And it does not hold up the trophies, which it has nothing to do with.
	if _, err := a.DB.SpeedAwards(ctx, raceID); err != nil {
		t.Errorf("a tie for 4th blocked the trophies: %v", err)
	}
}
