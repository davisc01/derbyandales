package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/season"
)

// Season-wide storage: frozen race results and manual point adjustments.
//
// The old system kept all of this in a second application with its own
// database, synchronised by exporting a CSV from one and uploading it to the
// other. Everything here reads the same rows the race did.

// Rules reads a season's settings in the form the calculations want them.
func (db *DB) Rules(ctx context.Context, seasonID int64) (season.Rules, error) {
	s, err := db.Season(ctx, seasonID)
	if err != nil {
		return season.Rules{}, err
	}
	return season.Rules{
		AutoQualPlaces: s.AutoQualPlaces,
		MaxEntries:     s.MaxChampionshipEntry,
		CountControl:   s.PointsCountControl,
	}, nil
}

// --- freezing ------------------------------------------------------------------

// FreezeRace records a finished race's standings and the wildcard points that
// follow from them, replacing anything frozen before.
//
// It is called when the last heat lands, and again by an explicit recompute. It
// is deliberately not called when a setting changes: a race that has been run is
// a fact, and the season should not quietly rewrite itself around it.
func (db *DB) FreezeRace(ctx context.Context, raceID int64) ([]season.PointAward, error) {
	race, err := db.Race(ctx, raceID)
	if err != nil {
		return nil, err
	}
	// The championship is the thing the points decide; it cannot also feed them.
	if race.Kind != model.RacePoints {
		return nil, fmt.Errorf("%s is the championship, so it does not award season points", raceName(race))
	}

	rules, err := db.Rules(ctx, race.SeasonID)
	if err != nil {
		return nil, err
	}
	standings, err := db.Standings(ctx, raceID)
	if err != nil {
		return nil, err
	}

	finishes := make([]season.Finish, 0, len(standings))
	for _, st := range standings {
		finishes = append(finishes, finishFrom(race, st))
	}
	awards := season.RacePoints(finishes, rules)

	byEntry := make(map[int64]season.Finish, len(finishes))
	for _, f := range finishes {
		byEntry[f.EntryID] = f
	}

	now := time.Now()
	err = db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM race_result WHERE race_id = ?`, raceID); err != nil {
			return err
		}
		for _, a := range awards {
			f := byEntry[a.EntryID]
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO race_result
					(race_id, entry_id, racer_id, place, average, heats, points, total_racers, frozen_at)
				VALUES (?,?,?,?,?,?,?,?,?)`,
				raceID, a.EntryID, a.RacerID, f.Place, f.Average, f.Heats,
				a.Points, a.Field, unix(now)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("saving the season points for %s: %w", raceName(race), err)
	}
	return awards, nil
}

// finishFrom converts a standings row into the form the season rules take.
func finishFrom(race model.Race, st Standing) season.Finish {
	return season.Finish{
		EntryID:    st.Entry.ID,
		RacerID:    st.Entry.RacerID,
		RaceID:     race.ID,
		RaceNumber: race.Number,
		Driver:     st.Entry.FullName(),
		CarName:    st.Entry.CarName,
		Place:      st.Place,
		Average:    st.Average,
		Heats:      st.Heats,
		IsControl:  st.Entry.IsControl,
	}
}

// raceName is what to call a race in a message someone reads at the venue.
func raceName(r model.Race) string {
	if r.Name != "" {
		return r.Name
	}
	if r.Kind == model.RaceChampionship {
		return "the championship"
	}
	return fmt.Sprintf("race %d", r.Number)
}

// RaceFrozen reports whether a race's result has been recorded for the season.
func (db *DB) RaceFrozen(ctx context.Context, raceID int64) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM race_result WHERE race_id = ?`, raceID).Scan(&n)
	return n > 0, err
}

// FrozenAt reports when a race's result was recorded, or the zero time.
func (db *DB) FrozenAt(ctx context.Context, raceID int64) (time.Time, error) {
	var at sql.NullInt64
	err := db.QueryRowContext(ctx,
		`SELECT MAX(frozen_at) FROM race_result WHERE race_id = ?`, raceID).Scan(&at)
	if err != nil || !at.Valid {
		return time.Time{}, err
	}
	return fromUnix(at.Int64), nil
}

// --- reading the season --------------------------------------------------------

// SeasonFinish is one frozen result with the racer and race attached.
type SeasonFinish struct {
	season.Finish
	Points int
	Field  int
	CarNum int
}

// SeasonFinishes returns every frozen result of a season's points races, in
// race then place order.
//
// Races that have not been frozen contribute nothing, which is what makes the
// season standings update a race at a time as the year goes on.
func (db *DB) SeasonFinishes(ctx context.Context, seasonID int64) ([]SeasonFinish, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT rr.entry_id, rr.racer_id, rr.race_id, r.number,
		       racer.first_name, racer.last_name, e.car_name, e.car_number,
		       rr.place, rr.average, rr.heats, rr.points, rr.total_racers, e.is_control
		FROM race_result rr
		JOIN race  r     ON r.id = rr.race_id
		JOIN entry e     ON e.id = rr.entry_id
		JOIN racer racer ON racer.id = rr.racer_id
		WHERE r.season_id = ? AND r.kind = 'points'
		ORDER BY r.number, rr.place`, seasonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SeasonFinish
	for rows.Next() {
		var f SeasonFinish
		var first, last string
		var isControl int
		if err := rows.Scan(&f.EntryID, &f.RacerID, &f.RaceID, &f.RaceNumber,
			&first, &last, &f.CarName, &f.CarNum,
			&f.Place, &f.Average, &f.Heats, &f.Points, &f.Field, &isControl); err != nil {
			return nil, err
		}
		f.Driver = model.Racer{FirstName: first, LastName: last}.FullName()
		f.IsControl = isControl != 0
		out = append(out, f)
	}
	return out, rows.Err()
}

// plainFinishes strips the storage fields back to what the rules take.
func plainFinishes(in []SeasonFinish) []season.Finish {
	out := make([]season.Finish, 0, len(in))
	for _, f := range in {
		out = append(out, f.Finish)
	}
	return out
}

// Qualifiers returns the season's seeded auto-qualifier list.
func (db *DB) Qualifiers(ctx context.Context, seasonID int64) ([]season.Slot, error) {
	finishes, err := db.SeasonFinishes(ctx, seasonID)
	if err != nil {
		return nil, err
	}
	rules, err := db.Rules(ctx, seasonID)
	if err != nil {
		return nil, err
	}
	return season.Qualifiers(plainFinishes(finishes), rules), nil
}

// Wildcard returns the season's points standings.
func (db *DB) Wildcard(ctx context.Context, seasonID int64) ([]season.WildcardRow, error) {
	finishes, err := db.SeasonFinishes(ctx, seasonID)
	if err != nil {
		return nil, err
	}
	rules, err := db.Rules(ctx, seasonID)
	if err != nil {
		return nil, err
	}
	slots, err := db.Qualifiers(ctx, seasonID)
	if err != nil {
		return nil, err
	}
	adjustments, err := db.Adjustments(ctx, seasonID)
	if err != nil {
		return nil, err
	}

	earned := map[int64]int{}
	names := map[int64]string{}
	// A racer counts for the season only once they have entered a car that is
	// not the pace car. Without this the CONTROL car's driver appears in the
	// standings as a competitor on zero points, which is how "Derby Ales" came
	// to be ranked 36th in the club's own published 2026 table.
	competed := map[int64]bool{}
	for _, f := range finishes {
		names[f.RacerID] = f.Driver
		earned[f.RacerID] += f.Points
		if !f.IsControl {
			competed[f.RacerID] = true
		}
	}

	held := map[int64]int{}
	for _, s := range slots {
		held[s.RacerID]++
	}
	adjusted := map[int64]int{}
	for _, a := range adjustments {
		adjusted[a.RacerID] += a.Points
		// An adjustment is a deliberate statement that this racer has a season,
		// so it brings them into the table even before they have scored.
		if _, ok := names[a.RacerID]; !ok {
			names[a.RacerID] = a.Driver
			competed[a.RacerID] = true
		}
	}

	racers := make([]season.RacerPoints, 0, len(names))
	for id, name := range names {
		if !competed[id] {
			continue
		}
		racers = append(racers, season.RacerPoints{
			RacerID:  id,
			Name:     name,
			Earned:   earned[id],
			Adjusted: adjusted[id],
			Slots:    held[id],
		})
	}
	return season.Wildcard(racers, rules), nil
}

// CompetingRacers lists the people who have entered a car of their own in a
// season.
//
// The CONTROL pace car belongs to a racer row like anybody else, so a plain
// list of racers offers "Derby Ales" as though it were a person — which is
// exactly how the pace car ended up ranked 36th in the club's published 2026
// standings. Anywhere a human is being chosen, choose from this.
func (db *DB) CompetingRacers(ctx context.Context, seasonID int64) ([]model.Racer, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT r.id, r.season_id, r.first_name, r.last_name
		FROM racer r
		JOIN entry e ON e.racer_id = r.id AND e.is_control = 0
		JOIN race  x ON x.id = e.race_id
		WHERE r.season_id = ? AND x.season_id = ?
		ORDER BY r.last_name, r.first_name`, seasonID, seasonID)
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

// --- adjustments ----------------------------------------------------------------

// AdjustmentView is a manual points correction with the racer's name attached.
type AdjustmentView struct {
	model.Adjustment
	Driver string
}

// Adjustments lists a season's manual corrections, newest first.
func (db *DB) Adjustments(ctx context.Context, seasonID int64) ([]AdjustmentView, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT a.id, a.season_id, a.racer_id, a.points, a.reason, a.created_at,
		       r.first_name, r.last_name
		FROM adjustment a JOIN racer r ON r.id = a.racer_id
		WHERE a.season_id = ?
		ORDER BY a.created_at DESC, a.id DESC`, seasonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AdjustmentView
	for rows.Next() {
		var a AdjustmentView
		var createdAt int64
		var first, last string
		if err := rows.Scan(&a.ID, &a.SeasonID, &a.RacerID, &a.Points, &a.Reason,
			&createdAt, &first, &last); err != nil {
			return nil, err
		}
		a.CreatedAt = fromUnix(createdAt)
		a.Driver = model.Racer{FirstName: first, LastName: last}.FullName()
		out = append(out, a)
	}
	return out, rows.Err()
}

// AddAdjustment records a manual points correction.
//
// The reason is required. An unexplained points change is the one thing in this
// application most likely to be argued about later, and "because I said so" is
// not an answer anybody accepts at a brewery.
func (db *DB) AddAdjustment(ctx context.Context, a model.Adjustment) (model.Adjustment, error) {
	if a.Reason == "" {
		return a, errors.New("an adjustment needs a reason, so it can be explained later")
	}
	if a.Points == 0 {
		return a, errors.New("an adjustment of zero points would change nothing")
	}
	a.CreatedAt = time.Now()
	res, err := db.ExecContext(ctx, `
		INSERT INTO adjustment (season_id, racer_id, points, reason, created_at)
		VALUES (?,?,?,?,?)`,
		a.SeasonID, a.RacerID, a.Points, a.Reason, unix(a.CreatedAt))
	if err != nil {
		return a, fmt.Errorf("saving the adjustment: %w", err)
	}
	a.ID, err = res.LastInsertId()
	return a, err
}

// DeleteAdjustment removes one correction.
func (db *DB) DeleteAdjustment(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM adjustment WHERE id = ?`, id)
	return err
}
