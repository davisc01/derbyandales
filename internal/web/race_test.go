package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The anomaly card only appears once every heat has been run, so it is exactly
// the kind of template that is never exercised until race night.
func TestTheAnomalyCardRendersOnAFinishedRace(t *testing.T) {
	s, a, seasonID := seasonServer(t)
	ctx := context.Background()

	races, err := a.DB.Races(ctx, seasonID)
	if err != nil {
		t.Fatal(err)
	}
	// The demo season's earlier nights are already raced in full.
	var finished int64
	for _, r := range races {
		heats, _ := a.DB.Heats(ctx, r.ID)
		if len(heats) > 0 && allHeatsRun(heats) {
			finished = r.ID
			break
		}
	}
	if finished == 0 {
		t.Fatal("the demo season has no finished race to render")
	}

	// Break one heat: every car in it loses half a second, which is what a
	// sticky gate looks like in the data.
	heats, _ := a.DB.Heats(ctx, finished)
	broken := heats[len(heats)/2]
	times := map[int]float64{}
	for _, l := range broken.Lanes {
		if l.EntryID != nil && l.FinishTime != nil {
			times[l.Lane] = *l.FinishTime + 0.5
		}
	}
	if len(times) < 3 {
		t.Skip("that heat has too few cars to flag")
	}
	if err := a.DB.RecordHeatResults(ctx, broken.ID, times); err != nil {
		t.Fatal(err)
	}

	if err := a.Race.SetRace(ctx, finished); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	handler(t, s).ServeHTTP(rec, httptest.NewRequest("GET", "/race", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "</html>") {
		t.Fatal("page is truncated")
	}
	if !strings.Contains(body, "Heats worth a second look") {
		t.Fatal("the anomaly card did not render on a race with a broken heat")
	}
	if !strings.Contains(body, "Worth re-running") {
		t.Error("a clear fault was not described as worth re-running")
	}
	if !strings.Contains(body, "Re-run heat") {
		t.Error("the card offers no way to re-run the heat")
	}
}

// Mid-race the check would be meaningless: a car's "slowest run of the night"
// is the slowest of the two it has had so far.
func TestNoAnomalyCardBeforeTheHeatsAreDone(t *testing.T) {
	s, a, _ := seasonServer(t)
	ctx := context.Background()

	// The demo's live race is still at check-in, so nothing has run.
	a.Race.LoadMostRecentRace(ctx)

	rec := httptest.NewRecorder()
	handler(t, s).ServeHTTP(rec, httptest.NewRequest("GET", "/race", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "Heats worth a second look") {
		t.Error("the anomaly card appeared before the heats were run")
	}
}
