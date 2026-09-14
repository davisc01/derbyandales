package app

import (
	"context"
	"fmt"
	"time"

	"github.com/davisc01/derbyandales/internal/bus"
)

// The club pauses racing halfway through the heats. People come to the table,
// vote for the design and theme trophies, and refuel — the venue is a bar.
//
// It is part of how a race night runs, so the software drives it: racing stops
// on its own at the halfway heat, voting opens at that moment and closes when
// racing resumes, and a backup is taken while everything is quiet.
//
// There is no set length. Nothing here counts down, and nothing ends an
// intermission except someone pressing resume.

// KeyIntermissionAfter overrides which heat the intermission follows. Zero
// disables the intermission; unset means halfway.
const KeyIntermissionAfter = "intermission_after_heat"

// IntermissionState is what the coordinator screen and the displays show.
type IntermissionState struct {
	Active bool `json:"active"`
	// AfterHeat is the heat the intermission follows, 0 when disabled.
	AfterHeat int `json:"after_heat"`
	// HeatsRemaining is how many are left once racing resumes.
	HeatsRemaining int `json:"heats_remaining"`
	// VotingOpen mirrors Active: the voting window is exactly the intermission.
	VotingOpen bool      `json:"voting_open"`
	StartedAt  time.Time `json:"started_at,omitempty"`
}

// IntermissionHeat returns the heat number the intermission follows for a race.
//
// The default is halfway through, rounded down: heat 10 of 20, heat 12 of 25.
// A stored setting overrides it, and zero turns the intermission off.
func (rc *RaceController) IntermissionHeat(ctx context.Context, raceID int64) int {
	if configured, err := rc.app.DB.SettingInt(ctx, KeyIntermissionAfter, -1); err == nil && configured >= 0 {
		return configured
	}
	p, err := rc.app.DB.Progress(ctx, raceID)
	if err != nil || p.Total < 4 {
		// Too few heats for a halfway point to mean anything.
		return 0
	}
	return p.Total / 2
}

// Intermission reports the current state.
func (rc *RaceController) Intermission(ctx context.Context) IntermissionState {
	rc.mu.Lock()
	state := IntermissionState{
		Active:     rc.intermission,
		VotingOpen: rc.intermission,
		StartedAt:  rc.intermissionAt,
	}
	raceID := rc.raceID
	rc.mu.Unlock()

	if raceID == 0 {
		return state
	}
	state.AfterHeat = rc.IntermissionHeat(ctx, raceID)
	if p, err := rc.app.DB.Progress(ctx, raceID); err == nil {
		state.HeatsRemaining = p.Total - p.Completed
	}
	return state
}

// VotingOpen reports whether the ballot is accepting votes.
//
// The tablet sits on the table all evening, so this is what stops taps outside
// the intermission from going nowhere silently.
func (rc *RaceController) VotingOpen() bool {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.intermission
}

// startIntermission pauses racing and opens voting.
//
// Called by the race controller when the halfway heat's results land. It is not
// exported: entering the intermission is something the race does, not something
// a person asks for.
func (rc *RaceController) startIntermission(ctx context.Context, raceID int64) {
	rc.mu.Lock()
	if rc.intermission {
		rc.mu.Unlock()
		return
	}
	rc.intermission = true
	rc.intermissionAt = time.Now()
	// Cancel any pending auto-advance. The intermission is a hard stop, not a
	// long pause between heats.
	rc.advanceAt = time.Time{}
	rc.mu.Unlock()

	if dev := rc.app.Timer.Device(); dev != nil {
		// Nothing should be armed while people are milling about near the track.
		dev.Disarm()
	}

	if err := rc.app.DB.EnsureVoteCategories(ctx, raceID); err != nil {
		rc.app.Log.Warn("preparing the ballot", "err", err)
	}

	// A known-quiet moment halfway through the night is when a snapshot is
	// cheapest and most useful.
	if _, err := rc.app.Backup(ctx, BackupIntermission); err != nil {
		rc.app.Log.Warn("intermission snapshot failed", "err", err)
	}

	_ = rc.app.DB.Audit(ctx, "system", "race.intermission", "voting opened")
	rc.app.Log.Info("intermission", "race", raceID)
	rc.app.Bus.Publish(bus.TopicRace, "intermission", rc.Intermission(ctx))
	rc.app.Bus.Publish(bus.TopicVote, "opened", nil)
}

// ResumeRacing ends the intermission, closes voting and arms the next heat.
func (rc *RaceController) ResumeRacing(ctx context.Context) error {
	rc.mu.Lock()
	if !rc.intermission {
		rc.mu.Unlock()
		return fmt.Errorf("there is no intermission to resume from")
	}
	rc.intermission = false
	rc.intermissionAt = time.Time{}
	raceID := rc.raceID
	rc.mu.Unlock()

	_ = rc.app.DB.Audit(ctx, "coordinator", "race.resume", "voting closed")
	rc.app.Log.Info("racing resumed", "race", raceID)
	rc.app.Bus.Publish(bus.TopicVote, "closed", nil)
	rc.app.Bus.Publish(bus.TopicRace, "resumed", rc.Intermission(ctx))

	return rc.ArmNext(ctx)
}

// ReopenVoting puts the race back into the intermission without racing.
//
// Someone will resume a minute before the last person votes. Reopening is one
// action rather than a database edit.
func (rc *RaceController) ReopenVoting(ctx context.Context) error {
	rc.mu.Lock()
	raceID := rc.raceID
	if rc.intermission {
		rc.mu.Unlock()
		return nil
	}
	if raceID == 0 {
		rc.mu.Unlock()
		return fmt.Errorf("no race is loaded")
	}
	rc.intermission = true
	rc.intermissionAt = time.Now()
	rc.advanceAt = time.Time{}
	rc.mu.Unlock()

	if dev := rc.app.Timer.Device(); dev != nil {
		dev.Disarm()
	}
	if err := rc.app.DB.EnsureVoteCategories(ctx, raceID); err != nil {
		return err
	}

	_ = rc.app.DB.Audit(ctx, "coordinator", "vote.reopen", "voting reopened")
	rc.app.Bus.Publish(bus.TopicVote, "opened", nil)
	rc.app.Bus.Publish(bus.TopicRace, "intermission", rc.Intermission(ctx))
	return nil
}

// EndIntermissionWithoutRacing closes voting but leaves racing stopped, for a
// coordinator who wants the ballot shut before carrying on.
func (rc *RaceController) EndIntermissionWithoutRacing(ctx context.Context) {
	rc.mu.Lock()
	was := rc.intermission
	rc.intermission = false
	rc.intermissionAt = time.Time{}
	rc.mu.Unlock()

	if was {
		rc.app.Bus.Publish(bus.TopicVote, "closed", nil)
		rc.app.Bus.Publish(bus.TopicRace, "intermission-ended", rc.Intermission(ctx))
	}
}

// shouldPauseAfter reports whether the heat just completed is the one the
// intermission follows.
func (rc *RaceController) shouldPauseAfter(ctx context.Context, raceID, heatNumber int64) bool {
	after := rc.IntermissionHeat(ctx, raceID)
	if after <= 0 || int64(after) != heatNumber {
		return false
	}
	// Only once per race. Re-running a heat around the halfway mark should not
	// call everyone back to the table a second time.
	done, err := rc.app.DB.Setting(ctx, intermissionDoneKey(raceID), "")
	if err == nil && done == "1" {
		return false
	}
	_ = rc.app.DB.SetSetting(ctx, intermissionDoneKey(raceID), "1")
	return true
}

// intermissionDoneKey records that a race has already had its intermission.
func intermissionDoneKey(raceID int64) string {
	return fmt.Sprintf("intermission_done_%d", raceID)
}
