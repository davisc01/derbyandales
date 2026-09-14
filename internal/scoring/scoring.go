// Package scoring turns raw heat times into a race result.
//
// The club's rule: each car runs four times, the slowest run is dropped, the
// rest are averaged, and the fastest average wins.
package scoring

import (
	"math"
	"sort"
)

// DNF is the time a timer reports for a lane that never finished. FastTrack
// sends 0.000 for an empty or unfinished lane; the driver rewrites it to this
// so it sorts last and is the natural candidate to be dropped.
const DNF = 9.999

// tieEpsilon is how close two averages must be to count as equal.
//
// It is not a display-rounding threshold. Timers report to the millisecond, so
// an average of three runs is a multiple of 1/3000 s: two genuinely different
// averages differ by at least 3.3e-4, which is six orders of magnitude above
// this. The epsilon exists purely to absorb floating-point noise, so that two
// cars with identical times compare equal regardless of the order the arithmetic
// happened in.
//
// This matters in practice. DerbyNet computes the same average in SQL as
// (SUM - MAX) / (COUNT - 1), which can land one bit away from summing the kept
// times. In the club's 2026 race 4, cars 46 and 91 ran identical times to the
// millisecond and were published at places 4 and 5 — separated by about 4e-16
// of arithmetic noise, nothing more. They are a tie, and this reports them as one.
const tieEpsilon = 1e-9

// Run is one car's trip down one lane.
type Run struct {
	Heat int
	Lane int
	Time float64
	// Ignored marks a run the coordinator has struck out — a false start, or a
	// car that came apart through no fault of the timer.
	Ignored bool
}

// Result is one car's standing in a race.
type Result struct {
	EntryID int64

	// Counted are the times that went into the average, fastest first.
	Counted []float64
	// Dropped is the slowest run, excluded from the average. Nil when the car
	// has only one usable run, since dropping it would leave nothing.
	Dropped *float64

	Heats   int // usable runs recorded
	Average float64
	Best    float64
	Worst   float64

	// Place is 1-based. Tied cars share a place and the next distinct place
	// skips, so two cars tied for 6th are followed by 8th.
	Place int
	// Tied reports whether this car shares its place with another.
	Tied bool
}

// ScoreEntry applies the drop-slowest rule to one car's runs.
func ScoreEntry(entryID int64, runs []Run) Result {
	r := Result{EntryID: entryID}

	times := make([]float64, 0, len(runs))
	for _, run := range runs {
		if run.Ignored {
			continue
		}
		times = append(times, run.Time)
	}
	if len(times) == 0 {
		return r
	}
	sort.Float64s(times)

	r.Heats = len(times)
	r.Best = times[0]
	r.Worst = times[len(times)-1]

	// Drop the slowest, unless that would leave nothing to average.
	counted := times
	if len(times) > 1 {
		dropped := times[len(times)-1]
		r.Dropped = &dropped
		counted = times[:len(times)-1]
	}

	r.Counted = append([]float64(nil), counted...)
	sum := 0.0
	for _, t := range counted {
		sum += t
	}
	r.Average = sum / float64(len(counted))
	return r
}

// Standings scores every car and orders them fastest first, assigning places
// with ties.
//
// Cars with no usable runs sort last: they have no average to compare, and
// dropping them silently would make them disappear from the results.
func Standings(runsByEntry map[int64][]Run) []Result {
	results := make([]Result, 0, len(runsByEntry))
	for entryID, runs := range runsByEntry {
		results = append(results, ScoreEntry(entryID, runs))
	}

	sort.Slice(results, func(i, j int) bool {
		a, b := results[i], results[j]
		// No usable runs sorts last.
		if (a.Heats == 0) != (b.Heats == 0) {
			return b.Heats == 0
		}
		if a.Heats > 0 && !equalTimes(a.Average, b.Average) {
			return a.Average < b.Average
		}
		// Break a genuine tie on the best single run, then on entry id so the
		// order is at least stable rather than arbitrary.
		if a.Heats > 0 && b.Heats > 0 && a.Best != b.Best {
			return a.Best < b.Best
		}
		return a.EntryID < b.EntryID
	})

	assignPlaces(results)
	return results
}

// assignPlaces walks the sorted results and numbers them, sharing a place
// between cars whose averages are equal at the published precision.
func assignPlaces(results []Result) {
	place := 1
	for i := range results {
		if results[i].Heats == 0 {
			// Unscored cars get no place rather than a misleading last.
			results[i].Place = 0
			continue
		}
		if i > 0 && results[i-1].Heats > 0 &&
			equalTimes(results[i].Average, results[i-1].Average) {
			results[i].Place = results[i-1].Place
			results[i].Tied = true
			results[i-1].Tied = true
		} else {
			results[i].Place = place
		}
		place++
	}
}

// equalTimes reports whether two averages are the same to within measurement
// resolution.
func equalTimes(a, b float64) bool {
	return math.Abs(a-b) < tieEpsilon
}

// PlaceInHeat ranks the lanes of a single finished heat, sharing a place on an
// exact tie. Timers report to the millisecond, so an exact tie is rare but real,
// and inventing an order for one would be a lie.
func PlaceInHeat(times map[int]float64) map[int]int {
	type laneTime struct {
		lane int
		time float64
	}
	entries := make([]laneTime, 0, len(times))
	for lane, t := range times {
		entries = append(entries, laneTime{lane, t})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].time != entries[j].time {
			return entries[i].time < entries[j].time
		}
		return entries[i].lane < entries[j].lane
	})

	places := make(map[int]int, len(entries))
	place := 1
	for i, e := range entries {
		if i > 0 && e.time == entries[i-1].time {
			places[e.lane] = places[entries[i-1].lane]
		} else {
			places[e.lane] = place
		}
		place++
	}
	return places
}

// PodiumPlaces is how far down the standings a tie has to be settled.
//
// The top three take trophies, and a trophy cannot be shared — so a tie that
// reaches them is run off. Below that a tie simply stands: two cars that ran
// the same average are the same speed, and saying so is more honest than
// inventing a difference nobody raced for.
const PodiumPlaces = 3

// Tie is a group of cars that finished level with each other.
type Tie struct {
	// Place is the position they share.
	Place int
	// EntryIDs are the cars, in the order the standings happened to sort them,
	// which is not meaningful — that is the whole problem.
	EntryIDs []int64
}

// Decides reports whether this tie has to be settled: it does when the cars in
// it are competing for a trophy.
//
// A group starting at place 3 still decides third, even though its lower half
// finishes fourth. A group starting at fourth decides nothing.
func (t Tie) Decides() bool { return t.Place > 0 && t.Place <= PodiumPlaces }

// Places lists the positions this tie covers, which is what makes it clear why
// a two-way tie for third is worth running off.
func (t Tie) Places() []int {
	out := make([]int, 0, len(t.EntryIDs))
	for i := range t.EntryIDs {
		out = append(out, t.Place+i)
	}
	return out
}

// Ties groups the cars that share a place.
//
// Cars with no usable run are left out: they have no place, so they are not
// tied with anybody, they are simply absent from the result.
func Ties(results []Result) []Tie {
	var out []Tie
	for i := 0; i < len(results); {
		if results[i].Place == 0 || !results[i].Tied {
			i++
			continue
		}
		tie := Tie{Place: results[i].Place}
		for ; i < len(results) && results[i].Place == tie.Place; i++ {
			tie.EntryIDs = append(tie.EntryIDs, results[i].EntryID)
		}
		out = append(out, tie)
	}
	return out
}

// UnsettledTies returns only the ties that have to be run off.
func UnsettledTies(results []Result) []Tie {
	var out []Tie
	for _, t := range Ties(results) {
		if t.Decides() {
			out = append(out, t)
		}
	}
	return out
}
