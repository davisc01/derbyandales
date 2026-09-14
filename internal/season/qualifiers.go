package season

import "sort"

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
	// is the "Total Entries" column on the published qualifiers list.
	Entries int

	// OverLimit marks a racer holding more slots than the cap allows. One of
	// them has to be substituted away before the bracket can be seeded.
	OverLimit bool

	// SubstitutedFor is the finish this slot replaced, when the slot exists
	// because a coordinator substituted an over-limit racer out. Nil otherwise.
	SubstitutedFor *Finish
}

// Substitution replaces one over-limit qualifying slot with another finisher
// from the same race.
type Substitution struct {
	OriginalEntryID   int64
	SubstituteEntryID int64
}

// Qualifiers builds the seeded auto-qualifier list from every finish of the
// season's completed races.
//
// Substitutions are applied first, so the entry counts and the over-limit flags
// describe the field as it actually stands rather than as it stood before the
// coordinator fixed it.
func Qualifiers(finishes []Finish, subs []Substitution, r Rules) []Slot {
	byEntry := make(map[int64]Finish, len(finishes))
	for _, f := range finishes {
		byEntry[f.EntryID] = f
	}

	substitutedOut := make(map[int64]Finish, len(subs))
	for _, s := range subs {
		if original, ok := byEntry[s.OriginalEntryID]; ok {
			substitutedOut[s.OriginalEntryID] = original
		}
	}

	slots := make([]Slot, 0, len(finishes))
	for _, f := range finishes {
		if !qualifies(f, r) {
			continue
		}
		if _, gone := substitutedOut[f.EntryID]; gone {
			continue
		}
		slots = append(slots, Slot{Finish: f})
	}
	for _, s := range subs {
		f, ok := byEntry[s.SubstituteEntryID]
		if !ok {
			continue // the substitute's race was deleted or un-frozen
		}
		original, ok := substitutedOut[s.OriginalEntryID]
		if !ok {
			continue // nothing was actually replaced
		}
		slots = append(slots, Slot{Finish: f, SubstitutedFor: &original})
	}

	sortSlots(slots)

	entries := map[int64]int{}
	for _, s := range slots {
		entries[s.RacerID]++
	}
	for i := range slots {
		slots[i].Seed = i + 1
		slots[i].Entries = entries[slots[i].RacerID]
		slots[i].OverLimit = entries[slots[i].RacerID] > r.MaxEntries
	}
	return slots
}

// qualifies reports whether a finish took one of the automatic slots.
func qualifies(f Finish, r Rules) bool {
	return !f.IsControl && f.Place > 0 && f.Place <= r.AutoQualPlaces
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
		if (a.Place == 1) != (b.Place == 1) {
			return a.Place == 1
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

// SubstituteCandidates lists who may take an over-limit racer's slot in a given
// race: a finisher from that race who did not auto-qualify, is not the pace car,
// and is not already standing in for somebody else.
//
// The replacement comes from the same race as the slot it fills, so the race
// still sends the same number of cars to the championship.
func SubstituteCandidates(finishes []Finish, raceID int64, subs []Substitution, r Rules) []Finish {
	used := make(map[int64]bool, len(subs))
	for _, s := range subs {
		used[s.SubstituteEntryID] = true
	}

	var out []Finish
	for _, f := range finishes {
		if f.RaceID != raceID || f.IsControl || f.Place == 0 {
			continue
		}
		if f.Place <= r.AutoQualPlaces || used[f.EntryID] {
			continue
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Place < out[j].Place })
	return out
}
