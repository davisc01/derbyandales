// Package schedule builds the qualifying heat schedule.
//
// The club's rule is that every car runs the track four times, once in each of
// the four lanes. That is exactly what a cyclic (rotational) generator gives
// you: pick a set of lane offsets, and heat h seats car (h + offset) in each
// lane. Over n heats every car visits every lane exactly once.
//
// The freedom left over is *which* offsets, and that determines who races whom.
// Offsets whose pairwise differences are all distinct produce a schedule where
// no two cars ever meet twice — a perfect schedule. We search for one.
//
// DerbyNet solves the same problem by shipping ~1.4 MB of precomputed generator
// tables. Searching at runtime is a few milliseconds and removes the table.
package schedule

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
)

// Bye marks a lane with no car in it. The lane runs empty and the timer masks it.
const Bye = -1

// Heat is one trip down the track: a car index per lane, or Bye.
type Heat []int

// Schedule is a complete qualifying schedule.
type Schedule struct {
	Lanes int
	Cars  int
	Heats []Heat

	// Offsets are the lane offsets chosen by the search. Lane j seats car
	// (h + Offsets[j]) mod n in heat h of the rough schedule.
	Offsets []int
	// Generator is the same thing in step form, which is how DerbyNet's tables
	// express it: the differences between consecutive offsets.
	Generator []int

	// RepeatedMeetings counts how many times a pair of cars meets beyond the
	// first. Zero means no car ever races the same opponent twice.
	RepeatedMeetings int
	// Exhaustive reports whether every candidate was examined, or whether the
	// search space was too large and it sampled instead.
	Exhaustive bool
}

// Perfect reports whether no two cars meet more than once.
func (s *Schedule) Perfect() bool { return s.RepeatedMeetings == 0 }

// RunsPerCar is how many times each car goes down the track: once per lane.
func (s *Schedule) RunsPerCar() int { return s.Lanes }

// exhaustiveLimit caps how many offset combinations are enumerated before the
// search switches to random sampling. Four lanes never comes close: even 200
// cars is only C(199,3) ≈ 1.3M.
const exhaustiveLimit = 2_000_000

// sampleTrials is how many random candidates are examined when the space is too
// large to enumerate — which only happens at high lane counts.
const sampleTrials = 200_000

// Generate builds a schedule for the given number of cars and lanes.
//
// Fewer cars than lanes is padded with byes, since a heat still has to fill the
// track. Everything else needs no padding: n cars produce exactly n heats.
func Generate(cars, lanes int) (*Schedule, error) {
	if lanes < 1 {
		return nil, fmt.Errorf("schedule: need at least 1 lane, got %d", lanes)
	}
	if cars < 2 {
		return nil, fmt.Errorf("schedule: need at least 2 cars, got %d", cars)
	}

	// n is the cycle length. Padding only happens when there are fewer cars
	// than lanes; the padded slots become byes.
	n := cars
	if n < lanes {
		n = lanes
	}

	offsets, meetings, exhaustive := searchOffsets(n, lanes)

	rough := make([]Heat, n)
	for h := 0; h < n; h++ {
		heat := make(Heat, lanes)
		for j := 0; j < lanes; j++ {
			car := (h + offsets[j]) % n
			if car >= cars {
				car = Bye
			}
			heat[j] = car
		}
		rough[h] = heat
	}

	s := &Schedule{
		Lanes:            lanes,
		Cars:             cars,
		Heats:            reorder(rough, lanes, cars),
		Offsets:          offsets,
		Generator:        steps(offsets, n),
		RepeatedMeetings: meetings,
		Exhaustive:       exhaustive,
	}
	return s, nil
}

// steps converts absolute offsets into the step form DerbyNet's tables use.
func steps(offsets []int, n int) []int {
	if len(offsets) < 2 {
		return nil
	}
	out := make([]int, len(offsets)-1)
	for i := 1; i < len(offsets); i++ {
		out[i-1] = ((offsets[i]-offsets[i-1])%n + n) % n
	}
	return out
}

// searchOffsets finds the lane offsets that produce the fewest repeated
// meetings between cars.
//
// It returns the lexicographically smallest best set, so the same inputs always
// produce the same schedule — which keeps the schedule reproducible and the
// tests stable.
func searchOffsets(n, lanes int) (offsets []int, meetings int, exhaustive bool) {
	// One lane is degenerate: every car runs alone.
	if lanes == 1 {
		return []int{0}, 0, true
	}
	// With as many lanes as cars there is only one possible set, and every heat
	// is the same field. Nothing to search.
	if lanes >= n {
		all := make([]int, lanes)
		for i := range all {
			all[i] = i % n
		}
		repeats, _ := scoreOffsets(all, n)
		return all, repeats, true
	}

	best := make([]int, lanes)
	bestScore := math.MaxInt
	bestFree := -1

	// Two objectives, in order. Repeated meetings is the club's stated rule —
	// "mix racers as well as it can". Free steps is the tie-break: among offset
	// sets that mix equally well, prefer one that also lets the heats be ordered
	// so nobody races back to back.
	consider := func(candidate []int) {
		score, free := scoreOffsets(candidate, n)
		if score < bestScore || (score == bestScore && free > bestFree) {
			bestScore, bestFree = score, free
			copy(best, candidate)
		}
	}

	// Lane 0 is always offset 0: shifting every offset by a constant produces
	// the same schedule with the heats renumbered, so fixing it loses nothing
	// and divides the search space by n.
	if total := combinations(n-1, lanes-1); total >= 0 && total <= exhaustiveLimit {
		candidate := make([]int, lanes)
		forEachCombination(n-1, lanes-1, func(rest []int) bool {
			candidate[0] = 0
			copy(candidate[1:], rest)
			consider(candidate)
			return true
		})
		return best, bestScore, true
	}

	// Too many combinations to enumerate. Sample instead, deterministically so
	// the schedule is still reproducible.
	rng := rand.New(rand.NewSource(int64(n*1000 + lanes)))
	pool := make([]int, n-1)
	for i := range pool {
		pool[i] = i + 1
	}
	candidate := make([]int, lanes)
	for t := 0; t < sampleTrials; t++ {
		rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
		candidate[0] = 0
		copy(candidate[1:], pool[:lanes-1])
		sort.Ints(candidate)
		consider(candidate)
	}
	return best, bestScore, false
}

// scoreOffsets evaluates a candidate offset set against both objectives.
//
// repeats counts how many times a pair of cars meets beyond the first, across
// the whole cycle of n heats. It works from the differences rather than by
// building the schedule: two lanes separated by offset difference d put every
// car alongside the car d positions away, once per cycle. So if two different
// lane-pairs share a difference (up to sign, mod n), every one of those meetings
// happens twice.
//
// freeSteps counts the step sizes that would visit every heat exactly once
// (coprime to n) while never putting a car in consecutive heats (not one of the
// offset differences). Any free step means a back-to-back-free running order
// exists; zero means one is impossible with this offset set.
func scoreOffsets(offsets []int, n int) (repeats, freeSteps int) {
	classes := make(map[int]int, len(offsets)*(len(offsets)-1)/2)
	occupied := make(map[int]bool, len(offsets)*(len(offsets)-1))

	for i := 0; i < len(offsets); i++ {
		for j := i + 1; j < len(offsets); j++ {
			d := ((offsets[j]-offsets[i])%n + n) % n
			if d == 0 {
				// Two lanes sharing an offset would seat the same car twice in
				// one heat. Reject outright.
				return math.MaxInt, 0
			}
			occupied[d] = true
			occupied[n-d] = true
			// A difference of d and one of n-d describe the same pairing.
			if n-d < d {
				d = n - d
			}
			classes[d]++
		}
	}

	for d, multiplicity := range classes {
		// Distinct car pairs at this distance. When d is exactly half the
		// cycle, {x, x+d} and {x+d, x} are the same pair, so there are half as
		// many — meaning even a single lane-pair at that distance double-covers.
		distinct := n
		if d*2 == n {
			distinct = n / 2
		}
		repeats += n*multiplicity - distinct
	}

	for k := 1; k < n; k++ {
		if !occupied[k] && gcd(k, n) == 1 {
			freeSteps++
		}
	}
	return repeats, freeSteps
}

// combinations returns C(n, k), or -1 if it would overflow the limit we care
// about.
func combinations(n, k int) int {
	if k < 0 || k > n {
		return 0
	}
	if k > n-k {
		k = n - k
	}
	result := 1
	for i := 0; i < k; i++ {
		result = result * (n - i) / (i + 1)
		if result > exhaustiveLimit {
			return -1
		}
	}
	return result
}

// forEachCombination calls fn with each sorted k-subset of {1..n}. It stops
// early when fn returns false.
func forEachCombination(n, k int, fn func([]int) bool) {
	if k == 0 {
		fn(nil)
		return
	}
	idx := make([]int, k)
	for i := range idx {
		idx[i] = i + 1
	}
	for {
		if !fn(idx) {
			return
		}
		// Advance to the next combination in lexicographic order.
		i := k - 1
		for i >= 0 && idx[i] == n-(k-1-i) {
			i--
		}
		if i < 0 {
			return
		}
		idx[i]++
		for j := i + 1; j < k; j++ {
			idx[j] = idx[j-1] + 1
		}
	}
}
