package web

import (
	"net/url"
	"strings"
	"testing"
)

// The racers scene is read by people looking for their own name on a TV across
// a room, so it is alphabetical by surname the way a programme is. Entries come
// out of the database in car-number order, which is the loading order for the
// track and no use for reading.
func TestTonightsRacersAreListedBySurname(t *testing.T) {
	s, a := testServerPair(t)
	h := handler(t, s)
	if _, err := a.AddDemoRace(t.Context()); err != nil {
		t.Fatal(err)
	}

	data := getJSON(t, h, "/api/race/roster")
	entries, _ := data["entries"].([]any)
	if len(entries) < 5 {
		t.Fatalf("got %d entries", len(entries))
	}

	var last string
	for _, e := range entries {
		row, _ := e.(map[string]any)
		driver, _ := row["driver"].(string)
		parts := strings.Fields(driver)
		surname := strings.ToLower(parts[len(parts)-1])
		if surname < last {
			t.Errorf("%q comes after %q", driver, last)
		}
		last = surname
	}
}

// The pace car is equipment, not a person, and must never appear in a list of
// racers — it is why "Derby Ales" is ranked 36th in the club's published 2026
// wildcard standings.
func TestTheRosterStillLeavesOutThePaceCar(t *testing.T) {
	s, a := testServerPair(t)
	h := handler(t, s)
	if _, err := a.AddDemoRace(t.Context()); err != nil {
		t.Fatal(err)
	}

	data := getJSON(t, h, "/api/race/roster")
	for _, e := range data["entries"].([]any) {
		row := e.(map[string]any)
		if row["car_name"] == "CONTROL" || row["driver"] == "Derby Ales" {
			t.Errorf("the pace car is in the roster: %v", row)
		}
	}
}

// The theme trophy is voted on for how well a car carries the night's theme,
// and until it was stored nothing anywhere named it: the ballot asked for the
// "Best Themed Car" and left the voter to remember what the theme was.
func TestTheNightsThemeReachesTheBallotAndTheTrophy(t *testing.T) {
	s, a := testServerPair(t)
	h := handler(t, s)
	race, err := a.AddDemoRace(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if code, body := postForm(t, h, "/api/vote/theme", url.Values{"theme": {"Movie Night"}}); code != 200 {
		t.Fatalf("saving the theme: %d %v", code, body)
	}

	if got := getJSON(t, h, "/api/vote/ballot")["theme"]; got != "Movie Night" {
		t.Errorf("ballot theme = %v", got)
	}

	// It rides along with the trophy it belongs to, and with nothing else.
	if err := a.DB.EnsureVoteCategories(t.Context(), race.ID); err != nil {
		t.Fatal(err)
	}
	cats, err := a.DB.VoteCategories(t.Context(), race.ID)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := a.DB.Entries(t.Context(), race.ID)
	if err != nil {
		t.Fatal(err)
	}
	var car int64
	for _, e := range entries {
		if !e.IsControl && !e.Excluded {
			car = e.ID
			break
		}
	}
	for _, c := range cats {
		if err := a.DB.DeclareVoteWinner(t.Context(), c.ID, car); err != nil {
			t.Fatalf("declaring %s: %v", c.Key, err)
		}
	}

	awards := getJSON(t, h, "/api/race/awards")["awards"].([]any)
	var themed, other int
	for _, a := range awards {
		row := a.(map[string]any)
		if row["theme"] == "Movie Night" {
			themed++
		} else if row["theme"] != nil {
			other++
		}
	}
	if themed != 1 {
		t.Errorf("%d awards carry the theme, want exactly the theme trophy", themed)
	}
	if other != 0 {
		t.Errorf("%d other awards carry a theme", other)
	}

	// Blank is a real answer: a night with no theme says nothing rather than
	// keeping last month's on the screen.
	if code, body := postForm(t, h, "/api/vote/theme", url.Values{"theme": {"  "}}); code != 200 {
		t.Fatalf("clearing the theme: %d %v", code, body)
	}
	if got := getJSON(t, h, "/api/vote/ballot")["theme"]; got != "" {
		t.Errorf("theme after clearing = %v", got)
	}
}

// The coordinator reads the scene list under pressure, mid-night, on a
// dropdown, so it runs in the order the evening does.
func TestTheSceneListRunsInTheOrderTheNightDoes(t *testing.T) {
	s := mustServer(t)
	body := getPage(t, handler(t, s), "/displays")

	want := []string{
		"Blank", "Tonight&#39;s Racers", "Car Slideshow", "Impound Display", "Now Racing",
		"Design trophies reveal", "Race Results Reveal", "Race Wrap-up", "Final Standings",
		"Voting", "Bracket", "Club Records",
	}
	at := 0
	for _, name := range want {
		i := strings.Index(body[at:], ">"+name+"<")
		if i < 0 {
			t.Fatalf("%q is not on the page, or is out of order", name)
		}
		at += i
	}
}
