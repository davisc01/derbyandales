package store

import (
	"context"
	"database/sql"
	"strings"

	"github.com/davisc01/derbyandales/internal/history"
)

// The previous-championship rule, in storage.
//
// A car gets one championship. Checking that has always been somebody
// remembering; these rows let the software do the remembering and leave the
// deciding to a person.

// PastCar is a car that raced in a previous championship.
type PastCar struct {
	Year      int
	CarName   string
	First     string
	Last      string
	CarNumber int
	Place     int

	// SameDriver marks a match where the surname agrees too. A car name alone
	// is a weaker signal: two people can call a car "Rocket" six years apart
	// and mean nothing by it.
	SameDriver bool
}

// Driver renders the racer's name as published.
func (c PastCar) Driver() string {
	if c.First == "" {
		return c.Last
	}
	return c.First + " " + c.Last
}

// ImportChampionship replaces one year's archived championship field.
func (db *DB) ImportChampionship(ctx context.Context, year int, cars []history.Car) (int, error) {
	n := 0
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM past_championship WHERE year = ?`, year); err != nil {
			return err
		}
		for _, c := range cars {
			key := history.Key(c.CarName)
			if key == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO past_championship
					(year, car_key, car_name, first_name, last_name, car_number, place)
				VALUES (?,?,?,?,?,?,?)
				ON CONFLICT(year, car_key, last_name) DO UPDATE SET
					car_name = excluded.car_name,
					first_name = excluded.first_name,
					car_number = excluded.car_number,
					place = excluded.place`,
				year, key, c.CarName, c.First, c.Last, c.CarNumber, c.Place); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// ChampionshipYears lists the years that have been imported, newest first.
func (db *DB) ChampionshipYears(ctx context.Context) ([]int, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT year FROM past_championship ORDER BY year DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var y int
		if err := rows.Scan(&y); err != nil {
			return nil, err
		}
		out = append(out, y)
	}
	return out, rows.Err()
}

// PastChampionshipCars reports how many archived cars are on record.
func (db *DB) PastChampionshipCars(ctx context.Context) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM past_championship`).Scan(&n)
	return n, err
}

// RacedBefore finds archived cars that look like this one.
//
// Matching is on the car name, because the rule is about the car. A match on
// the surname as well is the strong case and is marked; a name-only match is
// still worth showing, because a car does sometimes change hands, but it is
// not the same claim.
//
// Nothing here excludes anybody. It hands a person something to look at.
func (db *DB) RacedBefore(ctx context.Context, lastName, carName string) ([]PastCar, error) {
	key := history.Key(carName)
	if key == "" {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT year, car_name, first_name, last_name, car_number, place
		FROM past_championship WHERE car_key = ?
		ORDER BY year DESC`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PastCar
	for rows.Next() {
		var c PastCar
		if err := rows.Scan(&c.Year, &c.CarName, &c.First, &c.Last,
			&c.CarNumber, &c.Place); err != nil {
			return nil, err
		}
		c.SameDriver = strings.EqualFold(strings.TrimSpace(c.Last), strings.TrimSpace(lastName))
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// The strong matches first: same car, same family name.
	strong := make([]PastCar, 0, len(out))
	weak := make([]PastCar, 0, len(out))
	for _, c := range out {
		if c.SameDriver {
			strong = append(strong, c)
		} else {
			weak = append(weak, c)
		}
	}
	return append(strong, weak...), nil
}
