package season

import "testing"

// The seeding order decides who races whom in the championship, so it gets
// checked against the club's rule statement clause by clause.

// sixRaceSeason builds a full season: six races, three qualifiers each, and a
// spread of wildcard points.
func sixRaceSeason() ([]Slot, []WildcardRow) {
	var finishes []Finish
	id := int64(0)
	racer := int64(0)
	for race := 1; race <= 6; race++ {
		for place := 1; place <= 3; place++ {
			id++
			racer++
			finishes = append(finishes, Finish{
				EntryID: id, RacerID: racer, RaceID: int64(race), RaceNumber: race,
				Driver: driverName(racer), CarName: "Car " + driverName(racer),
				Place: place,
				// Winners are spread across a range that overlaps the
				// runners-up, so "winners first" has to be doing the work
				// rather than the times happening to sort that way.
				Average: 2.30 + float64(place)*0.02 + float64(race)*0.011,
			})
		}
	}
	qualifiers := Qualifiers(finishes, clubRules)

	var standings []WildcardRow
	for i := 1; i <= 8; i++ {
		standings = append(standings, WildcardRow{
			RacerPoints: RacerPoints{RacerID: int64(100 + i), Name: driverName(int64(100 + i))},
			Rank:        i,
			Total:       80 - i*5,
		})
	}
	return qualifiers, standings
}

func driverName(id int64) string {
	return "Racer " + string(rune('A'+int(id%26))) + string(rune('0'+int(id/26)))
}

// The club's shape: 6 x 3 auto-qualifiers plus 6 wildcards makes 24.
func TestTheFieldIsQualifiersThenWildcards(t *testing.T) {
	qualifiers, standings := sixRaceSeason()
	field := Field(qualifiers, standings, 6)

	if len(field) != 24 {
		t.Fatalf("%d entrants, want 24", len(field))
	}
	for i, c := range field {
		if c.Seed != i+1 {
			t.Fatalf("entry %d has seed %d", i, c.Seed)
		}
	}
	for _, c := range field[:18] {
		if c.Origin != OriginQualifier {
			t.Errorf("seed %d is a %s, want a qualifier", c.Seed, c.Origin)
		}
	}
	for _, c := range field[18:] {
		if c.Origin != OriginWildcard {
			t.Errorf("seed %d is a %s, want a wildcard", c.Seed, c.Origin)
		}
	}
}

// "Seeds 1-6: the six winners of the regular season races, ordered by average
// times." Six because there are six races, and a slower winner still outseeds a
// quicker runner-up.
func TestTheRaceWinnersTakeTheTopSeeds(t *testing.T) {
	qualifiers, standings := sixRaceSeason()
	field := Field(qualifiers, standings, 6)

	for _, c := range field[:6] {
		if c.Place != 1 {
			t.Errorf("seed %d finished %d, but the top six are the race winners",
				c.Seed, c.Place)
		}
	}
	for i := 1; i < 6; i++ {
		if field[i].Average < field[i-1].Average {
			t.Errorf("seed %d is quicker than seed %d; winners are ordered by average",
				field[i].Seed, field[i-1].Seed)
		}
	}
	// And at least one runner-up is quicker than the slowest winner, or this
	// test would pass on a straight sort by time.
	slowestWinner := field[5].Average
	quickest := field[6].Average
	if quickest >= slowestWinner {
		t.Skip("this fixture does not have a runner-up quicker than the slowest winner")
	}
	if field[6].Place == 1 {
		t.Error("a seventh race winner appeared")
	}
}

// "Seeds 7-8: the next two fastest auto-qualifiers... Seeds 9-18: the remaining
// auto-qualifiers, ordered by average time."
//
// Those two clauses are one ordered run. The split at 8 marks where the byes
// stop, not a change in how the list is sorted, so 7 through 18 must be in
// unbroken order by average.
func TestTheRemainingQualifiersRunStraightOnByAverage(t *testing.T) {
	qualifiers, standings := sixRaceSeason()
	field := Field(qualifiers, standings, 6)

	for i := 7; i < 18; i++ {
		if field[i].Average < field[i-1].Average {
			t.Errorf("seed %d (%.3f) is quicker than seed %d (%.3f) — the run from 7 to 18 is broken",
				field[i].Seed, field[i].Average, field[i-1].Seed, field[i-1].Average)
		}
	}
	// Nothing changes at the 8/9 boundary in particular.
	if field[8].Average < field[7].Average {
		t.Error("the order changes between seeds 8 and 9; it should not")
	}
}

// "Seeds 19-24: the wild-card racers, ordered by total wildcard points."
func TestWildcardsAreOrderedByPoints(t *testing.T) {
	qualifiers, standings := sixRaceSeason()
	field := Field(qualifiers, standings, 6)

	wildcards := field[18:]
	for i := 1; i < len(wildcards); i++ {
		if wildcards[i].Points > wildcards[i-1].Points {
			t.Errorf("seed %d has %d points, more than seed %d with %d",
				wildcards[i].Seed, wildcards[i].Points,
				wildcards[i-1].Seed, wildcards[i-1].Points)
		}
	}
	if wildcards[0].Points != standings[0].Total {
		t.Errorf("the top wildcard seed has %d points, want the standings leader's %d",
			wildcards[0].Points, standings[0].Total)
	}
}

// A racer already holding as many slots as the cap allows cannot also take a
// wildcard place. They stay in the published standings so their season shows,
// but the spot goes to the next racer down.
func TestAMaxedOutRacerDoesNotTakeAWildcardPlace(t *testing.T) {
	qualifiers, standings := sixRaceSeason()
	standings[0].MaxedOut = true
	standings[0].Slots = 3

	field := Field(qualifiers, standings, 6)
	for _, c := range field {
		if c.Origin == OriginWildcard && c.RacerID == standings[0].RacerID {
			t.Fatalf("%s took a wildcard place while holding %d qualifying slots",
				c.Driver, standings[0].Slots)
		}
	}
	// The place is not lost, it moves down the list.
	if len(field) != 24 {
		t.Errorf("%d entrants, want 24 — the place should have passed to the next racer", len(field))
	}
}

// Every seed is a different person's slot or a different car. Two of the same
// would mean somebody racing themselves.
func TestNoSlotIsIssuedTwice(t *testing.T) {
	qualifiers, standings := sixRaceSeason()
	field := Field(qualifiers, standings, 6)

	entries := map[int64]bool{}
	wildcardRacers := map[int64]bool{}
	for _, c := range field {
		switch c.Origin {
		case OriginQualifier:
			if entries[c.QualifyingEntryID] {
				t.Errorf("entry %d qualified twice", c.QualifyingEntryID)
			}
			entries[c.QualifyingEntryID] = true
		case OriginWildcard:
			if wildcardRacers[c.RacerID] {
				t.Errorf("%s took two wildcard places", c.Driver)
			}
			wildcardRacers[c.RacerID] = true
		}
	}
}

// A thin season should produce a smaller championship rather than a broken one.
func TestNotEnoughWildcardsShrinksTheField(t *testing.T) {
	qualifiers, standings := sixRaceSeason()

	field := Field(qualifiers, standings[:2], 6)
	if len(field) != 20 {
		t.Errorf("%d entrants with only two eligible wildcards, want 20", len(field))
	}
	for i, c := range field {
		if c.Seed != i+1 {
			t.Errorf("seeds are not contiguous: position %d has seed %d", i, c.Seed)
		}
	}
}

// Five races puts the winners in seeds 1 to 5. The number six in the club's
// rule is the number of races, not a constant.
func TestAShorterSeasonMovesTheBoundary(t *testing.T) {
	var finishes []Finish
	id, racer := int64(0), int64(0)
	for race := 1; race <= 5; race++ {
		for place := 1; place <= 3; place++ {
			id++
			racer++
			finishes = append(finishes, Finish{
				EntryID: id, RacerID: racer, RaceID: int64(race), RaceNumber: race,
				Driver: driverName(racer), Place: place,
				Average: 2.30 + float64(place)*0.02 + float64(race)*0.011,
			})
		}
	}
	field := Field(Qualifiers(finishes, clubRules), nil, 1)

	winners := 0
	for _, c := range field {
		if c.Origin == OriginQualifier && c.Place == 1 {
			winners++
		}
	}
	if winners != 5 {
		t.Errorf("%d race winners in a five-race season, want 5", winners)
	}
	for _, c := range field[:5] {
		if c.Place != 1 {
			t.Errorf("seed %d is not a race winner in a five-race season", c.Seed)
		}
	}
	if field[5].Place == 1 {
		t.Error("seed 6 is a race winner, but there were only five races")
	}
}
