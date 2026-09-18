package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/davisc01/derbyandales/internal/model"
)

// --- racers ------------------------------------------------------------------

// FindOrCreateRacer returns the racer with this name in the season, creating
// them if needed.
//
// Identity is a row, not a name string: that is what stops a typo splitting
// someone's season in two the way the old tracker did. Check-in should offer
// existing racers rather than calling this with free text.
func (db *DB) FindOrCreateRacer(ctx context.Context, seasonID int64, first, last string) (model.Racer, error) {
	r := model.Racer{SeasonID: seasonID, FirstName: first, LastName: last}

	err := db.QueryRowContext(ctx,
		`SELECT id FROM racer WHERE season_id = ? AND first_name = ? AND last_name = ?`,
		seasonID, first, last).Scan(&r.ID)
	if err == nil {
		return r, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return r, err
	}

	res, err := db.ExecContext(ctx,
		`INSERT INTO racer (season_id, first_name, last_name) VALUES (?, ?, ?)`,
		seasonID, first, last)
	if err != nil {
		return r, fmt.Errorf("insert racer: %w", err)
	}
	r.ID, err = res.LastInsertId()
	return r, err
}

// Racer loads one racer.
func (db *DB) Racer(ctx context.Context, id int64) (model.Racer, error) {
	var r model.Racer
	err := db.QueryRowContext(ctx,
		`SELECT id, season_id, first_name, last_name FROM racer WHERE id = ?`, id).
		Scan(&r.ID, &r.SeasonID, &r.FirstName, &r.LastName)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// Racers lists a season's racers.
func (db *DB) Racers(ctx context.Context, seasonID int64) ([]model.Racer, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, season_id, first_name, last_name FROM racer
		 WHERE season_id = ? ORDER BY last_name, first_name`, seasonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Racer
	for rows.Next() {
		var r model.Racer
		if err := rows.Scan(&r.ID, &r.SeasonID, &r.FirstName, &r.LastName); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- entries -----------------------------------------------------------------

// EntryView is an entry joined to its racer, which is what every screen needs.
type EntryView struct {
	model.Entry
	FirstName string
	LastName  string
}

// FullName renders the driver the way standings.csv expects.
func (e EntryView) FullName() string {
	return model.Racer{FirstName: e.FirstName, LastName: e.LastName}.FullName()
}

const entryCols = `e.id, e.race_id, e.racer_id, e.car_number, e.car_name, e.photo_id,
	e.is_control, e.excluded, e.exclusion_reason, e.checked_in_at, e.note,
	r.first_name, r.last_name`

func scanEntry(sc interface{ Scan(...any) error }) (EntryView, error) {
	var e EntryView
	var photoID, checkedIn sql.NullInt64
	var isControl, excluded int
	err := sc.Scan(&e.ID, &e.RaceID, &e.RacerID, &e.CarNumber, &e.CarName, &photoID,
		&isControl, &excluded, &e.ExclusionReason, &checkedIn, &e.Note,
		&e.FirstName, &e.LastName)
	if err != nil {
		return e, err
	}
	e.PhotoID = nullInt(photoID)
	e.IsControl = isControl != 0
	e.Excluded = excluded != 0
	e.CheckedInAt = fromUnixPtr(checkedIn)
	return e, nil
}

// CreateEntry checks a car in.
func (db *DB) CreateEntry(ctx context.Context, e model.Entry) (model.Entry, error) {
	if e.CheckedInAt == nil {
		now := time.Now()
		e.CheckedInAt = &now
	}
	res, err := db.ExecContext(ctx, `
		INSERT INTO entry (race_id, racer_id, car_number, car_name, photo_id,
			is_control, excluded, exclusion_reason, checked_in_at, note)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		e.RaceID, e.RacerID, e.CarNumber, e.CarName, ptrArg(e.PhotoID),
		boolInt(e.IsControl), boolInt(e.Excluded), e.ExclusionReason,
		unixPtr(e.CheckedInAt), e.Note)
	if err != nil {
		return e, fmt.Errorf("insert entry: %w", err)
	}
	e.ID, err = res.LastInsertId()
	return e, err
}

// Entries lists a race's entries in car-number order.
func (db *DB) Entries(ctx context.Context, raceID int64) ([]EntryView, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+entryCols+` FROM entry e
		 JOIN racer r ON r.id = e.racer_id
		 WHERE e.race_id = ? ORDER BY e.car_number`, raceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EntryView
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Entry loads one entry.
func (db *DB) Entry(ctx context.Context, id int64) (EntryView, error) {
	row := db.QueryRowContext(ctx,
		`SELECT `+entryCols+` FROM entry e JOIN racer r ON r.id = e.racer_id WHERE e.id = ?`, id)
	e, err := scanEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// UpdateEntry writes back the mutable fields.
//
// racer_id is one of them: a car checked in against the wrong person is a
// different correction from a misspelled name. RenameRacer fixes the name
// everywhere it appears; this moves one car to somebody else and leaves both
// their other cars alone.
func (db *DB) UpdateEntry(ctx context.Context, e model.Entry) error {
	_, err := db.ExecContext(ctx, `
		UPDATE entry SET racer_id=?, car_number=?, car_name=?, photo_id=?, is_control=?,
			excluded=?, exclusion_reason=?, note=?
		WHERE id=?`,
		e.RacerID, e.CarNumber, e.CarName, ptrArg(e.PhotoID), boolInt(e.IsControl),
		boolInt(e.Excluded), e.ExclusionReason, e.Note, e.ID)
	return err
}

// DeleteEntry removes an entry. Only possible before a schedule exists, since
// afterwards the heats would refer to a car that is not there.
func (db *DB) DeleteEntry(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM entry WHERE id = ?`, id)
	return err
}

// RacingEntries lists the entries that take part in the schedule: everything
// checked in, including the CONTROL car and excluded cars.
//
// Excluded cars still race — they are ineligible for the standings, not for the
// track — so they must be in the schedule or the lanes will not fill.
func (db *DB) RacingEntries(ctx context.Context, raceID int64) ([]EntryView, error) {
	all, err := db.Entries(ctx, raceID)
	if err != nil {
		return nil, err
	}
	out := make([]EntryView, 0, len(all))
	for _, e := range all {
		if e.CheckedInAt != nil {
			out = append(out, e)
		}
	}
	return out, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// RenameRacer corrects a racer's name everywhere it appears, which is every
// entry they have made: the name lives on the racer, not on the entries.
//
// A name that already belongs to another racer this season is refused rather
// than silently merged. Two rows with one name are either the same person —
// which is a merge, and changes their points — or two people, and only
// somebody who knows them can say which.
func (db *DB) RenameRacer(ctx context.Context, racerID int64, first, last string) error {
	first, last = strings.TrimSpace(first), strings.TrimSpace(last)
	if first == "" && last == "" {
		return errors.New("a racer needs a name")
	}
	var seasonID int64
	if err := db.QueryRowContext(ctx, `SELECT season_id FROM racer WHERE id = ?`, racerID).Scan(&seasonID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var other int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM racer WHERE season_id = ? AND first_name = ? AND last_name = ? AND id != ?`,
		seasonID, first, last, racerID).Scan(&other)
	if err == nil {
		return fmt.Errorf("%s %s is already a racer this season — merge the two instead if they are the same person", first, last)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = db.ExecContext(ctx,
		`UPDATE racer SET first_name = ?, last_name = ? WHERE id = ?`, first, last, racerID)
	return err
}

// MergeRacers folds one racer into another: every entry, recorded result and
// points adjustment moves across, and the duplicate is removed.
//
// This is the fix for a name typed two ways at check-in, which otherwise splits
// one person's season in two and can keep them out of a wildcard spot. It
// reports the races in which both had a car. There the frozen points are now
// wrong — the rule is best car only, and each half earned points for its own
// best car — and only a recompute puts that right.
func (db *DB) MergeRacers(ctx context.Context, keepID, dropID int64) (shared int, err error) {
	if keepID == dropID {
		return 0, errors.New("that is the same racer")
	}
	var keepSeason, dropSeason int64
	if err := db.QueryRowContext(ctx, `SELECT season_id FROM racer WHERE id = ?`, keepID).Scan(&keepSeason); err != nil {
		return 0, ErrNotFound
	}
	if err := db.QueryRowContext(ctx, `SELECT season_id FROM racer WHERE id = ?`, dropID).Scan(&dropSeason); err != nil {
		return 0, ErrNotFound
	}
	if keepSeason != dropSeason {
		return 0, errors.New("those racers are in different seasons")
	}

	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT a.race_id) FROM entry a
		JOIN entry b ON b.race_id = a.race_id
		WHERE a.racer_id = ? AND b.racer_id = ?`, keepID, dropID).Scan(&shared); err != nil {
		return 0, err
	}

	err = db.Tx(ctx, func(tx *sql.Tx) error {
		for _, table := range []string{"entry", "race_result", "adjustment"} {
			if _, err := tx.ExecContext(ctx,
				`UPDATE `+table+` SET racer_id = ? WHERE racer_id = ?`, keepID, dropID); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM racer WHERE id = ?`, dropID)
		return err
	})
	return shared, err
}
