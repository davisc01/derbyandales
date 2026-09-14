package scoring

import (
	"math"
	"sort"
	"strconv"
)

// Spotting a heat that went wrong.
//
// Cars are slow or fast for their own reasons — a wheel, a bad polish, a bit of
// luck at the gate. But if *every* car in one heat posted its slowest run of the
// night, the cars are not the explanation. Something about that heat was: a
// sticky gate, a knock to the track, someone leaning on the rail. The same in
// reverse — every car's fastest — points at a start that released early or a
// timer that started late, which makes the times too good rather than too bad.
//
// Either way the heat is a fault, not a result, and the fix is to run it again.
//
// This is a flag, not a verdict. It says a coincidence is unlikely, and how
// unlikely, and leaves the decision to the person who was standing there.

// AnomalyKind says which direction a heat went wrong in.
type AnomalyKind string

const (
	// AllSlowest: every car in the heat ran its worst time of the night here.
	AllSlowest AnomalyKind = "slowest"
	// AllFastest: every car ran its best. Suspect the start, not the cars.
	AllFastest AnomalyKind = "fastest"
)

// MinAnomalyCars is how many cars a heat needs before the coincidence means
// anything.
//
// Two cars both running their worst in the same heat is a one-in-sixteen event,
// which happens somewhere in a twenty-five heat race more often than not. Three
// is one in sixty-four, which is worth a look. Below that the flag would cry
// wolf, and a check nobody believes is worse than no check.
const MinAnomalyCars = 3

// HeatAnomaly is one heat worth running again.
type HeatAnomaly struct {
	Heat int
	Kind AnomalyKind

	// Cars is how many cars the judgement is based on.
	Cars int

	// OneIn is the odds against this happening by chance in a single heat:
	// 256 for four cars with four runs each. It is reported rather than reduced
	// to a yes or no, because "1 in 256" and "1 in 64" deserve different
	// reactions and only a person can decide which they are looking at.
	OneIn float64

	// Margin is the smallest gap, across the cars, between the run in this heat
	// and that car's next-nearest run. It is the number that separates a fault
	// from a coincidence: a sticky gate costs everyone tenths of a second, while
	// four cars happening to peak in the same heat are a few thousandths apart.
	Margin float64

	// Clear marks a heat where every car's run sits further from its other runs
	// than those other runs sit from each other — so each car is an outlier
	// against its own form, not merely last in a close set.
	//
	// This is the flag to act on. Measured against simulated races it survives
	// about one clean race in four hundred, where the bare all-slowest rule
	// throws a coincidence about one race in five. It is also self-calibrating:
	// consistent and scattered fields produce the same false-alarm rate, which
	// a fixed threshold in seconds does not.
	//
	// The trade is sensitivity. A fault of a tenth of a second or more is caught
	// nearly always; one of a few hundredths usually is not, which is why the
	// weaker heats stay in the list with their margins shown rather than being
	// silently dropped.
	Clear bool

	// EntryIDs are the cars involved, so the heat can be described.
	EntryIDs []int64
}

// HeatAnomalies finds heats where every car recorded its slowest — or every car
// its fastest — run of the race.
//
// Pass every car that ran, including the pace car and any excluded entries.
// They are as good a witness to a bad heat as anyone else, and leaving them out
// only makes the evidence thinner.
//
// One property of the club's schedule makes this test trustworthy: every car
// runs each lane exactly once, so each heat holds one car from each lane. A
// permanently slow lane therefore spreads a car's worst runs evenly across the
// whole race instead of piling them into any one heat. Whatever this finds, it
// is not lane bias.
func HeatAnomalies(runsByEntry map[int64][]Run) []HeatAnomaly {
	type mark struct {
		entryID  int64
		slowest  bool
		fastest  bool
		runCount int

		// gap is how far this run sits from the car's next-nearest run; spread
		// is how far apart the car's other runs are. Comparing the two is what
		// separates "slowest, by a mile" from "slowest, by a whisker".
		gap    float64
		spread float64
		// judged is false when the car has too few other runs for spread to
		// mean anything.
		judged bool
	}
	byHeat := map[int][]mark{}

	for entryID, runs := range runsByEntry {
		usable := make([]Run, 0, len(runs))
		for _, r := range runs {
			if !r.Ignored {
				usable = append(usable, r)
			}
		}
		// A single run is its own best and worst, which would say nothing while
		// making every heat it appears in look remarkable.
		if len(usable) < 2 {
			continue
		}

		times := make([]float64, 0, len(usable))
		for _, r := range usable {
			times = append(times, r.Time)
		}
		sort.Float64s(times)
		best, worst := times[0], times[len(times)-1]

		// A car that ran the same time every trip has no best or worst to speak
		// of, and would otherwise count as both.
		if best == worst {
			continue
		}

		// With only two runs there are no "other runs" to measure a spread
		// against, so the margin can be reported but not judged.
		judged := len(times) >= 3

		for _, r := range usable {
			m := mark{
				entryID:  entryID,
				slowest:  r.Time == worst,
				fastest:  r.Time == best,
				runCount: len(usable),
				judged:   judged,
			}
			switch {
			case m.slowest:
				others := times[:len(times)-1]
				m.gap = r.Time - others[len(others)-1]
				m.spread = others[len(others)-1] - others[0]
			case m.fastest:
				others := times[1:]
				m.gap = others[0] - r.Time
				m.spread = others[len(others)-1] - others[0]
			}
			byHeat[r.Heat] = append(byHeat[r.Heat], m)
		}
	}

	var out []HeatAnomaly
	for heat, marks := range byHeat {
		if len(marks) < MinAnomalyCars {
			continue
		}
		allSlowest, allFastest := true, true
		odds := 1.0
		margin := math.Inf(1)
		clear := true
		ids := make([]int64, 0, len(marks))
		for _, m := range marks {
			allSlowest = allSlowest && m.slowest
			allFastest = allFastest && m.fastest
			odds *= float64(m.runCount)
			if m.gap < margin {
				margin = m.gap
			}
			// Every car has to be an outlier against its own form. One car
			// clearly off its pace is racing; all of them is a fault.
			clear = clear && m.judged && m.gap >= m.spread
			ids = append(ids, m.entryID)
		}
		if !allSlowest && !allFastest {
			continue
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

		kind := AllSlowest
		if allFastest {
			kind = AllFastest
		}
		out = append(out, HeatAnomaly{
			Heat: heat, Kind: kind, Cars: len(marks),
			OneIn: odds, Margin: margin, Clear: clear, EntryIDs: ids,
		})
	}

	// The ones worth acting on first: clear faults, widest margin, then the
	// least likely coincidence. A coordinator reading this list at the end of a
	// long night should find the heat to re-run at the top of it.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Clear != b.Clear {
			return a.Clear
		}
		if a.Margin != b.Margin {
			return a.Margin > b.Margin
		}
		if a.OneIn != b.OneIn {
			return a.OneIn > b.OneIn
		}
		return a.Heat < b.Heat
	})
	return out
}

// Description says what was seen, in the words someone would use out loud.
func (a HeatAnomaly) Description() string {
	what := "slowest"
	if a.Kind == AllFastest {
		what = "fastest"
	}
	return "all " + strconv.Itoa(a.Cars) + " cars ran their " + what +
		" time of the night in this heat"
}
