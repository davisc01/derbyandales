package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/davisc01/derbyandales/internal/app"
)

// seasonServer returns a server whose database holds a demo season five races
// deep, so the season screen has something real to render.
func seasonServer(t *testing.T) (*Server, *app.App, int64) {
	t.Helper()
	s, a := testServer(t)
	ctx := context.Background()

	live, err := a.SeedDemoSeason(ctx, 2027)
	if err != nil {
		t.Fatalf("seed demo season: %v", err)
	}
	race, err := a.DB.Race(ctx, live.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, a, race.SeasonID
}

// An empty database must not break the page: the season screen is in the nav
// bar from the first launch, before anybody has raced anything.
func TestSeasonPageRendersWithNoSeason(t *testing.T) {
	s := mustServer(t)
	rec := httptest.NewRecorder()
	handler(t, s).ServeHTTP(rec, httptest.NewRequest("GET", "/season", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "</html>") {
		t.Error("page is truncated")
	}
}

func TestSeasonPageShowsTheStandings(t *testing.T) {
	s, _, _ := seasonServer(t)
	rec := httptest.NewRecorder()
	handler(t, s).ServeHTTP(rec, httptest.NewRequest("GET", "/season", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "</html>") {
		t.Fatal("page is truncated")
	}
	for _, want := range []string{"Auto-qualifiers", "Wildcard standings", "Adjustments"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not show %q", want)
		}
	}
	// The derived field size is the number that tells a coordinator whether the
	// championship is seedable, so it has to be on the page.
	if !strings.Contains(body, "qualifying places decided") {
		t.Error("the page does not show the derived field size")
	}
}

// Errors from this screen are read by someone deciding whether to argue with a
// racer, so they must be sentences rather than constraint names.
func TestSeasonErrorsAreReadable(t *testing.T) {
	s, _, seasonID := seasonServer(t)
	h := handler(t, s)

	post := func(path string, form url.Values) map[string]string {
		t.Helper()
		form.Set("season_id", itoa(seasonID))
		req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			t.Fatalf("%s unexpectedly succeeded: %s", path, rec.Body.String())
		}
		var out map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s did not return JSON: %s", path, rec.Body.String())
		}
		return out
	}

	cases := []struct {
		name string
		path string
		form url.Values
	}{
		{"an adjustment with no reason", "/api/season/adjust",
			url.Values{"racer_id": {"1"}, "points": {"5"}, "reason": {""}}},
		{"an adjustment with no racer", "/api/season/adjust",
			url.Values{"racer_id": {"0"}, "points": {"5"}, "reason": {"why"}}},
		{"points that are not a number", "/api/season/adjust",
			url.Values{"racer_id": {"1"}, "points": {"lots"}, "reason": {"why"}}},
		{"a substitution that was never made", "/api/season/substitute/undo",
			url.Values{"original_entry_id": {"1"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := post(c.path, c.form)["error"]
			if msg == "" {
				t.Fatal("no error message")
			}
			for _, leak := range []string{"UNIQUE", "constraint", "idx_", "sql:", "nil pointer"} {
				if strings.Contains(msg, leak) {
					t.Errorf("error leaks implementation detail %q: %s", leak, msg)
				}
			}
		})
	}
}

// The substitute picker only offers cars that can legally take the slot, which
// is what stops a wrong bracket being built by clicking the obvious thing.
func TestSubstituteCandidatesComeFromTheSameRace(t *testing.T) {
	s, a, seasonID := seasonServer(t)
	ctx := context.Background()

	slots, err := a.DB.Qualifiers(ctx, seasonID)
	if err != nil {
		t.Fatal(err)
	}
	var over int64
	var raceNumber int
	for _, slot := range slots {
		if slot.OverLimit {
			over = slot.EntryID
			raceNumber = slot.RaceNumber
		}
	}
	if over == 0 {
		t.Skip("the demo season has no racer over the entry cap")
	}

	rec := httptest.NewRecorder()
	handler(t, s).ServeHTTP(rec, httptest.NewRequest("GET",
		"/api/season/substitutes?entry_id="+itoa(over)+"&season_id="+itoa(seasonID), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}

	var out struct {
		Race       int `json:"race"`
		Candidates []struct {
			Place int `json:"place"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Race != raceNumber {
		t.Errorf("candidates offered from race %d, the slot is from race %d", out.Race, raceNumber)
	}
	if len(out.Candidates) == 0 {
		t.Fatal("no candidates offered")
	}
	for _, c := range out.Candidates {
		if c.Place <= 3 {
			t.Errorf("a car that finished %d was offered, but it already qualified", c.Place)
		}
	}
}
