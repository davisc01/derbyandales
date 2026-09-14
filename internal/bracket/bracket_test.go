package bracket

import (
	"fmt"
	"sort"
	"testing"
)

// The club's existing bracket is the reference. It was drawn by hand, and if
// the generated one does not reproduce it, the generator is wrong rather than
// the drawing.

// pairs renders a round as comparable strings, low seed first within a matchup.
func pairs(ms []Matchup) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		a, b := m.Top, m.Bottom
		if a > b {
			a, b = b, a
		}
		out = append(out, fmt.Sprintf("%dv%d", a, b))
	}
	sort.Strings(out)
	return out
}

// The club's 24-car championship: eight first-round matchups, byes for the top
// eight seeds. These are the matchups on the bracket hanging at the venue.
func TestTheClubs24CarBracketIsReproduced(t *testing.T) {
	b, err := Build(24)
	if err != nil {
		t.Fatal(err)
	}

	if b.Capacity != 32 || b.Byes != 8 || b.Rounds != 5 {
		t.Errorf("capacity %d, byes %d, rounds %d; want 32, 8, 5",
			b.Capacity, b.Byes, b.Rounds)
	}

	// Round one has sixteen slots, but half of them are byes, so only eight
	// are races.
	var raced []Matchup
	for _, m := range b.Round(1) {
		if !m.Walkover() && !m.Empty() {
			raced = append(raced, m)
		}
	}
	got := pairs(raced)
	want := []string{"10v23", "11v22", "12v21", "13v20", "14v19", "15v18", "16v17", "9v24"}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("%d first-round races, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("first round = %v, want %v", got, want)
			break
		}
	}

	// Byes go to seeds 1 to 8, which is what "the top eight enter at round two"
	// means in practice.
	byes := map[int]bool{}
	for _, m := range b.Round(1) {
		if m.Walkover() {
			s := m.Top
			if s == Bye {
				s = m.Bottom
			}
			byes[s] = true
		}
	}
	if len(byes) != 8 {
		t.Fatalf("%d byes, want 8", len(byes))
	}
	for seed := 1; seed <= 8; seed++ {
		if !byes[seed] {
			t.Errorf("seed %d did not get a bye", seed)
		}
	}
}

// The halves of the bracket are what stop the two best cars meeting early. The
// club's diagram puts 1, 4, 5 and 8 in one half and 2, 3, 6, 7 in the other.
func TestTheTopSeedsAreSplitBetweenTheHalves(t *testing.T) {
	b, _ := Build(24)
	round1 := b.Round(1)
	half := len(round1) / 2

	top := map[int]bool{}
	for _, m := range round1[:half] {
		for _, s := range []int{m.Top, m.Bottom} {
			if s != Bye && s <= 8 {
				top[s] = true
			}
		}
	}
	for _, seed := range []int{1, 4, 5, 8} {
		if !top[seed] {
			t.Errorf("seed %d is not in the top half", seed)
		}
	}
	for _, seed := range []int{2, 3, 6, 7} {
		if top[seed] {
			t.Errorf("seed %d is in the top half, want the bottom", seed)
		}
	}
}

// --- properties, at every field size ------------------------------------------

// simulate runs a whole bracket with a given winner rule and returns the
// champion, plus how many races were actually run.
func simulate(t *testing.T, b *Bracket, wins func(a, c int) int) (champion, races int) {
	t.Helper()
	// results[round][position] = winning seed
	results := map[int]map[int]int{}
	for r := 1; r <= b.Rounds; r++ {
		results[r] = map[int]int{}
	}

	for round := 1; round <= b.Rounds; round++ {
		for _, m := range b.Round(round) {
			top, bottom := m.Top, m.Bottom
			if round > 1 {
				prev := results[round-1]
				top = prev[m.Position*2-1]
				bottom = prev[m.Position*2]
			}
			switch {
			case top == Bye && bottom == Bye:
				// Nobody here: only possible in a round one slot that was all
				// byes, which Build never produces for a valid field.
				t.Fatalf("round %d position %d has nobody in it", round, m.Position)
			case top == Bye:
				results[round][m.Position] = bottom
			case bottom == Bye:
				results[round][m.Position] = top
			default:
				results[round][m.Position] = wins(top, bottom)
				races++
			}
		}
	}
	return results[b.Rounds][1], races
}

// Every field size from a two-car final to a 64-car bracket has to produce a
// legal championship. This is the test the old eight-element tables could not
// have passed.
func TestEveryFieldSizeProducesOneChampion(t *testing.T) {
	for n := 2; n <= 64; n++ {
		b, err := Build(n)
		if err != nil {
			t.Fatalf("%d entrants: %v", n, err)
		}

		// Every seed appears exactly once in round one.
		seen := map[int]int{}
		for _, m := range b.Round(1) {
			for _, s := range []int{m.Top, m.Bottom} {
				if s != Bye {
					seen[s]++
				}
			}
		}
		if len(seen) != n {
			t.Errorf("%d entrants: %d seeds placed", n, len(seen))
		}
		for seed := 1; seed <= n; seed++ {
			if seen[seed] != 1 {
				t.Errorf("%d entrants: seed %d appears %d times", n, seed, seen[seed])
			}
		}

		// Byes are exactly capacity − entrants, and they go to the top seeds.
		var byeSeeds []int
		for _, m := range b.Round(1) {
			if m.Walkover() {
				s := m.Top
				if s == Bye {
					s = m.Bottom
				}
				byeSeeds = append(byeSeeds, s)
			}
		}
		if len(byeSeeds) != b.Byes {
			t.Errorf("%d entrants: %d byes, want %d", n, len(byeSeeds), b.Byes)
		}
		sort.Ints(byeSeeds)
		for i, s := range byeSeeds {
			if s != i+1 {
				t.Errorf("%d entrants: byes went to %v, want the top %d seeds",
					n, byeSeeds, b.Byes)
				break
			}
		}

		// The best car wins the whole thing, and every car but one loses
		// exactly once — so the number of races is entrants − 1.
		champion, races := simulate(t, b, func(a, c int) int {
			if a < c {
				return a
			}
			return c
		})
		if champion != 1 {
			t.Errorf("%d entrants: seed %d won with the top seed always winning", n, champion)
		}
		if races != n-1 {
			t.Errorf("%d entrants: %d races run, want %d", n, races, n-1)
		}
	}
}

// Seed s faces seed capacity+1−s in the first round: the strongest car in the
// field against the weakest, not against another contender.
func TestFirstRoundPairsStrongestAgainstWeakest(t *testing.T) {
	for _, capacity := range []int{4, 8, 16, 32, 64} {
		b, _ := Build(capacity)
		for _, m := range b.Round(1) {
			if m.Top+m.Bottom != capacity+1 {
				t.Errorf("capacity %d: seed %d drawn against %d, want %d",
					capacity, m.Top, m.Bottom, capacity+1-m.Top)
			}
		}
	}
}

// The two best cars must not meet before the final, and more generally the top
// four must not meet before the semis. That is the whole point of seeding.
func TestTopSeedsCannotMeetEarly(t *testing.T) {
	for n := 4; n <= 64; n++ {
		b, _ := Build(n)

		// Track which round each seed is eliminated in, with the better seed
		// always winning: the round seed 2 loses in is the round it met seed 1.
		out := map[int]int{}
		results := map[int]map[int]int{}
		for r := 1; r <= b.Rounds; r++ {
			results[r] = map[int]int{}
		}
		for round := 1; round <= b.Rounds; round++ {
			for _, m := range b.Round(round) {
				top, bottom := m.Top, m.Bottom
				if round > 1 {
					top = results[round-1][m.Position*2-1]
					bottom = results[round-1][m.Position*2]
				}
				switch {
				case top == Bye:
					results[round][m.Position] = bottom
				case bottom == Bye:
					results[round][m.Position] = top
				case top < bottom:
					results[round][m.Position] = top
					out[bottom] = round
				default:
					results[round][m.Position] = bottom
					out[top] = round
				}
			}
		}

		if seed2 := out[2]; n >= 2 && seed2 != b.Rounds {
			t.Errorf("%d entrants: seed 2 met seed 1 in round %d of %d",
				n, seed2, b.Rounds)
		}
		// Seeds 3 and 4 should survive to the semi-final at least, whenever the
		// field is big enough to have one.
		if b.Rounds >= 2 && n >= 4 {
			for _, seed := range []int{3, 4} {
				if out[seed] < b.Rounds-1 {
					t.Errorf("%d entrants: seed %d went out in round %d, before the semi (%d)",
						n, seed, out[seed], b.Rounds-1)
				}
			}
		}
	}
}

// A bracket cannot be built from nothing, and the message has to say so in
// words rather than by panicking somewhere downstream.
func TestTooSmallAFieldIsRefused(t *testing.T) {
	for _, n := range []int{-1, 0, 1} {
		if _, err := Build(n); err == nil {
			t.Errorf("Build(%d) was allowed", n)
		}
	}
}

func TestCapacityRoundsUpToAPowerOfTwo(t *testing.T) {
	for n, want := range map[int]int{2: 2, 3: 4, 4: 4, 5: 8, 16: 16, 17: 32, 24: 32, 32: 32, 33: 64} {
		if got := Capacity(n); got != want {
			t.Errorf("Capacity(%d) = %d, want %d", n, got, want)
		}
	}
}

// Results have to flow into the right slot, or the bracket fills in the wrong
// order and the page stops matching the racing.
func TestResultsFeedTheRoundAbove(t *testing.T) {
	b, _ := Build(24)

	for round := 1; round < b.Rounds; round++ {
		seen := map[[2]int]bool{}
		for _, m := range b.Round(round) {
			nr, np, top, ok := Feeds(m.Round, m.Position, b.Rounds)
			if !ok {
				t.Fatalf("round %d position %d feeds nothing", m.Round, m.Position)
			}
			if nr != round+1 {
				t.Errorf("round %d feeds round %d", round, nr)
			}
			key := [2]int{np, boolToInt(top)}
			if seen[key] {
				t.Errorf("two matchups both feed round %d position %d slot %v", nr, np, top)
			}
			seen[key] = true
		}
	}

	// The final feeds nothing: that is how the champion is recognised.
	if _, _, _, ok := Feeds(b.Rounds, 1, b.Rounds); ok {
		t.Error("the final feeds another matchup")
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
