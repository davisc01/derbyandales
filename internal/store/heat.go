package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/schedule"
	"github.com/davisc01/derbyandales/internal/scoring"
)

// HeatView is a heat with its lanes resolved to entries, which is what the
// race screen and the displays need.
type HeatView struct {
	model.Heat
	Lanes []LaneView
}

// RunOff reports a heat that exists to settle a tie for a trophy. Its times
// decide an order and nothing else — they are not part of any average.
func (h HeatView) RunOff() bool { return h.RunoffPlace != nil }

// LaneView is one lane of a heat, with the car in it.
type LaneView struct {
	Lane      int
	EntryID   *int64
	CarNumber int
	CarName   string
	// PhotoID is the car's picture, which the impound screen needs: an official
	// hunting for a car on a shelf recognises it far faster than a number.
	PhotoID     *int64
	DriverFirst string
	DriverLast  string
	FinishTime  *float64
	FinishPlace *int
	Ignored     bool
}

// Bye reports whether this lane runs empty.
func (l LaneView) Bye() bool { return l.EntryID == nil }

// DriverName renders the driver's full name.
func (l LaneView) DriverName() string {
	return model.Racer{FirstName: l.DriverFirst, LastName: l.DriverLast}.FullName()
}

// Complete reports whether every lane in use has a time.
func (h HeatView) Complete() bool {
	for _, l := range h.Lanes {
		if l.EntryID != nil && l.FinishTime == nil {
			return false
		}
	}
	return true
}

// LaneMask returns the bitmask of lanes in use, for masking the timer.
func (h HeatView) LaneMask() uint {
	var mask uint
	for _, l := range h.Lanes {
		if l.EntryID != nil {
			mask |= 1 << uint(l.Lane-1)
		}
	}
	return mask
}

// SaveSchedule writes a generated schedule, replacing any existing one.
//
// entries must be in the same order as the car indices in the schedule.
func (db *DB) SaveSchedule(ctx context.Context, raceID int64, s *schedule.Schedule, entries []EntryView) error {
	if err := schedule.Validate(s); err != nil {
		// Refuse to store a schedule that breaks the club's rule. Discovering
		// this at the track means a racer called to two lanes at once.
		return fmt.Errorf("refusing to save an invalid schedule: %w", err)
	}
	if len(entries) != s.Cars {
		return fmt.Errorf("schedule is for %d cars but %d entries were given", s.Cars, len(entries))
	}

	return db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM heat WHERE race_id = ? AND phase = ?`, raceID, model.PhaseQualifying); err != nil {
			return err
		}

		for i, heat := range s.Heats {
			res, err := tx.ExecContext(ctx,
				`INSERT INTO heat (race_id, number, phase, status) VALUES (?,?,?,?)`,
				raceID, i+1, model.PhaseQualifying, model.HeatPending)
			if err != nil {
				return fmt.Errorf("insert heat %d: %w", i+1, err)
			}
			heatID, err := res.LastInsertId()
			if err != nil {
				return err
			}

			for lane, car := range heat {
				var entryID any
				if car != schedule.Bye {
					entryID = entries[car].ID
				}
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO heat_lane (heat_id, lane, entry_id) VALUES (?,?,?)`,
					heatID, lane+1, entryID); err != nil {
					return fmt.Errorf("insert heat %d lane %d: %w", i+1, lane+1, err)
				}
			}
		}
		return nil
	})
}

const heatLaneCols = `hl.lane, hl.entry_id, hl.finish_time, hl.finish_place, hl.ignored,
	e.car_number, e.car_name, e.photo_id, r.first_name, r.last_name`

// Heats lists a race's heats with their lanes, in running order.
func (db *DB) Heats(ctx context.Context, raceID int64) ([]HeatView, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, race_id, number, phase, bracket_matchup_id, status, armed_at, completed_at, runoff_place
		 FROM heat WHERE race_id = ? ORDER BY number`, raceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var heats []HeatView
	for rows.Next() {
		h, err := scanHeat(rows)
		if err != nil {
			return nil, err
		}
		heats = append(heats, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range heats {
		lanes, err := db.heatLanes(ctx, heats[i].ID)
		if err != nil {
			return nil, err
		}
		heats[i].Lanes = lanes
	}
	return heats, nil
}

func scanHeat(sc interface{ Scan(...any) error }) (HeatView, error) {
	var h HeatView
	var matchupID, armedAt, completedAt, runoff sql.NullInt64
	err := sc.Scan(&h.ID, &h.RaceID, &h.Number, &h.Phase, &matchupID,
		&h.Status, &armedAt, &completedAt, &runoff)
	if err != nil {
		return h, err
	}
	h.BracketMatchupID = nullInt(matchupID)
	h.ArmedAt = fromUnixPtr(armedAt)
	h.CompletedAt = fromUnixPtr(completedAt)
	h.RunoffPlace = nullIntAsInt(runoff)
	return h, nil
}

func (db *DB) heatLanes(ctx context.Context, heatID int64) ([]LaneView, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+heatLaneCols+` FROM heat_lane hl
		 LEFT JOIN entry e ON e.id = hl.entry_id
		 LEFT JOIN racer r ON r.id = e.racer_id
		 WHERE hl.heat_id = ? ORDER BY hl.lane`, heatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LaneView
	for rows.Next() {
		var l LaneView
		var entryID sql.NullInt64
		var finishTime sql.NullFloat64
		var finishPlace sql.NullInt64
		var ignored int
		var carNumber, photoID sql.NullInt64
		var carName, first, last sql.NullString

		if err := rows.Scan(&l.Lane, &entryID, &finishTime, &finishPlace, &ignored,
			&carNumber, &carName, &photoID, &first, &last); err != nil {
			return nil, err
		}
		l.PhotoID = nullInt(photoID)
		l.EntryID = nullInt(entryID)
		l.FinishTime = nullFloat(finishTime)
		l.FinishPlace = nullIntAsInt(finishPlace)
		l.Ignored = ignored != 0
		l.CarNumber = int(carNumber.Int64)
		l.CarName = carName.String
		l.DriverFirst = first.String
		l.DriverLast = last.String
		out = append(out, l)
	}
	return out, rows.Err()
}

// Heat loads one heat with its lanes.
func (db *DB) Heat(ctx context.Context, id int64) (HeatView, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, race_id, number, phase, bracket_matchup_id, status, armed_at, completed_at, runoff_place
		 FROM heat WHERE id = ?`, id)
	h, err := scanHeat(row)
	if errors.Is(err, sql.ErrNoRows) {
		return h, ErrNotFound
	}
	if err != nil {
		return h, err
	}
	h.Lanes, err = db.heatLanes(ctx, h.ID)
	return h, err
}

// NextHeat returns the lowest-numbered heat that has not been run.
//
// "Not run" is defined by the results themselves rather than by a status flag,
// so a heat that was re-run or edited is still found correctly.
func (db *DB) NextHeat(ctx context.Context, raceID int64) (HeatView, error) {
	var id int64
	err := db.QueryRowContext(ctx, `
		SELECT h.id FROM heat h
		WHERE h.race_id = ?
		  AND EXISTS (
		      SELECT 1 FROM heat_lane hl
		      WHERE hl.heat_id = h.id AND hl.entry_id IS NOT NULL AND hl.finish_time IS NULL
		  )
		ORDER BY h.number LIMIT 1`, raceID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return HeatView{}, ErrNotFound
	}
	if err != nil {
		return HeatView{}, err
	}
	return db.Heat(ctx, id)
}

// SetHeatStatus records a heat's progress.
func (db *DB) SetHeatStatus(ctx context.Context, heatID int64, status model.HeatStatus) error {
	var stamp any
	col := ""
	switch status {
	case model.HeatArmed:
		col, stamp = "armed_at", time.Now().Unix()
	case model.HeatComplete:
		col, stamp = "completed_at", time.Now().Unix()
	}
	if col == "" {
		_, err := db.ExecContext(ctx, `UPDATE heat SET status = ? WHERE id = ?`, status, heatID)
		return err
	}
	_, err := db.ExecContext(ctx,
		`UPDATE heat SET status = ?, `+col+` = ? WHERE id = ?`, status, stamp, heatID)
	return err
}

// RecordHeatResults writes the finish times for a heat and derives the places.
//
// Places are computed from the times rather than taken from the timer, so they
// are consistent with what is published and ties are handled the same way
// everywhere.
func (db *DB) RecordHeatResults(ctx context.Context, heatID int64, times map[int]float64) error {
	if len(times) == 0 {
		return fmt.Errorf("no results given for heat %d", heatID)
	}
	places := scoring.PlaceInHeat(times)

	return db.Tx(ctx, func(tx *sql.Tx) error {
		for lane, t := range times {
			if _, err := tx.ExecContext(ctx,
				`UPDATE heat_lane SET finish_time = ?, finish_place = ?
				 WHERE heat_id = ? AND lane = ? AND entry_id IS NOT NULL`,
				t, places[lane], heatID, lane); err != nil {
				return fmt.Errorf("record lane %d: %w", lane, err)
			}
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE heat SET status = ?, completed_at = ? WHERE id = ?`,
			model.HeatComplete, time.Now().Unix(), heatID); err != nil {
			return err
		}
		return nil
	})
}

// ClearHeatResults wipes a heat so it can be re-run.
func (db *DB) ClearHeatResults(ctx context.Context, heatID int64) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE heat_lane SET finish_time = NULL, finish_place = NULL, ignored = 0
			 WHERE heat_id = ?`, heatID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE heat SET status = ?, armed_at = NULL, completed_at = NULL WHERE id = ?`,
			model.HeatPending, heatID)
		return err
	})
}

// SetLaneIgnored strikes out one run — a false start, or a car that came apart
// through no fault of the timer. The run stays visible but is excluded from
// scoring.
func (db *DB) SetLaneIgnored(ctx context.Context, heatID, lane int64, ignored bool) error {
	_, err := db.ExecContext(ctx,
		`UPDATE heat_lane SET ignored = ? WHERE heat_id = ? AND lane = ?`,
		boolInt(ignored), heatID, lane)
	return err
}

// HeatProgress counts a race's heats and how many have been run.
type HeatProgress struct {
	Total     int
	Completed int
}

// Progress reports how far through a race we are.
func (db *DB) Progress(ctx context.Context, raceID int64) (HeatProgress, error) {
	var p HeatProgress
	err := db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			SUM(CASE WHEN done THEN 1 ELSE 0 END)
		FROM (
			SELECT h.id,
			       NOT EXISTS (
			           SELECT 1 FROM heat_lane hl
			           WHERE hl.heat_id = h.id AND hl.entry_id IS NOT NULL
			             AND hl.finish_time IS NULL
			       ) AS done
			FROM heat h WHERE h.race_id = ?
		)`, raceID).Scan(&p.Total, &p.Completed)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	return p, err
}

// RunsByEntry collects every recorded run for a race, keyed by entry, ready for
// the scoring package.
//
// Excluded entries are left out: they race, but they are not in the standings.
func (db *DB) RunsByEntry(ctx context.Context, raceID int64) (map[int64][]scoring.Run, error) {
	return db.runsByEntry(ctx, raceID, true)
}

// AllRunsByEntry collects every recorded run, including the excluded cars that
// RunsByEntry leaves out.
//
// This is for asking whether a heat was timed properly rather than who won. An
// excluded car is still a car that went down the track with a clock on it, so
// it is evidence about the heat even though it is not in the standings.
func (db *DB) AllRunsByEntry(ctx context.Context, raceID int64) (map[int64][]scoring.Run, error) {
	return db.runsByEntry(ctx, raceID, false)
}

func (db *DB) runsByEntry(ctx context.Context, raceID int64, scoredOnly bool) (map[int64][]scoring.Run, error) {
	where := ""
	if scoredOnly {
		where = " AND e.excluded = 0"
	}
	rows, err := db.QueryContext(ctx, `
		SELECT hl.entry_id, h.number, hl.lane, hl.finish_time, hl.ignored
		FROM heat_lane hl
		JOIN heat h ON h.id = hl.heat_id
		JOIN entry e ON e.id = hl.entry_id
		WHERE h.race_id = ? AND hl.finish_time IS NOT NULL
		  AND h.runoff_place IS NULL`+where+`
		ORDER BY h.number, hl.lane`, raceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64][]scoring.Run{}
	for rows.Next() {
		var entryID int64
		var run scoring.Run
		var ignored int
		if err := rows.Scan(&entryID, &run.Heat, &run.Lane, &run.Time, &ignored); err != nil {
			return nil, err
		}
		run.Ignored = ignored != 0
		out[entryID] = append(out[entryID], run)
	}
	return out, rows.Err()
}

// Standing is one row of the race results, ready to display or publish.
type Standing struct {
	scoring.Result
	Entry EntryView
}

// Standings scores a race and returns the results in finishing order.
//
// A tie that reaches the podium and has been run off is split here, using the
// run-off's order. The tied cars keep their identical averages — the run-off
// settled which trophy each takes, not how fast they went.
func (db *DB) Standings(ctx context.Context, raceID int64) ([]Standing, error) {
	runs, err := db.RunsByEntry(ctx, raceID)
	if err != nil {
		return nil, err
	}
	entries, err := db.Entries(ctx, raceID)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]EntryView, len(entries))
	for _, e := range entries {
		byID[e.ID] = e
	}

	results := scoring.Standings(runs)
	order, _, err := db.runOffOrder(ctx, raceID)
	if err != nil {
		return nil, err
	}
	applyRunOffs(results, order)
	out := make([]Standing, 0, len(results))
	for _, r := range results {
		out = append(out, Standing{Result: r, Entry: byID[r.EntryID]})
	}
	return out, nil
}

// HeatAnomalyView is a flagged heat with enough about the cars to describe it
// on screen.
type HeatAnomalyView struct {
	scoring.HeatAnomaly
	HeatID int64

	// Entries are the cars involved. Deliberately not named Cars: the embedded
	// anomaly already has a Cars count, and a field of the same name would
	// shadow it silently.
	Entries []EntryView
}

// HeatAnomalies looks for heats that were a fault rather than a result.
//
// It is meaningful only once a race is over, because it compares each car's run
// against the rest of that car's night. Half way through, everybody's slowest
// run so far is just the slowest of two.
func (db *DB) HeatAnomalies(ctx context.Context, raceID int64) ([]HeatAnomalyView, error) {
	runs, err := db.AllRunsByEntry(ctx, raceID)
	if err != nil {
		return nil, err
	}
	found := scoring.HeatAnomalies(runs)
	if len(found) == 0 {
		return nil, nil
	}

	entries, err := db.Entries(ctx, raceID)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]EntryView, len(entries))
	for _, e := range entries {
		byID[e.ID] = e
	}

	heats, err := db.Heats(ctx, raceID)
	if err != nil {
		return nil, err
	}
	heatIDs := make(map[int]int64, len(heats))
	for _, h := range heats {
		heatIDs[h.Number] = h.ID
	}

	out := make([]HeatAnomalyView, 0, len(found))
	for _, a := range found {
		v := HeatAnomalyView{HeatAnomaly: a, HeatID: heatIDs[a.Heat]}
		for _, id := range a.EntryIDs {
			if e, ok := byID[id]; ok {
				v.Entries = append(v.Entries, e)
			}
		}
		out = append(out, v)
	}
	return out, nil
}
