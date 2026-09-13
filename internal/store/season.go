package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/davisc01/derbyandales/internal/model"
)

// ErrNotFound is returned when a lookup by id finds nothing.
var ErrNotFound = errors.New("not found")

const seasonCols = `id, year, name, lane_count, track_length_ft, scale_denom,
	race_count, auto_qual_places, wildcard_spots, max_championship_entry,
	points_count_control, bracket_lane_a, bracket_lane_b, created_at`

func scanSeason(sc interface{ Scan(...any) error }) (model.Season, error) {
	var s model.Season
	var createdAt int64
	var countControl int
	err := sc.Scan(&s.ID, &s.Year, &s.Name, &s.LaneCount, &s.TrackLengthFt, &s.ScaleDenom,
		&s.RaceCount, &s.AutoQualPlaces, &s.WildcardSpots, &s.MaxChampionshipEntry,
		&countControl, &s.BracketLaneA, &s.BracketLaneB, &createdAt)
	if err != nil {
		return s, err
	}
	s.PointsCountControl = countControl != 0
	s.CreatedAt = fromUnix(createdAt)
	return s, nil
}

// DefaultSeason returns a season pre-filled with the club's current rules:
// 6 races, top 3 auto-qualify, 6 wildcards, 28 ft 4-lane track at 1:25 scale.
func DefaultSeason(year int) model.Season {
	return model.Season{
		Year:                 year,
		Name:                 fmt.Sprintf("%d Season", year),
		LaneCount:            4,
		TrackLengthFt:        28,
		ScaleDenom:           25,
		RaceCount:            6,
		AutoQualPlaces:       3,
		WildcardSpots:        6,
		MaxChampionshipEntry: 3,
		PointsCountControl:   false,
		BracketLaneA:         1,
		BracketLaneB:         2,
	}
}

// CreateSeason inserts a season and returns it with its assigned id.
func (db *DB) CreateSeason(ctx context.Context, s model.Season) (model.Season, error) {
	s.CreatedAt = time.Now()
	countControl := 0
	if s.PointsCountControl {
		countControl = 1
	}
	res, err := db.ExecContext(ctx, `
		INSERT INTO season (year, name, lane_count, track_length_ft, scale_denom,
			race_count, auto_qual_places, wildcard_spots, max_championship_entry,
			points_count_control, bracket_lane_a, bracket_lane_b, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.Year, s.Name, s.LaneCount, s.TrackLengthFt, s.ScaleDenom,
		s.RaceCount, s.AutoQualPlaces, s.WildcardSpots, s.MaxChampionshipEntry,
		countControl, s.BracketLaneA, s.BracketLaneB, unix(s.CreatedAt))
	if err != nil {
		return s, fmt.Errorf("insert season: %w", err)
	}
	s.ID, err = res.LastInsertId()
	return s, err
}

// UpdateSeason writes back every mutable field.
func (db *DB) UpdateSeason(ctx context.Context, s model.Season) error {
	countControl := 0
	if s.PointsCountControl {
		countControl = 1
	}
	_, err := db.ExecContext(ctx, `
		UPDATE season SET year=?, name=?, lane_count=?, track_length_ft=?, scale_denom=?,
			race_count=?, auto_qual_places=?, wildcard_spots=?, max_championship_entry=?,
			points_count_control=?, bracket_lane_a=?, bracket_lane_b=?
		WHERE id=?`,
		s.Year, s.Name, s.LaneCount, s.TrackLengthFt, s.ScaleDenom,
		s.RaceCount, s.AutoQualPlaces, s.WildcardSpots, s.MaxChampionshipEntry,
		countControl, s.BracketLaneA, s.BracketLaneB, s.ID)
	return err
}

// Season loads one season by id.
func (db *DB) Season(ctx context.Context, id int64) (model.Season, error) {
	row := db.QueryRowContext(ctx, `SELECT `+seasonCols+` FROM season WHERE id = ?`, id)
	s, err := scanSeason(row)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

// Seasons lists every season, newest year first.
func (db *DB) Seasons(ctx context.Context) ([]model.Season, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+seasonCols+` FROM season ORDER BY year DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Season
	for rows.Next() {
		s, err := scanSeason(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// --- races -------------------------------------------------------------------

const raceCols = `id, season_id, number, name, date, venue, kind, status, created_at`

func scanRace(sc interface{ Scan(...any) error }) (model.Race, error) {
	var r model.Race
	var date, createdAt int64
	err := sc.Scan(&r.ID, &r.SeasonID, &r.Number, &r.Name, &date, &r.Venue,
		&r.Kind, &r.Status, &createdAt)
	if err != nil {
		return r, err
	}
	r.Date = fromUnix(date)
	r.CreatedAt = fromUnix(createdAt)
	return r, nil
}

// CreateRace inserts a race.
func (db *DB) CreateRace(ctx context.Context, r model.Race) (model.Race, error) {
	if r.Kind == "" {
		r.Kind = model.RacePoints
	}
	if r.Status == "" {
		r.Status = model.StatusSetup
	}
	r.CreatedAt = time.Now()
	res, err := db.ExecContext(ctx, `
		INSERT INTO race (season_id, number, name, date, venue, kind, status, created_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		r.SeasonID, r.Number, r.Name, unix(r.Date), r.Venue, r.Kind, r.Status, unix(r.CreatedAt))
	if err != nil {
		return r, fmt.Errorf("insert race: %w", err)
	}
	r.ID, err = res.LastInsertId()
	return r, err
}

// Race loads one race by id.
func (db *DB) Race(ctx context.Context, id int64) (model.Race, error) {
	row := db.QueryRowContext(ctx, `SELECT `+raceCols+` FROM race WHERE id = ?`, id)
	r, err := scanRace(row)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// Races lists a season's races in running order: points races by number, then
// the championship.
func (db *DB) Races(ctx context.Context, seasonID int64) ([]model.Race, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+raceCols+` FROM race WHERE season_id = ?
		 ORDER BY CASE kind WHEN 'points' THEN 0 ELSE 1 END, number`, seasonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Race
	for rows.Next() {
		r, err := scanRace(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetRaceStatus advances a race through the run-of-show.
func (db *DB) SetRaceStatus(ctx context.Context, id int64, status model.RaceStatus) error {
	_, err := db.ExecContext(ctx, `UPDATE race SET status = ? WHERE id = ?`, status, id)
	return err
}
