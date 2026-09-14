package season

import "testing"

// The points rule decides who reaches the championship without winning a race,
// so these are the edge cases that would quietly change the field.

var testRules = Rules{AutoQualPlaces: 3, MaxEntries: 3}

// field builds a straightforward race: one car each, placed 1..n.
func field(n int) []Finish {
	out := make([]Finish, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, Finish{
			EntryID: int64(i), RacerID: int64(i), RaceID: 1, RaceNumber: 1,
			Place: i, Average: 2.3 + float64(i)/100,
		})
	}
	return out
}

func pointsByEntry(awards []PointAward) map[int64]int {
	out := map[int64]int{}
	for _, a := range awards {
		out[a.EntryID] = a.Points
	}
	return out
}

// Fourth place scores the size of the field, and every place below it one
// fewer. This is the club's published rule stated back to it.
func TestFourthPlaceScoresTheWholeField(t *testing.T) {
	got := pointsByEntry(RacePoints(field(20), testRules))

	for place, want := range map[int64]int{1: 0, 2: 0, 3: 0, 4: 20, 5: 19, 6: 18, 20: 4} {
		if got[place] != want {
			t.Errorf("place %d scored %d, want %d", place, got[place], want)
		}
	}
}

// In a field larger than the points ladder the last few finishers score nothing
// rather than going negative and taking points off people.
func TestPointsStopAtZeroRatherThanGoingNegative(t *testing.T) {
	// 5 cars, so only place 4 and 5 score at all: 5 and 4 points.
	got := pointsByEntry(RacePoints(field(30), Rules{AutoQualPlaces: 3, MaxEntries: 3}))
	for entry, points := range got {
		if points < 0 {
			t.Errorf("place %d scored %d", entry, points)
		}
	}
	// Place 34 would be the first negative one in a 30-car field; place 30
	// should be at 4 and nothing should be below zero.
	if got[30] != 4 {
		t.Errorf("last place scored %d, want 4", got[30])
	}
}

// Bringing three cars is allowed and common. It must not be three chances at
// the wildcard.
func TestOnlyARacersBestCarScores(t *testing.T) {
	finishes := []Finish{
		{EntryID: 1, RacerID: 10, Place: 4},
		{EntryID: 2, RacerID: 10, Place: 6}, // same racer, slower car
		{EntryID: 3, RacerID: 11, Place: 5},
	}
	got := pointsByEntry(RacePoints(finishes, testRules))

	if got[1] == 0 {
		t.Error("the racer's best car scored nothing")
	}
	if got[2] != 0 {
		t.Errorf("the racer's second car scored %d, want 0", got[2])
	}
	if got[3] == 0 {
		t.Error("the other racer scored nothing")
	}
}

// A racer whose best car auto-qualified takes no wildcard points at all, not
// even for their other cars. They already have their championship slot; the
// wildcard list exists to give one to somebody who does not.
func TestAnAutoQualifiedRacerScoresNothingForTheirOtherCars(t *testing.T) {
	// A ten-car field, so the lower places are still worth something.
	finishes := field(10)
	finishes[1].RacerID = finishes[6].RacerID // 2nd and 7th are the same racer
	got := pointsByEntry(RacePoints(finishes, testRules))

	if got[2] != 0 || got[7] != 0 {
		t.Errorf("an auto-qualified racer scored %d and %d, want 0 and 0", got[2], got[7])
	}
	if got[4] == 0 {
		t.Error("the racer who did not qualify scored nothing")
	}
}

// Tied cars share a place, so they share a points value and the place after the
// tie is worth correspondingly less. That falls out of using the raw place, and
// it is the club's rule rather than an accident of the arithmetic.
func TestTiedCarsShareAPointsValue(t *testing.T) {
	finishes := []Finish{
		{EntryID: 1, RacerID: 1, Place: 1},
		{EntryID: 2, RacerID: 2, Place: 2},
		{EntryID: 3, RacerID: 3, Place: 3},
		{EntryID: 4, RacerID: 4, Place: 4},
		{EntryID: 5, RacerID: 5, Place: 4}, // tied for 4th
		{EntryID: 6, RacerID: 6, Place: 6}, // so 5th does not exist
	}
	got := pointsByEntry(RacePoints(finishes, testRules))

	if got[4] != got[5] {
		t.Errorf("cars tied for 4th scored %d and %d", got[4], got[5])
	}
	if got[6] != got[4]-2 {
		t.Errorf("the place after a two-way tie scored %d, want %d", got[6], got[4]-2)
	}
}

// The pace car is club equipment. It scores nothing and, by default, is not
// counted in the field either — counting it inflated every racer's points by
// one for years.
func TestThePaceCarNeitherScoresNorCounts(t *testing.T) {
	finishes := append(field(9), Finish{
		EntryID: 99, RacerID: 99, Place: 10, IsControl: true, CarName: "CONTROL",
	})

	got := pointsByEntry(RacePoints(finishes, testRules))
	if got[99] != 0 {
		t.Errorf("the pace car scored %d", got[99])
	}
	if got[4] != 9 {
		t.Errorf("fourth place scored %d in a 9-car field, want 9", got[4])
	}

	counted := testRules
	counted.CountControl = true
	if old := pointsByEntry(RacePoints(finishes, counted))[4]; old != 10 {
		t.Errorf("with the old behaviour fourth scored %d, want 10", old)
	}
}

// A car that never recorded a usable run has no place, and inventing one for it
// would push everyone else down the points ladder.
func TestAnUnplacedCarScoresNothing(t *testing.T) {
	finishes := append(field(5), Finish{EntryID: 50, RacerID: 50, Place: 0})
	got := pointsByEntry(RacePoints(finishes, testRules))

	if got[50] != 0 {
		t.Errorf("an unplaced car scored %d", got[50])
	}
	if got[4] != 6 {
		t.Errorf("fourth scored %d, want 6 — the unplaced car still turned up", got[4])
	}
}
