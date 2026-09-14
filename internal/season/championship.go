package season

// Who goes into the championship, and in what order.
//
// The club's rule, in its own words:
//
//	Seeds 1–6    the six winners of the regular season races, by average time
//	Seeds 7–8    the next two fastest auto-qualifiers, by average time
//	Seeds 9–18   the remaining auto-qualifiers, by average time
//	Seeds 19–24  the wild-card racers, by total wildcard points
//	Seeds 1–8 get a bye into round two
//
// Two things about that are worth writing down, because both look like rules
// and only one is.
//
// The split at seed 8 is not an ordering rule. Seeds 7–8 and 9–18 are both
// "auto-qualifiers by average time" and they run straight on from each other,
// so 7 through 18 is one list. The reason 7–8 are called out separately is that
// the byes stop there — and the byes are not chosen either. A 24-car field
// needs a 32-car bracket, which leaves 8 byes, which land on the top 8 seeds
// because that is what the bracket construction does. Every one of those
// numbers moves together if the season changes shape.
//
// Nor is "seeds 1–6" a rule about the number six. It is "the race winners
// first", and there are six of them because there are six races. A five-race
// season puts winners in 1–5 and everything else shifts up.

// Origin says how a car got into the championship.
type Origin string

const (
	// OriginQualifier: finished in the top places of a regular-season race.
	OriginQualifier Origin = "qualifier"
	// OriginWildcard: did not auto-qualify, got in on season points.
	OriginWildcard Origin = "wildcard"
)

// Candidate is one place in the championship field.
//
// It names a racer rather than a car, because at the point the field is decided
// nobody has turned up yet. A qualifier does carry the car that earned the
// slot, which is almost always the car they will bring.
type Candidate struct {
	Seed   int
	Origin Origin

	RacerID int64
	Driver  string

	// QualifyingEntryID, CarName, RaceNumber, Place and Average describe the
	// finish that won the slot. Empty for a wildcard, which was won across the
	// whole season rather than in one race.
	QualifyingEntryID int64
	CarName           string
	RaceNumber        int
	Place             int
	Average           float64

	// Points is the wildcard total that won the slot. Zero for a qualifier.
	Points int
}

// Field returns the championship entrants in seed order: the auto-qualifiers as
// they are already ranked, then the wildcard racers on points.
//
// spots is how many wildcard places there are. Fewer are returned when there
// are not enough eligible racers to fill them — the field simply comes out
// smaller, and the bracket adjusts.
func Field(qualifiers []Slot, standings []WildcardRow, spots int) []Candidate {
	out := make([]Candidate, 0, len(qualifiers)+spots)

	for _, q := range qualifiers {
		out = append(out, Candidate{
			Origin:            OriginQualifier,
			RacerID:           q.RacerID,
			Driver:            q.Driver,
			QualifyingEntryID: q.EntryID,
			CarName:           q.CarName,
			RaceNumber:        q.RaceNumber,
			Place:             q.Place,
			Average:           q.Average,
		})
	}

	taken := 0
	for _, row := range standings {
		if taken >= spots {
			break
		}
		// A racer already holding every slot they are allowed cannot take a
		// wildcard as well. They stay in the published standings so their
		// season is visible, but they are not in contention.
		if row.MaxedOut {
			continue
		}
		out = append(out, Candidate{
			Origin:  OriginWildcard,
			RacerID: row.RacerID,
			Driver:  row.Name,
			Points:  row.Total,
		})
		taken++
	}

	for i := range out {
		out[i].Seed = i + 1
	}
	return out
}
