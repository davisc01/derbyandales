package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// A car checked in after racing has started used to be in no heat and nobody
// was told; it turned up at the reveal with no runs. The desk has to say so,
// and race control has to offer to fit it in — end to end, through the same
// endpoints the two screens use.
func TestRaceControlNoticesACarCheckedInAfterRacingStarted(t *testing.T) {
	s, a, _ := seasonServer(t)
	ctx := context.Background()
	h := handler(t, s)

	live := liveRace(t, a)
	if err := a.Race.SetRace(ctx, live.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Race.GenerateSchedule(ctx, live.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		heat, err := a.DB.NextHeat(ctx, live.ID)
		if err != nil {
			t.Fatal(err)
		}
		times := map[int]float64{}
		for _, l := range heat.Lanes {
			if l.EntryID != nil {
				times[l.Lane] = 2.5 + float64(l.Lane)*0.01
			}
		}
		if err := a.DB.RecordHeatResults(ctx, heat.ID, times); err != nil {
			t.Fatal(err)
		}
	}

	code, body := postForm(t, h, "/api/entry", url.Values{
		"race_id": {itoa(live.ID)}, "first_name": {"Wendy"}, "last_name": {"Latecomer"},
		"car_number": {"321"}, "car_name": {"Fashionably Late"},
	})
	if code != http.StatusOK {
		t.Fatalf("check-in: %d %v", code, body)
	}
	if late, _ := body["late"].(string); !strings.Contains(late, "race control") {
		t.Errorf("the desk was not told the car needs fitting in: %v", body)
	}

	page := func() string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/race", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("race page: %d", rec.Code)
		}
		return rec.Body.String()
	}
	body1 := page()
	if !strings.Contains(body1, "Checked in after the schedule was built") ||
		!strings.Contains(body1, "Fashionably Late") {
		t.Fatal("race control does not mention the late car")
	}
	if !strings.Contains(body1, "Fit it into the race") {
		t.Error("race control offers no way to fit the car in")
	}

	if code, body := postForm(t, h, "/api/race/latecars", url.Values{}); code != http.StatusOK {
		t.Fatalf("fit in: %d %v", code, body)
	}
	if strings.Contains(page(), "Checked in after the schedule was built") {
		t.Error("the card is still there after the car was fitted in")
	}
	if got := getJSON(t, h, "/api/race/latecars"); got["cars"] != nil {
		t.Errorf("still reported late: %v", got["cars"])
	}
}

// Before the schedule exists every car is simply waiting for check-in to
// close, and telling the desk otherwise would be a warning that means nothing.
func TestCheckingInBeforeTheScheduleSaysNothingAboutBeingLate(t *testing.T) {
	h, raceID := checkinFixture(t)
	code, body := postForm(t, h, "/api/entry", url.Values{
		"race_id": {itoa(raceID)}, "first_name": {"Ada"}, "last_name": {"Early"},
		"car_number": {"7"}, "car_name": {"On Time"},
	})
	if code != http.StatusOK {
		t.Fatalf("check-in: %d %v", code, body)
	}
	if _, ok := body["late"]; ok {
		t.Errorf("an on-time check-in was called late: %v", body)
	}
}
