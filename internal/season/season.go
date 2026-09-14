// Package season turns finished races into the two lists that decide the
// championship field: the auto-qualifiers and the wildcard points standings.
//
// The club's old tracker was a separate application with its own database,
// fed by hand-exported CSVs. That is the only reason it existed, and it is why
// this package is pure: it takes finishes in and gives rankings out, so the
// rules can be tested against the club's published results without a database
// anywhere near them.
package season

// Rules are the season settings the calculations depend on. They live on the
// season row so a year with a different number of races needs no code change.
type Rules struct {
	// AutoQualPlaces is how many finishers per race qualify automatically.
	// The club uses 3.
	AutoQualPlaces int

	// MaxEntries caps how many championship slots one racer may hold. Note the
	// two thresholds this produces, which are deliberately different:
	//
	//   over limit — count >  MaxEntries, so a slot must be substituted away
	//   maxed out  — count >= MaxEntries, so no more wildcard contention
	//
	// A racer holding exactly the cap is maxed out but not over limit: they
	// keep every slot, they simply cannot win another through wildcards.
	MaxEntries int

	// CountControl restores the old tracker's behaviour of counting the CONTROL
	// pace car in the field size. It is wrong — see FieldSize — and defaults to
	// false. It exists so the app can reproduce published history exactly.
	CountControl bool
}

// Finish is one car's result in one finished race: everything the season needs
// and nothing about how the times were produced.
type Finish struct {
	EntryID int64
	RacerID int64
	RaceID  int64

	// RaceNumber orders races and is what "Race 4" in the published CSV means.
	RaceNumber int

	Driver  string
	CarName string

	// Place is 1-based, as assigned by the scoring package: tied cars share a
	// place and the next distinct place skips. The points formula uses the raw
	// place, so a shared place shares a points value and the place after a tie
	// is worth correspondingly less. That is the club's rule, not an accident.
	Place   int
	Average float64
	Heats   int

	// IsControl marks the pace car. It races and it is ranked, but it takes no
	// award, earns no points and cannot qualify.
	IsControl bool
}

// FieldSize is the "number of racers at the race" the club's points rule refers
// to.
//
// The old tracker took this as the row count of the exported standings, which
// included the CONTROL pace car — so every racer's points were one higher than
// the published rule says they should be. The pace car is club equipment, not a
// competitor, so it is not counted here.
//
// Excluded cars are already absent: they never reach the standings at all.
func FieldSize(finishes []Finish, r Rules) int {
	n := 0
	for _, f := range finishes {
		if f.IsControl && !r.CountControl {
			continue
		}
		n++
	}
	return n
}
