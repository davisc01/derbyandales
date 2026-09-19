package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// The reveal is where a record average is announced, as the car comes up. The
// standings the reveal reads have to carry it, and only for cars that earned it.
func TestTheRevealIsToldAboutARecordAverage(t *testing.T) {
	s, a, seasonID := seasonServer(t)
	ctx := context.Background()

	// The last night the demo season has fully raced.
	races, _ := a.DB.Races(ctx, seasonID)
	var raceID int64
	for _, r := range races {
		heats, _ := a.DB.Heats(ctx, r.ID)
		if len(heats) > 0 && allHeatsRun(heats) {
			raceID = r.ID
		}
	}
	if raceID == 0 {
		t.Fatal("the demo season has no finished race")
	}

	// One car runs every heat in 2.000, far under anything on file, and the
	// race is recorded again the way a corrected result would be.
	heats, _ := a.DB.Heats(ctx, raceID)
	entries, _ := a.DB.Entries(ctx, raceID)
	var car int
	var carID int64
	for _, e := range entries {
		if !e.IsControl && !e.Excluded {
			car, carID = e.CarNumber, e.ID
			break
		}
	}
	for _, h := range heats {
		times := map[int]float64{}
		for _, l := range h.Lanes {
			if l.FinishTime != nil {
				times[l.Lane] = *l.FinishTime
				if l.EntryID != nil && *l.EntryID == carID {
					times[l.Lane] = 2.000
				}
			}
		}
		if err := a.DB.RecordHeatResults(ctx, h.ID, times); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.DB.FreezeRace(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	// Loading the demo race is what lets the demo's own times count.
	if err := a.Race.SetRace(ctx, raceID); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	handler(t, s).ServeHTTP(rec, httptest.NewRequest("GET", "/api/race/standings", nil))
	var got struct {
		Standings []struct {
			CarNumber int    `json:"car_number"`
			Average   string `json:"average"`
			Record    *struct {
				Kind     string `json:"kind"`
				Previous string `json:"previous"`
			} `json:"record"`
		} `json:"standings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range got.Standings {
		if row.Record == nil {
			continue
		}
		if row.CarNumber == car {
			found = true
		}
		// Any other car marked has to have earned it: faster than the record
		// it is shown beating, which is named beside it.
		var was float64
		if _, err := fmt.Sscanf(row.Record.Previous, "was %f,", &was); err != nil {
			t.Fatalf("the record it beat is not given: %q", row.Record.Previous)
		}
		avg, _ := strconv.ParseFloat(row.Average, 64)
		if row.Record.Kind != "average" || avg >= was {
			t.Errorf("car %d (%s) marked %+v", row.CarNumber, row.Average, row.Record)
		}
	}
	if !found {
		t.Errorf("car %d ran 2.000 all night and was not marked as a record average", car)
	}
}

// The records page and the scene have to work with nothing on file: they are
// in the nav bar from the first launch.
func TestTheRecordsWorkWithNothingOnFile(t *testing.T) {
	s := mustServer(t)
	h := handler(t, s)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/records", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No runs on record yet") {
		t.Errorf("status %d; the empty page did not say so", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/records", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("records scene data: status %d", rec.Code)
	}
}

// With a season on file, the page shows its records.
func TestTheRecordsPageShowsTheRecords(t *testing.T) {
	s, a, seasonID := seasonServer(t)
	races, _ := a.DB.Races(context.Background(), seasonID)
	if err := a.Race.SetRace(context.Background(), races[0].ID); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	handler(t, s).ServeHTTP(rec, httptest.NewRequest("GET", "/records", nil))
	body := rec.Body.String()
	for _, want := range []string{"Fastest run", "Fastest average", "Lane records", "Personal bests",
		"demo season"} {
		if !strings.Contains(body, want) {
			t.Errorf("the records page is missing %q", want)
		}
	}
}

// The wrap-up of a finished night: every lane accounted for, three fastest
// heats, and totals that agree with the heats actually run.
func TestTheWrapUpReviewsAFinishedNight(t *testing.T) {
	s, a, seasonID := seasonServer(t)
	ctx := context.Background()

	races, _ := a.DB.Races(ctx, seasonID)
	var raceID int64
	for _, r := range races {
		heats, _ := a.DB.Heats(ctx, r.ID)
		if len(heats) > 0 && allHeatsRun(heats) {
			raceID = r.ID
			break
		}
	}
	if raceID == 0 {
		t.Fatal("the demo season has no finished race")
	}
	if err := a.Race.SetRace(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	heats, _ := a.DB.Heats(ctx, raceID)

	rec := httptest.NewRecorder()
	handler(t, s).ServeHTTP(rec, httptest.NewRequest("GET", "/api/race/wrapup", nil))
	var got struct {
		Heats int `json:"heats"`
		Lanes []struct {
			Lane int `json:"lane"`
			Wins int `json:"wins"`
		} `json:"lanes"`
		Fastest []struct {
			Heat int    `json:"heat"`
			Time string `json:"time"`
		} `json:"fastest_heats"`
		Margin *struct {
			Gap string `json:"gap"`
		} `json:"margin"`
		Timer *struct {
			Reruns struct {
				Count int `json:"count"`
			} `json:"reruns"`
		} `json:"timer"`
		Control *struct {
			Average string `json:"average"`
			Usual   string `json:"usual"`
		} `json:"control"`
		TopRaces *struct {
			Top []struct {
				Rank    int  `json:"rank"`
				Tonight bool `json:"tonight"`
			} `json:"top"`
			Of       int `json:"of"`
			ThisRace *struct {
				Rank    int  `json:"rank"`
				Tonight bool `json:"tonight"`
			} `json:"this_race"`
		} `json:"top_races"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	if got.Heats != len(heats) {
		t.Errorf("%d heats reviewed, %d run", got.Heats, len(heats))
	}
	if len(got.Lanes) != 4 {
		t.Errorf("%d lanes", len(got.Lanes))
	}
	wins := 0
	for _, l := range got.Lanes {
		wins += l.Wins
	}
	// Every heat has a winner; a dead heat would give two.
	if wins < len(heats) {
		t.Errorf("%d lane wins across %d heats", wins, len(heats))
	}
	if len(got.Fastest) != 3 || got.Fastest[0].Heat == got.Fastest[1].Heat {
		t.Errorf("fastest heats = %+v, want three different heats", got.Fastest)
	}
	if got.Margin == nil {
		t.Error("no winning margin")
	}
	if got.Timer == nil {
		t.Error("no timer health")
	}
	// The demo's CONTROL car ran this night, so it is reviewed.
	if got.Control == nil || got.Control.Average == "" {
		t.Errorf("no CONTROL car review: %+v", got.Control)
	}
	// Five demo nights are finished, so all five are ranked and this one has
	// a place among them — marked in the list if it made the top five.
	if tr := got.TopRaces; tr == nil || len(tr.Top) != 5 || tr.Of != 5 || tr.ThisRace == nil ||
		tr.ThisRace.Rank < 1 || !tr.ThisRace.Tonight || !tr.Top[tr.ThisRace.Rank-1].Tonight {
		t.Errorf("top races = %+v", got.TopRaces)
	}
}
