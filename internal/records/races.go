package records

import "sort"

// The fastest race nights the club has had.
//
// A night is measured by the average of every counted run in it: the cars that
// count, the runs that finished. Non-finishes are left out, since a 9.999 is a
// car coming apart rather than the track being slow, and one of them would move
// a night by a hundredth. Run-offs are left out because they are two cars in a
// heat of their own, not the night as scheduled.
//
// Championships are not ranked. Their field is the season's fastest cars, so
// they would crowd the list with nights that were quick because of who was
// invited rather than how the racing went.

// RaceSpeed is one race night's average.
type RaceSpeed struct {
	Race    Race
	Average float64
	Runs    int
	// Rank is 1-based. Nights with the same average share a rank.
	Rank int
}

// RaceSpeeds ranks every points race night, fastest average first.
func RaceSpeeds(runs []Run) []RaceSpeed {
	sums := map[Race]float64{}
	counts := map[Race]int{}
	for _, r := range runs {
		if r.Race.Championship || r.RunOff {
			continue
		}
		sums[r.Race] += r.Time
		counts[r.Race]++
	}
	out := make([]RaceSpeed, 0, len(sums))
	for race, sum := range sums {
		out = append(out, RaceSpeed{Race: race, Average: sum / float64(counts[race]), Runs: counts[race]})
	}
	sort.Slice(out, func(i, j int) bool {
		if d := out[i].Average - out[j].Average; d < -epsilon || d > epsilon {
			return d < 0
		}
		return out[i].Race.Before(out[j].Race)
	})
	for i := range out {
		out[i].Rank = i + 1
		if i > 0 {
			if d := out[i].Average - out[i-1].Average; d > -epsilon && d < epsilon {
				out[i].Rank = out[i-1].Rank
			}
		}
	}
	return out
}
