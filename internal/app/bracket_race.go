package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/davisc01/derbyandales/internal/bus"
	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/store"
)

// A championship flagged as a bracket is raced from race control like any other
// night. The differences are all in what "next" and "done" mean: the next heat
// is the next matchup with both cars known, built when it is armed rather than
// scheduled up front; a result moves a winner up the bracket rather than into an
// average; and the race is over when there is a champion, not when a schedule
// runs out.

// ErrDeadHeat is returned when both cars in a matchup record the same time.
var ErrDeadHeat = errors.New("both cars recorded the same time — run the matchup again")

// armNextMatchup arms whatever the bracket needs raced next.
func (rc *RaceController) armNextMatchup(ctx context.Context, race model.Race) error {
	if _, err := rc.app.DB.Champion(ctx, race.ID); err == nil {
		rc.finishBracket(ctx, race.ID)
		return nil
	}

	m, err := rc.app.DB.NextMatchup(ctx, race.ID)
	if errors.Is(err, store.ErrNotFound) {
		all, _ := rc.app.DB.Matchups(ctx, race.ID)
		if len(all) == 0 {
			return errors.New("the bracket has not been built — do that on the championship page first")
		}
		return errors.New("no matchup is ready to race")
	}
	if err != nil {
		return err
	}

	// A matchup that already has a heat with times, and no winner, finished
	// level. The only thing to do is run it again, so the old times go.
	if m.HeatID != nil {
		if heat, err := rc.app.DB.Heat(ctx, *m.HeatID); err == nil && anyRecorded(heat) {
			if err := rc.app.DB.ClearHeatResults(ctx, heat.ID); err != nil {
				return err
			}
		}
	}

	heat, err := rc.app.DB.CreateMatchupHeat(ctx, race.ID, m.ID)
	if err != nil {
		return err
	}
	return rc.arm(ctx, heat)
}

// decideMatchup turns a bracket heat's times into a winner. It reports whether
// the bracket now has its champion.
//
// It is called after every recorded bracket heat, from the timer and from times
// typed in, so the two paths cannot disagree about what a result means.
func (rc *RaceController) decideMatchup(ctx context.Context, heat store.HeatView) (bool, error) {
	if heat.BracketMatchupID == nil {
		return false, nil
	}
	m, err := rc.app.DB.Matchup(ctx, *heat.BracketMatchupID)
	if err != nil {
		return false, err
	}

	// A correction to a matchup already decided. If the times now say the
	// other car won, the old result has to come out first — which is refused
	// once the winner has raced again.
	if m.WinnerEntryID != nil {
		winner, err := headToHead(heat, m)
		if err != nil || winner == *m.WinnerEntryID {
			return false, err
		}
		if err := rc.app.DB.UndoMatchupWinner(ctx, heat.RaceID, m.ID); err != nil {
			return false, err
		}
	}

	if err := rc.app.Bracket.RecordResult(ctx, heat.RaceID, m.ID); err != nil {
		if errors.Is(err, ErrDeadHeat) {
			rc.app.Bus.Publish(bus.TopicRace, "dead-heat", map[string]any{
				"heat":   heat.Number,
				"reason": "both cars recorded the same time, so the matchup runs again",
			})
		}
		return false, err
	}
	_, err = rc.app.DB.Champion(ctx, heat.RaceID)
	return err == nil, nil
}

// headToHead reports which car in a matchup the heat's times favour.
func headToHead(heat store.HeatView, m model.BracketMatchup) (int64, error) {
	times := map[int64]float64{}
	for _, l := range heat.Lanes {
		if l.EntryID != nil && l.FinishTime != nil && !l.Ignored {
			times[*l.EntryID] = *l.FinishTime
		}
	}
	top, bottom := *m.TopEntryID, *m.BottomEntryID
	tt, okT := times[top]
	bt, okB := times[bottom]
	switch {
	case !okT || !okB:
		return 0, errors.New("that heat does not have a time for both cars yet")
	case tt < bt:
		return top, nil
	case bt < tt:
		return bottom, nil
	}
	return 0, ErrDeadHeat
}

// finishBracket stops racing once there is a champion.
//
// The bracket has already marked the championship complete and taken its
// snapshot when the final was decided. What is left is to stop the loop: there
// is nothing more to arm, and a season's points are not touched, because a
// bracket produces no averages to score.
func (rc *RaceController) finishBracket(ctx context.Context, raceID int64) {
	rc.mu.Lock()
	wasRunning := rc.running
	rc.running = false
	rc.advanceAt = time.Time{}
	rc.mu.Unlock()

	if dev := rc.app.Timer.Device(); dev != nil {
		dev.Disarm()
	}
	if wasRunning {
		rc.app.Bus.Publish(bus.TopicRace, "complete", map[string]any{"race_id": raceID})
	}
}

// undoForReRun takes a bracket heat's result back out before it is run again.
func (rc *RaceController) undoForReRun(ctx context.Context, heat store.HeatView) error {
	if heat.BracketMatchupID == nil {
		return nil
	}
	m, err := rc.app.DB.Matchup(ctx, *heat.BracketMatchupID)
	if err != nil || m.WinnerEntryID == nil {
		return err
	}
	_, wasChampion := rc.app.DB.Champion(ctx, heat.RaceID)
	if err := rc.app.DB.UndoMatchupWinner(ctx, heat.RaceID, m.ID); err != nil {
		return err
	}
	// Re-running the final un-crowns the champion, so the championship is back
	// to being raced.
	if wasChampion == nil {
		if err := rc.app.DB.SetRaceStatus(ctx, heat.RaceID, model.StatusRacing); err != nil {
			return err
		}
	}
	_ = rc.app.DB.Audit(ctx, "coordinator", "bracket.undo",
		fmt.Sprintf("round %d position %d re-run", m.Round, m.Position))
	return nil
}
