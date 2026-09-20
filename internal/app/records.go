package app

import (
	"context"
	"sort"

	"github.com/davisc01/derbyandales/internal/records"
	"github.com/davisc01/derbyandales/internal/store"
)

// The club records, and noticing when one falls.
//
// Everything here is worked out from the times on each call. Nothing is
// cached, because the moment a record matters most is the instant a heat
// lands, and a cache is most likely to be stale at exactly that instant.

// Records is the record book as it stands.
type Records struct {
	records.Book
	Data store.RecordData
	// Demo reports that the demo season is counted, because a demo race is
	// loaded. Its times are invented, and the page says so.
	Demo bool
}

// Records works out every club record.
func (a *App) Records(ctx context.Context) (Records, error) {
	demo := a.DemoLoaded(ctx)
	d, err := a.DB.RecordData(ctx, demo)
	if err != nil {
		return Records{}, err
	}
	return Records{Book: records.Compute(d.Runs, d.Results), Data: d, Demo: demo}, nil
}

// HeatRecords reports what one heat broke, keyed by lane: the track record,
// a lane's record, or a racer's personal best.
//
// The heat is judged against everything run before it, so asking again later
// — a display reconnecting, the heat shown again — gives the same answer
// rather than one spoiled by the heats since.
func (a *App) HeatRecords(ctx context.Context, heat store.HeatView) (map[int]records.Notice, error) {
	d, err := a.DB.RecordData(ctx, a.DemoLoaded(ctx))
	if err != nil {
		return nil, err
	}
	race, ok := d.Races[heat.RaceID]
	if !ok {
		return nil, nil
	}
	return records.HeatNotices(d.Runs, race, d.HeatSeq[heat.ID]), nil
}

// AverageRecords reports which cars in a race ran a record average, keyed by
// car number. Empty until the race is over: an average from half a night is
// not an average, and the reveal is where the room should hear it anyway.
func (a *App) AverageRecords(ctx context.Context, raceID int64) (map[int]records.Notice, error) {
	d, err := a.DB.RecordData(ctx, a.DemoLoaded(ctx))
	if err != nil {
		return nil, err
	}
	race, ok := d.Races[raceID]
	if !ok {
		return nil, nil
	}
	return records.AverageNotices(d.Results, race), nil
}

// DemoLoaded reports whether the race loaded now belongs to the demo season.
func (a *App) DemoLoaded(ctx context.Context) bool {
	id := a.Race.CurrentRaceID()
	if id == 0 {
		return false
	}
	race, err := a.DB.Race(ctx, id)
	if err != nil {
		return false
	}
	season, err := a.DB.Season(ctx, race.SeasonID)
	if err != nil {
		return false
	}
	return IsDemoSeason(season.Name)
}

// BrokenTonight is a record one of tonight's runs broke.
type BrokenTonight struct {
	Heat int
	Lane int
	records.Notice
}

// NightRecords lists every record broken in one race, heat by heat, and the
// record averages once the race is over. Each heat is judged as it was when it
// landed, so a record later beaten the same night still counts as broken.
func (a *App) NightRecords(ctx context.Context, raceID int64) (runs []BrokenTonight, averages map[int]records.Notice, err error) {
	d, err := a.DB.RecordData(ctx, a.DemoLoaded(ctx))
	if err != nil {
		return nil, nil, err
	}
	race, ok := d.Races[raceID]
	if !ok {
		return nil, nil, nil
	}
	heats, err := a.DB.Heats(ctx, raceID)
	if err != nil {
		return nil, nil, err
	}
	for _, h := range heats {
		notices := records.HeatNotices(d.Runs, race, d.HeatSeq[h.ID])
		for lane, n := range notices {
			runs = append(runs, BrokenTonight{Heat: h.Number, Lane: lane, Notice: n})
		}
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].Heat != runs[j].Heat {
			return runs[i].Heat < runs[j].Heat
		}
		return runs[i].Lane < runs[j].Lane
	})
	return runs, records.AverageNotices(d.Results, race), nil
}
