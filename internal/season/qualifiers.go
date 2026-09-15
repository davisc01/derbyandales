package season

import (
	"sort"
	"strconv"
	"strings"
)

// Auto-qualifiers.
//
// The top AutoQualPlaces finishers of every race take a championship slot, and
// the order of that list is the seeding order: index 1 is the top seed.
//
// The club orders it by winners first — every race winner, fastest average
// first — and then everybody else by average regardless of which race they ran
// or where they placed in it. So the slowest race winner still outseeds the
// fastest runner-up. That is deliberate: winning a night is worth more than a
// time, and times across nights are not strictly comparable anyway, since track
// conditions and the field differ.

// Slot is one place in the championship field, held by one car.
type Slot struct {
	Finish

	// Seed is 1-based and is simply the position in the ordered list.
	Seed int

	// Entries is how many slots this racer holds across the whole season, which
	// is the "Total Entries" column on the published qualifiers list. It never
	// exceeds the cap: a finish that would take a racer past it passes down.
	Entries int

	// PassedOver lists the cars that finished above this one in its race but
	// could not take the place, which is why this car has it.
	PassedOver []Passed

	// TiedWith is a car that finished level with this one but did not get the
	// place, because the race had no more to give. Nil almost always. When it
	// is set, which of the two qualifies has not been raced for, and a person
	// needs to settle it on the track.
	TiedWith *Finish

	// RaceTop marks the best qualifier from its race — normally the winner. It
	// is what "the race winners are seeded first" means once a winner's place
	// has passed down.
	RaceTop bool
}

// Passed is a car that finished in a qualifying place without taking it.
type Passed struct {
	Finish
	Reason PassReason
}

// PassReason says why a qualifying finish did not take the place.
type PassReason string

const (
	// PassAtCap: the racer already holds as many championship places as the
	// cap allows. They can still race, but the place goes to the next car.
	PassAtCap PassReason = "at-cap"
	// PassAlreadyQualified: this car took a place at an earlier race and was
	// not allowed to race again. It cannot take a second.
	PassAlreadyQualified PassReason = "already-qualified"
)

// Qualifiers builds the seeded auto-qualifier list from every finish of the
// season's completed races.
//
// The races are taken in order, because the club's rules depend on what a
// racer already held when each race was run:
//
//   - The top AutoQualPlaces cars of a race take a place each.
//   - A racer who already holds MaxEntries places can still race, but a top
//     finish does not add another. The place goes down to the next car.
//   - A car that has qualified is not allowed to race again before the
//     championship. If one does, it cannot take a second place either.
//
// So nobody ever ends up over the cap, and there is nothing for a person to
// substitute by hand — the next finisher simply has the place.
func Qualifiers(finishes []Finish, r Rules) []Slot {
	byRace := map[int][]Finish{}
	var races []int
	for _, f := range finishes {
		if _, ok := byRace[f.RaceNumber]; !ok {
			races = append(races, f.RaceNumber)
		}
		byRace[f.RaceNumber] = append(byRace[f.RaceNumber], f)
	}
	sort.Ints(races)

	held := map[int64]int{}
	qualifiedCar := map[string]bool{}
	var slots []Slot

	for _, race := range races {
		field := byRace[race]
		sort.SliceStable(field, func(i, j int) bool { return field[i].Place < field[j].Place })

		taken := 0
		var passed []Passed
		first := true
		for _, f := range field {
			if f.IsControl || f.Place == 0 {
				continue
			}
			key := CarKey(f.RacerID, f.CarName)
			// The race has given all its places. A car level on place with
			// the last one taken is not let in as well — that would send one
			// more car to the championship than the season has room for — but
			// the tie is recorded, because the order it sorted in is not a
			// result.
			if taken >= r.AutoQualPlaces {
				last := &slots[len(slots)-1]
				if f.Place == last.Place && !qualifiedCar[key] && held[f.RacerID] < r.MaxEntries {
					tied := f
					last.TiedWith = &tied
				}
				break
			}
			switch {
			case qualifiedCar[key]:
				passed = append(passed, Passed{Finish: f, Reason: PassAlreadyQualified})
				continue
			case held[f.RacerID] >= r.MaxEntries && r.MaxEntries > 0:
				passed = append(passed, Passed{Finish: f, Reason: PassAtCap})
				continue
			}
			held[f.RacerID]++
			qualifiedCar[key] = true
			slots = append(slots, Slot{Finish: f, PassedOver: passed, RaceTop: first})
			passed = nil
			first = false
			taken++
		}
	}

	sortSlots(slots)
	for i := range slots {
		slots[i].Seed = i + 1
		slots[i].Entries = held[slots[i].RacerID]
	}
	return slots
}

// CarKey identifies one car across a season: the racer, and the car's name
// folded for case and spacing, which is how people name their cars to each
// other and how the club's own lists tell two cars apart.
func CarKey(racerID int64, carName string) string {
	return strconv.FormatInt(racerID, 10) + "|" + strings.Join(strings.Fields(strings.ToLower(carName)), " ")
}

// sortSlots puts race winners first by average, then everyone else by average.
//
// The tie-break matters: two of one racer's cars ran 2.343 in the club's 2026
// race 5 and were published in place order. Without an explicit tie-break the
// order would depend on however the rows came back from the database, and the
// seeding would shuffle between page loads.
func sortSlots(slots []Slot) {
	sort.Slice(slots, func(i, j int) bool {
		a, b := slots[i], slots[j]
		if a.RaceTop != b.RaceTop {
			return a.RaceTop
		}
		if a.Average != b.Average {
			return a.Average < b.Average
		}
		if a.Place != b.Place {
			return a.Place < b.Place
		}
		if a.RaceNumber != b.RaceNumber {
			return a.RaceNumber < b.RaceNumber
		}
		return a.EntryID < b.EntryID
	})
}
