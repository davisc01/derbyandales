package store

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// HeatEventKind is something that happened to a heat besides being run.
type HeatEventKind string

const (
	HeatReRun        HeatEventKind = "rerun"
	HeatManual       HeatEventKind = "manual"
	HeatBadRead      HeatEventKind = "bad_read"
	HeatFalseTrigger HeatEventKind = "false_trigger"
)

// HeatEvent is one of them.
type HeatEvent struct {
	Heat  int
	Kind  HeatEventKind
	Lanes []int
}

// RecordHeatEvent notes that something happened to a heat.
func (db *DB) RecordHeatEvent(ctx context.Context, raceID int64, heat int, kind HeatEventKind, lanes []int) error {
	parts := make([]string, 0, len(lanes))
	for _, l := range lanes {
		parts = append(parts, strconv.Itoa(l))
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO heat_event (race_id, heat_number, kind, lanes, at) VALUES (?,?,?,?,?)`,
		raceID, heat, string(kind), strings.Join(parts, ","), time.Now().Unix())
	return err
}

// HeatEvents lists what happened to a race's heats, in the order it happened.
func (db *DB) HeatEvents(ctx context.Context, raceID int64) ([]HeatEvent, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT heat_number, kind, lanes FROM heat_event
		WHERE race_id = ? ORDER BY at, id`, raceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HeatEvent
	for rows.Next() {
		var e HeatEvent
		var kind, lanes string
		if err := rows.Scan(&e.Heat, &kind, &lanes); err != nil {
			return nil, err
		}
		e.Kind = HeatEventKind(kind)
		for _, p := range strings.Split(lanes, ",") {
			if n, err := strconv.Atoi(p); err == nil {
				e.Lanes = append(e.Lanes, n)
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
