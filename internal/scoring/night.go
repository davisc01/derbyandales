package scoring

import (
	"math"
	"sort"
)

// The night in review: what the track did, for the wrap-up at the end.
//
// Two kinds of number live here and they take different cars. The lanes are a
// question about the track, so every car that went down it is evidence — the
// pace car and an excluded car included, since a slow lane is slow for them
// too. The highlights name a car as having done something, so they take only
// the cars that count: a car ruled out of the results does not get "fastest
// heat of the night" either.

// NightRun is one run, for the review.
type NightRun struct {
	Heat    int
	Lane    int
	Time    float64
	EntryID int64
	// Counts is false for the pace car and for an excluded car.
	Counts bool
}

// NightStats is the review of one race night.
type NightStats struct {
	Heats int
	Runs  int
	DNFs  int

	// Lanes are ordered fastest average first.
	Lanes []LaneStat

	// FastestHeats are the heats with the fastest counted run in them, one
	// entry per heat, fastest first.
	FastestHeats []HeatBest

	// Closest is the heat won by the smallest margin between two counted cars
	// that both finished. Nil when no heat had two.
	Closest *Margin

	// Steadiest is the counted car whose runs were closest together. It only
	// considers cars that finished every run: a non-finish is not a time.
	Steadiest *Spread
}

// LaneStat is one lane across the night.
type LaneStat struct {
	Lane int
	Runs int
	// Average is over the runs that finished. A non-finish is 9.999 by
	// convention, and one of those would move a lane's average by a quarter of
	// a second — that is what the DNF count is for.
	Average float64
	// Wins are the heats this lane's car won.
	Wins int
	DNFs int

	// Expected is how many heats the lane would have won if every lane were
	// the same: a share of each heat it ran in, split between the cars that
	// finished it. A lane that ran in fewer heats expects fewer wins.
	Expected float64
	// OneIn is the odds against a lane winning this far from Expected by luck
	// alone, in either direction. Reported as odds rather than a verdict, like
	// the heat check: seven wins from five expected is about 1 in 2, which is
	// nothing, and a person should see that it is nothing.
	OneIn float64
	// Unusual marks a lane far enough out that it is worth a look even after
	// allowing for there being several lanes to be unlucky in.
	Unusual bool
}

// HeatBest is a heat's fastest counted run.
type HeatBest struct {
	Heat    int
	Lane    int
	EntryID int64
	Time    float64
}

// Margin is how close a heat's top two counted cars were.
type Margin struct {
	Heat              int
	Winner, RunnerUp  int64
	WinTime, NextTime float64
	Gap               float64
}

// Spread is one car's slowest run less its fastest.
type Spread struct {
	EntryID     int64
	Best, Worst float64
	Gap         float64
}

// FastestHeatCount is how many heats the review lists.
const FastestHeatCount = 3

// dnf reports a run that did not finish, which the timer's 0.000 has already
// been rewritten to 9.999 for.
func dnf(t float64) bool { return t >= 9.9 || t <= 0 }

// Night reviews one race night's runs. Run-off heats and struck-out lanes are
// the caller's to leave out: neither is part of the night as scheduled.
func Night(runs []NightRun) NightStats {
	var s NightStats
	byHeat := map[int][]NightRun{}
	lanes := map[int]*LaneStat{}
	sums := map[int]float64{}
	finishedIn := map[int]int{}
	byCar := map[int64][]float64{}
	carDNF := map[int64]bool{}
	chances := map[int][]float64{} // per lane, its chance of winning each heat it finished

	for _, r := range runs {
		s.Runs++
		byHeat[r.Heat] = append(byHeat[r.Heat], r)
		l := lanes[r.Lane]
		if l == nil {
			l = &LaneStat{Lane: r.Lane}
			lanes[r.Lane] = l
		}
		l.Runs++
		if dnf(r.Time) {
			s.DNFs++
			l.DNFs++
			if r.Counts {
				carDNF[r.EntryID] = true
			}
			continue
		}
		sums[r.Lane] += r.Time
		finishedIn[r.Lane]++
		if r.Counts {
			byCar[r.EntryID] = append(byCar[r.EntryID], r.Time)
		}
	}
	s.Heats = len(byHeat)

	for _, heat := range byHeat {
		// A heat's winner is its fastest finisher. A dead heat is a win for
		// each lane in it: neither lane was slower.
		best := 0.0
		for _, r := range heat {
			if !dnf(r.Time) && (best == 0 || r.Time < best) {
				best = r.Time
			}
		}
		if best == 0 {
			continue
		}
		finishers := 0
		for _, r := range heat {
			if !dnf(r.Time) {
				finishers++
			}
			if !dnf(r.Time) && equalTimes(r.Time, best) {
				lanes[r.Lane].Wins++
			}
		}
		for _, r := range heat {
			if !dnf(r.Time) {
				chances[r.Lane] = append(chances[r.Lane], 1/float64(finishers))
			}
		}

		var counted []NightRun
		for _, r := range heat {
			if r.Counts && !dnf(r.Time) {
				counted = append(counted, r)
			}
		}
		sort.Slice(counted, func(i, j int) bool { return counted[i].Time < counted[j].Time })
		if len(counted) == 0 {
			continue
		}
		top := counted[0]
		s.FastestHeats = append(s.FastestHeats, HeatBest{Heat: top.Heat, Lane: top.Lane, EntryID: top.EntryID, Time: top.Time})
		if len(counted) >= 2 {
			m := Margin{Heat: top.Heat, Winner: top.EntryID, RunnerUp: counted[1].EntryID,
				WinTime: top.Time, NextTime: counted[1].Time, Gap: counted[1].Time - top.Time}
			if s.Closest == nil || m.Gap < s.Closest.Gap-tieEpsilon ||
				(equalTimes(m.Gap, s.Closest.Gap) && m.Heat < s.Closest.Heat) {
				s.Closest = &m
			}
		}
	}

	for lane, l := range lanes {
		if n := finishedIn[lane]; n > 0 {
			l.Average = sums[lane] / float64(n)
		}
		for _, p := range chances[lane] {
			l.Expected += p
		}
		if p := winOdds(chances[lane], l.Wins); p > 0 {
			l.OneIn = 1 / p
			// Four lanes are four chances for one to look odd, so the bar is
			// raised by the number of lanes: 1 in 20 each would flag a lane on
			// about one clean night in five.
			l.Unusual = p*float64(len(lanes)) < 0.05
		}
		s.Lanes = append(s.Lanes, *l)
	}
	sort.Slice(s.Lanes, func(i, j int) bool {
		a, b := s.Lanes[i], s.Lanes[j]
		// A lane where nothing finished has no average and goes last.
		if (a.Average == 0) != (b.Average == 0) {
			return b.Average == 0
		}
		if !equalTimes(a.Average, b.Average) {
			return a.Average < b.Average
		}
		return a.Lane < b.Lane
	})

	sort.Slice(s.FastestHeats, func(i, j int) bool {
		a, b := s.FastestHeats[i], s.FastestHeats[j]
		if !equalTimes(a.Time, b.Time) {
			return a.Time < b.Time
		}
		return a.Heat < b.Heat
	})
	if len(s.FastestHeats) > FastestHeatCount {
		s.FastestHeats = s.FastestHeats[:FastestHeatCount]
	}

	// The steadiest car needs a full night to be judged on: two runs close
	// together say little, so a car must have run as many times as the most
	// any car did.
	most := 0
	for _, t := range byCar {
		if len(t) > most {
			most = len(t)
		}
	}
	for id, t := range byCar {
		if carDNF[id] || len(t) < most || len(t) < 3 {
			continue
		}
		sort.Float64s(t)
		sp := Spread{EntryID: id, Best: t[0], Worst: t[len(t)-1], Gap: t[len(t)-1] - t[0]}
		if s.Steadiest == nil || sp.Gap < s.Steadiest.Gap-tieEpsilon ||
			(equalTimes(sp.Gap, s.Steadiest.Gap) && sp.Best < s.Steadiest.Best) {
			s.Steadiest = &sp
		}
	}
	return s
}

// winOdds is the chance of a lane doing at least this far from its expected
// wins, by luck. Each heat is its own coin with its own odds — a three-car heat
// is one in three, a four-car heat one in four — so the count of wins follows
// the distribution of a sum of unequal coins, worked out exactly rather than
// approximated: a night is only twenty-odd heats.
//
// Two-sided: a lane winning far too few is as much a finding as one winning
// far too many.
func winOdds(chances []float64, wins int) float64 {
	if len(chances) == 0 {
		return 0
	}
	// dist[k] is the chance of exactly k wins.
	dist := []float64{1}
	expected := 0.0
	for _, p := range chances {
		expected += p
		next := make([]float64, len(dist)+1)
		for k, q := range dist {
			next[k] += q * (1 - p)
			next[k+1] += q * p
		}
		dist = next
	}
	tail := 0.0
	if float64(wins) >= expected {
		for k := wins; k < len(dist); k++ {
			tail += dist[k]
		}
	} else {
		for k := 0; k <= wins && k < len(dist); k++ {
			tail += dist[k]
		}
	}
	return math.Min(1, 2*tail)
}
