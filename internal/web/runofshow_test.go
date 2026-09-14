package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/davisc01/derbyandales/internal/app"
	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/store"
)

// The run of show is the answer to the bus-factor problem, so what it says has
// to be true at every point in a race night — including before one has started
// and after one has finished.

func stepNamed(t *testing.T, steps []Step, title string) Step {
	t.Helper()
	for _, s := range steps {
		if strings.EqualFold(s.Title, title) {
			return s
		}
	}
	t.Fatalf("no step called %q", title)
	return Step{}
}

func nowStep(steps []Step) (Step, bool) {
	for _, s := range steps {
		if s.State == StepNow {
			return s, true
		}
	}
	return Step{}, false
}

// Someone opens the app for the first time. They should be told what to do, not
// shown nine greyed-out lines.
func TestOnAnEmptyDatabaseTheFirstStepIsTheNextThingToDo(t *testing.T) {
	s := mustServer(t)
	steps, _ := s.runOfShow(context.Background())

	if len(steps) != 11 {
		t.Fatalf("%d steps, want 11", len(steps))
	}
	now, ok := nowStep(steps)
	if !ok {
		t.Fatal("nothing is marked as the next thing to do")
	}
	if now.Number != 1 {
		t.Errorf("the next step is %d (%s), want the first", now.Number, now.Title)
	}
	for _, step := range steps {
		if step.Hint == "" {
			t.Errorf("step %d (%s) has no explanation", step.Number, step.Title)
		}
		if step.Link == "" {
			t.Errorf("step %d (%s) does not say where to go", step.Number, step.Title)
		}
	}
}

// Exactly one step is the next thing at any moment, or the screen is not
// answering the question it exists to answer.
func TestExactlyOneStepIsNextAtEachPointOfTheNight(t *testing.T) {
	s, a, _ := seasonServer(t)
	ctx := context.Background()

	live := liveRace(t, a)
	if err := a.Race.SetRace(ctx, live.ID); err != nil {
		t.Fatal(err)
	}

	countNow := func(label string) {
		t.Helper()
		steps, _ := s.runOfShow(ctx)
		n := 0
		for _, step := range steps {
			if step.State == StepNow {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s: %d steps marked as next, want exactly 1", label, n)
		}
	}

	countNow("race loaded, timer untested")

	// A timer that has been tested unblocks check-in.
	connectAndBench(t, a)
	countNow("timer tested")

	steps, _ := s.runOfShow(ctx)
	checkin := stepNamed(t, steps, "Check the cars in")
	if checkin.State != StepNow {
		t.Errorf("check-in is %q once the timer has been tested", checkin.State)
	}
	if !strings.Contains(checkin.Detail, "checked in") {
		t.Errorf("check-in does not say how many cars: %q", checkin.Detail)
	}

	// Building the schedule moves it on.
	if _, err := a.Race.GenerateSchedule(ctx, live.ID); err != nil {
		t.Fatal(err)
	}
	countNow("schedule built")
	steps, _ = s.runOfShow(ctx)
	if stepNamed(t, steps, "Check the cars in").State != StepDone {
		t.Error("check-in is not done after the schedule was built")
	}
}

// Until the timer has been tested, check-in cannot be closed — and the screen
// should say so rather than letting somebody find out at the button.
func TestAnUntestedTimerBlocksCheckInWithAReason(t *testing.T) {
	s, a, _ := seasonServer(t)
	ctx := context.Background()

	live := liveRace(t, a)
	a.Race.SetRace(ctx, live.ID)

	steps, _ := s.runOfShow(ctx)
	checkin := stepNamed(t, steps, "Check the cars in")
	if checkin.State != StepBlocked {
		t.Fatalf("check-in is %q with no timer tested", checkin.State)
	}
	if !strings.Contains(checkin.Blocker, "timer") {
		t.Errorf("the blocker does not mention the timer: %q", checkin.Blocker)
	}
}

// A simulated timer must never read as "the track has been checked". That is
// the difference between a green tick that means something and one that does not.
func TestASimulatedTimerDoesNotClaimTheTrackWasTested(t *testing.T) {
	s, a, _ := seasonServer(t)
	ctx := context.Background()

	live := liveRace(t, a)
	a.Race.SetRace(ctx, live.ID)
	connectAndBench(t, a)

	steps, _ := s.runOfShow(ctx)
	timerStep := stepNamed(t, steps, "Test the timer")
	if !strings.Contains(strings.ToLower(timerStep.Detail), "simulated") {
		t.Errorf("a simulated timer reads as %q", timerStep.Detail)
	}
	// It must say so loudly, not just in passing: a tick that means nothing is
	// worse than no tick.
	if timerStep.Caveat == "" {
		t.Error("a simulated timer test carries no warning")
	}
	if !strings.Contains(timerStep.Caveat, "real timer has not been tested") {
		t.Errorf("the warning does not say the real timer is untested: %q", timerStep.Caveat)
	}
	// And it still lets the night be rehearsed.
	if timerStep.State != StepDone {
		t.Errorf("the simulated timer test blocks the night (%q)", timerStep.State)
	}
}

// The intermission is a hard stop, and while it is happening it is the only
// thing happening.
func TestDuringTheIntermissionRacingIsNotTheNextStep(t *testing.T) {
	s, a, _ := seasonServer(t)
	ctx := context.Background()

	live := liveRace(t, a)
	a.Race.SetRace(ctx, live.ID)
	connectAndBench(t, a)
	if _, err := a.Race.GenerateSchedule(ctx, live.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.Race.ReopenVoting(ctx); err != nil {
		t.Fatalf("ReopenVoting: %v", err)
	}
	t.Cleanup(func() { a.Race.EndIntermissionWithoutRacing(ctx) })

	steps, _ := s.runOfShow(ctx)
	inter := stepNamed(t, steps, "Intermission and voting")
	if inter.State != StepNow {
		t.Errorf("the intermission is %q while voting is open", inter.State)
	}
	if racing := stepNamed(t, steps, "Race"); racing.State == StepNow {
		t.Error("racing is still the next step during the intermission")
	}
}

// Publishing is blocked, with the reason, until there is a website to publish to.
func TestPublishingIsBlockedUntilTheWebsiteFolderIsSet(t *testing.T) {
	s, a, _ := seasonServer(t)
	ctx := context.Background()

	live := liveRace(t, a)
	a.Race.SetRace(ctx, live.ID)

	steps, _ := s.runOfShow(ctx)
	pub := stepNamed(t, steps, "Publish to the website")
	if pub.State != StepBlocked {
		t.Fatalf("publishing is %q with no website folder set", pub.State)
	}
	if pub.Blocker == "" {
		t.Fatal("nothing says why publishing is blocked")
	}
	for _, leak := range []string{"stat ", "no such file", "syscall"} {
		if strings.Contains(pub.Blocker, leak) {
			t.Errorf("the blocker leaks a system message: %q", pub.Blocker)
		}
	}
}

func TestTheRunOfShowPageRenders(t *testing.T) {
	s := mustServer(t)
	rec := httptest.NewRecorder()
	handler(t, s).ServeHTTP(rec, httptest.NewRequest("GET", "/run", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "</html>") {
		t.Fatal("page is truncated")
	}
	for _, want := range []string{"Tonight", "Test the timer", "Publish to the website", "Next"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not show %q", want)
		}
	}
	// It tells people where to plug things in, which is half the job at a venue.
	if !strings.Contains(body, "/vote") || !strings.Contains(body, "/display") {
		t.Error("the page does not show the tablet and TV addresses")
	}
}

// --- helpers ---------------------------------------------------------------------

// liveRace returns the demo season's race that has not been run.
func liveRace(t *testing.T, a *app.App) model.Race {
	t.Helper()
	seasons, _ := a.DB.Seasons(context.Background())
	if len(seasons) == 0 {
		t.Fatal("no seasons")
	}
	races, _ := a.DB.Races(context.Background(), seasons[0].ID)
	for _, r := range races {
		if r.Status == model.StatusCheckin {
			return r
		}
	}
	t.Fatal("no race waiting at check-in")
	return model.Race{}
}

func connectAndBench(t *testing.T, a *app.App) {
	t.Helper()
	ctx := context.Background()
	if err := a.Timer.Connect(ctx, "", "simulator"); err != nil {
		t.Fatalf("connect simulator: %v", err)
	}
	if _, err := a.Timer.RunBench(ctx); err != nil {
		t.Fatalf("bench: %v", err)
	}
}

var _ = store.EntryView{}

// A tie for a trophy is something to go and do, on the track — so it belongs on
// the checklist as the next thing rather than as a blocked step.
func TestATieForATrophyBecomesTheNextThingToDo(t *testing.T) {
	s, a, _ := seasonServer(t)
	ctx := context.Background()

	live := liveRace(t, a)
	a.Race.SetRace(ctx, live.ID)
	connectAndBench(t, a)
	if _, err := a.Race.GenerateSchedule(ctx, live.ID); err != nil {
		t.Fatal(err)
	}

	// Run the whole race with the two quickest cars dead level.
	entries, _ := a.DB.Entries(ctx, live.ID)
	var first, second int64
	for _, e := range entries {
		if !e.EarnsPoints() {
			continue
		}
		if first == 0 {
			first = e.ID
		} else if second == 0 {
			second = e.ID
		}
	}
	heats, _ := a.DB.Heats(ctx, live.ID)
	for _, h := range heats {
		times := map[int]float64{}
		for _, l := range h.Lanes {
			if l.EntryID == nil {
				continue
			}
			if *l.EntryID == first || *l.EntryID == second {
				times[l.Lane] = 2.100
			} else {
				times[l.Lane] = 2.500 + float64(*l.EntryID%9)*0.01
			}
		}
		if err := a.DB.RecordHeatResults(ctx, h.ID, times); err != nil {
			t.Fatal(err)
		}
	}

	steps, _ := s.runOfShow(ctx)

	// The reveal comes first: it is where the room learns there is a tie, and
	// settling it beforehand would give the ending away.
	reveal := stepNamed(t, steps, "Reveal the results")
	if reveal.State != StepNow {
		t.Errorf("the reveal is %q; it should come before the run-off", reveal.State)
	}

	runOff := stepNamed(t, steps, "Run off any tie for a trophy")
	if runOff.State != StepWaiting && runOff.State != StepNow {
		t.Errorf("the run-off step is %q", runOff.State)
	}
	if !strings.Contains(runOff.Detail, "tied for 1st") {
		t.Errorf("the step does not say what is tied: %q", runOff.Detail)
	}
	if runOff.Link != "/race" {
		t.Errorf("the run-off sends you to %s", runOff.Link)
	}

	// The final standings wait for the tie to be settled: the wrap-up table is
	// the one people photograph, so it must not still say T1.
	if final := stepNamed(t, steps, "Final standings"); final.State != StepWaiting {
		t.Errorf("the final standings are %q with a tie outstanding", final.State)
	}
	// And the trophies are blocked, with the reason.
	awards := stepNamed(t, steps, "Awards")
	if awards.State != StepBlocked {
		t.Errorf("awards is %q with a trophy tie outstanding", awards.State)
	}
	if !strings.Contains(awards.Blocker, "tie") {
		t.Errorf("the blocker does not mention the tie: %q", awards.Blocker)
	}

	// And the race screen offers the run-off.
	page := get(t, s, "/race").Body.String()
	if !strings.Contains(page, "Tied for a trophy") {
		t.Error("the race screen does not show the tie")
	}
	if !strings.Contains(page, "Run it off") {
		t.Error("the race screen offers no way to settle it")
	}
}

// The order the club runs the end of the night in: reveal the results — which
// is where the room learns there is a tie — then settle the tie on the track,
// then put the finished table up for the wrap-up.
func TestTheEndOfTheNightRunsRevealThenRunOffThenFinalStandings(t *testing.T) {
	s, a, _ := seasonServer(t)
	ctx := context.Background()

	live := liveRace(t, a)
	a.Race.SetRace(ctx, live.ID)
	connectAndBench(t, a)
	if _, err := a.Race.GenerateSchedule(ctx, live.ID); err != nil {
		t.Fatal(err)
	}

	entries, _ := a.DB.Entries(ctx, live.ID)
	var first, second int64
	for _, e := range entries {
		if !e.EarnsPoints() {
			continue
		}
		if first == 0 {
			first = e.ID
		} else if second == 0 {
			second = e.ID
		}
	}
	heats, _ := a.DB.Heats(ctx, live.ID)
	for _, h := range heats {
		times := map[int]float64{}
		for _, l := range h.Lanes {
			if l.EntryID == nil {
				continue
			}
			if *l.EntryID == first || *l.EntryID == second {
				times[l.Lane] = 2.100
			} else {
				times[l.Lane] = 2.500 + float64(*l.EntryID)*0.004
			}
		}
		if err := a.DB.RecordHeatResults(ctx, h.ID, times); err != nil {
			t.Fatal(err)
		}
	}

	next := func() string {
		t.Helper()
		steps, _ := s.runOfShow(ctx)
		for _, st := range steps {
			if st.State == StepNow {
				return st.Title
			}
		}
		return "(nothing)"
	}
	display, err := a.DB.RegisterDisplay(ctx, "wrapup")
	if err != nil {
		t.Fatal(err)
	}
	showScene := func(scene store.Scene) {
		t.Helper()
		form := url.Values{"id": {itoa(display.ID)}, "scene": {string(scene)}}
		req := httptest.NewRequest("POST", "/api/displays/scene", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		handler(t, s).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("showing %s: %d %s", scene, rec.Code, rec.Body.String())
		}
	}

	// The reveal comes first, with the tie still standing.
	if got := next(); got != "Reveal the results" {
		t.Fatalf("after the heats the next step is %q, want the reveal", got)
	}
	standings, _ := a.DB.Standings(ctx, live.ID)
	if !standings[0].Tied {
		t.Fatal("the reveal would not show a tie; the fixture is not set up")
	}

	showScene(store.SceneReveal)
	if got := next(); got != "Run off any tie for a trophy" {
		t.Fatalf("after the reveal the next step is %q, want the run-off", got)
	}

	// Settle it.
	if err := a.Race.ArmRunOff(ctx, live.ID, 1); err != nil {
		t.Fatalf("ArmRunOff: %v", err)
	}
	ties, _ := a.DB.UnsettledTies(ctx, live.ID)
	heat, _ := a.DB.Heat(ctx, ties[0].HeatID)
	times := map[int]float64{}
	for _, l := range heat.Lanes {
		if l.EntryID == nil {
			continue
		}
		if *l.EntryID == second {
			times[l.Lane] = 2.010
		} else {
			times[l.Lane] = 2.080
		}
	}
	if err := a.DB.RecordHeatResults(ctx, heat.ID, times); err != nil {
		t.Fatal(err)
	}

	if got := next(); got != "Final standings" {
		t.Fatalf("after the run-off the next step is %q, want the final standings", got)
	}
	// The table people photograph must be the settled one, not the one that
	// still says T1.
	settled, _ := a.DB.Standings(ctx, live.ID)
	if settled[0].Tied || settled[0].Entry.ID != second {
		t.Errorf("the final standings still show a tie, or the wrong winner")
	}

	showScene(store.SceneFinal)
	if got := next(); got != "Awards" {
		t.Fatalf("after the final standings the next step is %q, want the awards", got)
	}
}
