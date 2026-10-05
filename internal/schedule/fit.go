package schedule

import (
	"fmt"
	"math/rand"
)

// A car that turns up after racing has started still has to run once in every
// lane, and nobody else may gain or lose a run to make room for it — four runs,
// one per lane, is the rule every average is built on. So the heats not yet run
// are taken apart into what each car still owes ("car c, lane l") and dealt back
// out into new heats with the late cars' runs added.
//
// That is edge-colouring a bipartite multigraph of cars against lanes: each
// colour is a heat, and a colour class can hold each car and each lane at most
// once, which is exactly what a heat is. König's theorem says the colours needed
// are the highest degree — the busiest lane or the busiest car — and no more.
// With P full heats left and k late cars every lane is owed P + k runs, so the
// remainder becomes P + k heats with every lane filled. Only when P + k is
// under four, near the very end of the night, does the late car's own four runs
// set the count, and the lanes nobody owes are left empty.

// fitTrials is how many shuffled dealing orders are tried. Each gives a valid
// set of heats; they differ in who meets whom, and the one with the fewest
// repeat meetings is kept.
const fitTrials = 64

// spreadWeight pulls a car's runs apart within the restructured block. Spacing
// alone only objects to one or two heats of rest, so without this a lone late
// car can have all four runs bunched into the next dozen heats and then sit out
// the rest of the night — the coordinator was promised "spread through what's
// left".
const spreadWeight = 10

// Fit restructures the heats not yet run so that late cars are blended in.
//
// run is every heat that is fixed — raced, on the track, or already being
// loaded — and remaining is the heats that may be rebuilt. Existing cars are
// numbered 0..cars-1 and the late ones cars..cars+late-1. The returned heats
// replace remaining, in running order.
func Fit(run, remaining []Heat, cars, late, lanes int) ([]Heat, error) {
	if lanes < 1 {
		return nil, fmt.Errorf("schedule: need at least 1 lane, got %d", lanes)
	}
	if late < 1 {
		return nil, fmt.Errorf("schedule: no late cars to fit")
	}
	total := cars + late

	// What each car still owes, lane by lane.
	type owe struct{ car, lane int }
	var owed []owe
	for _, heat := range remaining {
		for lane, car := range heat {
			if car == Bye {
				continue
			}
			if car < 0 || car >= cars {
				return nil, fmt.Errorf("schedule: car index %d out of range", car)
			}
			owed = append(owed, owe{car, lane})
		}
	}
	for car := cars; car < total; car++ {
		for lane := 0; lane < lanes; lane++ {
			owed = append(owed, owe{car, lane})
		}
	}

	carDemand := make([]int, total)
	laneDemand := make([]int, lanes)
	for _, o := range owed {
		carDemand[o.car]++
		laneDemand[o.lane]++
	}
	heats := 0
	for _, d := range carDemand {
		heats = max(heats, d)
	}
	for _, d := range laneDemand {
		heats = max(heats, d)
	}

	// Seeded from the shape of the problem so the same night gives the same
	// heats — a coordinator who fits a car in, looks, and asks again should not
	// see the schedule shuffle under them.
	rng := rand.New(rand.NewSource(int64(len(run)*10_000 + len(remaining)*100 + total)))
	var best []Heat
	bestRepeats := -1
	for t := 0; t < fitTrials; t++ {
		if t > 0 {
			rng.Shuffle(len(owed), func(i, j int) { owed[i], owed[j] = owed[j], owed[i] })
		}
		c := newColouring(total, lanes, heats)
		for _, o := range owed {
			c.add(o.car, o.lane)
		}
		candidate := c.heats()
		if r := repeatsWith(run, candidate, total); bestRepeats < 0 || r < bestRepeats {
			best, bestRepeats = candidate, r
		}
	}

	best = orderAfter(run, best, total)

	// The rule checked over the whole night, not just the new part: a mistake
	// here would put a car down a lane twice or not at all, and that is found
	// out by a racer being called to the track they already ran.
	all := append(append([]Heat{}, run...), best...)
	if err := Validate(&Schedule{Lanes: lanes, Cars: total, Heats: all}); err != nil {
		return nil, err
	}
	return best, nil
}

// colouring deals runs into heats. carAt[car][h] is the lane that car runs in
// heat h, laneAt[lane][h] the car in that lane; -1 is free.
type colouring struct {
	carAt, laneAt [][]int
}

func newColouring(cars, lanes, heats int) *colouring {
	grid := func(n int) [][]int {
		g := make([][]int, n)
		for i := range g {
			g[i] = make([]int, heats)
			for h := range g[i] {
				g[i][h] = -1
			}
		}
		return g
	}
	return &colouring{carAt: grid(cars), laneAt: grid(lanes)}
}

func freeIn(row []int) int {
	for h, v := range row {
		if v < 0 {
			return h
		}
	}
	return -1
}

// add places one run. Take a heat a where the car is free and a heat b where
// the lane is free. If the lane is free in a too, done. Otherwise swap a and b
// along the chain of runs that starts at that lane — lane in a, that car in b,
// that lane in a, ... — which frees the lane in a. In a bipartite graph the
// chain cannot come back round to this car, so the car is still free in a.
func (c *colouring) add(car, lane int) {
	a := freeIn(c.carAt[car])
	b := freeIn(c.laneAt[lane])
	if c.laneAt[lane][a] >= 0 {
		c.flip(lane, a, b)
	}
	c.carAt[car][a] = lane
	c.laneAt[lane][a] = car
}

func (c *colouring) flip(lane, a, b int) {
	type slot struct{ car, lane, heat int }
	var chain []slot
	onLane, v, h := true, lane, a
	for {
		if onLane {
			car := c.laneAt[v][h]
			if car < 0 {
				break
			}
			chain = append(chain, slot{car, v, h})
			v = car
		} else {
			l := c.carAt[v][h]
			if l < 0 {
				break
			}
			chain = append(chain, slot{v, l, h})
			v = l
		}
		onLane = !onLane
		if h == a {
			h = b
		} else {
			h = a
		}
	}
	for _, s := range chain {
		c.carAt[s.car][s.heat] = -1
		c.laneAt[s.lane][s.heat] = -1
	}
	for _, s := range chain {
		h := a
		if s.heat == a {
			h = b
		}
		c.carAt[s.car][h] = s.lane
		c.laneAt[s.lane][h] = s.car
	}
}

func (c *colouring) heats() []Heat {
	lanes := len(c.laneAt)
	n := len(c.laneAt[0])
	out := make([]Heat, n)
	for h := range out {
		heat := make(Heat, lanes)
		for lane := range heat {
			heat[lane] = Bye
			if car := c.laneAt[lane][h]; car >= 0 {
				heat[lane] = car
			}
		}
		out[h] = heat
	}
	return out
}

// repeatsWith counts repeat meetings across the whole night, so a late car is
// not put against the same rival four times when the rest of the field is
// there to choose from.
func repeatsWith(run, candidate []Heat, cars int) int {
	all := append(append([]Heat{}, run...), candidate...)
	return RepeatCount(&Schedule{Cars: cars, Heats: all})
}

// orderAfter puts the rebuilt heats in running order, knowing what has already
// run: a car that raced in the last heat must not open the new block.
func orderAfter(run, heats []Heat, cars int) []Heat {
	cost := func(h []Heat) int { return spacingCostAfter(run, h, cars) + spreadCost(run, h, cars) }

	// Greedy first: each time take whichever heat costs least next.
	pool := append([]Heat{}, heats...)
	out := make([]Heat, 0, len(heats))
	for len(pool) > 0 {
		bestIdx, bestCost := 0, -1
		for i, h := range pool {
			if c := cost(append(out[:len(out):len(out)], h)); bestCost < 0 || c < bestCost {
				bestIdx, bestCost = i, c
			}
		}
		out = append(out, pool[bestIdx])
		pool = append(pool[:bestIdx], pool[bestIdx+1:]...)
	}

	// Then pairwise swaps, as repair does for a fresh schedule.
	best := cost(out)
	const maxPasses = 20
	for pass := 0; pass < maxPasses && best > 0; pass++ {
		improved := false
		for i := 0; i < len(out); i++ {
			for j := i + 1; j < len(out); j++ {
				out[i], out[j] = out[j], out[i]
				if c := cost(out); c < best {
					best, improved = c, true
				} else {
					out[i], out[j] = out[j], out[i]
				}
			}
		}
		if !improved {
			break
		}
	}
	return out
}

// spreadCost objects to a car's runs in the block sitting closer together than
// an even spread would put them.
func spreadCost(run, heats []Heat, cars int) int {
	count := make([]int, cars)
	for _, heat := range heats {
		for _, car := range heat {
			if car != Bye {
				count[car]++
			}
		}
	}
	last := lastSeen(run, cars)
	cost := 0
	for h, heat := range heats {
		pos := len(run) + h
		for _, car := range heat {
			if car == Bye {
				continue
			}
			target := len(heats) / count[car]
			if gap := pos - last[car]; gap < target {
				cost += spreadWeight * (target - gap)
			}
			last[car] = pos
		}
	}
	return cost
}

// lastSeen is the position of each car's last run in run, or far in the past.
func lastSeen(run []Heat, cars int) []int {
	last := make([]int, cars)
	for i := range last {
		last[i] = -1 << 20
	}
	for h, heat := range run {
		for _, car := range heat {
			if car != Bye && car < cars {
				last[car] = h
			}
		}
	}
	return last
}
