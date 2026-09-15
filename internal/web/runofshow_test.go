package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	// And publishing is blocked, with the reason.
	pub := stepNamed(t, steps, "Publish to the website")
	if pub.State != StepBlocked {
		t.Errorf("publishing is %q with a trophy tie outstanding", pub.State)
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

// The order the club runs the end of the night in: the two voted trophies
// first, on their own; then the results revealed slowest to fastest with the
// speed trophies handed over as the top three come up; then any tie settled on
// the track; then the finished table for the wrap-up.
func TestTheEndOfTheNightRunsInCeremonyOrder(t *testing.T) {
	s, a, _ := seasonServer(t)
	ctx := context.Background()

	live := liveRace(t, a)
	a.Race.SetRace(ctx, live.ID)
	connectAndBench(t, a)
	if _, err := a.Race.GenerateSchedule(ctx, live.ID); err != nil {
		t.Fatal(err)
	}

	// A website to publish into, so the last step is reachable rather than
	// blocked on a setting.
	site := t.TempDir()
	if err := os.MkdirAll(filepath.Join(site, "content", "races"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := a.DB.SetSetting(ctx, store.KeyDerbySitePath, site); err != nil {
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

	// Nothing has been voted on, so the design and theme trophies cannot be
	// presented — and the checklist says so rather than sitting on them.
	steps, _ := s.runOfShow(ctx)
	trophies := stepNamed(t, steps, "Present the design and theme trophies")
	if trophies.State != StepBlocked {
		t.Errorf("the voted trophies are %q with nothing declared", trophies.State)
	}

	// Declare them, as the intermission would have. The ballot is normally
	// prepared when the intermission opens; this race never had one.
	if err := a.DB.EnsureVoteCategories(ctx, live.ID); err != nil {
		t.Fatal(err)
	}
	cats, err := a.DB.VoteCategories(ctx, live.ID)
	if err != nil || len(cats) == 0 {
		t.Fatalf("no ballot categories: %v", err)
	}
	for i, c := range cats {
		if err := a.DB.DeclareVoteWinner(ctx, c.ID, entries[i+1].ID); err != nil {
			t.Fatalf("declaring %s: %v", c.Label, err)
		}
	}

	// They come first, before any talk of times.
	if got := next(); got != "Present the design and theme trophies" {
		t.Fatalf("after the heats the next step is %q, want the voted trophies", got)
	}
	showScene(store.SceneAwards)

	// Then the reveal, with the tie still standing.
	if got := next(); got != "Reveal the results" {
		t.Fatalf("after the trophies the next step is %q, want the reveal", got)
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
	if got := next(); got != "Publish to the website" {
		t.Fatalf("after the final standings the next step is %q, want publishing", got)
	}

	// And the speed trophies need no separate step: they are the top of the
	// standings, derived rather than stored, so they cannot disagree with it.
	awards, _ := a.DB.RaceAwards(ctx, live.ID)
	speed := 0
	for _, aw := range awards {
		if aw.Source == model.AwardAuto {
			speed++
		}
	}
	if speed != 3 {
		t.Errorf("%d speed trophies after the run-off, want 3", speed)
	}
	for _, aw := range awards {
		if aw.Source == model.AwardAuto && aw.Name == "1st" && aw.Entry.ID != second {
			t.Error("the 1st trophy did not follow the run-off")
		}
	}
}

// The voted trophies go first, on their own — they were decided at the
// intermission and have nothing to do with times.
func TestTheVotedTrophiesComeBeforeTheResults(t *testing.T) {
	s, a, _ := seasonServer(t)
	ctx := context.Background()

	live := liveRace(t, a)
	a.Race.SetRace(ctx, live.ID)
	connectAndBench(t, a)
	if _, err := a.Race.GenerateSchedule(ctx, live.ID); err != nil {
		t.Fatal(err)
	}
	heats, _ := a.DB.Heats(ctx, live.ID)
	for _, h := range heats {
		times := map[int]float64{}
		for _, l := range h.Lanes {
			if l.EntryID != nil {
				times[l.Lane] = 2.300 + float64(*l.EntryID)*0.004
			}
		}
		if err := a.DB.RecordHeatResults(ctx, h.ID, times); err != nil {
			t.Fatal(err)
		}
	}

	// Nothing declared: the step says so rather than sitting there as "next".
	steps, _ := s.runOfShow(ctx)
	trophies := stepNamed(t, steps, "Present the design and theme trophies")
	if trophies.State != StepBlocked {
		t.Errorf("the voted trophies are %q with nothing declared", trophies.State)
	}
	if !strings.Contains(trophies.Blocker, "declared") {
		t.Errorf("the blocker does not explain: %q", trophies.Blocker)
	}

	entries, _ := a.DB.Entries(ctx, live.ID)
	if err := a.DB.EnsureVoteCategories(ctx, live.ID); err != nil {
		t.Fatal(err)
	}
	cats, _ := a.DB.VoteCategories(ctx, live.ID)
	for i, c := range cats {
		if err := a.DB.DeclareVoteWinner(ctx, c.ID, entries[i+1].ID); err != nil {
			t.Fatal(err)
		}
	}

	steps, _ = s.runOfShow(ctx)
	trophies = stepNamed(t, steps, "Present the design and theme trophies")
	if trophies.State != StepNow {
		t.Fatalf("with two trophies declared the step is %q, want next", trophies.State)
	}
	if !strings.Contains(trophies.Detail, "trophies") {
		t.Errorf("detail reads %q", trophies.Detail)
	}
	if stepNamed(t, steps, "Reveal the results").State == StepNow {
		t.Error("the reveal is next; the voted trophies come first")
	}

	// And the scene serves them with the car, not as a list of names.
	rec := get(t, s, "/api/race/awards")
	if rec.Code != http.StatusOK {
		t.Fatalf("awards API: %d", rec.Code)
	}
	var out struct {
		Awards []struct {
			Name  string `json:"name"`
			Voted bool   `json:"voted"`
			Car   string `json:"car_name"`
		} `json:"awards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	voted := 0
	for _, a := range out.Awards {
		if a.Voted {
			voted++
			if a.Car == "" {
				t.Errorf("%s has no car on it", a.Name)
			}
		}
	}
	if voted != 2 {
		t.Errorf("%d voted trophies in the scene data, want 2", voted)
	}
	// The speed trophies are there too, but they are handed over during the
	// reveal rather than in this scene.
	if len(out.Awards) != 5 {
		t.Errorf("%d trophies in total, want 5", len(out.Awards))
	}
}

// A bracket championship is a different evening: no schedule, no vote, no
// reveal. Its checklist has to walk somebody from check-in to a champion
// without once pointing at a step that does not apply.
func TestABracketChampionshipHasItsOwnRunOfShow(t *testing.T) {
	s, a, seasonID, champID := championshipFixture(t)
	ctx := context.Background()
	connectAndBench(t, a)
	if err := a.Race.SetRace(ctx, champID); err != nil {
		t.Fatal(err)
	}

	next := func() string {
		t.Helper()
		steps, _ := s.runOfShow(ctx)
		for _, st := range steps {
			if st.Title == "Intermission and voting" || st.Title == "Reveal the results" {
				t.Errorf("a bracket's checklist includes %q", st.Title)
			}
		}
		now, ok := nowStep(steps)
		if !ok {
			return ""
		}
		return now.Title
	}

	if got := next(); got != "Check the field in and build the bracket" {
		t.Fatalf("before the bracket is built, next is %q", got)
	}

	proposals, err := a.Bracket.Seed(ctx, seasonID, champID)
	if err != nil {
		t.Fatal(err)
	}
	st, err := a.Bracket.Generate(ctx, champID, proposals, "test")
	if err != nil {
		t.Fatal(err)
	}
	if got := next(); got != "Put the bracket on the screen" {
		t.Fatalf("once built, next is %q", got)
	}
	if err := a.DB.RecordSceneShown(ctx, champID, store.SceneBracket); err != nil {
		t.Fatal(err)
	}
	if got := next(); got != "Race the bracket" {
		t.Fatalf("with the bracket on screen, next is %q", got)
	}

	seedOf := map[int64]int{}
	for _, sd := range st.Seeds {
		seedOf[sd.Entry.ID] = sd.Seed
	}
	if err := a.Race.Start(ctx, champID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Race.Stop)
	for i := 0; i < 40; i++ {
		h := a.Race.State(ctx).Heat
		times := map[int]float64{}
		for _, l := range h.Lanes {
			if l.EntryID != nil {
				times[l.Lane] = 2.4 + float64(seedOf[*l.EntryID])*0.01
			}
		}
		if err := a.Race.EnterTimes(ctx, h.ID, times, "test"); err != nil {
			t.Fatal(err)
		}
		if _, err := a.DB.Champion(ctx, champID); err == nil {
			break
		}
		if err := a.Race.ArmNext(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// The bracket was put up before the first matchup. That is not the same
	// as presenting the champion, which has to happen after the final.
	if got := next(); got != "Present the champion" {
		t.Fatalf("after the final, next is %q", got)
	}
	time.Sleep(1100 * time.Millisecond) // scene times are stored to the second
	if err := a.DB.RecordSceneShown(ctx, champID, store.SceneBracket); err != nil {
		t.Fatal(err)
	}
	steps, _ := s.runOfShow(ctx)
	if got := stepNamed(t, steps, "Present the champion"); got.State != StepDone {
		t.Errorf("showing the finished bracket left presenting the champion %s", got.State)
	}
	if got := stepNamed(t, steps, "Race the bracket"); !strings.Contains(got.Detail, "Champion") {
		t.Errorf("the racing step does not name the champion: %q", got.Detail)
	}
}

// A championship raced as a normal night — every one from 2023 to 2025 — keeps
// the reveal and the run-off but has nobody voting.
func TestAStandardChampionshipSkipsTheVote(t *testing.T) {
	s, a, _, champID := championshipFixture(t)
	ctx := context.Background()
	if err := a.DB.SetRaceFormat(ctx, champID, model.FormatStandard); err != nil {
		t.Fatal(err)
	}
	if err := a.Race.SetRace(ctx, champID); err != nil {
		t.Fatal(err)
	}
	steps, _ := s.runOfShow(ctx)
	if len(steps) != 9 {
		t.Errorf("%d steps, want 9", len(steps))
	}
	for i, st := range steps {
		if st.Number != i+1 {
			t.Errorf("step %d is numbered %d", i+1, st.Number)
		}
		if strings.Contains(st.Title, "Intermission") || strings.Contains(st.Title, "design and theme") {
			t.Errorf("a championship's checklist includes %q", st.Title)
		}
	}
	stepNamed(t, steps, "Reveal the results")
	stepNamed(t, steps, "Run off any tie for a trophy")
}
