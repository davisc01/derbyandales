package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/davisc01/derbyandales/internal/store"
	"github.com/davisc01/derbyandales/internal/timer"
)

// The intermission is a hard stop halfway through the heats, during which
// voting is open. These drive it against the simulator.

func TestIntermissionDefaultsToHalfway(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	s, err := a.Race.GenerateSchedule(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	want := len(s.Heats) / 2
	if got := a.Race.IntermissionHeat(ctx, raceID); got != want {
		t.Errorf("intermission after heat %d, want %d (half of %d)", got, want, len(s.Heats))
	}
}

func TestIntermissionCanBeMovedOrDisabled(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}

	if err := a.DB.SetSetting(ctx, KeyIntermissionAfter, "6"); err != nil {
		t.Fatal(err)
	}
	if got := a.Race.IntermissionHeat(ctx, raceID); got != 6 {
		t.Errorf("configured intermission = %d, want 6", got)
	}

	// Zero turns it off — some nights may not want one.
	if err := a.DB.SetSetting(ctx, KeyIntermissionAfter, "0"); err != nil {
		t.Fatal(err)
	}
	if got := a.Race.IntermissionHeat(ctx, raceID); got != 0 {
		t.Errorf("disabled intermission = %d, want 0", got)
	}
}

// Racing stops on its own at the halfway heat and voting opens. Nobody has to
// remember either.
func TestRacingPausesForTheIntermission(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	if err := a.DB.SetSetting(ctx, store.KeyAutoAdvanceSecs, "0"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	after := a.Race.IntermissionHeat(ctx, raceID)
	if after < 2 {
		t.Fatalf("intermission heat %d is too early to test", after)
	}

	if a.Race.VotingOpen() {
		t.Fatal("voting should be closed before the intermission")
	}

	if err := a.Race.Start(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Race.Stop)

	runHeatsUntil(t, a, raceID, func() bool { return a.Race.Intermission(ctx).Active }, 60*time.Second)

	state := a.Race.Intermission(ctx)
	if !state.Active {
		t.Fatal("racing never paused for the intermission")
	}
	p, _ := a.DB.Progress(ctx, raceID)
	if p.Completed != after {
		t.Errorf("paused after %d heats, want %d", p.Completed, after)
	}
	if !state.VotingOpen || !a.Race.VotingOpen() {
		t.Error("voting should open with the intermission")
	}

	// The ballot is ready without anyone preparing it.
	categories, err := a.DB.VoteCategories(ctx, raceID)
	if err != nil || len(categories) == 0 {
		t.Errorf("the ballot was not prepared: %v", err)
	}

	// A quiet moment is when a snapshot is cheapest.
	found := false
	for _, b := range ListBackups(a.Paths.Backups) {
		if b.Reason == BackupIntermission {
			found = true
		}
	}
	if !found {
		t.Error("the intermission should have taken a snapshot")
	}
}

// Auto-advance must not step over the pause.
func TestAutoAdvanceCannotSkipTheIntermission(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	a.DB.SetSetting(ctx, store.KeyAutoAdvanceSecs, "0")
	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	after := a.Race.IntermissionHeat(ctx, raceID)

	if err := a.Race.Start(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Race.Stop)

	runHeatsUntil(t, a, raceID, func() bool { return a.Race.Intermission(ctx).Active }, 60*time.Second)

	// Give auto-advance every chance to misbehave.
	time.Sleep(2 * time.Second)

	p, _ := a.DB.Progress(ctx, raceID)
	if p.Completed != after {
		t.Errorf("auto-advance ran past the intermission: %d heats done, want %d",
			p.Completed, after)
	}
	if !a.Race.Intermission(ctx).Active {
		t.Error("the intermission ended by itself")
	}
}

// A coordinator who reflexively presses "arm next heat" should be told, not
// have the race restart around them.
func TestArmingIsRefusedDuringTheIntermission(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	a.DB.SetSetting(ctx, store.KeyAutoAdvanceSecs, "0")
	a.Race.GenerateSchedule(ctx, raceID)
	if err := a.Race.Start(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Race.Stop)

	runHeatsUntil(t, a, raceID, func() bool { return a.Race.Intermission(ctx).Active }, 60*time.Second)

	err := a.Race.ArmNext(ctx)
	if err == nil {
		t.Fatal("arming during the intermission was allowed")
	}
	if !strings.Contains(err.Error(), "intermission") {
		t.Errorf("error = %q, want it to explain why", err)
	}
}

// Resuming closes voting and carries on.
func TestResumingClosesVoting(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	a.DB.SetSetting(ctx, store.KeyAutoAdvanceSecs, "0")
	a.Race.GenerateSchedule(ctx, raceID)
	if err := a.Race.Start(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Race.Stop)

	runHeatsUntil(t, a, raceID, func() bool { return a.Race.Intermission(ctx).Active }, 60*time.Second)

	if err := a.Race.ResumeRacing(ctx); err != nil {
		t.Fatalf("ResumeRacing: %v", err)
	}
	if a.Race.VotingOpen() {
		t.Error("voting should close when racing resumes")
	}
	if a.Race.Intermission(ctx).Active {
		t.Error("the intermission should be over")
	}

	// Resuming twice is a mistake worth reporting rather than ignoring.
	if err := a.Race.ResumeRacing(ctx); err == nil {
		t.Error("resuming with no intermission should report an error")
	}
}

// Someone will resume a minute before the last person votes.
func TestVotingCanBeReopened(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	a.DB.SetSetting(ctx, store.KeyAutoAdvanceSecs, "0")
	a.Race.GenerateSchedule(ctx, raceID)
	if err := a.Race.SetRace(ctx, raceID); err != nil {
		t.Fatal(err)
	}

	if a.Race.VotingOpen() {
		t.Fatal("voting starts closed")
	}
	if err := a.Race.ReopenVoting(ctx); err != nil {
		t.Fatalf("ReopenVoting: %v", err)
	}
	if !a.Race.VotingOpen() {
		t.Error("voting should be open after reopening")
	}

	a.Race.EndIntermissionWithoutRacing(ctx)
	if a.Race.VotingOpen() {
		t.Error("voting should be closed again")
	}
}

// Re-running a heat around the halfway mark must not call everyone back to the
// table a second time.
func TestIntermissionHappensOncePerRace(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	a.DB.SetSetting(ctx, store.KeyAutoAdvanceSecs, "0")
	a.Race.GenerateSchedule(ctx, raceID)
	after := int64(a.Race.IntermissionHeat(ctx, raceID))

	if !a.Race.shouldPauseAfter(ctx, raceID, after) {
		t.Fatal("the first time should pause")
	}
	if a.Race.shouldPauseAfter(ctx, raceID, after) {
		t.Error("the second time should not pause again")
	}
}

// runHeatsUntil plays the part of the person at the track until done() is true.
func runHeatsUntil(t *testing.T, a *App, raceID int64, done func() bool, timeout time.Duration) {
	t.Helper()
	sim := a.Timer.Simulator()
	if sim == nil {
		t.Fatal("expected the simulated timer")
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		p, err := a.DB.Progress(context.Background(), raceID)
		if err == nil && p.Completed >= p.Total {
			return
		}
		if a.Timer.Device().State() == timer.StateMark {
			sim.CloseGate()
			time.Sleep(MinGateSettle)
			sim.OpenGate()
		}
		time.Sleep(120 * time.Millisecond)
	}
	t.Fatalf("timed out after %v", timeout)
}
