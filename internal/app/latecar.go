package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/davisc01/derbyandales/internal/bus"
	"github.com/davisc01/derbyandales/internal/schedule"
	"github.com/davisc01/derbyandales/internal/store"
)

// LateCarPlan is what race control tells the coordinator about cars checked in
// after the schedule was built, and what fitting them in would do.
type LateCarPlan struct {
	Cars []store.EntryView `json:"cars"`
	// CanFit is false once the race is over; Reason says why.
	CanFit bool   `json:"can_fit"`
	Reason string `json:"reason,omitempty"`
	// KeptThrough is the last heat left exactly as it is: everything raced,
	// the heat on the track, and the one being loaded at impound.
	KeptThrough int `json:"kept_through"`
	// Unrun is how many heats after that are rebuilt; Heats is how many they
	// become, and EmptyLanes how many lanes in those run with no car.
	Unrun      int `json:"unrun"`
	Heats      int `json:"heats"`
	EmptyLanes int `json:"empty_lanes"`

	fitted [][]*int64
}

// Added is how many heats fitting the cars in adds to the night.
func (p LateCarPlan) Added() int { return p.Heats - p.Unrun }

// LateCars reports cars checked in after the schedule was built, and works out
// — without changing anything — how they would be fitted in.
//
// Nothing here happens on its own. Rebuilding the rest of the night changes who
// races whom and when, and the coordinator is the one standing next to the
// impound table; they choose the moment.
func (rc *RaceController) LateCars(ctx context.Context, raceID int64) (LateCarPlan, error) {
	var plan LateCarPlan
	race, err := rc.app.DB.Race(ctx, raceID)
	if err != nil {
		return plan, err
	}
	// A bracket has no schedule to be late for: its heats are its matchups.
	if race.Bracket() {
		return plan, nil
	}
	late, err := rc.app.DB.UnscheduledEntries(ctx, raceID)
	if err != nil || len(late) == 0 {
		return plan, err
	}
	plan.Cars = late

	// Places and points were written when the last heat landed, and a
	// finished race is not reopened as a side effect of a check-in. The car
	// did not race; the coordinator is told so rather than shown nothing.
	frozen, err := rc.app.DB.RaceFrozen(ctx, raceID)
	if err != nil {
		return plan, err
	}
	if frozen {
		plan.Reason = "The race had already finished, so they did not race."
		return plan, nil
	}

	season, _, err := rc.seasonAndRace(ctx, raceID)
	if err != nil {
		return plan, err
	}
	heats, err := rc.app.DB.Heats(ctx, raceID)
	if err != nil {
		return plan, err
	}
	rc.mu.Lock()
	var onTrack int64
	if rc.current != nil && rc.raceID == raceID {
		onTrack = rc.current.ID
	}
	rc.mu.Unlock()

	plan.KeptThrough = keptThrough(heats, onTrack)

	// Car indices for the scheduler: every car already in a heat, then the
	// late ones.
	index := map[int64]int{}
	var ids []int64
	for _, h := range heats {
		for _, l := range h.Lanes {
			if l.EntryID != nil && !h.RunOff() {
				if _, ok := index[*l.EntryID]; !ok {
					index[*l.EntryID] = len(ids)
					ids = append(ids, *l.EntryID)
				}
			}
		}
	}
	cars := len(ids)
	for _, e := range late {
		index[e.ID] = len(ids)
		ids = append(ids, e.ID)
	}

	var run, remaining []schedule.Heat
	for _, h := range heats {
		if h.RunOff() {
			continue
		}
		heat := make(schedule.Heat, season.LaneCount)
		for i := range heat {
			heat[i] = schedule.Bye
		}
		for _, l := range h.Lanes {
			if l.EntryID != nil && l.Lane >= 1 && l.Lane <= season.LaneCount {
				heat[l.Lane-1] = index[*l.EntryID]
			}
		}
		if h.Number <= plan.KeptThrough {
			run = append(run, heat)
		} else {
			remaining = append(remaining, heat)
		}
	}

	fitted, err := schedule.Fit(run, remaining, cars, len(late), season.LaneCount)
	if err != nil {
		return plan, fmt.Errorf("could not fit the late cars in: %w", err)
	}
	plan.CanFit = true
	plan.Unrun = len(remaining)
	plan.Heats = len(fitted)
	for _, heat := range fitted {
		lanes := make([]*int64, len(heat))
		for i, car := range heat {
			if car == schedule.Bye {
				plan.EmptyLanes++
				continue
			}
			id := ids[car]
			lanes[i] = &id
		}
		plan.fitted = append(plan.fitted, lanes)
	}
	return plan, nil
}

// keptThrough is the last heat that must not change: anything with a time in
// it, the heat on the track, and the next one — which the impound screen has
// already told the official to put in a tray. Changing a tray that is loaded
// is how the wrong car ends up in a lane.
//
// The rule mirrors handleImpound's: the heat on the track is the armed one or,
// failing that, the first with no times; the one being loaded is the next
// unrun heat after it.
func keptThrough(heats []store.HeatView, onTrack int64) int {
	kept := 0
	track := -1
	for i, h := range heats {
		if anyRecorded(h) {
			kept = max(kept, h.Number)
		}
		if h.ID == onTrack {
			track = i
		}
	}
	if track < 0 {
		for i, h := range heats {
			if !h.Complete() {
				track = i
				break
			}
		}
	}
	if track < 0 {
		return kept
	}
	kept = max(kept, heats[track].Number)
	for _, h := range heats[track+1:] {
		if !h.Complete() {
			return max(kept, h.Number)
		}
	}
	return kept
}

// FitLateCars rebuilds the heats not yet run so the late cars race too.
//
// Everyone already scheduled keeps exactly the lanes they were still owed, so
// no average changes shape; the late cars get one run in each lane. It may be
// done during the intermission — it arms nothing, and the intermission is the
// likeliest moment for somebody to walk in.
//
// Adding heats before the halfway pause moves that pause later, by about half
// a heat per car, because the halfway point is worked out from the heat count.
// It cannot move it onto a heat already run, since the count only grows.
func (rc *RaceController) FitLateCars(ctx context.Context, raceID int64) (LateCarPlan, error) {
	// Two coordinators' tabs pressing at once must not each rebuild the night
	// from the same starting point.
	rc.fitting.Lock()
	defer rc.fitting.Unlock()

	plan, err := rc.LateCars(ctx, raceID)
	if err != nil {
		return plan, err
	}
	if len(plan.Cars) == 0 {
		return plan, errors.New("every car checked in is already in the schedule")
	}
	if !plan.CanFit {
		return plan, errors.New(plan.Reason)
	}

	if _, err := rc.app.Backup(ctx, BackupLateCar); err != nil {
		rc.app.Log.Warn("snapshot before fitting in a late car failed", "err", err)
	}
	if err := rc.app.DB.ReplaceUnrunHeats(ctx, raceID, plan.KeptThrough, plan.fitted); err != nil {
		if errors.Is(err, store.ErrHeatAlreadyRun) {
			return plan, errors.New("a heat finished while this was being worked out — try again")
		}
		return plan, err
	}

	names := make([]string, len(plan.Cars))
	for i, e := range plan.Cars {
		names[i] = fmt.Sprintf("%s (#%d)", e.CarName, e.CarNumber)
	}
	_ = rc.app.DB.Audit(ctx, "coordinator", "race.latecar", fmt.Sprintf(
		"%s: heats %d on rebuilt, %d unrun → %d, %d empty lanes",
		strings.Join(names, ", "), plan.KeptThrough+1, plan.Unrun, plan.Heats, plan.EmptyLanes))
	rc.app.Log.Info("late cars fitted in", "race", raceID, "cars", len(plan.Cars),
		"kept_through", plan.KeptThrough, "heats", plan.Heats, "empty_lanes", plan.EmptyLanes)

	rc.app.Bus.Publish(bus.TopicRace, "rescheduled", map[string]any{
		"race_id":     raceID,
		"cars":        len(plan.Cars),
		"added":       plan.Added(),
		"empty_lanes": plan.EmptyLanes,
	})
	return plan, nil
}
