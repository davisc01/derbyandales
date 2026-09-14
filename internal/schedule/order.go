package schedule

import "math"

// The generator gives a schedule that is *correct* — every car once in every
// lane, opponents well mixed — but badly ordered. Straight out of the generator,
// car 0 races in heats 1, 2, 3 and 4 back to back and then never again.
//
// So correctness and ergonomics are separated: generate first, then reorder.
// This is DerbyNet's factoring and it is the right one.

const (
	// A car in the immediately preceding heat has no time to get back to the
	// start line, which is the thing racers actually complain about.
	consecutiveWeight = 1000
	// Compounding that by putting them in the same lane twice running.
	sameLaneWeight = 1000
	// One heat of rest still feels rushed, but it is not a real problem.
	nearWeight = 100
	// Keep every car's number of completed runs roughly in step, so nobody is
	// sitting on four runs while someone else has none.
	balanceWeight = 10
)

// reorder chooses the running order of the heats.
//
// Two strategies are tried and the better kept. The greedy pass is general but
// myopic. The step orderings exploit the fact that the rough schedule is
// cyclic: taking heats in steps of k visits them all when k is coprime to n,
// and consecutive heats then differ by exactly k — so if k is not one of the
// differences between two lane offsets, no car can appear in both. That gives
// zero back-to-back racing outright, where it is achievable at all.
func reorder(rough []Heat, lanes, cars int) []Heat {
	n := len(rough)

	best := greedyOrder(rough, lanes, cars)
	bestCost := spacingCost(best, cars)

	for k := 1; k < n; k++ {
		if gcd(k, n) != 1 {
			continue // would revisit a heat before covering them all
		}
		candidate := make([]Heat, n)
		for i := 0; i < n; i++ {
			candidate[i] = rough[(i*k)%n]
		}
		if cost := spacingCost(candidate, cars); cost < bestCost {
			best, bestCost = candidate, cost
		}
	}
	return repair(best, cars)
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// greedyOrder picks heats from the rough pool one at a time, each time taking
// whichever remaining heat is least objectionable next.
func greedyOrder(rough []Heat, lanes, cars int) []Heat {
	remaining := make([]Heat, len(rough))
	copy(remaining, rough)

	out := make([]Heat, 0, len(rough))
	runs := make([]int, cars)
	lastHeat := make([]int, cars)
	lastLane := make([]int, cars)
	for i := range lastHeat {
		lastHeat[i] = math.MinInt / 2 // never raced
		lastLane[i] = -1
	}

	for position := 0; len(remaining) > 0; position++ {
		bestIdx, bestScore := 0, math.Inf(1)
		for i, heat := range remaining {
			score := penalty(heat, position, runs, lastHeat, lastLane, lanes, cars)
			if score < bestScore {
				bestIdx, bestScore = i, score
			}
		}

		chosen := remaining[bestIdx]
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
		out = append(out, chosen)

		for lane, car := range chosen {
			if car == Bye {
				continue
			}
			runs[car]++
			lastHeat[car] = position
			lastLane[car] = lane
		}
	}
	return out
}

// repair hill-climbs over pairwise swaps of heat positions to squeeze out
// back-to-back runs the greedy pass left behind.
//
// The greedy is myopic: it commits to a heat without knowing what it will need
// later, and can paint itself into a corner near the end. Swapping positions
// cannot break correctness — every heat keeps its cars and lanes, only the
// running order changes — so this is free to run.
func repair(heats []Heat, cars int) []Heat {
	if len(heats) < 3 {
		return heats
	}
	best := spacingCost(heats, cars)

	// Bounded so a pathological field cannot spin here while a room waits.
	const maxPasses = 20
	for pass := 0; pass < maxPasses && best > 0; pass++ {
		improved := false
		for i := 0; i < len(heats); i++ {
			for j := i + 1; j < len(heats); j++ {
				heats[i], heats[j] = heats[j], heats[i]
				if cost := spacingCost(heats, cars); cost < best {
					best = cost
					improved = true
				} else {
					heats[i], heats[j] = heats[j], heats[i] // undo
				}
			}
		}
		if !improved {
			break
		}
	}
	return heats
}

// spacingCost measures how rushed the running order is: heavily for a car in
// back-to-back heats, lightly for one heat of rest.
func spacingCost(heats []Heat, cars int) int {
	last := make([]int, cars)
	lastLane := make([]int, cars)
	for i := range last {
		last[i] = -10
		lastLane[i] = -1
	}
	cost := 0
	for h, heat := range heats {
		for lane, car := range heat {
			if car == Bye {
				continue
			}
			switch gap := h - last[car]; {
			case gap == 1:
				cost += consecutiveWeight
				if lastLane[car] == lane {
					cost += sameLaneWeight
				}
			case gap == 2:
				cost += nearWeight
			}
			last[car] = h
			lastLane[car] = lane
		}
	}
	return cost
}

// BackToBack counts how many times a car races in the heat immediately after
// one it was already in.
//
// For small fields this cannot always reach zero: with a perfect offset set the
// pairwise differences are all distinct, and once there are few enough cars they
// cover every residue — meaning every pair of heats shares a car and some
// back-to-back racing is unavoidable. The UI surfaces this rather than pretending
// the schedule is worse than it could be.
func BackToBack(s *Schedule) int {
	last := make([]int, s.Cars)
	for i := range last {
		last[i] = -10
	}
	n := 0
	for h, heat := range s.Heats {
		for _, car := range heat {
			if car == Bye {
				continue
			}
			if h-last[car] == 1 {
				n++
			}
			last[car] = h
		}
	}
	return n
}

// penalty scores placing heat at the given position.
func penalty(heat Heat, position int, runs, lastHeat, lastLane []int, lanes, cars int) float64 {
	score := 0.0

	for lane, car := range heat {
		if car == Bye {
			continue
		}
		switch gap := position - lastHeat[car]; {
		case gap == 1:
			score += consecutiveWeight
			if lastLane[car] == lane {
				score += sameLaneWeight
			}
		case gap == 2:
			score += nearWeight
		}
	}

	// After this heat, each car should have completed about the same share of
	// its runs. Squared deviation punishes one car falling far behind more than
	// several cars drifting slightly.
	expected := float64(position+1) * float64(lanes) / float64(cars)
	for car := 0; car < cars; car++ {
		n := float64(runs[car])
		for _, c := range heat {
			if c == car {
				n++
				break
			}
		}
		dev := n - expected
		score += balanceWeight * dev * dev
	}
	return score
}
