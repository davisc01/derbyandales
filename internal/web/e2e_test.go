//go:build e2e

package web

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/davisc01/derbyandales/internal/app"
	"github.com/davisc01/derbyandales/internal/timer"
)

// Whole seasons, end to end, through the same HTTP endpoints the pages use.
//
// The unit tests prove each rule on its own. This proves they hold together
// over a year: six race nights, one run on the simulated timer and the rest
// typed in, with the vote at every intermission, a racer reaching the cap so
// places pass down, a car that already qualified turning up again, a tie for a
// trophy run off — then the championship as a bracket, raced to a champion, and
// every file published into a folder laid out like the website. A second season
// with five races and one wildcard proves the season's shape really is a
// setting: its bracket has to come out as a bye-free sixteen.
//
// Run with `make e2e`. It is kept out of `make test` because the simulated race
// takes most of a minute of real gate settling.
//
// The test reaches into the app in two places only, both standing in for a
// person rather than for a page: the person at the track opening and closing
// the gate, and the person at the championship table reading the seeding sheet
// to know who to check in.

// e2e drives the server the way the pages do.
type e2e struct {
	t    *testing.T
	s    *Server
	a    *app.App
	h    http.Handler
	site string
}

func newE2E(t *testing.T) *e2e {
	t.Helper()
	s, a := testServer(t)
	site := t.TempDir()
	if err := os.MkdirAll(filepath.Join(site, "content", "races"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := &e2e{t: t, s: s, a: a, h: handler(t, s), site: site}

	// Settings saves with a redirect, like the form it backs.
	rec := e.raw("POST", "/settings", url.Values{"derby_site_path": {site}, "auto_advance_secs": {"0"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("saving settings: %d %s", rec.Code, rec.Body.String())
	}
	e.post("/api/timer/connect", url.Values{"profile": {timer.SimulatorKey}})
	e.post("/api/timer/bench", nil)
	return e
}

func (e *e2e) raw(method, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// post fails the test on anything but success and returns the decoded body.
func (e *e2e) post(path string, form url.Values) map[string]any {
	e.t.Helper()
	code, body := postForm(e.t, e.h, path, form)
	if code != http.StatusOK {
		e.t.Fatalf("POST %s %v: %d %v", path, form, code, body)
	}
	return body
}

// try posts and returns the error message, if there was one.
func (e *e2e) try(path string, form url.Values) (map[string]any, string) {
	code, body := postForm(e.t, e.h, path, form)
	if code != http.StatusOK {
		msg, _ := body["error"].(string)
		return body, msg
	}
	return body, ""
}

func (e *e2e) get(path string) map[string]any {
	e.t.Helper()
	return getJSON(e.t, e.h, path)
}

func id(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.FormatInt(int64(n), 10)
	case string:
		return n
	}
	return ""
}

// racer is one of the invented people racing the test seasons.
type racer struct {
	first, last string
	pace        float64
}

func field(n int) []racer {
	out := make([]racer, n)
	for i := range out {
		// Distinct paces 30 ms apart: the order is known in advance, and no
		// two cars tie unless a test makes them.
		out[i] = racer{first: "Racer", last: fmt.Sprintf("%02d", i+1), pace: 2.300 + float64(i)*0.030}
	}
	return out
}

// createSeason makes a season through the check-in page's endpoint.
func (e *e2e) createSeason(year, races int) string {
	e.t.Helper()
	s := e.post("/api/season", url.Values{"year": {strconv.Itoa(year)}, "race_count": {strconv.Itoa(races)}})
	return id(s["ID"])
}

// checkIn enters every racer with a car built for this race — a car that has
// qualified may not race again, so everyone brings a new one each night — plus
// the pace car. It returns the entry id of each racer's car.
func (e *e2e) checkIn(raceID string, number int, racers []racer) map[string]string {
	e.t.Helper()
	entries := map[string]string{}
	e.post("/api/entry", url.Values{
		"race_id": {raceID}, "first_name": {"Derby"}, "last_name": {"Ales"},
		"car_name": {"CONTROL"}, "car_number": {"1"}, "is_control": {"true"},
	})
	for i, r := range racers {
		out := e.post("/api/entry", url.Values{
			"race_id": {raceID}, "first_name": {r.first}, "last_name": {r.last},
			"car_name":   {fmt.Sprintf("%s %s Mk %d", r.first, r.last, number)},
			"car_number": {strconv.Itoa(10 + i)},
		})
		if w, _ := out["warning"].(string); w != "" {
			e.t.Fatalf("a new car for %s %s was warned about: %s", r.first, r.last, w)
		}
		entries[r.last] = id(out["id"])
	}
	return entries
}

// paceOf turns a car's entry into its racer's pace.
type paceOf func(entryID string) float64

// timesFor lays out a heat's times. Every car runs every lane once, so a small
// per-lane offset changes nobody's average relative to anybody else's — and a
// tie made by giving two cars one pace stays exactly a tie.
func timesFor(heat map[string]any, pace paceOf) url.Values {
	form := url.Values{"heat_id": {id(heat["ID"])}}
	lanes, _ := heat["Lanes"].([]any)
	for _, raw := range lanes {
		l := raw.(map[string]any)
		if l["EntryID"] == nil {
			continue
		}
		lane := int(l["Lane"].(float64))
		t := pace(id(l["EntryID"])) + float64(lane)*0.001
		form.Set(fmt.Sprintf("lane_%d", lane), strconv.FormatFloat(t, 'f', 3, 64))
	}
	return form
}

// vote casts a small, lopsided ballot so each question has a clear leader, and
// declares the winners.
func (e *e2e) vote(raceID string) {
	e.t.Helper()
	ballot := e.get("/api/vote/ballot?race_id=" + raceID)
	if open, _ := ballot["open"].(bool); !open {
		e.t.Fatal("voting is not open at the intermission")
	}
	cats := ballot["categories"].([]any)
	cars := ballot["cars"].([]any)
	if len(cats) < 2 || len(cars) < 3 {
		e.t.Fatalf("the ballot has %d questions and %d cars", len(cats), len(cars))
	}
	for i, raw := range cats {
		cat := id(raw.(map[string]any)["id"])
		leader := id(cars[i].(map[string]any)["id"])
		for n := 0; n < 3; n++ {
			e.post("/api/vote/cast", url.Values{"category_id": {cat}, "entry_id": {leader}})
		}
		e.post("/api/vote/cast", url.Values{"category_id": {cat}, "entry_id": {id(cars[2].(map[string]any)["id"])}})
	}
	// The tablet was misclicked once; undo takes it back.
	e.post("/api/vote/cast", url.Values{"category_id": {id(cats[0].(map[string]any)["id"])}, "entry_id": {id(cars[2].(map[string]any)["id"])}})
	e.post("/api/vote/undo", url.Values{"category_id": {id(cats[0].(map[string]any)["id"])}})
}

func (e *e2e) declareWinners(raceID string) {
	e.t.Helper()
	tally := e.get("/api/vote/tally?race_id=" + raceID)
	for _, raw := range tally["categories"].([]any) {
		c := raw.(map[string]any)
		rows, _ := c["tally"].([]any)
		if len(rows) == 0 {
			e.t.Fatalf("no votes counted for %v", c["label"])
		}
		leader := rows[0].(map[string]any)
		e.post("/api/vote/declare", url.Values{"category_id": {id(c["id"])}, "entry_id": {id(leader["entry_id"])}})
	}
}

// raceByHand runs a whole night with typed-in times, the way a night with no
// timer goes: enter the heat, arm the next, and vote when it stops halfway.
func (e *e2e) raceByHand(raceID string, pace paceOf) {
	e.t.Helper()
	e.post("/api/race/load", url.Values{"race_id": {raceID}})
	e.post("/api/race/schedule", url.Values{"race_id": {raceID}})

	voted := false
	state := e.post("/api/race/next", nil)
	for guard := 0; guard < 200; guard++ {
		heat, _ := state["heat"].(map[string]any)
		if heat == nil {
			break
		}
		if complete(heat) {
			break // ArmNext found nothing left and the race has finished
		}
		e.post("/api/race/times", timesFor(heat, pace))

		next, msg := e.try("/api/race/next", nil)
		if strings.Contains(msg, "intermission") {
			e.vote(raceID)
			voted = true
			next = e.post("/api/race/resume", nil)
		} else if msg != "" {
			e.t.Fatalf("arming the next heat: %s", msg)
		}
		state = next
	}
	if !voted {
		e.t.Error("the night never stopped for its intermission")
	}
	e.declareWinners(raceID)
}

// complete reports whether every car in a heat has a time.
func complete(heat map[string]any) bool {
	lanes, _ := heat["Lanes"].([]any)
	for _, raw := range lanes {
		l := raw.(map[string]any)
		if l["EntryID"] != nil && l["FinishTime"] == nil {
			return false
		}
	}
	return true
}

// raceOnTheTimer runs a night on the simulated timer, playing the person at the
// track who stages each heat and lifts the gate.
func (e *e2e) raceOnTheTimer(raceID string) {
	e.t.Helper()
	e.post("/api/race/load", url.Values{"race_id": {raceID}})
	e.post("/api/race/schedule", url.Values{"race_id": {raceID}})
	e.post("/api/race/start", url.Values{"race_id": {raceID}})

	sim := e.a.Timer.Simulator()
	voted := false
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		st := e.get("/api/race/state")
		if b, _ := st["intermission"].(bool); b {
			e.vote(raceID)
			voted = true
			e.post("/api/race/resume", nil)
			continue
		}
		if done, total := st["completed"].(float64), st["heat_total"].(float64); total > 0 && done >= total {
			break
		}
		if e.a.Timer.Device().State() == timer.StateMark {
			sim.CloseGate()
			time.Sleep(MinGateSettleE2E)
			sim.OpenGate()
		}
		time.Sleep(100 * time.Millisecond)
	}
	// The last heat's auto-advance finds nothing to arm and finishes the race.
	waitFor(e.t, 10*time.Second, func() bool {
		race, _ := e.a.DB.Race(context.Background(), mustInt(raceID))
		return race.Status != "racing"
	})
	e.post("/api/race/stop", nil)
	if !voted {
		e.t.Error("the timed night never stopped for its intermission")
	}
	e.declareWinners(raceID)
}

// MinGateSettleE2E holds the simulated gate closed long enough for the real
// debounce to believe it.
const MinGateSettleE2E = 700 * time.Millisecond

func waitFor(t *testing.T, d time.Duration, ok func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if ok() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("timed out waiting")
}

func mustInt(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// readSiteCSV reads a published file, failing the test if it is missing.
func (e *e2e) readSiteCSV(parts ...string) [][]string {
	e.t.Helper()
	path := filepath.Join(append([]string{e.site, "content", "races"}, parts...)...)
	raw, err := os.ReadFile(path)
	if err != nil {
		e.t.Fatalf("%s was not published: %v", path, err)
	}
	if strings.HasPrefix(string(raw), "\ufeff") {
		e.t.Errorf("%s starts with a BOM, which breaks the site's column matching", path)
	}
	rows, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	if err != nil {
		e.t.Fatalf("%s is not valid CSV: %v", path, err)
	}
	return rows
}

func (e *e2e) publishRace(raceID string) {
	e.t.Helper()
	out := e.post("/api/publish", url.Values{"what": {"race"}, "race_id": {raceID}})
	if out["count"].(float64) == 0 {
		e.t.Errorf("publishing race %s wrote nothing", raceID)
	}
}

// championship creates the season's championship as a bracket, checks the
// field in from the seeding sheet, builds the bracket and races it to a
// champion. It returns the champion's car name and how many heats were raced.
func (e *e2e) championship(seasonID string, pace paceOf) (string, int) {
	e.t.Helper()
	ctx := context.Background()
	champ := e.post("/api/race", url.Values{
		"season_id": {seasonID}, "number": {"1"}, "kind": {"championship"}, "format": {"bracket"},
	})
	champID := id(champ["ID"])

	proposals, err := e.a.Bracket.Seed(ctx, mustInt(seasonID), mustInt(champID))
	if err != nil {
		e.t.Fatalf("reading the seeding sheet: %v", err)
	}
	for i, p := range proposals {
		racer, err := e.a.DB.Racer(ctx, p.RacerID)
		if err != nil {
			e.t.Fatal(err)
		}
		car := p.CarName
		if car == "" {
			car = racer.FullName() + " Wildcard"
		}
		e.post("/api/entry", url.Values{
			"race_id": {champID}, "first_name": {racer.FirstName}, "last_name": {racer.LastName},
			"car_name": {car}, "car_number": {strconv.Itoa(100 + i)},
		})
	}
	e.post("/api/bracket/generate", url.Values{"season_id": {seasonID}})
	state := e.post("/api/race/start", url.Values{"race_id": {champID}})

	heats := 0
	for guard := 0; guard < 100; guard++ {
		heat, _ := state["heat"].(map[string]any)
		if heat == nil || complete(heat) {
			break
		}
		e.post("/api/race/times", timesFor(heat, pace))
		heats++
		state = e.post("/api/race/next", nil)
	}
	e.post("/api/race/stop", nil)

	b := e.get("/api/race/bracket?race_id=" + champID)
	c, _ := b["champion"].(map[string]any)
	if c == nil {
		e.t.Fatalf("no champion after %d heats", heats)
	}
	e.publishRace(champID)
	return c["car"].(string), heats
}

func TestE2EAFullSeasonToAChampion(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	const year = 2031
	seasonID := e.createSeason(year, 6)
	racers := field(16)

	paces := map[string]float64{} // entry id → pace, across every race
	pace := func(entryID string) float64 {
		if p, ok := paces[entryID]; ok {
			return p
		}
		return 2.95 // the pace car, and anything unexpected, at the back
	}
	var raceIDs []string

	for n := 1; n <= 6; n++ {
		race := e.post("/api/race", url.Values{"season_id": {seasonID}, "number": {strconv.Itoa(n)}})
		raceID := id(race["ID"])
		raceIDs = append(raceIDs, raceID)
		entries := e.checkIn(raceID, n, racers)
		for i, r := range racers {
			paces[entries[r.last]] = r.pace
			// Race 5: the 2nd and 3rd fastest are dead level, so the trophy
			// for 2nd has to be run off.
			if n == 5 && i == 2 {
				paces[entries[r.last]] = racers[1].pace
			}
		}

		// Race 2: race 1's winner brings back the car that won it. Race 1 ran on
		// the simulator, so who that is comes from its published standings.
		// Check-in says so, and the car is marked ineligible from the warning.
		if n == 2 {
			var winnerName, winnerCar string
			for _, row := range e.readSiteCSV(strconv.Itoa(year), "race-1", "standings.csv")[1:] {
				if row[3] != "CONTROL" {
					winnerName, winnerCar = row[2], row[3]
					break
				}
			}
			first, last, _ := strings.Cut(winnerName, " ")
			out, _ := e.try("/api/entry", url.Values{
				"race_id": {raceID}, "first_name": {first}, "last_name": {last},
				"car_name": {winnerCar}, "car_number": {"90"},
			})
			w, _ := out["warning"].(string)
			if !strings.Contains(w, "already qualified") {
				t.Fatalf("a car that already qualified was checked in without a warning: %q", w)
			}
			e.post("/api/entry/update", url.Values{
				"id": {id(out["id"])}, "excluded": {"true"}, "reason": {out["exclusion_reason"].(string)},
			})
			paces[id(out["id"])] = 2.20 // faster than anyone: it must still not place
		}

		if n == 1 {
			e.raceOnTheTimer(raceID)
			// The simulator's times are its own, so give race 1's cars their
			// racers' pace from here on for anything that reads them.
		} else {
			e.raceByHand(raceID, pace)
		}

		if n == 5 {
			ties, err := e.a.DB.UnsettledTies(ctx, mustInt(raceID))
			if err != nil || len(ties) != 1 || ties[0].Place != 2 {
				t.Fatalf("race 5 should have one tie, for 2nd: %+v (%v)", ties, err)
			}
			state := e.post("/api/race/runoff", url.Values{"race_id": {raceID}, "place": {"2"}})
			heat := state["heat"].(map[string]any)
			// Racer 03 wins it.
			form := url.Values{"heat_id": {id(heat["ID"])}}
			for _, raw := range heat["Lanes"].([]any) {
				l := raw.(map[string]any)
				if l["EntryID"] == nil {
					continue
				}
				t := "2.400"
				if id(l["EntryID"]) == entries["03"] {
					t = "2.350"
				}
				form.Set(fmt.Sprintf("lane_%d", int(l["Lane"].(float64))), t)
			}
			e.post("/api/race/times", form)
		}

		e.publishRace(raceID)
	}
	e.post("/api/publish", url.Values{"what": {"season"}, "season_id": {seasonID}})

	// --- what the website now holds ---------------------------------------------

	for n := 1; n <= 6; n++ {
		dir := []string{strconv.Itoa(year), fmt.Sprintf("race-%d", n)}
		heats := e.readSiteCSV(append(dir, "heats.csv")...)
		if heats[0][0] != "Heat" || len(heats) < 17*4 {
			t.Errorf("race %d heats.csv has header %v and %d rows", n, heats[0], len(heats)-1)
		}
		standings := e.readSiteCSV(append(dir, "standings.csv")...)
		if standings[1][0] != "1" {
			t.Errorf("race %d standings.csv does not start at 1st: %v", n, standings[1])
		}
		awards := e.readSiteCSV(append(dir, "awards.csv")...)
		names := map[string]string{}
		for _, row := range awards[1:] {
			names[row[0]] = row[1] + " " + row[2]
		}
		for _, trophy := range []string{"1st", "2nd", "3rd"} {
			if names[trophy] == "" {
				t.Errorf("race %d awards.csv has no %s trophy", n, trophy)
			}
		}
		if len(awards)-1 < 5 {
			t.Errorf("race %d awards.csv has %d trophies, want the three speed and two voted", n, len(awards)-1)
		}
		if _, err := os.Stat(filepath.Join(append([]string{e.site, "content", "races"}, append(dir, "index.md")...)...)); err != nil {
			t.Errorf("race %d has no index.md", n)
		}
		if n == 5 && names["2nd"] != "Racer 03" {
			t.Errorf("race 5's 2nd went to %q, but Racer 03 won the run-off", names["2nd"])
		}
		if n == 2 {
			for _, row := range standings[1:] {
				if row[1] == "90" {
					t.Error("the ineligible car appears in race 2's standings")
				}
			}
		}
	}

	qualifiers := e.readSiteCSV(strconv.Itoa(year), "season-standings", "qualifiers.csv")
	if len(qualifiers)-1 != 18 {
		t.Errorf("%d qualifiers published, want 6 races × 3", len(qualifiers)-1)
	}
	held := map[string]int{}
	passedDown := 0
	for _, row := range qualifiers[1:] {
		held[row[1]]++
		if row[7] != "" {
			t.Errorf("Over Limit is %q for %s — nobody can be over the cap now", row[7], row[1])
		}
		if finish, _ := strconv.Atoi(row[4]); finish > 3 {
			passedDown++
		}
	}
	for driver, n := range held {
		if n > 3 {
			t.Errorf("%s holds %d places, over the cap", driver, n)
		}
	}
	// The three fastest win the top three every night from race 2 on, so they
	// reach the cap and later nights' places pass down.
	if passedDown == 0 {
		t.Error("no place passed down, though three racers reached the cap")
	}
	wildcard := e.readSiteCSV(strconv.Itoa(year), "season-standings", "wildcard.csv")
	if wildcard[0][0] != "Rank" || len(wildcard) < 10 {
		t.Errorf("wildcard.csv has header %v and %d rows", wildcard[0], len(wildcard)-1)
	}

	// --- championship night ---------------------------------------------------------

	champion, heats := e.championship(seasonID, func(entryID string) float64 {
		// Faster by a lower entry id: arbitrary, but fixed.
		return 2.3 + float64(mustInt(entryID)%97)*0.001
	})
	if heats != 23 {
		t.Errorf("the 24-car bracket took %d heats, want 23", heats)
	}
	dir := []string{strconv.Itoa(year), "championship"}
	standings := e.readSiteCSV(append(dir, "standings.csv")...)
	if standings[1][0] != "1" || standings[1][3] != champion {
		t.Errorf("the championship standings start %v, want the champion %s in 1st", standings[1], champion)
	}
	if _, err := os.Stat(filepath.Join(append([]string{e.site, "content", "races"}, append(dir, "awards.csv")...)...)); err == nil {
		t.Error("the championship published an awards.csv, which its page has never had")
	}
}

// Five races and one wildcard is a sixteen-car field: a bracket with no byes.
// If that falls out of the settings end to end, the season's shape really is
// a setting.
func TestE2EAFiveRaceSeasonMakesAByeFreeBracket(t *testing.T) {
	e := newE2E(t)
	const year = 2032
	seasonID := e.createSeason(year, 5)

	e.post("/api/season/settings", url.Values{
		"season_id": {seasonID}, "name": {"2032 Season"}, "race_count": {"5"},
		"auto_qual_places": {"3"}, "wildcard_spots": {"1"}, "max_championship_entry": {"3"},
		"track_length_ft": {"28"}, "bracket_lane_a": {"1"}, "bracket_lane_b": {"2"},
		"points_count_control": {"false"},
	})

	racers := field(12)
	paces := map[string]float64{}
	pace := func(entryID string) float64 {
		if p, ok := paces[entryID]; ok {
			return p
		}
		return 2.95
	}
	for n := 1; n <= 5; n++ {
		race := e.post("/api/race", url.Values{"season_id": {seasonID}, "number": {strconv.Itoa(n)}})
		raceID := id(race["ID"])
		entries := e.checkIn(raceID, n, racers)
		for i, r := range racers {
			// Rotate who is fast, so the places spread across the field.
			paces[entries[r.last]] = 2.3 + float64((i+n*3)%len(racers))*0.03
		}
		e.raceByHand(raceID, pace)
		e.publishRace(raceID)
	}
	e.post("/api/publish", url.Values{"what": {"season"}, "season_id": {seasonID}})

	state := e.get("/api/bracket/plan?races=5&places=3&wildcards=1")
	if state["capacity"].(float64) != 16 || state["byes"].(float64) != 0 {
		t.Errorf("the planner makes 5 races and 1 wildcard a %v-car bracket with %v byes", state["capacity"], state["byes"])
	}

	_, heats := e.championship(seasonID, func(entryID string) float64 {
		return 2.3 + float64(mustInt(entryID)%89)*0.001
	})
	if heats != 15 {
		t.Errorf("the 16-car bracket took %d heats, want 15", heats)
	}
	b := e.get("/api/bracket/state?season_id=" + seasonID)
	if b["entrants"].(float64) != 16 || b["byes"].(float64) != 0 {
		t.Errorf("the championship has %v cars and %v byes, want 16 and none", b["entrants"], b["byes"])
	}
	e.readSiteCSV(strconv.Itoa(year), "championship", "standings.csv")
}
