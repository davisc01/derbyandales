package season

import (
	"sort"
	"strconv"
)

// The wildcard standings: everyone who did not auto-qualify, ranked by points,
// with the top WildcardSpots of them taking the remaining championship places.

// RacerPoints is one racer's season totals before ranking.
type RacerPoints struct {
	RacerID int64
	Name    string

	// Earned is the sum of the frozen per-race awards.
	Earned int
	// Adjusted is the signed total of manual corrections.
	Adjusted int

	// Slots is how many auto-qualifier slots this racer already holds.
	Slots int
}

// WildcardRow is one line of the standings.
type WildcardRow struct {
	RacerPoints

	Rank  int
	Total int

	// MaxedOut means this racer already holds every championship place they are
	// allowed (count >= the cap), so their points cannot win them another.
	MaxedOut bool
}

// Wildcard ranks the season's racers.
//
// Maxed-out racers sort to the bottom regardless of their points. They are kept
// in the list rather than dropped because the club publishes it, and a racer
// who scored well all season should be able to see that they did — the published
// file prints "Max Entries Reached" in place of their total.
//
// Do not pass the pace car's driver in. "Derby Ales" appears in the club's own
// published 2026 standings on zero points because the old tracker treated the
// CONTROL car's name as a racer; there is no such person.
func Wildcard(racers []RacerPoints, r Rules) []WildcardRow {
	rows := make([]WildcardRow, 0, len(racers))
	for _, p := range racers {
		rows = append(rows, WildcardRow{
			RacerPoints: p,
			Total:       p.Earned + p.Adjusted,
			MaxedOut:    p.Slots >= r.MaxEntries,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.MaxedOut != b.MaxedOut {
			return b.MaxedOut
		}
		if a.Total != b.Total {
			return a.Total > b.Total
		}
		// Names, so a page reload cannot reshuffle racers who are level.
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.RacerID < b.RacerID
	})

	for i := range rows {
		rows[i].Rank = i + 1
	}
	return rows
}

// FormatPoints renders a total the way the published file does. A maxed-out
// racer's number is replaced by the reason it no longer matters.
func FormatPoints(row WildcardRow) string {
	if row.MaxedOut {
		return "Max Entries Reached"
	}
	return strconv.Itoa(row.Total)
}
