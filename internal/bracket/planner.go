package bracket

import "fmt"

// The Championship Planner.
//
// The shape of the championship is not chosen, it is a consequence: six races,
// top three from each, plus six wildcards makes twenty-four cars, which needs a
// bracket of thirty-two, which means eight byes. Change any of those and the
// rest move. This works the arithmetic out in advance so a change to the season
// can be looked at before it is made rather than discovered in February.

// Plan is the championship a given season shape produces.
type Plan struct {
	Races          int
	AutoQualPlaces int
	Wildcards      int

	Entrants   int
	Capacity   int
	Byes       int
	FirstRound int // matchups actually raced in round one
	Rounds     int

	// Warnings are the things worth knowing before committing to this shape.
	Warnings []string
}

// ByeFree reports a bracket with no byes at all, where every car races in
// round one.
func (p Plan) ByeFree() bool { return p.Byes == 0 }

// PlanFor works out the championship a season shape produces.
func PlanFor(races, autoQualPlaces, wildcards int) Plan {
	p := Plan{Races: races, AutoQualPlaces: autoQualPlaces, Wildcards: wildcards}
	p.Entrants = races*autoQualPlaces + wildcards
	if p.Entrants < 2 {
		p.Warnings = append(p.Warnings,
			"That is not enough cars for a championship.")
		return p
	}

	p.Capacity = Capacity(p.Entrants)
	p.Byes = p.Capacity - p.Entrants
	p.FirstRound = p.Entrants - p.Capacity/2
	p.Rounds = 0
	for c := p.Capacity; c > 1; c /= 2 {
		p.Rounds++
	}

	// Two shapes are legal but not what anybody wants, and both are easy to
	// walk into by changing one number.
	if p.FirstRound < 2 {
		p.Warnings = append(p.Warnings, fmt.Sprintf(
			"Only %d first-round race%s — nearly the whole field would sit out round one. "+
				"Add wildcards, or drop to a smaller bracket.",
			p.FirstRound, plural(p.FirstRound)))
	}
	qualifiers := races * autoQualPlaces
	if p.Byes > qualifiers {
		p.Warnings = append(p.Warnings, fmt.Sprintf(
			"%d byes but only %d auto-qualifiers, so byes would reach the wildcard racers. "+
				"A bye is meant to reward winning during the season.",
			p.Byes, qualifiers))
	}
	return p
}

// SuggestWildcards lists the wildcard counts that give a clean bracket for a
// given number of races: the bye-free ones first, then the smallest number of
// byes available.
//
// This is the question actually being asked when a season changes — "we are
// dropping to five races, how many wildcards should we take?" — and it has a
// better answer than keeping whatever the number used to be.
func SuggestWildcards(races, autoQualPlaces, max int) []Plan {
	var out []Plan
	for w := 0; w <= max; w++ {
		p := PlanFor(races, autoQualPlaces, w)
		if p.Entrants < 2 || len(p.Warnings) > 0 {
			continue
		}
		out = append(out, p)
	}
	return out
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
