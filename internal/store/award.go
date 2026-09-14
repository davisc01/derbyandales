package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/davisc01/derbyandales/internal/model"
)

// The speed trophies.
//
// Unlike the voted awards these are not a decision, they are the top of the
// standings — so the software works them out rather than asking somebody to
// read a table and type three names into another screen, which is what the old
// system required.

// SpeedAwardNames are the club's names for the top three, in order.
var SpeedAwardNames = []string{
	"Fastest in Event",
	"2nd Fastest in Event",
	"3rd Fastest in Event",
}

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
