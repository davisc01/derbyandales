package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/davisc01/derbyandales/internal/model"
)

// UnscheduledEntries lists the cars checked in for a race that are in none of
// its heats — cars that arrived after the schedule was built.
//
// Check-in stays open once racing starts, because people turn up late to a bar,
// and until now a car checked in then was simply never called to the track:
// it sat in the standings with no runs and nobody was told. Run-off heats do
// not count as being scheduled; only the four qualifying runs do.
func (db *DB) UnscheduledEntries(ctx context.Context, raceID int64) ([]EntryView, error) {
	all, err := db.RacingEntries(ctx, raceID)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT hl.entry_id FROM heat_lane hl
		JOIN heat h ON h.id = hl.heat_id
		WHERE h.race_id = ? AND h.phase = ? AND h.runoff_place IS NULL
		  AND hl.entry_id IS NOT NULL`, raceID, model.PhaseQualifying)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scheduled := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		scheduled[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Nothing is late if there is no schedule yet: every car is simply waiting
	// for check-in to close.
	if len(scheduled) == 0 {
		return nil, nil
	}
	var out []EntryView
	for _, e := range all {
		if !scheduled[e.ID] {
			out = append(out, e)
		}
	}
	return out, nil
}

// ErrHeatAlreadyRun refuses a restructure that would overwrite a heat with
// times in it.
var ErrHeatAlreadyRun = errors.New("a heat in that part of the night has already been run")

// ReplaceUnrunHeats rebuilds a race's qualifying heats after heat number after.
// Each element of heats is one heat, lane by lane; a nil entry is an empty lane.
//
// It refuses if any of those heats has a time. The caller chose after from
// what had run when it looked, and the timer may have landed a result since —
// overwriting that would lose a heat the cars really ran.
func (db *DB) ReplaceUnrunHeats(ctx context.Context, raceID int64, after int, heats [][]*int64) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		var timed int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM heat_lane hl JOIN heat h ON h.id = hl.heat_id
			WHERE h.race_id = ? AND h.number > ? AND hl.finish_time IS NOT NULL`,
			raceID, after).Scan(&timed); err != nil {
			return err
		}
		if timed > 0 {
			return ErrHeatAlreadyRun
		}
		// Run-off heats only exist once every qualifying heat has run, so
		// there are none after an unrun heat; refusing keeps it that way.
		var other int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM heat
			WHERE race_id = ? AND number > ? AND (phase != ? OR runoff_place IS NOT NULL)`,
			raceID, after, model.PhaseQualifying).Scan(&other); err != nil {
			return err
		}
		if other > 0 {
			return errors.New("the rest of the night is a run-off or a bracket, which cannot be rebuilt")
		}

		if _, err := tx.ExecContext(ctx,
			`DELETE FROM heat WHERE race_id = ? AND number > ?`, raceID, after); err != nil {
			return err
		}
		for i, heat := range heats {
			number := after + 1 + i
			res, err := tx.ExecContext(ctx,
				`INSERT INTO heat (race_id, number, phase, status) VALUES (?,?,?,?)`,
				raceID, number, model.PhaseQualifying, model.HeatPending)
			if err != nil {
				return fmt.Errorf("insert heat %d: %w", number, err)
			}
			heatID, err := res.LastInsertId()
			if err != nil {
				return err
			}
			for lane, entryID := range heat {
				var id any
				if entryID != nil {
					id = *entryID
				}
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO heat_lane (heat_id, lane, entry_id) VALUES (?,?,?)`,
					heatID, lane+1, id); err != nil {
					return fmt.Errorf("insert heat %d lane %d: %w", number, lane+1, err)
				}
			}
		}
		return nil
	})
}
