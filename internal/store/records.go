package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/davisc01/derbyandales/internal/history"
	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/records"
	"github.com/davisc01/derbyandales/internal/scoring"
)

// The club records, in storage: the archive's race nights, and the one place
// the archive and this software's own races are put together.

// ImportArchiveRace replaces one archived race night.
func (db *DB) ImportArchiveRace(ctx context.Context, year int, kind model.RaceKind, number int, cars []history.RaceCar) (int, error) {
	runs := 0
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM archive_car WHERE year = ? AND kind = ? AND number = ?`,
			year, kind, number); err != nil {
			return err
		}
		for _, c := range cars {
			res, err := tx.ExecContext(ctx, `
				INSERT INTO archive_car (year, kind, number, car_number, first_name,
					last_name, car_name, place, is_control, excluded)
				VALUES (?,?,?,?,?,?,?,?,?,?)`,
				year, kind, number, c.CarNumber, c.First, c.Last, c.CarName, c.Place,
				boolInt(c.Control), boolInt(c.Excluded))
			if err != nil {
				return err
			}
			id, err := res.LastInsertId()
			if err != nil {
				return err
			}
			for _, r := range c.Runs {
				// A car listed twice in one heat is a broken file, not two runs.
				if _, err := tx.ExecContext(ctx, `
					INSERT OR IGNORE INTO archive_run (car_id, heat, lane, time)
					VALUES (?,?,?,?)`, id, r.Heat, r.Lane, r.Time); err != nil {
					return err
				}
				runs++
			}
		}
		return nil
	})
	return runs, err
}

// ArchiveRaces reports how many race nights are imported, and their runs.
func (db *DB) ArchiveRaces(ctx context.Context) (races, runs int, err error) {
	err = db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM (SELECT DISTINCT year, kind, number FROM archive_car)),
		       (SELECT COUNT(*) FROM archive_run)`).Scan(&races, &runs)
	return races, runs, err
}

// RecordData is everything the records are worked out from.
type RecordData struct {
	Runs    []records.Run
	Results []records.Result

	// Races maps this software's races to their place in the order, and
	// HeatSeq its heats to their order within their race — which is the order
	// they were run, not their number, because a re-run happens later.
	Races   map[int64]records.Race
	HeatSeq map[int64]int

	// LeftOut are heats flagged as a fault and not re-run. A timer that fires
	// early gives every car its best run of the night, which is exactly what
	// would otherwise set a record.
	LeftOut []LeftOutHeat
}

// LeftOutHeat is a heat whose runs are kept out of the records.
type LeftOutHeat struct {
	RaceID int64
	Race   records.Race
	Heat   int
}

// RecordData gathers every run and result that counts, from the archive and
// from this software's races.
//
// What counts is decided here, once: not the pace car, not an excluded car,
// not a struck-out lane, not a non-finish, and not a run-off's times in an
// average. A night this software raced is read from its own tables even if the
// archive has a copy.
//
// withDemo counts the demo season too, for rehearsing on it.
func (db *DB) RecordData(ctx context.Context, withDemo bool) (RecordData, error) {
	d := RecordData{Races: map[int64]records.Race{}, HeatSeq: map[int64]int{}}
	if err := db.archiveRecordData(ctx, &d, withDemo); err != nil {
		return d, err
	}
	if err := db.liveRecordData(ctx, &d, withDemo); err != nil {
		return d, err
	}
	return d, nil
}

// raced is the SQL test for "this software has times for that race night".
const raced = `EXISTS (
	SELECT 1 FROM race r
	JOIN season s ON s.id = r.season_id
	JOIN heat h ON h.race_id = r.id
	JOIN heat_lane hl ON hl.heat_id = h.id
	WHERE s.year = ac.year AND r.kind = ac.kind AND r.number = ac.number
	  AND hl.finish_time IS NOT NULL AND %s)`

// realSeason leaves out the demo season. Its times are made up, and it is
// dated this year, so counting it would both invent records and hide the real
// nights the archive holds for the same year. A demo night is rehearsed with
// it counted, though, or the record notices would never be seen in rehearsal.
func realSeason(withDemo bool) string {
	if withDemo {
		return "1"
	}
	return `s.name NOT LIKE '%demo data%'`
}

func (db *DB) archiveRecordData(ctx context.Context, d *RecordData, withDemo bool) error {
	rows, err := db.QueryContext(ctx, `
		SELECT ac.id, ac.year, ac.kind, ac.number, ac.car_number, ac.first_name,
		       ac.last_name, ac.car_name, ac.place, ar.heat, ar.lane, ar.time
		FROM archive_car ac
		JOIN archive_run ar ON ar.car_id = ac.id
		WHERE ac.is_control = 0 AND ac.excluded = 0 AND NOT `+fmt.Sprintf(raced, realSeason(withDemo))+`
		ORDER BY ac.id, ar.heat`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type night struct {
		result records.Result
		runs   []scoring.Run
	}
	nights := map[int64]*night{}
	var order []int64
	for rows.Next() {
		var (
			id          int64
			kind        string
			first, last string
			run         records.Run
			place       int
		)
		if err := rows.Scan(&id, &run.Race.Year, &kind, &run.Race.Number, &run.CarNumber,
			&first, &last, &run.CarName, &place, &run.Seq, &run.Lane, &run.Time); err != nil {
			return err
		}
		run.Race.Championship = kind == string(model.RaceChampionship)
		run.Person = history.PersonKey(first, last)
		run.Driver = driver(first, last)

		n := nights[id]
		if n == nil {
			n = &night{result: records.Result{Car: run.Car, Race: run.Race, Place: place}}
			nights[id] = n
			order = append(order, id)
		}
		n.runs = append(n.runs, scoring.Run{Heat: run.Seq, Lane: run.Lane, Time: run.Time})
		if finished(run.Time) {
			d.Runs = append(d.Runs, run)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// The average is worked out again from the heat times rather than read
	// from the standings, because one year's standings (2019 race 5) averaged
	// all four runs. The club's rule is drop the slowest, and a record has to
	// be measured the same way every year.
	for _, id := range order {
		n := nights[id]
		if n.result.Place <= 0 {
			continue
		}
		n.result.Average = scoring.ScoreEntry(0, n.runs).Average
		d.Results = append(d.Results, n.result)
	}
	return nil
}

func (db *DB) liveRecordData(ctx context.Context, d *RecordData, withDemo bool) error {
	races, err := db.QueryContext(ctx, `
		SELECT r.id, s.year, r.kind, r.number, r.format
		FROM race r JOIN season s ON s.id = r.season_id
		WHERE `+realSeason(withDemo))
	if err != nil {
		return err
	}
	bracket := map[int64]bool{}
	for races.Next() {
		var id int64
		var kind, format string
		var ref records.Race
		if err := races.Scan(&id, &ref.Year, &kind, &ref.Number, &format); err != nil {
			races.Close()
			return err
		}
		ref.Championship = kind == string(model.RaceChampionship)
		d.Races[id] = ref
		bracket[id] = format == string(model.FormatBracket)
	}
	races.Close()
	if err := races.Err(); err != nil {
		return err
	}

	// Heats in the order they were run. A heat with no completion time yet
	// sorts last, since it cannot have run before one that has.
	heats, err := db.QueryContext(ctx, `
		SELECT id, race_id FROM heat
		ORDER BY race_id, completed_at IS NULL, completed_at, number`)
	if err != nil {
		return err
	}
	next := map[int64]int{}
	for heats.Next() {
		var id, raceID int64
		if err := heats.Scan(&id, &raceID); err != nil {
			heats.Close()
			return err
		}
		next[raceID]++
		d.HeatSeq[id] = next[raceID]
	}
	heats.Close()
	if err := heats.Err(); err != nil {
		return err
	}

	leftOut, err := db.faultyHeats(ctx, d)
	if err != nil {
		return err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT h.race_id, h.id, hl.lane, hl.finish_time, h.runoff_place IS NOT NULL,
		       e.car_number, e.car_name, rc.first_name, rc.last_name
		FROM heat_lane hl
		JOIN heat h   ON h.id = hl.heat_id
		JOIN entry e  ON e.id = hl.entry_id
		JOIN racer rc ON rc.id = e.racer_id
		WHERE hl.finish_time IS NOT NULL AND hl.ignored = 0
		  AND e.excluded = 0 AND e.is_control = 0`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var raceID, heatID int64
		var first, last string
		var run records.Run
		if err := rows.Scan(&raceID, &heatID, &run.Lane, &run.Time, &run.RunOff,
			&run.CarNumber, &run.CarName, &first, &last); err != nil {
			rows.Close()
			return err
		}
		ref, ok := d.Races[raceID]
		if !ok || !finished(run.Time) || leftOut[heatID] {
			continue
		}
		run.Race = ref
		run.Seq = d.HeatSeq[heatID]
		run.Person = history.PersonKey(first, last)
		run.Driver = driver(first, last)
		d.Runs = append(d.Runs, run)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// Results are the frozen ones: a race's result exists once its last heat
	// has landed, and an average from half a night is not an average.
	res, err := db.QueryContext(ctx, `
		SELECT rr.race_id, rr.place, rr.average, e.car_number, e.car_name,
		       rc.first_name, rc.last_name
		FROM race_result rr
		JOIN entry e  ON e.id = rr.entry_id
		JOIN racer rc ON rc.id = rr.racer_id
		WHERE e.is_control = 0 AND e.excluded = 0 AND rr.place > 0`)
	if err != nil {
		return err
	}
	defer res.Close()
	for res.Next() {
		var raceID int64
		var first, last string
		var r records.Result
		if err := res.Scan(&raceID, &r.Place, &r.Average, &r.CarNumber, &r.CarName,
			&first, &last); err != nil {
			return err
		}
		ref, ok := d.Races[raceID]
		if !ok {
			continue
		}
		r.Race = ref
		r.Person = history.PersonKey(first, last)
		r.Driver = driver(first, last)
		if bracket[raceID] {
			r.Average = 0
		}
		d.Results = append(d.Results, r)
	}
	return res.Err()
}

// faultyHeats finds the heats to keep out of the records: those clearly
// flagged as every car running its fastest. It is asked only of races with
// every heat run, because before that nobody's night is complete enough to be
// an outlier against.
func (db *DB) faultyHeats(ctx context.Context, d *RecordData) (map[int64]bool, error) {
	out := map[int64]bool{}
	rows, err := db.QueryContext(ctx, `
		SELECT h.race_id FROM heat h
		GROUP BY h.race_id
		HAVING SUM(EXISTS (
			SELECT 1 FROM heat_lane hl
			WHERE hl.heat_id = h.id AND hl.entry_id IS NOT NULL
			  AND hl.finish_time IS NULL)) = 0
		ORDER BY h.race_id`)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		if _, ok := d.Races[id]; ok {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		found, err := db.HeatAnomalies(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, a := range found {
			if a.Clear && a.Kind == scoring.AllFastest {
				out[a.HeatID] = true
				d.LeftOut = append(d.LeftOut, LeftOutHeat{RaceID: id, Race: d.Races[id], Heat: a.Heat})
			}
		}
	}
	return out, nil
}

// finished separates a time from a non-finish, which is recorded as 9.999.
func finished(t float64) bool { return t > 0 && t < 9.9 }

func driver(first, last string) string {
	return strings.TrimSpace(strings.TrimSpace(first) + " " + strings.TrimSpace(last))
}

// ControlNight is the CONTROL car's night: the same car down the same track,
// which makes it the one measure of how fast the track itself ran.
type ControlNight struct {
	Race records.Race
	// RaceID is this software's race, or zero for one read from the archive.
	RaceID  int64
	CarName string
	// Average is its drop-slowest average, the figure its standings row shows.
	Average     float64
	Best, Worst float64
	Runs, DNFs  int
}

// ControlNights lists the CONTROL car's nights, oldest first, from the archive
// and this software's races alike.
//
// Where a night had more than one CONTROL car — 2026 race 3 had a second one,
// excluded — the one that was not excluded is the one that ran as the pace
// car, and it is the one taken.
func (db *DB) ControlNights(ctx context.Context, withDemo bool) ([]ControlNight, error) {
	type key struct {
		race records.Race
		id   int64
		car  int
		name string
		excl bool
		live int64
	}
	runs := map[key][]float64{}

	rows, err := db.QueryContext(ctx, `
		SELECT ac.id, ac.year, ac.kind, ac.number, ac.car_number, ac.car_name, ac.excluded, ar.time
		FROM archive_car ac JOIN archive_run ar ON ar.car_id = ac.id
		WHERE ac.is_control = 1 AND NOT `+fmt.Sprintf(raced, realSeason(withDemo)))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k key
		var kind string
		var t float64
		if err := rows.Scan(&k.id, &k.race.Year, &kind, &k.race.Number, &k.car, &k.name, &k.excl, &t); err != nil {
			rows.Close()
			return nil, err
		}
		k.race.Championship = kind == string(model.RaceChampionship)
		runs[k] = append(runs[k], t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// This software's nights: every lane the CONTROL car ran, leaving out
	// run-offs and struck lanes as the standings do.
	rows, err = db.QueryContext(ctx, `
		SELECT e.id, s.year, r.kind, r.number, r.id, e.car_number, e.car_name, e.excluded, hl.finish_time
		FROM heat_lane hl
		JOIN heat h   ON h.id = hl.heat_id
		JOIN race r   ON r.id = h.race_id
		JOIN season s ON s.id = r.season_id
		JOIN entry e  ON e.id = hl.entry_id
		WHERE e.is_control = 1 AND hl.finish_time IS NOT NULL AND hl.ignored = 0
		  AND h.runoff_place IS NULL AND `+realSeason(withDemo))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k key
		var kind string
		var t float64
		if err := rows.Scan(&k.id, &k.race.Year, &kind, &k.race.Number, &k.live, &k.car, &k.name, &k.excl, &t); err != nil {
			rows.Close()
			return nil, err
		}
		k.race.Championship = kind == string(model.RaceChampionship)
		// Archive and live ids share a number space; keep them apart.
		k.id = -k.id
		runs[k] = append(runs[k], t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	chosen := map[records.Race]key{}
	for k := range runs {
		cur, ok := chosen[k.race]
		if !ok || (cur.excl && !k.excl) || (cur.excl == k.excl && k.car < cur.car) {
			chosen[k.race] = k
		}
	}

	out := make([]ControlNight, 0, len(chosen))
	for race, k := range chosen {
		n := ControlNight{Race: race, RaceID: k.live, CarName: k.name}
		sr := make([]scoring.Run, 0, len(runs[k]))
		for _, t := range runs[k] {
			n.Runs++
			sr = append(sr, scoring.Run{Time: t})
			if !finished(t) {
				n.DNFs++
				continue
			}
			if n.Best == 0 || t < n.Best {
				n.Best = t
			}
			if t > n.Worst {
				n.Worst = t
			}
		}
		n.Average = scoring.ScoreEntry(0, sr).Average
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Race.Before(out[j].Race) })
	return out, nil
}
