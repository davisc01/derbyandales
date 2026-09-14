package web

import (
	"context"
	"net/http"
	"net/http/httptest"
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

	if len(steps) != 9 {
		t.Fatalf("%d steps, want 9", len(steps))
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
