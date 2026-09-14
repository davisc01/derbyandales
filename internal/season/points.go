package season

// Wildcard points.
//
// The club's published rule: a racer who does not auto-qualify earns points
// according to where their best car finished, "equal to the number of racers at
// the race" for the first place below the auto-qualifying cut, one fewer for
// each place after that.
//
//	points = max(0, field − (place − (auto_qual_places + 1)))
//
// With 3 auto-qualifying places and a 23-car field, 4th scores 23, 5th scores
// 22, and so on down to 26th, which scores nothing.

// PointAward is one entry's wildcard points for one race, with the two numbers
// that produced them. They are stored alongside the points so that "why did I
// get 14?" is answerable months later, after the settings have moved on.
type PointAward struct {
	EntryID int64
	RacerID int64
	Place   int
	Points  int

	// Field is the racer count the award was computed against.
	Field int
}

// RacePoints computes every entry's wildcard points for one finished race.
//
// Three rules zero an entry out, and they are separate for a reason:
//
//   - The CONTROL pace car never scores. It is equipment.
//   - Only a racer's best-placed car scores. Bringing three cars is allowed and
//     common, but it must not be three bites at the wildcard.
//   - A racer whose best car auto-qualified scores nothing at all that race.
//     They already have their championship slot; the wildcard list exists to
//     give one to somebody who does not.
//
// The third is easy to get subtly wrong. It is the *racer's best place* that is
// tested, not each car's place, so a racer who wins the race earns no points for
// their second car finishing 7th either.
func RacePoints(finishes []Finish, r Rules) []PointAward {
	field := FieldSize(finishes, r)

	best := map[int64]int{}
	for _, f := range finishes {
		if f.IsControl || f.Place == 0 {
			continue
		}
		if p, ok := best[f.RacerID]; !ok || f.Place < p {
			best[f.RacerID] = f.Place
		}
	}

	out := make([]PointAward, 0, len(finishes))
	for _, f := range finishes {
		a := PointAward{EntryID: f.EntryID, RacerID: f.RacerID, Place: f.Place, Field: field}
		switch {
		case f.IsControl, f.Place == 0:
			// No points, and no place either — say nothing about them.
		case f.Place != best[f.RacerID]:
			// Not this racer's best car.
		case best[f.RacerID] <= r.AutoQualPlaces:
			// Already auto-qualified.
		default:
			a.Points = points(field, f.Place, r.AutoQualPlaces)
		}
		out = append(out, a)
	}
	return out
}

// points applies the formula, floored at zero: in a large field the last few
// finishers score nothing rather than going negative.
func points(field, place, autoQual int) int {
	p := field - (place - (autoQual + 1))
	if p < 0 {
		return 0
	}
	return p
}
