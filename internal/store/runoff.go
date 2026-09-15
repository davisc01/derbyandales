package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/scoring"
)

// Run-offs.
//
// A tie below the podium stands: two cars that ran the same average are the
// same speed, and the club is content to publish that. A tie for 1st, 2nd or
// 3rd is different, because a trophy cannot be shared, so those are settled on
// the track — and so is a tie lower down that a championship place has passed
// down to, for the same reason.
//
// The run-off decides an order and nothing else. Its times are not part of any
// average — see the 0004 migration for why — so the tied cars keep the
// identical averages that tied them, and only their places differ afterwards.

// TieView is a tie that needs settling, with the cars named.
type TieView struct {
	scoring.Tie
	Entries []EntryView

	// HeatID and HeatNumber are the run-off heat once one exists.
	HeatID     int64
	HeatNumber int
	// Settled reports that the run-off produced an order — which is not the
	// same as the heat having times in it. A run-off that finished level has
	// been run and has settled nothing.
	Settled bool
	// DeadHeat marks exactly that case: run, and still level.
	DeadHeat bool
	// ForPlace marks a tie that decides a championship place rather than a
	// trophy: a qualifying place that passed down the results to where two
	// cars finished level.
	ForPlace bool
}

// Describe says what is tied, in the words someone would use at the venue.
func (t TieView) Describe() string {
	switch len(t.Places()) {
	case 0, 1:
		return fmt.Sprintf("tied for %s", ordinal(t.Place))
	case 2:
		return fmt.Sprintf("tied for %s and %s", ordinal(t.Place), ordinal(t.Place+1))
	}
	return fmt.Sprintf("tied for %s through %s", ordinal(t.Place),
		ordinal(t.Place+len(t.EntryIDs)-1))
}

func ordinal(n int) string {
	suffix := "th"
	switch {
	case n%100 >= 11 && n%100 <= 13:
	case n%10 == 1:
		suffix = "st"
	case n%10 == 2:
		suffix = "nd"
	case n%10 == 3:
		suffix = "rd"
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

// UnsettledTies lists the ties in a race that reach a trophy, with the cars in
// them and any run-off already arranged.
func (db *DB) UnsettledTies(ctx context.Context, raceID int64) ([]TieView, error) {
	race, err := db.Race(ctx, raceID)
	if err != nil {
		return nil, err
	}
	// A bracket's shared places are rounds, not times. Two semi-final losers
	// are both 3rd because neither raced the other; running them off would be
	// a third-place match, and the club does not race one.
	if race.Bracket() {
		return nil, nil
	}
	standings, err := db.Standings(ctx, raceID)
	if err != nil {
		return nil, err
	}
	results := make([]scoring.Result, 0, len(standings))
	byID := make(map[int64]EntryView, len(standings))
	for _, st := range standings {
		results = append(results, st.Result)
		byID[st.Entry.ID] = st.Entry
	}

	runoffs, err := db.runoffHeats(ctx, raceID)
	if err != nil {
		return nil, err
	}
	order, dead, err := db.runOffOrder(ctx, raceID)
	if err != nil {
		return nil, err
	}

	// A tie further down still has to be settled when a championship place
	// depends on it. That happens when a place passes down — the racer above
	// is at the cap, or the car had already qualified — and lands on two cars
	// that finished level. Only one of them can have it.
	forPlace := map[int]bool{}
	if race.Kind == model.RacePoints {
		if slots, err := db.Qualifiers(ctx, race.SeasonID); err == nil {
			for _, sl := range slots {
				if sl.RaceID == raceID && sl.TiedWith != nil {
					forPlace[sl.Place] = true
				}
			}
		}
	}

	var out []TieView
	for _, tie := range scoring.Ties(results) {
		if tie.Place > TrophyPlaces(race) && !forPlace[tie.Place] {
			continue
		}
		v := TieView{Tie: tie, ForPlace: tie.Place > TrophyPlaces(race)}
		for _, id := range tie.EntryIDs {
			v.Entries = append(v.Entries, byID[id])
		}
		if h, ok := runoffs[tie.Place]; ok {
			v.HeatID = h.ID
			v.HeatNumber = h.Number
			v.DeadHeat = dead[tie.Place]
			// Settled means an order came out of it. Every car has to have a
			// position, or the tie is still a tie.
			v.Settled = true
			for _, id := range tie.EntryIDs {
				if _, ok := order[id]; !ok {
					v.Settled = false
				}
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// runoffHeats returns the run-off heats of a race, keyed by the place they
// settle.
func (db *DB) runoffHeats(ctx context.Context, raceID int64) (map[int]HeatView, error) {
	heats, err := db.Heats(ctx, raceID)
	if err != nil {
		return nil, err
	}
	out := map[int]HeatView{}
	for _, h := range heats {
		if h.RunoffPlace != nil {
			out[*h.RunoffPlace] = h
		}
	}
	return out, nil
}

// CreateRunOff builds the heat that settles a tie.
//
// The cars go on the first lanes in order. There is no way to balance lanes
// across a single run — that is what four runs, one per lane, exists to do — so
// a run-off carries whatever lane advantage the track has. The club already
// accepts exactly that for every head-to-head in the championship; it is worth
// knowing rather than pretending away.
func (db *DB) CreateRunOff(ctx context.Context, raceID int64, place int) (HeatView, error) {
	ties, err := db.UnsettledTies(ctx, raceID)
	if err != nil {
		return HeatView{}, err
	}
	var tie *TieView
	for i := range ties {
		if ties[i].Place == place {
			tie = &ties[i]
		}
	}
	if tie == nil {
		return HeatView{}, fmt.Errorf("nothing is tied for %s", ordinal(place))
	}
	if tie.HeatID != 0 {
		return db.Heat(ctx, tie.HeatID)
	}

	race, err := db.Race(ctx, raceID)
	if err != nil {
		return HeatView{}, err
	}
	s, err := db.Season(ctx, race.SeasonID)
	if err != nil {
		return HeatView{}, err
	}
	if len(tie.Entries) > s.LaneCount {
		return HeatView{}, fmt.Errorf(
			"%d cars are tied for %s but the track has %d lanes — settle this one by hand",
			len(tie.Entries), ordinal(place), s.LaneCount)
	}

	var heatID int64
	err = db.Tx(ctx, func(tx *sql.Tx) error {
		var next int
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(number), 0) + 1 FROM heat WHERE race_id = ?`,
			raceID).Scan(&next); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO heat (race_id, number, phase, status, runoff_place)
			VALUES (?,?,?,?,?)`,
			raceID, next, model.PhaseQualifying, model.HeatPending, place)
		if err != nil {
			return err
		}
		if heatID, err = res.LastInsertId(); err != nil {
			return err
		}
		for lane := 1; lane <= s.LaneCount; lane++ {
			var entryID any
			if lane <= len(tie.Entries) {
				entryID = tie.Entries[lane-1].ID
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO heat_lane (heat_id, lane, entry_id) VALUES (?,?,?)`,
				heatID, lane, entryID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return HeatView{}, err
	}
	return db.Heat(ctx, heatID)
}

// runOffOrder reads the settled order of every completed run-off in a race:
// entry id to its position within its tie, 0-based.
func (db *DB) runOffOrder(ctx context.Context, raceID int64) (map[int64]int, map[int]bool, error) {
	heats, err := db.runoffHeats(ctx, raceID)
	if err != nil {
		return nil, nil, err
	}

	order := map[int64]int{}
	dead := map[int]bool{}
	for place, h := range heats {
		if !h.Complete() {
			continue
		}
		type run struct {
			entry int64
			time  float64
		}
		var runs []run
		for _, l := range h.Lanes {
			if l.EntryID == nil || l.FinishTime == nil || l.Ignored {
				continue
			}
			runs = append(runs, run{*l.EntryID, *l.FinishTime})
		}
		if len(runs) < 2 {
			continue
		}
		sort.Slice(runs, func(i, j int) bool { return runs[i].time < runs[j].time })

		// A run-off that itself ties has settled nothing. Saying so is better
		// than picking one, which is the whole reason the run-off exists.
		if runs[0].time == runs[1].time {
			dead[place] = true
			continue
		}
		for i, r := range runs {
			order[r.entry] = i
		}
	}
	return order, dead, nil
}

// DeadHeatRunOffs lists the places whose run-off finished level and has to be
// run again.
func (db *DB) DeadHeatRunOffs(ctx context.Context, raceID int64) ([]int, error) {
	_, dead, err := db.runOffOrder(ctx, raceID)
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, len(dead))
	for place := range dead {
		out = append(out, place)
	}
	sort.Ints(out)
	return out, nil
}

// applyRunOffs splits tied groups whose run-off has been run.
//
// The averages are left exactly as they were. Two cars that tied for first
// still show the same average afterwards — they were the same speed, and the
// run-off decided the trophy, not the times.
func applyRunOffs(results []scoring.Result, order map[int64]int) {
	if len(order) == 0 {
		return
	}
	for i := 0; i < len(results); {
		if results[i].Place == 0 || !results[i].Tied {
			i++
			continue
		}
		start, place := i, results[i].Place
		for ; i < len(results) && results[i].Place == place; i++ {
		}
		group := results[start:i]

		// Only if every car in the group raced the run-off.
		settled := true
		for _, r := range group {
			if _, ok := order[r.EntryID]; !ok {
				settled = false
			}
		}
		if !settled {
			continue
		}

		sort.SliceStable(group, func(a, b int) bool {
			return order[group[a].EntryID] < order[group[b].EntryID]
		})
		for k := range group {
			group[k].Place = place + k
			group[k].Tied = false
		}
	}
}

// ErrNoRunOff is returned when a run-off is asked about that does not exist.
var ErrNoRunOff = errors.New("no run-off has been run for that place")
