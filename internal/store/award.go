package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/davisc01/derbyandales/internal/model"
)

// The speed trophies.
//
// Unlike the voted awards these are not a decision, they are the top of the
// standings — so the software works them out rather than asking somebody to
// read a table and type three names into another screen, which is what the old
// system required.

// SpeedAwardNames are the club's names for the top three, in order.
//
// Plainly "1st", "2nd", "3rd". Earlier seasons published these as "Fastest in
// Event" and so on; the club asked for the shorter names, so files written from
// 2027 onwards will differ from the archive in that one column.
var SpeedAwardNames = []string{"1st", "2nd", "3rd"}

// TrophyPlaces is how many places in a race are handed a trophy — and so how
// far down a tie has to be run off.
//
// A race night gives 1st, 2nd and 3rd. The championship gives one: it exists to
// decide the season trophy, so a tie for 2nd there is just a tie, and there is
// no third-place matchup in a bracket.
func TrophyPlaces(r model.Race) int {
	if r.Kind == model.RaceChampionship {
		return 1
	}
	return len(SpeedAwardNames)
}

// AwardTypeSpeed is the award type the old system used for these.
const AwardTypeSpeed = "Speed Trophy"

// SpeedAwards works out the top three trophies from the standings.
//
// It derives rather than stores. The speed trophies *are* the top of the
// standings, so keeping a copy of them in the award table only creates a way
// for the two to disagree — after a re-run, a struck-out lane, a corrected
// time. Deriving them means they cannot.
//
// The pace car is skipped. It races, it is ranked, and it can and does finish
// high on a thin night, but it is club equipment and it takes no trophy. That
// is the whole reason Entry.EarnsPoints exists.
//
// A tie for one of these places is refused, because a trophy is handed to one
// person and picking between two equal cars is not the software's call.
func (db *DB) SpeedAwards(ctx context.Context, raceID int64) ([]AwardView, error) {
	// A trophy cannot be shared, and picking between two cars that ran the same
	// average is not the software's call to make. Settle the run-off first.
	ties, err := db.UnsettledTies(ctx, raceID)
	if err != nil {
		return nil, err
	}
	race, err := db.Race(ctx, raceID)
	if err != nil {
		return nil, err
	}
	for _, t := range ties {
		// A tie for a championship place lower down is not a tie for a trophy.
		if t.Settled || t.ForPlace {
			continue
		}
		names := make([]string, 0, len(t.Entries))
		for _, e := range t.Entries {
			names = append(names, fmt.Sprintf("#%d %s", e.CarNumber, e.CarName))
		}
		if t.DeadHeat {
			return nil, fmt.Errorf("the run-off for %s finished level as well — run heat %d again",
				ordinal(t.Place), t.HeatNumber)
		}
		return nil, fmt.Errorf("%s are %s — run that off before setting the trophies",
			strings.Join(names, " and "), t.Describe())
	}

	standings, err := db.Standings(ctx, raceID)
	if err != nil {
		return nil, err
	}

	var out []AwardView
	for _, st := range standings {
		if st.Place == 0 || !st.Entry.EarnsPoints() {
			continue
		}
		i := len(out)
		a := AwardView{Entry: st.Entry}
		a.RaceID = raceID
		a.Name = SpeedAwardNames[i]
		a.AwardType = AwardTypeSpeed
		id := st.Entry.ID
		a.EntryID = &id
		a.Sort = i
		a.Source = model.AwardAuto
		out = append(out, a)
		if len(out) == TrophyPlaces(race) {
			break
		}
	}
	return out, nil
}

// RaceAwards is every trophy for a race: the ones decided on the ballot or by
// hand, and the speed trophies derived from the standings.
//
// A stored award whose name matches a speed trophy wins, so a coordinator can
// still hand "1st" to somebody else — a car disqualified after the fact — and
// have that stick.
func (db *DB) RaceAwards(ctx context.Context, raceID int64) ([]AwardView, error) {
	stored, err := db.Awards(ctx, raceID)
	if err != nil {
		return nil, err
	}
	named := map[string]bool{}
	for _, a := range stored {
		named[a.Name] = true
	}

	speed, err := db.SpeedAwards(ctx, raceID)
	if err != nil {
		// A tie for a trophy is not an error to a caller that only wants to
		// show what has been decided — it means the speed ones have not been.
		speed = nil
	}

	out := make([]AwardView, 0, len(stored)+len(speed))
	for _, a := range speed {
		if !named[a.Name] {
			out = append(out, a)
		}
	}
	out = append(out, stored...)
	return out, nil
}

// SetAwardWinner points an award at a different car, for the cases the software
// cannot know about — a car that was disqualified after the fact, or a trophy
// the club decided to give to somebody else.
func (db *DB) SetAwardWinner(ctx context.Context, awardID, entryID int64) error {
	entry, err := db.Entry(ctx, entryID)
	if err != nil {
		return err
	}
	if !entry.EarnsPoints() {
		if entry.IsControl {
			return fmt.Errorf("the CONTROL car takes no trophies")
		}
		return fmt.Errorf("car %d was ruled ineligible, so it cannot take an award", entry.CarNumber)
	}
	_, err = db.ExecContext(ctx,
		`UPDATE award SET entry_id = ?, source = ? WHERE id = ?`,
		entryID, model.AwardManual, awardID)
	return err
}
