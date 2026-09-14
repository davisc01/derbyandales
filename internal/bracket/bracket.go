// Package bracket builds and advances a single-elimination championship.
//
// Nothing here is specific to a field of 24. The old system hardcoded that size
// in two eight-element tables that raise an index error at any other, which is
// why changing the number of races in a season was not something the club could
// do. Everything below derives from the number of entrants.
package bracket

import (
	"errors"
	"fmt"
	"math/bits"
)

// Bye is the seed number used for a slot nobody occupies.
const Bye = 0

// Bracket is a complete single-elimination structure: every matchup of every
// round, with the seeds that start in round one already placed.
type Bracket struct {
	// Entrants is how many cars are actually in the field.
	Entrants int
	// Capacity is the next power of two at or above Entrants. The difference
	// between the two is the number of byes.
	Capacity int
	// Byes is how many top seeds skip round one.
	Byes int
	// Rounds counts from round one to the final inclusive.
	Rounds int

	// Matchups are ordered by round, then by position down the page.
	Matchups []Matchup
}

// Matchup is one head-to-head. Round and Position are both 1-based.
type Matchup struct {
	Round    int
	Position int

	// Top and Bottom are seed numbers in round one, and Bye where the slot is
	// empty. In later rounds they are zero until a winner arrives from the
	// round before.
	Top    int
	Bottom int
}

// Walkover reports whether only one car is in this matchup, so that car
// advances without racing.
func (m Matchup) Walkover() bool {
	return (m.Top == Bye) != (m.Bottom == Bye)
}

// Empty reports whether neither slot is filled.
func (m Matchup) Empty() bool { return m.Top == Bye && m.Bottom == Bye }

// Capacity returns the smallest power of two at or above n, which is the size
// of bracket needed to hold n entrants.
func Capacity(n int) int {
	if n <= 1 {
		return 1
	}
	return 1 << bits.Len(uint(n-1))
}

// SeedOrder returns the seeds in bracket order for a given capacity: the order
// they appear down the first-round page.
//
// The construction is the standard one, and it is the reason the bracket is
// fair rather than merely tidy:
//
//	order(1)  = [1]
//	order(2n) = for each seed s in order(n): emit s, then emit (2n + 1 − s)
//
// Every property the club relies on falls out of it. Seed s meets seed
// capacity+1−s in round one, so the strongest faces the weakest. The top seeds
// are spread so that seeds 1 and 2 cannot meet before the final, 1 and 3 not
// before the semi, and so on. And when the field is short of a full bracket,
// the missing seeds are all at the bottom, so the byes land on the top seeds
// without anybody having to arrange that.
func SeedOrder(capacity int) []int {
	order := []int{1}
	for size := 2; size <= capacity; size *= 2 {
		next := make([]int, 0, size)
		for _, s := range order {
			next = append(next, s, size+1-s)
		}
		order = next
	}
	return order
}

// Build lays out a bracket for a given number of entrants.
//
// Entrants are assumed to be seeded 1..n. Seeds above n do not exist, and a
// matchup that would have contained one becomes a bye for its partner.
func Build(entrants int) (*Bracket, error) {
	if entrants < 2 {
		return nil, fmt.Errorf("a championship needs at least two cars, not %d", entrants)
	}

	capacity := Capacity(entrants)
	b := &Bracket{
		Entrants: entrants,
		Capacity: capacity,
		Byes:     capacity - entrants,
		Rounds:   bits.Len(uint(capacity)) - 1,
	}

	// Round one: consecutive pairs of the seed order.
	order := SeedOrder(capacity)
	for i := 0; i < len(order); i += 2 {
		top, bottom := order[i], order[i+1]
		if top > entrants {
			top = Bye
		}
		if bottom > entrants {
			bottom = Bye
		}
		b.Matchups = append(b.Matchups, Matchup{
			Round: 1, Position: i/2 + 1, Top: top, Bottom: bottom,
		})
	}

	// Later rounds are empty until winners arrive: matchup p of round r takes
	// the winners of matchups 2p−1 and 2p of round r−1.
	for round := 2; round <= b.Rounds; round++ {
		for position := 1; position <= capacity>>uint(round); position++ {
			b.Matchups = append(b.Matchups, Matchup{Round: round, Position: position})
		}
	}
	return b, nil
}

// Round returns the matchups of one round, in page order.
func (b *Bracket) Round(round int) []Matchup {
	var out []Matchup
	for _, m := range b.Matchups {
		if m.Round == round {
			out = append(out, m)
		}
	}
	return out
}

// Feeds says which matchup a result flows into, and whether it arrives in the
// top or the bottom slot.
//
// The final feeds nothing, which is how the champion is recognised.
func Feeds(round, position, rounds int) (nextRound, nextPosition int, top bool, ok bool) {
	if round >= rounds {
		return 0, 0, false, false
	}
	// Positions pair up two at a time, and the odd one of each pair lands on
	// top — which is what keeps the page order stable as the bracket fills.
	return round + 1, (position + 1) / 2, position%2 == 1, true
}

// ErrNotPending is returned when a result is recorded against a matchup that is
// not waiting for one.
var ErrNotPending = errors.New("that matchup is not waiting for a result")
