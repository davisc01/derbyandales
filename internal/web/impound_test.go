package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The impound screen is read by someone loading cars into a tray while the
// current heat is on the track. It shows two heats and nothing else.

type impoundHeatJSON struct {
	Heat  int `json:"heat"`
	Lanes []struct {
		Lane      int    `json:"lane"`
		CarNumber int    `json:"car_number"`
		PhotoID   *int64 `json:"photo_id"`
	} `json:"lanes"`
}

func impoundState(t *testing.T, s *Server) (cur, next *impoundHeatJSON) {
	t.Helper()
	rec := get(t, s, "/api/race/impound")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Current  *impoundHeatJSON `json:"current"`
		Upcoming *impoundHeatJSON `json:"upcoming"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Current, out.Upcoming
}

func TestImpoundShowsThisHeatAndTheNext(t *testing.T) {
	s, a, _ := seasonServer(t)
	ctx := context.Background()

	live := liveRace(t, a)
	a.Race.SetRace(ctx, live.ID)
	if _, err := a.Race.GenerateSchedule(ctx, live.ID); err != nil {
		t.Fatal(err)
	}

	cur, next := impoundState(t, s)
	if cur == nil || next == nil {
		t.Fatal("the impound screen has nothing to show at the start of a race")
	}
	if cur.Heat != 1 || next.Heat != 2 {
		t.Fatalf("showing heats %d and %d, want 1 and 2", cur.Heat, next.Heat)
	}
	if len(cur.Lanes) == 0 {
		t.Fatal("no lanes")
	}
	// Every occupied lane has a car number: that is what somebody reads off a
	// shelf. A lane with no car is still listed so the tray matches the track.
	cars := 0
	for _, l := range cur.Lanes {
		if l.Lane == 0 {
			t.Error("a lane has no number")
		}
		if l.CarNumber != 0 {
			cars++
		}
	}
	if cars == 0 {
		t.Error("no cars in the current heat")
	}

	// It moves on as the race does — that is the whole point, so the official
	// is loading the next tray while the current four are running.
	heats, _ := a.DB.Heats(ctx, live.ID)
	times := map[int]float64{}
	for _, l := range heats[0].Lanes {
		if l.EntryID != nil {
			times[l.Lane] = 2.4
		}
	}
	if err := a.DB.RecordHeatResults(ctx, heats[0].ID, times); err != nil {
		t.Fatal(err)
	}

	cur, next = impoundState(t, s)
	if cur.Heat != 2 || next.Heat != 3 {
		t.Errorf("after heat 1 it shows %d and %d, want 2 and 3", cur.Heat, next.Heat)
	}
}

// At the last heat there is nothing more to load, and saying so is better than
// showing the first heat again.
func TestImpoundSaysWhenThereIsNothingMoreToLoad(t *testing.T) {
	s, a, _ := seasonServer(t)
	ctx := context.Background()

	live := liveRace(t, a)
	a.Race.SetRace(ctx, live.ID)
	a.Race.GenerateSchedule(ctx, live.ID)

	heats, _ := a.DB.Heats(ctx, live.ID)
	for _, h := range heats[:len(heats)-1] {
		times := map[int]float64{}
		for _, l := range h.Lanes {
			if l.EntryID != nil {
				times[l.Lane] = 2.4
			}
		}
		a.DB.RecordHeatResults(ctx, h.ID, times)
	}

	cur, next := impoundState(t, s)
	if cur == nil {
		t.Fatal("no current heat at the last heat")
	}
	if next != nil {
		t.Errorf("a heat after the last one was offered: %d", next.Heat)
	}
}

// A screen that does one job all night should not need setting, and should not
// be changeable by accident from the coordinator.
func TestADisplayCanBePinnedToASceneByItsAddress(t *testing.T) {
	s := mustServer(t)

	rec := httptest.NewRecorder()
	handler(t, s).ServeHTTP(rec, httptest.NewRequest("GET", "/display?scene=impound", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "</html>") {
		t.Error("page is truncated")
	}

	// The displays page offers that address, so nobody has to know the trick.
	page := get(t, s, "/displays").Body.String()
	if !strings.Contains(page, "scene=impound") {
		t.Error("the displays page does not offer the impound address")
	}
	// And the addresses are links now, not text to copy by eye.
	if !strings.Contains(page, `<a href="http://`) {
		t.Error("the display addresses are not clickable")
	}
}
