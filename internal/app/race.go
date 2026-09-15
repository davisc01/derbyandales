package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/davisc01/derbyandales/internal/bus"
	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/schedule"
	"github.com/davisc01/derbyandales/internal/scoring"
	"github.com/davisc01/derbyandales/internal/store"
	"github.com/davisc01/derbyandales/internal/timer"
)

// RaceController runs a race night: it arms heats, records what the timer says,
// and moves on.
//
// The design goal is that the coordinator touches nothing during a normal heat.
// Results land, the screens show them, the next heat arms itself. Every manual
// control is for an exception — a re-run, a struck-out lane — rather than part
// of the main path.
type RaceController struct {
	app *App

	mu       sync.Mutex
	raceID   int64
	current  *store.HeatView
	running  bool
	autoNext bool
	stop     context.CancelFunc

	// pending is the heat whose results have just been shown and which will
	// advance when the pause elapses.
	advanceAt time.Time

	// intermission is the halfway pause, during which voting is open. It is a
	// hard stop: auto-advance must not step over it.
	intermission   bool
	intermissionAt time.Time
}

// DefaultAutoAdvance is how long a finished heat stays on the screens before
// the next one arms — which is also how long the finish order is up for, since
// the screens follow the race rather than run a timer of their own. Long enough
// to read four names and times, short enough to keep a twenty-five heat race
// moving. The club asked for seven.
const DefaultAutoAdvance = 7 * time.Second

// NewRaceController returns an idle controller.
func NewRaceController(a *App) *RaceController { return &RaceController{app: a} }

// LoadMostRecentRace points the controller at the race most likely to be the
// one in progress: the newest season's furthest-along race that is not finished.
//
// Without this, a TV plugged in at the venue would show nothing until someone
// opened the coordinator screen and chose a race — which is exactly the kind of
// step that gets forgotten while carrying an HDMI cable.
func (rc *RaceController) LoadMostRecentRace(ctx context.Context) {
	seasons, err := rc.app.DB.Seasons(ctx)
	if err != nil || len(seasons) == 0 {
		return
	}

	// Seasons come back newest first.
	for _, season := range seasons {
		races, err := rc.app.DB.Races(ctx, season.ID)
		if err != nil {
			continue
		}
		// Walk backwards: the last race that has started is the interesting one.
		for i := len(races) - 1; i >= 0; i-- {
			if races[i].Status == model.StatusComplete {
				continue
			}
			if err := rc.SetRace(ctx, races[i].ID); err == nil {
				rc.app.Log.Info("loaded race for the displays",
					"season", season.Name, "race", races[i].Name)
				return
			}
		}
	}
}

// RaceState is what the coordinator screen and the displays show.
type RaceState struct {
	Running   bool            `json:"running"`
	RaceID    int64           `json:"race_id"`
	RaceName  string          `json:"race_name"`
	Heat      *store.HeatView `json:"heat,omitempty"`
	HeatNo    int             `json:"heat_no"`
	HeatTotal int             `json:"heat_total"`
	Completed int             `json:"completed"`
	Timer     string          `json:"timer_state"`
	Gate      string          `json:"gate"`
	AutoNext  bool            `json:"auto_next"`
	// Intermission is true while racing is paused for voting.
	Intermission bool `json:"intermission"`
	// AdvanceIn is how long until the next heat arms, when counting down.
	AdvanceIn float64 `json:"advance_in_secs"`
}

// State reports the current race state.
func (rc *RaceController) State(ctx context.Context) RaceState {
	rc.mu.Lock()
	s := RaceState{
		Running:      rc.running,
		RaceID:       rc.raceID,
		Heat:         rc.current,
		AutoNext:     rc.autoNext,
		Intermission: rc.intermission,
	}
	if !rc.advanceAt.IsZero() {
		if remaining := time.Until(rc.advanceAt); remaining > 0 {
			s.AdvanceIn = remaining.Seconds()
		}
	}
	rc.mu.Unlock()

	if s.Heat != nil {
		s.HeatNo = s.Heat.Number
	}
	if s.RaceID != 0 {
		race, err := rc.app.DB.Race(ctx, s.RaceID)
		if err == nil {
			s.RaceName = race.Name
		}
		if p, err := rc.app.DB.Progress(ctx, s.RaceID); err == nil {
			s.HeatTotal, s.Completed = p.Total, p.Completed
		}
		// A bracket builds each heat as it is armed, so the heats that exist
		// are not the whole night. What is left is one heat per undecided
		// matchup.
		if race.Bracket() {
			if all, err := rc.app.DB.Matchups(ctx, s.RaceID); err == nil {
				s.HeatTotal = s.Completed
				for _, m := range all {
					if m.WinnerEntryID == nil {
						s.HeatTotal++
					}
				}
			}
		}
	}

	status := rc.app.Timer.Status()
	s.Timer = status.State
	switch {
	case !status.Connected:
		s.Gate = "no timer"
	case !status.GateKnown:
		s.Gate = "unknown"
	case status.GateClosed:
		s.Gate = "closed"
	default:
		s.Gate = "open"
	}
	return s
}

// GenerateSchedule builds and stores the heat schedule for a race.
//
// This is what "close check-in" does. It refuses to run over a race that has
// already started, because regenerating would discard results.
func (rc *RaceController) GenerateSchedule(ctx context.Context, raceID int64) (*schedule.Schedule, error) {
	season, race, err := rc.seasonAndRace(ctx, raceID)
	if err != nil {
		return nil, err
	}
	// A bracket's heats are its matchups, built one at a time as each is
	// armed. A round-robin schedule on top of that would put every car on the
	// track four times in a race that is supposed to eliminate them.
	if race.Bracket() {
		return nil, errors.New("this championship runs as a bracket — build it on the championship page instead")
	}

	if p, err := rc.app.DB.Progress(ctx, raceID); err == nil && p.Completed > 0 {
		return nil, fmt.Errorf("%d heats have already been run; clear them first", p.Completed)
	}

	entries, err := rc.app.DB.RacingEntries(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if len(entries) < 2 {
		return nil, fmt.Errorf("need at least 2 cars checked in, have %d", len(entries))
	}

	s, err := schedule.Generate(len(entries), season.LaneCount)
	if err != nil {
		return nil, err
	}
	if err := rc.app.DB.SaveSchedule(ctx, raceID, s, entries); err != nil {
		return nil, err
	}
	if err := rc.app.DB.SetRaceStatus(ctx, raceID, model.StatusRacing); err != nil {
		return nil, err
	}

	_ = rc.app.DB.Audit(ctx, "coordinator", "race.schedule",
		fmt.Sprintf("%s: %d cars, %d heats", race.Name, len(entries), len(s.Heats)))

	if _, err := rc.app.Backup(ctx, BackupCheckinClose); err != nil {
		rc.app.Log.Warn("snapshot after closing check-in failed", "err", err)
	}

	rc.app.Bus.Publish(bus.TopicRace, "scheduled", map[string]any{
		"race_id":           raceID,
		"heats":             len(s.Heats),
		"cars":              s.Cars,
		"perfect":           s.Perfect(),
		"repeated_meetings": s.RepeatedMeetings,
		"back_to_back":      schedule.BackToBack(s),
	})
	return s, nil
}

func (rc *RaceController) seasonAndRace(ctx context.Context, raceID int64) (model.Season, model.Race, error) {
	race, err := rc.app.DB.Race(ctx, raceID)
	if err != nil {
		return model.Season{}, model.Race{}, err
	}
	season, err := rc.app.DB.Season(ctx, race.SeasonID)
	return season, race, err
}

// Start begins running a race.
func (rc *RaceController) Start(ctx context.Context, raceID int64) error {
	if rc.app.Timer.Device() == nil {
		return errors.New("connect the timer first")
	}

	bench := rc.app.Timer.LastBench()
	if !bench.Ready() {
		return errors.New("the timer test has not passed — run it, or record an override")
	}
	if bench.Stale(timer.MaxBenchAge) {
		return errors.New("the timer test is more than two hours old — run it again")
	}

	race, err := rc.app.DB.Race(ctx, raceID)
	if err != nil {
		return err
	}
	if race.Bracket() {
		if all, err := rc.app.DB.Matchups(ctx, raceID); err != nil || len(all) == 0 {
			return errors.New("the bracket has not been built — do that on the championship page first")
		}
	}

	rc.Stop()

	loopCtx, cancel := context.WithCancel(context.Background())
	rc.mu.Lock()
	rc.raceID = raceID
	rc.running = true
	rc.autoNext = true
	rc.stop = cancel
	rc.mu.Unlock()

	go rc.loop(loopCtx)

	rc.app.Bus.Publish(bus.TopicRace, "started", rc.State(ctx))
	return rc.ArmNext(ctx)
}

// Stop halts racing without discarding anything.
func (rc *RaceController) Stop() {
	rc.mu.Lock()
	stop := rc.stop
	rc.stop = nil
	rc.running = false
	rc.advanceAt = time.Time{}
	rc.intermission = false
	rc.mu.Unlock()

	if stop != nil {
		stop()
	}
	if dev := rc.app.Timer.Device(); dev != nil {
		dev.Disarm()
	}
	rc.app.Bus.Publish(bus.TopicRace, "stopped", nil)
}

// SetAutoAdvance turns automatic progression on or off.
func (rc *RaceController) SetAutoAdvance(on bool) {
	rc.mu.Lock()
	rc.autoNext = on
	if !on {
		rc.advanceAt = time.Time{}
	}
	rc.mu.Unlock()
	rc.app.Bus.Publish(bus.TopicRace, "auto-advance", map[string]bool{"on": on})
}

// ArmNext loads the next unrun heat and arms the timer for it.
//
// It refuses during the intermission. A coordinator who reflexively presses
// "arm next heat" while people are at the table voting should be told, not
// have the race silently restart around them.
func (rc *RaceController) ArmNext(ctx context.Context) error {
	rc.mu.Lock()
	raceID := rc.raceID
	paused := rc.intermission
	rc.advanceAt = time.Time{}
	rc.mu.Unlock()

	if paused {
		return errors.New("the race is in its intermission — resume racing to carry on")
	}

	if raceID == 0 {
		return errors.New("no race is running")
	}

	race, err := rc.app.DB.Race(ctx, raceID)
	if err != nil {
		return err
	}
	if race.Bracket() {
		return rc.armNextMatchup(ctx, race)
	}

	heat, err := rc.app.DB.NextHeat(ctx, raceID)
	if errors.Is(err, store.ErrNotFound) {
		return rc.finishRace(ctx, raceID)
	}
	if err != nil {
		return err
	}
	return rc.arm(ctx, heat)
}

// ArmHeat arms a specific heat, for re-running one.
func (rc *RaceController) ArmHeat(ctx context.Context, heatID int64) error {
	heat, err := rc.app.DB.Heat(ctx, heatID)
	if err != nil {
		return err
	}
	rc.mu.Lock()
	rc.raceID = heat.RaceID
	rc.advanceAt = time.Time{}
	rc.mu.Unlock()
	return rc.arm(ctx, heat)
}

func (rc *RaceController) arm(ctx context.Context, heat store.HeatView) error {
	dev := rc.app.Timer.Device()
	if dev == nil {
		return errors.New("no timer connected")
	}

	season, _, err := rc.seasonAndRace(ctx, heat.RaceID)
	if err != nil {
		return err
	}

	if err := dev.ArmHeat(heat.LaneMask(), season.LaneCount); err != nil {
		return fmt.Errorf("arm the timer: %w", err)
	}
	if err := rc.app.DB.SetHeatStatus(ctx, heat.ID, model.HeatArmed); err != nil {
		return err
	}

	rc.mu.Lock()
	rc.current = &heat
	rc.mu.Unlock()

	rc.app.Bus.Publish(bus.TopicRace, "heat.armed", rc.State(ctx))
	rc.app.Log.Info("heat armed", "race", heat.RaceID, "heat", heat.Number)
	return nil
}

// loop consumes timer events and drives the race forward.
func (rc *RaceController) loop(ctx context.Context) {
	dev := rc.app.Timer.Device()
	if dev == nil {
		return
	}
	events, unsubscribe := dev.Subscribe()
	defer unsubscribe()

	// A slow ticker handles the auto-advance countdown and the overdue check,
	// neither of which is driven by an incoming event.
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case ev, ok := <-events:
			if !ok {
				return
			}
			rc.handleTimerEvent(ctx, ev)

		case <-tick.C:
			rc.tick(ctx, dev)
		}
	}
}

func (rc *RaceController) handleTimerEvent(ctx context.Context, ev timer.Event) {
	switch ev.Kind {
	case timer.EvStateChanged, timer.EvGateOpen, timer.EvGateClosed:
		// The screens care about staging and starting, so republish state.
		rc.app.Bus.Publish(bus.TopicRace, "state", rc.State(ctx))

	case timer.EvRaceFinished:
		rc.recordFinish(ctx)

	case timer.EvLostConnection:
		rc.app.Log.Error("timer lost mid-race")
		rc.app.Bus.Publish(bus.TopicRace, "timer-lost", nil)
	}
}

// recordFinish stores the results of the heat that just ran.
func (rc *RaceController) recordFinish(ctx context.Context) {
	dev := rc.app.Timer.Device()
	if dev == nil {
		return
	}

	rc.mu.Lock()
	heat := rc.current
	autoNext := rc.autoNext
	rc.mu.Unlock()
	if heat == nil {
		return
	}

	results, missing := dev.Finish()
	if len(results) == 0 {
		return
	}

	times := make(map[int]float64, len(results))
	allDNF := true
	for _, r := range results {
		times[r.Lane] = r.Time
		if r.Time < timer.DNFTime {
			allDNF = false
		}
	}

	// Every lane reading as a non-finish, with the timer having reported every
	// one of them, nearly always means it was triggered with no cars on it — a
	// hand through the beam, a knocked gate. Recording that as a heat would be
	// worse than useless, so stop and let a person decide.
	//
	// A bad read is a different thing and must not be swallowed by this. There
	// the timer said nothing about those lanes at all, the cars did run, and
	// 9.999 is the honest record of what was timed.
	if allDNF && len(missing) == 0 {
		rc.app.Log.Warn("every lane read as a non-finish; racing paused",
			"heat", heat.Number)
		rc.app.Bus.Publish(bus.TopicRace, "suspect-result", map[string]any{
			"heat":   heat.Number,
			"reason": "no lane recorded a finish — the timer may have triggered with no cars on the track",
		})
		rc.SetAutoAdvance(false)
		return
	}

	// A lane the timer never mentioned is recorded at 9.999 rather than left
	// out, but it is said out loud: the coordinator may well want to re-run it,
	// and finding out from the standings a week later would be worse.
	if len(missing) > 0 {
		lanes := make([]string, 0, len(missing))
		for _, l := range missing {
			lanes = append(lanes, strconv.Itoa(l))
		}
		rc.app.Log.Warn("the timer did not report every lane",
			"heat", heat.Number, "lanes", strings.Join(lanes, ","))
		rc.app.Bus.Publish(bus.TopicRace, "bad-read", map[string]any{
			"heat":  heat.Number,
			"lanes": missing,
			"reason": fmt.Sprintf("the timer reported nothing for lane %s — recorded as 9.999. Re-run the heat if that is not right.",
				strings.Join(lanes, " and ")),
		})
	}

	if err := rc.app.DB.RecordHeatResults(ctx, heat.ID, times); err != nil {
		rc.app.Log.Error("recording heat results", "heat", heat.Number, "err", err)
		return
	}

	updated, err := rc.app.DB.Heat(ctx, heat.ID)
	if err == nil {
		rc.mu.Lock()
		rc.current = &updated
		rc.mu.Unlock()
	}

	// A bracket heat is decided before anything is shown, so the screens
	// learn the winner with the times. A dead heat still advances: the next
	// thing armed is the same matchup, and nobody has a choice to make.
	if updated.BracketMatchupID != nil {
		champion, err := rc.decideMatchup(ctx, updated)
		if err != nil && !errors.Is(err, ErrDeadHeat) {
			rc.app.Log.Warn("deciding the matchup", "heat", heat.Number, "err", err)
			rc.SetAutoAdvance(false)
		}
		rc.app.Bus.Publish(bus.TopicRace, "heat.finished", rc.State(ctx))
		if champion {
			rc.finishBracket(ctx, heat.RaceID)
			return
		}
		if autoNext && (err == nil || errors.Is(err, ErrDeadHeat)) {
			rc.mu.Lock()
			rc.advanceAt = time.Now().Add(rc.advancePause(ctx))
			rc.mu.Unlock()
		}
		return
	}

	rc.app.Bus.Publish(bus.TopicRace, "heat.finished", rc.State(ctx))
	rc.app.Log.Info("heat complete", "heat", heat.Number, "lanes", len(times))

	// Halfway: stop, open voting, and wait for a person. Checked before
	// auto-advance so the pause cannot be stepped over.
	if rc.shouldPauseAfter(ctx, heat.RaceID, int64(heat.Number)) {
		rc.startIntermission(ctx, heat.RaceID)
		return
	}

	if autoNext {
		pause := rc.advancePause(ctx)
		rc.mu.Lock()
		rc.advanceAt = time.Now().Add(pause)
		rc.mu.Unlock()
	}
}

// advancePause reads the configured gap between heats.
func (rc *RaceController) advancePause(ctx context.Context) time.Duration {
	secs, err := rc.app.DB.SettingInt(ctx, store.KeyAutoAdvanceSecs, int(DefaultAutoAdvance.Seconds()))
	if err != nil || secs < 0 {
		return DefaultAutoAdvance
	}
	return time.Duration(secs) * time.Second
}

// tick handles the auto-advance countdown and overdue results.
func (rc *RaceController) tick(ctx context.Context, dev *timer.Device) {
	rc.mu.Lock()
	advanceAt := rc.advanceAt
	running := rc.running
	paused := rc.intermission
	rc.mu.Unlock()

	if !running || paused {
		return
	}

	if !advanceAt.IsZero() && time.Now().After(advanceAt) {
		rc.mu.Lock()
		rc.advanceAt = time.Time{}
		rc.mu.Unlock()
		if err := rc.ArmNext(ctx); err != nil {
			rc.app.Log.Warn("arming the next heat", "err", err)
		}
		return
	}

	// A car that stops on the track never breaks the finish beam.
	if dev.Machine().Overdue() {
		rc.app.Log.Warn("results overdue; returning to armed")
		dev.Machine().ReturnToMark()
		rc.app.Bus.Publish(bus.TopicRace, "overdue", rc.State(ctx))
	}
}

// ArmRunOff builds the run-off that settles a tie for a trophy, and arms it.
//
// A tie below the podium is left alone: two cars that ran the same average are
// the same speed. A tie for 1st, 2nd or 3rd cannot stand, because a trophy is a
// thing handed to one person.
func (rc *RaceController) ArmRunOff(ctx context.Context, raceID int64, place int) error {
	rc.mu.Lock()
	paused := rc.intermission
	rc.mu.Unlock()
	if paused {
		return errors.New("the race is in its intermission — resume racing before running a tie off")
	}

	heat, err := rc.app.DB.CreateRunOff(ctx, raceID, place)
	if err != nil {
		return err
	}
	// A run-off that has already been run and finished level is re-run rather
	// than added to, so the second attempt replaces the first.
	if heat.Complete() {
		if err := rc.app.DB.ClearHeatResults(ctx, heat.ID); err != nil {
			return err
		}
	}
	_ = rc.app.DB.Audit(ctx, "coordinator", "race.runoff",
		fmt.Sprintf("heat %d settles the tie for place %d", heat.Number, place))
	return rc.ArmHeat(ctx, heat.ID)
}

// ReRun clears a heat's results and arms it again.
//
// This is the answer to a crash, a car that came apart on the track, a false
// start, or a heat the anomaly check flagged at the end of the night. It works
// on any heat that has already been run, in any order, so a heat can be put
// right at the end of the race without disturbing the ones after it: clearing
// its times makes it the next heat waiting to run, and once it is done the
// race carries on from wherever it had got to.
func (rc *RaceController) ReRun(ctx context.Context, heatID int64) error {
	rc.mu.Lock()
	paused := rc.intermission
	rc.mu.Unlock()

	// The intermission is a hard stop, and that has to include this door. A
	// coordinator tidying up a heat while people are standing at the voting
	// table must not send cars down the track at them.
	if paused {
		return errors.New("the race is in its intermission — resume racing before re-running a heat")
	}

	heat, err := rc.app.DB.Heat(ctx, heatID)
	if err != nil {
		return err
	}
	// A heat with no times has not been run, so "re-run" would really mean
	// "jump to it", skipping everything in between. If that is ever wanted it
	// should be its own control with its own wording.
	if !anyRecorded(heat) {
		return fmt.Errorf("heat %d has not been run yet, so there is nothing to re-run", heat.Number)
	}

	if err := rc.undoForReRun(ctx, heat); err != nil {
		return err
	}
	if err := rc.app.DB.ClearHeatResults(ctx, heatID); err != nil {
		return err
	}
	_ = rc.app.DB.Audit(ctx, "coordinator", "race.rerun", fmt.Sprintf("heat %d", heat.Number))
	return rc.ArmHeat(ctx, heatID)
}

// anyRecorded reports whether a heat has at least one time against it.
func anyRecorded(heat store.HeatView) bool {
	for _, l := range heat.Lanes {
		if l.FinishTime != nil {
			return true
		}
	}
	return false
}

// finishRace wraps up when every heat has been run.
func (rc *RaceController) finishRace(ctx context.Context, raceID int64) error {
	// The last heat stays on screen. Blanking it the moment the race ends would
	// wipe the final result while people are still looking at it.
	rc.mu.Lock()
	rc.running = false
	rc.advanceAt = time.Time{}
	rc.mu.Unlock()

	if dev := rc.app.Timer.Device(); dev != nil {
		dev.Disarm()
	}
	if err := rc.app.DB.SetRaceStatus(ctx, raceID, model.StatusVoting); err != nil {
		return err
	}
	if _, err := rc.app.Backup(ctx, BackupRaceComplete); err != nil {
		rc.app.Log.Warn("snapshot after race completion failed", "err", err)
	}

	// Record the night into the season standings while the result is fresh.
	// This is deliberately not fatal: the racing happened either way, and the
	// points can be recomputed from the same rows at any time. Failing the end
	// of a race over a points table would be the wrong trade at a venue.
	if err := rc.app.Season.FreezeRace(ctx, raceID); err != nil {
		rc.app.Log.Warn("recording season points failed", "race", raceID, "err", err)
	}

	standings, err := rc.app.DB.Standings(ctx, raceID)
	if err != nil {
		return err
	}
	// Now that every car has a full night behind it, look for a heat that was a
	// fault rather than a result. This can only be asked at the end: half way
	// through, everybody's slowest run so far is just the slowest of two.
	anomalies, err := rc.app.DB.HeatAnomalies(ctx, raceID)
	if err != nil {
		rc.app.Log.Warn("checking the heats for anomalies failed", "race", raceID, "err", err)
	}
	clear := 0
	for _, a := range anomalies {
		if a.Clear {
			clear++
		}
		rc.app.Log.Info("heat anomaly", "heat", a.Heat, "kind", string(a.Kind),
			"cars", a.Cars, "margin", a.Margin, "clear", a.Clear)
	}

	rc.app.Log.Info("race complete", "race", raceID, "cars", len(standings))
	rc.app.Bus.Publish(bus.TopicRace, "complete", map[string]any{
		"race_id":   raceID,
		"cars":      len(standings),
		"anomalies": len(anomalies),
		"suspect":   clear,
	})
	return nil
}

// CurrentRaceID reports which race is loaded, or zero.
func (rc *RaceController) CurrentRaceID() int64 {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.raceID
}

// SetRace loads a race without starting it, so the displays have something to
// show before the first heat.
func (rc *RaceController) SetRace(ctx context.Context, raceID int64) error {
	if _, err := rc.app.DB.Race(ctx, raceID); err != nil {
		return err
	}
	rc.mu.Lock()
	rc.raceID = raceID
	rc.current = nil
	rc.mu.Unlock()

	if heat, err := rc.app.DB.NextHeat(ctx, raceID); err == nil {
		rc.mu.Lock()
		rc.current = &heat
		rc.mu.Unlock()
	}
	rc.app.Bus.Publish(bus.TopicRace, "race-loaded", rc.State(ctx))
	return nil
}

// EnterTimes records a heat's times by hand.
//
// Two jobs, and they are the same job. A correction — a lane the timer missed,
// a time typed off the printout — and running the whole night with no timer at
// all, which is how anybody learns this software or tests a change. DerbyNet
// had manual entry and it was used constantly.
//
// The times go through exactly the path a timer's do: places are derived here,
// not taken on trust, so a hand-entered heat and a timed one are the same kind
// of thing afterwards. A lane left blank is left alone rather than zeroed.
func (rc *RaceController) EnterTimes(ctx context.Context, heatID int64, times map[int]float64, actor string) error {
	rc.mu.Lock()
	paused := rc.intermission
	rc.mu.Unlock()
	if paused {
		return errors.New("the race is in its intermission — resume racing first")
	}

	heat, err := rc.app.DB.Heat(ctx, heatID)
	if err != nil {
		return err
	}
	if len(times) == 0 {
		return errors.New("no times were entered")
	}

	occupied := map[int]bool{}
	for _, l := range heat.Lanes {
		if l.EntryID != nil {
			occupied[l.Lane] = true
		}
	}
	for lane, t := range times {
		if !occupied[lane] {
			return fmt.Errorf("lane %d has no car in heat %d", lane, heat.Number)
		}
		// A time of zero is what a timer sends for a lane that never finished,
		// and it is rewritten to 9.999 so it sorts last. Someone typing times
		// in should get the same treatment rather than a car that appears to
		// have won by a mile.
		if t <= 0 {
			times[lane] = scoring.DNF
		}
		if t > 0 && t < 0.5 {
			return fmt.Errorf("%.3f s is too fast to be real — check lane %d", t, lane)
		}
	}

	if err := rc.app.DB.RecordHeatResults(ctx, heatID, times); err != nil {
		return err
	}
	_ = rc.app.DB.Audit(ctx, actor, "race.manual",
		fmt.Sprintf("heat %d: %d lanes entered by hand", heat.Number, len(times)))

	updated, err := rc.app.DB.Heat(ctx, heatID)
	if err == nil {
		rc.mu.Lock()
		if rc.current != nil && rc.current.ID == heatID {
			rc.current = &updated
		}
		rc.mu.Unlock()

		// Typed-in times decide a matchup exactly as timed ones do. A dead
		// heat is recorded and not an error: the times are what happened, and
		// arming next runs the matchup again.
		if updated.BracketMatchupID != nil {
			champion, err := rc.decideMatchup(ctx, updated)
			if err != nil && !errors.Is(err, ErrDeadHeat) {
				return err
			}
			if champion {
				defer rc.finishBracket(ctx, heat.RaceID)
			}
		}
	}
	rc.app.Bus.Publish(bus.TopicRace, "heat.finished", rc.State(ctx))
	rc.app.Log.Info("heat entered by hand", "heat", heat.Number, "lanes", len(times))
	return nil
}
