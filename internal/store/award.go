package store

import (
	"context"
	"database/sql"
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

// AwardTypeSpeed is the award type the old system used for these.
const AwardTypeSpeed = "Speed Trophy"

// GenerateSpeedAwards sets the top three trophies from the standings.
//
// The pace car is skipped. It races, it is ranked, and it can and does finish
// high on a thin night — but it is club equipment and it takes no trophy. That
// is the whole reason Entry.EarnsPoints exists, and this is the last place in
// the application that needed it.
//
// Awards already declared from the ballot are left alone: this replaces the
// automatic ones only.
func (db *DB) GenerateSpeedAwards(ctx context.Context, raceID int64) ([]AwardView, error) {
	// A trophy cannot be shared, and picking between two cars that ran the same
	// average is not the software's call to make. Settle the run-off first.
	ties, err := db.UnsettledTies(ctx, raceID)
	if err != nil {
		return nil, err
	}
	for _, t := range ties {
		if t.Settled {
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

	var winners []Standing
	for _, st := range standings {
		if st.Place == 0 || !st.Entry.EarnsPoints() {
			continue
		}
		winners = append(winners, st)
		if len(winners) == len(SpeedAwardNames) {
			break
		}
	}

	err = db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM award WHERE race_id = ? AND source = ?`,
			raceID, model.AwardAuto); err != nil {
			return err
		}
		for i, w := range winners {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO award (race_id, name, award_type, entry_id, sort, source)
				VALUES (?,?,?,?,?,?)`,
				raceID, SpeedAwardNames[i], AwardTypeSpeed, w.Entry.ID, i, model.AwardAuto); err != nil {
				return fmt.Errorf("saving the %s award: %w", SpeedAwardNames[i], err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return db.Awards(ctx, raceID)
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
