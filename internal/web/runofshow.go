package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/store"
)

// The run of show.
//
// This screen is the point of the whole project. Today a race night means
// following a written procedure across four web interfaces and two machines,
// which is why one person can run one. Here it is a list, in order, that says
// what is done, what is next, and what is stopping it — so somebody who has
// never run a race can be handed the laptop.
//
// Every step is derived from the state of the database and the timer. Nothing
// is ticked off by being pressed: a step is done when the thing it describes
// has actually happened.

// StepState is how far along one step is.
type StepState string

const (
	// StepDone: this has happened.
	StepDone StepState = "done"
	// StepNow: this is the thing to do next. Exactly one step is ever in this
	// state — see markNext, which is what makes the screen answerable.
	StepNow StepState = "now"
	// StepBlocked: it cannot be done yet, and Blocker says why.
	StepBlocked StepState = "blocked"
	// StepWaiting: its turn has not come.
	StepWaiting StepState = "waiting"
	// stepReady: internal. The step could be done now, but something earlier
	// may also be ready and only one thing can be next.
	stepReady StepState = "ready"
)

// markNext turns the ready steps into exactly one "next" and a queue behind it.
//
// Several steps are genuinely doable at once — once the schedule exists you
// could do the intros or start racing, and when a race ends the reveal, the
// awards and the publish are all available. But the question this screen
// answers is "what do I do now", and three answers is the same as none. The
// club's order decides, so the earliest ready step wins and the rest wait.
//
// force names a step that takes precedence whatever is ready before it, or -1.
// It exists for the intermission, which is a hard stop rather than a step in a
// queue: while it is running it is the only thing happening, and offering
// somebody the intros instead would be wrong.
func markNext(steps []Step, force int) {
	next := false
	if force >= 0 && force < len(steps) {
		steps[force].State = StepNow
		next = true
	}
	for i := range steps {
		if steps[i].State != stepReady {
			continue
		}
		if next {
			steps[i].State = StepWaiting
			continue
		}
		steps[i].State = StepNow
		next = true
	}
}

// Step is one line of the checklist.
type Step struct {
	Number int
	Title  string
	State  StepState

	// Detail is what is true right now — "18 checked in, 14 with photos".
	Detail string
	// Blocker is what is stopping it, when it is blocked.
	Blocker string
	// Caveat is a warning about a step that counts as done but should not be
	// trusted — a timer test run against the simulator, for instance.
	Caveat string
	// Hint explains the step to somebody who has not done it before.
	Hint string

	// Link is where to go to do it, and Action names that place.
	Link   string
	Action string
}

// runOfShow builds the checklist from the current state.
func (s *Server) runOfShow(ctx context.Context) ([]Step, model.Race) {
	var race model.Race
	raceID := s.app.Race.CurrentRaceID()
	if raceID != 0 {
		race, _ = s.app.DB.Race(ctx, raceID)
	}

	timer := s.app.Timer.Status()
	state := s.app.Race.State(ctx)
	inter := s.app.Race.Intermission(ctx)

	var entries []store.EntryView
	var progress store.HeatProgress
	if raceID != 0 {
		entries, _ = s.app.DB.RacingEntries(ctx, raceID)
		progress, _ = s.app.DB.Progress(ctx, raceID)
	}
	photos := 0
	for _, e := range entries {
		if e.PhotoID != nil {
			photos++
		}
	}

	scheduled := progress.Total > 0
	racedAll := scheduled && progress.Completed >= progress.Total

	steps := []Step{
		{
			Number: 1, Title: "Open the race",
			Hint: "Pick tonight's race so the screens know what they are showing.",
			Link: "/race", Action: "Race screen",
		},
		{
			Number: 2, Title: "Test the timer",
			Hint: "Do this while cars are still being carried in, not after the " +
				"room is seated. A jammed gate is the commonest race-night failure.",
			Link: "/timer/test", Action: "Timer test",
		},
		{
			Number: 3, Title: "Check the cars in",
			Hint: "Driver, car name, number and a photo. Closing check-in builds the schedule.",
			Link: "/checkin", Action: "Check-in",
		},
		{
			Number: 4, Title: "Introduce the racers",
			Hint: "Put the roster on the TV while people find their seats.",
			Link: "/displays", Action: "Displays",
		},
		{
			Number: 5, Title: "Race",
			Hint: "Results land, the screens show them, the next heat arms itself. " +
				"You should not need to touch anything.",
			Link: "/race", Action: "Race screen",
		},
		{
			Number: 6, Title: "Intermission and voting",
			Hint: "Racing stops by itself halfway through and voting opens. It ends " +
				"when you press resume — there is no timer on it.",
			Link: "/voting", Action: "Voting",
		},
		{
			Number: 7, Title: "Reveal the results",
			Hint: "Slowest to fastest, one car at a time, on the TV. A tie for a " +
				"trophy shows here as a tie — it is settled after the reveal, not before.",
			Link: "/displays", Action: "Displays",
		},
		{
			Number: 8, Title: "Run off any tie for a trophy",
			Hint: "1st, 2nd and 3rd are handed to somebody, so a tie there is raced " +
				"again head to head. A tie further down stands.",
			Link: "/race", Action: "Race screen",
		},
		{
			Number: 9, Title: "Final standings",
			Hint: "The whole table at once, settled, for the wrap-up.",
			Link: "/displays", Action: "Displays",
		},
		{
			Number: 10, Title: "Awards",
			Hint: "The three speed trophies come from the standings. The design and " +
				"theme trophies come from the ballot.",
			Link: "/voting", Action: "Voting",
		},
		{
			Number: 11, Title: "Publish to the website",
			Hint: "Writes the files and stops. You review and commit them yourself — " +
				"the app never runs git.",
			Link: "/publish", Action: "Publish",
		},
	}

	// --- 1. a race is loaded ---------------------------------------------------
	if raceID == 0 {
		steps[0].State = stepReady
		steps[0].Detail = "No race loaded."
		for i := 1; i < len(steps); i++ {
			steps[i].State = StepWaiting
		}
		markNext(steps, -1)
		return steps, race
	}
	steps[0].State = StepDone
	steps[0].Detail = race.Name
	if race.Venue != "" {
		steps[0].Detail += " at " + race.Venue
	}

	// --- 2. the timer -----------------------------------------------------------
	//
	// "Ready" is the bench's own word for passed-or-deliberately-overridden. A
	// jammed gate switch must never stop a race, so an override counts — the
	// point is that somebody decided, not that everything worked.
	switch {
	case !timer.Connected:
		steps[1].State = stepReady
		steps[1].Detail = "No timer connected."
	case timer.Bench == nil:
		steps[1].State = stepReady
		steps[1].Detail = "Never tested."
	case timer.Bench.OverrideReason != "":
		steps[1].State = StepDone
		steps[1].Detail = "Overridden: " + timer.Bench.OverrideReason
	case timer.BenchStale:
		steps[1].State = stepReady
		steps[1].Detail = "Last tested more than two hours ago — run it again."
	case timer.BenchReady && timer.Simulated:
		// A simulated timer never reports a clean pass, and this screen must
		// not imply the track has been checked. The night can still be
		// rehearsed, so the step does not block — it is marked instead.
		steps[1].State = StepDone
		steps[1].Detail = "Simulated timer"
		steps[1].Caveat = "This was the simulator. The real timer has not been tested."
	case timer.BenchReady:
		steps[1].State = StepDone
		steps[1].Detail = timer.Identity
	default:
		steps[1].State = stepReady
		steps[1].Detail = fmt.Sprintf("%d check%s failed",
			len(timer.Bench.Failures()), plural(len(timer.Bench.Failures())))
	}

	// --- 3. check-in ------------------------------------------------------------
	steps[2].Detail = fmt.Sprintf("%d car%s checked in, %d with photos",
		len(entries), plural(len(entries)), photos)
	switch {
	case scheduled:
		steps[2].State = StepDone
		steps[2].Detail += fmt.Sprintf(" · %d heats scheduled", progress.Total)
	case steps[1].State != StepDone:
		steps[2].State = StepBlocked
		steps[2].Blocker = "The timer test has not passed yet."
	default:
		steps[2].State = stepReady
	}

	// --- 4. intros --------------------------------------------------------------
	switch {
	case progress.Completed > 0:
		steps[3].State = StepDone
	case scheduled:
		steps[3].State = stepReady
	default:
		steps[3].State = StepWaiting
	}

	// --- 5, 6, 7. the racing ------------------------------------------------------
	half := s.app.Race.IntermissionHeat(ctx, raceID)
	switch {
	case !scheduled:
		steps[4].State = StepBlocked
		steps[4].Blocker = "Close check-in to build the schedule."
	case racedAll:
		steps[4].State = StepDone
		steps[4].Detail = fmt.Sprintf("All %d heats run", progress.Total)
	default:
		steps[4].State = stepReady
		steps[4].Detail = fmt.Sprintf("Heat %d of %d", progress.Completed+1, progress.Total)
		if !state.Running {
			steps[4].Detail += " · not started"
		}
	}

	switch {
	case inter.Active:
		// The intermission is a hard stop, so racing stops being available
		// rather than merely queueing behind it.
		steps[5].State = stepReady
		steps[5].Detail = fmt.Sprintf("Voting is open · %d heats still to run", inter.HeatsRemaining)
		steps[4].State = StepWaiting
		steps[4].Detail = "Paused for the intermission"
	case !scheduled:
		steps[5].State = StepWaiting
		steps[5].Detail = "Starts by itself halfway through the heats."
	case half == 0:
		steps[5].State = StepWaiting
		steps[5].Detail = "Turned off for this race — there will be no break."
	case progress.Completed > half:
		steps[5].State = StepDone
		steps[5].Detail = "Done · voting closed"
	default:
		steps[5].State = StepWaiting
		steps[5].Detail = fmt.Sprintf("Starts by itself after heat %d", half)
	}

	// --- 7. the reveal --------------------------------------------------------------
	//
	// This comes before the run-off deliberately. The reveal walks up the order
	// and shows the tie as a tie, which is the moment the room finds out there
	// is one; settling it first would give the ending away.
	revealed, _ := s.app.DB.SceneShown(ctx, raceID, store.SceneReveal)
	switch {
	case !racedAll:
		steps[6].State = StepWaiting
	case revealed:
		steps[6].State = StepDone
		steps[6].Detail = "Shown on a screen."
	default:
		steps[6].State = stepReady
	}

	// --- 8. run off a tie for a trophy -------------------------------------------
	ties, _ := s.app.DB.UnsettledTies(ctx, raceID)
	var unsettled []store.TieView
	for _, t := range ties {
		if !t.Settled {
			unsettled = append(unsettled, t)
		}
	}

	switch {
	case !racedAll:
		steps[7].State = StepWaiting
	case len(unsettled) > 0:
		steps[7].State = stepReady
		t := unsettled[0]
		names := make([]string, 0, len(t.Entries))
		for _, e := range t.Entries {
			names = append(names, fmt.Sprintf("#%d %s", e.CarNumber, e.CarName))
		}
		if t.DeadHeat {
			steps[7].Detail = fmt.Sprintf("The run-off for %s finished level too — run heat %d again",
				ordinalOf(t.Place), t.HeatNumber)
		} else {
			steps[7].Detail = fmt.Sprintf("%s are %s", strings.Join(names, " and "), t.Describe())
		}
	default:
		// A tie that has been run off no longer shows as a tie, so "were there
		// any" is answered by whether a run-off heat exists — not by looking at
		// the standings, which by then say no.
		runoffs, _ := s.app.DB.RunOffHeats(ctx, raceID)
		steps[7].State = StepDone
		if len(runoffs) > 0 {
			steps[7].Detail = fmt.Sprintf("%d settled on the track", len(runoffs))
		} else {
			steps[7].Detail = "Nothing was tied for a trophy."
		}
	}

	// --- 9. the final standings -------------------------------------------------
	//
	// After the run-off, so the table people photograph is the settled one
	// rather than the one that still says T1.
	shownFinal, _ := s.app.DB.SceneShown(ctx, raceID, store.SceneFinal)
	switch {
	case !racedAll || len(unsettled) > 0:
		steps[8].State = StepWaiting
	case shownFinal:
		steps[8].State = StepDone
		steps[8].Detail = "Shown on a screen."
	default:
		steps[8].State = stepReady
	}

	// --- 10. awards ---------------------------------------------------------------
	awards, _ := s.app.DB.Awards(ctx, raceID)
	decided := 0
	for _, a := range awards {
		if a.EntryID != nil {
			decided++
		}
	}
	steps[9].Detail = fmt.Sprintf("%d award%s decided", decided, plural(decided))
	switch {
	case !racedAll:
		steps[9].State = StepBlocked
		steps[9].Blocker = "The heats are not finished."
	case len(unsettled) > 0:
		steps[9].State = StepBlocked
		steps[9].Blocker = "A tie for a trophy has not been run off yet."
	case decided == 0:
		steps[9].State = stepReady
	default:
		steps[9].State = StepDone
	}

	// --- 11. publish ---------------------------------------------------------------
	if _, err := s.app.Publish.SitePath(ctx); err != nil {
		steps[10].State = StepBlocked
		steps[10].Blocker = err.Error()
	} else if !racedAll {
		steps[10].State = StepBlocked
		steps[10].Blocker = "The heats are not finished."
	} else {
		plan, err := s.app.Publish.PlanRace(ctx, raceID)
		switch {
		case err != nil:
			steps[10].State = StepBlocked
			steps[10].Blocker = err.Error()
		case plan.Changes() == 0:
			steps[10].State = StepDone
			steps[10].Detail = "Published — nothing left to write."
		default:
			steps[10].State = stepReady
			steps[10].Detail = fmt.Sprintf("%d file%s to write",
				plan.Changes(), plural(plan.Changes()))
		}
	}

	// The intermission is the one thing that takes the order out of the queue's
	// hands: it is a hard stop rather than a step waiting its turn.
	//
	// A tie for a trophy does not, even though it also needs the track. It sits
	// at step 8, after the reveal, because the reveal is where the room learns
	// there is a tie — settling it first would give the ending away.
	force := -1
	if inter.Active {
		force = 5
	}
	markNext(steps, force)
	return steps, race
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (s *Server) runRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /run", s.handleRunOfShow)
	mux.HandleFunc("POST /api/awards/speed", s.handleSpeedAwards)
}

func (s *Server) handleRunOfShow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	steps, race := s.runOfShow(ctx)

	// The step to do next, so the page can lead with it rather than making
	// somebody scan a list to find out where they are.
	var now *Step
	for i := range steps {
		if steps[i].State == StepNow {
			now = &steps[i]
			break
		}
	}

	races, _ := s.allRaces(r)
	s.render(w, r, "run.html", pageData{
		Title:  "Tonight",
		Active: "run",
		Data: map[string]any{
			"Steps": steps,
			"Now":   now,
			"Race":  race,
			"Races": races,
			"URLs":  s.app.URLs(),
		},
	})
}

// handleSpeedAwards works out the three speed trophies from the standings.
func (s *Server) handleSpeedAwards(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx := r.Context()
	raceID := s.raceIDForm(r)
	if raceID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which race?"})
		return
	}

	awards, err := s.app.DB.GenerateSpeedAwards(ctx, raceID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	_ = s.app.DB.Audit(ctx, "coordinator", "awards.speed",
		fmt.Sprintf("race %d: %d trophies from the standings", raceID, len(awards)))

	out := make([]map[string]any, 0, len(awards))
	for _, a := range awards {
		out = append(out, map[string]any{
			"name": a.Name, "car": a.Entry.CarName,
			"number": a.Entry.CarNumber, "driver": a.Entry.FullName(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"awards": out})
}

// ordinalOf renders a place the way it is said out loud.
func ordinalOf(n int) string {
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
