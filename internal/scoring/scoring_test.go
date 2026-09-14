package scoring

import (
	"math"
	"testing"
)

func runs(times ...float64) []Run {
	out := make([]Run, len(times))
	for i, t := range times {
		out[i] = Run{Heat: i + 1, Lane: i%4 + 1, Time: t}
	}
	return out
}

func nearly(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// The club's rule: four runs, drop the slowest, average the rest.
func TestDropSlowestOfFour(t *testing.T) {
	r := ScoreEntry(1, runs(2.40, 2.50, 2.45, 2.90))

	if r.Heats != 4 {
		t.Errorf("Heats = %d, want 4", r.Heats)
	}
	if r.Dropped == nil || !nearly(*r.Dropped, 2.90) {
		t.Errorf("Dropped = %v, want 2.90", r.Dropped)
	}
	want := (2.40 + 2.45 + 2.50) / 3
	if !nearly(r.Average, want) {
		t.Errorf("Average = %v, want %v", r.Average, want)
	}
	if !nearly(r.Best, 2.40) || !nearly(r.Worst, 2.90) {
		t.Errorf("Best/Worst = %v/%v, want 2.40/2.90", r.Best, r.Worst)
	}
	if len(r.Counted) != 3 {
		t.Errorf("Counted = %v, want 3 times", r.Counted)
	}
}

// A car that missed heats still has to be scored on what it ran.
func TestFewerThanFourRuns(t *testing.T) {
	r := ScoreEntry(1, runs(2.40, 2.60, 2.50))
	if r.Heats != 3 {
		t.Errorf("Heats = %d, want 3", r.Heats)
	}
	if r.Dropped == nil || !nearly(*r.Dropped, 2.60) {
		t.Errorf("Dropped = %v, want 2.60", r.Dropped)
	}
	if want := (2.40 + 2.50) / 2; !nearly(r.Average, want) {
		t.Errorf("Average = %v, want %v", r.Average, want)
	}
}

// Dropping the only run would leave nothing to average, so a single run counts.
func TestSingleRunIsNotDropped(t *testing.T) {
	r := ScoreEntry(1, runs(2.44))
	if r.Dropped != nil {
		t.Errorf("Dropped = %v, want nil", *r.Dropped)
	}
	if !nearly(r.Average, 2.44) {
		t.Errorf("Average = %v, want 2.44", r.Average)
	}
	if r.Heats != 1 {
		t.Errorf("Heats = %d, want 1", r.Heats)
	}
}

func TestNoRunsIsScoredButUnplaced(t *testing.T) {
	r := ScoreEntry(1, nil)
	if r.Heats != 0 || r.Average != 0 {
		t.Errorf("got %+v, want an empty result", r)
	}
}

// A DNF is the slowest possible run, so the drop-slowest rule absorbs exactly
// one of them for free — which is the point.
func TestDNFIsTheDroppedRun(t *testing.T) {
	r := ScoreEntry(1, runs(2.40, 2.45, 2.50, DNF))
	if r.Dropped == nil || !nearly(*r.Dropped, DNF) {
		t.Errorf("Dropped = %v, want the DNF", r.Dropped)
	}
	want := (2.40 + 2.45 + 2.50) / 3
	if !nearly(r.Average, want) {
		t.Errorf("Average = %v, want %v — the DNF must not drag the average", r.Average, want)
	}
}

// Two DNFs cannot both be dropped; the second one has to count, or a car that
// barely ran would beat cars that completed every heat.
func TestSecondDNFStillCounts(t *testing.T) {
	r := ScoreEntry(1, runs(2.40, 2.45, DNF, DNF))
	if r.Average < 3.0 {
		t.Errorf("Average = %v; a car with two DNFs should be heavily penalised", r.Average)
	}
}

// A run struck out by the coordinator — a false start, a car that came apart —
// is excluded before the drop-slowest rule is applied.
func TestIgnoredRunsAreExcluded(t *testing.T) {
	rs := runs(2.40, 2.45, 2.50, 9.999)
	rs[3].Ignored = true

	r := ScoreEntry(1, rs)
	if r.Heats != 3 {
		t.Errorf("Heats = %d, want 3 (the ignored run should not count)", r.Heats)
	}
	if r.Dropped == nil || !nearly(*r.Dropped, 2.50) {
		t.Errorf("Dropped = %v, want 2.50", r.Dropped)
	}
	if want := (2.40 + 2.45) / 2; !nearly(r.Average, want) {
		t.Errorf("Average = %v, want %v", r.Average, want)
	}
}

func TestStandingsOrderFastestFirst(t *testing.T) {
	results := Standings(map[int64][]Run{
		10: runs(2.50, 2.52, 2.51, 2.90), // avg 2.51
		20: runs(2.40, 2.42, 2.41, 2.80), // avg 2.41  <- winner
		30: runs(2.60, 2.62, 2.61, 3.00), // avg 2.61
	})

	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	wantOrder := []int64{20, 10, 30}
	for i, want := range wantOrder {
		if results[i].EntryID != want {
			t.Errorf("place %d is entry %d, want %d", i+1, results[i].EntryID, want)
		}
		if results[i].Place != i+1 {
			t.Errorf("entry %d has place %d, want %d", results[i].EntryID, results[i].Place, i+1)
		}
	}
}

// Tied cars share a place and the next distinct place skips, so two cars tied
// for 6th are followed by 8th. MDnATracker already parses "T6" from DerbyNet,
// so ties are a real thing that happens.
func TestTiesSharePlaceAndSkipTheNext(t *testing.T) {
	results := Standings(map[int64][]Run{
		1: runs(2.40, 2.40, 2.40, 2.90),
		2: runs(2.40, 2.40, 2.40, 2.95), // identical average, different dropped run
		3: runs(2.60, 2.60, 2.60, 3.00),
	})

	if results[0].Place != 1 || results[1].Place != 1 {
		t.Errorf("tied cars got places %d and %d, want both 1",
			results[0].Place, results[1].Place)
	}
	if !results[0].Tied || !results[1].Tied {
		t.Error("tied cars should be marked Tied")
	}
	if results[2].Place != 3 {
		t.Errorf("place after a two-way tie for 1st = %d, want 3", results[2].Place)
	}
	if results[2].Tied {
		t.Error("the third car is not tied")
	}
}

// Ties must survive the arithmetic being done in a different order. Two cars
// that ran the same times are tied however the average was computed.
func TestTiesAreRobustToArithmeticOrder(t *testing.T) {
	// The same three times summed in two different orders.
	forwards := (2.409 + 2.418 + 2.423) / 3
	backwards := (2.423 + 2.418 + 2.409) / 3
	if !equalTimes(forwards, backwards) {
		t.Error("summing the same times in a different order broke the tie")
	}

	// DerbyNet computes this as (SUM - MAX) / (COUNT - 1), which can land a bit
	// away from summing the kept times. That must still read as a tie.
	kept := (2.358 + 2.44 + 2.452) / 3
	sqlStyle := (2.358 + 2.44 + 2.452 + 2.46 - 2.46) / 3
	if !equalTimes(kept, sqlStyle) {
		t.Errorf("%.17g and %.17g should compare equal", kept, sqlStyle)
	}
}

// The epsilon must not be so loose that genuinely different cars tie. Timers
// report to the millisecond, so the smallest real difference between two
// three-run averages is 1/3000 s — far above the epsilon.
func TestSmallestRealDifferenceIsNotATie(t *testing.T) {
	a := ScoreEntry(1, runs(2.400, 2.400, 2.400, 9.0))
	b := ScoreEntry(2, runs(2.400, 2.400, 2.401, 9.0))
	if equalTimes(a.Average, b.Average) {
		t.Errorf("averages %.17g and %.17g differ by one millisecond across three "+
			"runs and must not be treated as a tie", a.Average, b.Average)
	}
}

// A car with no usable runs must not silently vanish from the results, but it
// also has no time to be ranked by.
func TestUnscoredCarsSortLastWithNoPlace(t *testing.T) {
	results := Standings(map[int64][]Run{
		1: runs(2.40, 2.41, 2.42, 2.90),
		2: nil,
		3: runs(2.50, 2.51, 2.52, 3.00),
	})

	if len(results) != 3 {
		t.Fatalf("got %d results, want 3 — nobody should disappear", len(results))
	}
	last := results[len(results)-1]
	if last.EntryID != 2 {
		t.Errorf("last result is entry %d, want the unscored entry 2", last.EntryID)
	}
	if last.Place != 0 {
		t.Errorf("unscored entry has place %d, want 0 (unplaced)", last.Place)
	}
	if results[0].Place != 1 || results[1].Place != 2 {
		t.Error("scored entries should still be placed 1 and 2")
	}
}

func TestStandingsAreDeterministic(t *testing.T) {
	in := map[int64][]Run{
		1: runs(2.40, 2.40, 2.40, 2.40),
		2: runs(2.40, 2.40, 2.40, 2.40),
		3: runs(2.40, 2.40, 2.40, 2.40),
	}
	first := Standings(in)
	for i := 0; i < 20; i++ {
		got := Standings(in)
		for j := range got {
			if got[j].EntryID != first[j].EntryID {
				t.Fatalf("identical cars ordered differently between runs: %d vs %d",
					got[j].EntryID, first[j].EntryID)
			}
		}
	}
}

func TestPlaceInHeat(t *testing.T) {
	places := PlaceInHeat(map[int]float64{
		1: 2.760,
		2: 2.540,
		3: 2.434,
		4: 2.475,
	})
	// These are the real lane times from 2026 race 4, heat 1.
	want := map[int]int{3: 1, 4: 2, 2: 3, 1: 4}
	for lane, wantPlace := range want {
		if places[lane] != wantPlace {
			t.Errorf("lane %d placed %d, want %d", lane, places[lane], wantPlace)
		}
	}
}

// Timers report to the millisecond, so an exact tie is rare but real. Inventing
// an order for one would be a lie.
func TestPlaceInHeatSharesExactTies(t *testing.T) {
	places := PlaceInHeat(map[int]float64{
		1: 2.500,
		2: 2.500,
		3: 2.600,
	})
	if places[1] != 1 || places[2] != 1 {
		t.Errorf("tied lanes placed %d and %d, want both 1", places[1], places[2])
	}
	if places[3] != 3 {
		t.Errorf("lane after a two-way tie placed %d, want 3", places[3])
	}
}
