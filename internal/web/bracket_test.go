package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/davisc01/derbyandales/internal/app"
	"github.com/davisc01/derbyandales/internal/model"
)

// The championship page changes shape three times in one evening — no race,
// seeding review, live bracket — and each of those is a template that is only
// ever exercised on the one night of the year it matters.

// championshipFixture returns a server whose season is finished and whose
// championship has a full field checked in.
func championshipFixture(t *testing.T) (*Server, *app.App, int64, int64) {
	t.Helper()
	s, a, seasonID := seasonServer(t)
	ctx := context.Background()

	// Finish the season's last race so all six count.
	races, _ := a.DB.Races(ctx, seasonID)
	for _, r := range races {
		if r.Status != model.StatusCheckin {
			continue
		}
		heats, _ := a.DB.Heats(ctx, r.ID)
		if len(heats) == 0 {
			if _, err := a.Race.GenerateSchedule(ctx, r.ID); err != nil {
				t.Fatalf("scheduling the last race: %v", err)
			}
			heats, _ = a.DB.Heats(ctx, r.ID)
		}
		for i, h := range heats {
			times := map[int]float64{}
			for _, l := range h.Lanes {
				if l.EntryID != nil {
					times[l.Lane] = 2.30 + float64(*l.EntryID%17)*0.01 + float64(i%3)*0.004
				}
			}
			if err := a.DB.RecordHeatResults(ctx, h.ID, times); err != nil {
				t.Fatal(err)
			}
		}
		a.DB.SetRaceStatus(ctx, r.ID, model.StatusVoting)
		if err := a.Season.FreezeRace(ctx, r.ID); err != nil {
			t.Fatalf("freezing the last race: %v", err)
		}
	}

	champ, err := a.DB.CreateRace(ctx, model.Race{
		SeasonID: seasonID, Number: 1, Name: "Championship",
		Date: time.Now(), Venue: "The Testing Room",
		Kind: model.RaceChampionship, Format: model.FormatBracket, Status: model.StatusCheckin,
	})
	if err != nil {
		t.Fatal(err)
	}

	field, err := a.DB.ProposeSeeding(ctx, seasonID, champ.ID)
	if err != nil {
		t.Fatal(err)
	}
	number := 200
	for _, c := range field {
		number++
		carName := c.CarName
		if carName == "" {
			carName = "Wildcard " + c.Driver
		}
		now := time.Now()
		if _, err := a.DB.CreateEntry(ctx, model.Entry{
			RaceID: champ.ID, RacerID: c.RacerID, CarNumber: number,
			CarName: carName, CheckedInAt: &now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return s, a, seasonID, champ.ID
}

func get(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler(t, s).ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

// The page is in the nav from the first launch, long before there is a season.
func TestChampionshipPageRendersWithNothingSetUp(t *testing.T) {
	s := mustServer(t)
	rec := get(t, s, "/championship")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "</html>") {
		t.Error("page is truncated")
	}
}

// The planner is the answer to "what happens if we drop to five races", and it
// should be on the page whether or not a championship exists yet.
func TestTheChampionshipPageShowsTheDerivedShape(t *testing.T) {
	s, _, _ := seasonServer(t)
	rec := get(t, s, "/championship")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "</html>") {
		t.Fatal("page is truncated")
	}
	for _, want := range []string{"Entrants", "Byes", "First round", "Rounds"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not show %q", want)
		}
	}
	// 6 races x 3 plus 6 wildcards: 24 cars in a 32 bracket with 8 byes.
	if !strings.Contains(body, "Seeds 1&ndash;8 go straight into round two") {
		t.Error("the page does not say who gets a bye")
	}
}

func TestTheSeedingReviewRendersTheWholeField(t *testing.T) {
	s, _, _, _ := championshipFixture(t)

	rec := get(t, s, "/championship")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "</html>") {
		t.Fatal("page is truncated")
	}
	if !strings.Contains(body, "24 of 24 places filled") {
		t.Error("the seeding review does not report a full field")
	}
	for _, want := range []string{"auto-qualifier", "wild-card", "Build the bracket"} {
		if !strings.Contains(body, want) {
			t.Errorf("the seeding review does not show %q", want)
		}
	}
}

func TestBuildingTheBracketFromThePage(t *testing.T) {
	s, a, seasonID, champID := championshipFixture(t)
	ctx := context.Background()

	req := httptest.NewRequest("POST", "/api/bracket/generate",
		strings.NewReader("season_id="+itoa(seasonID)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler(t, s).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}

	var out struct {
		Entrants, Capacity, Byes, Rounds int
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Entrants != 24 || out.Capacity != 32 || out.Byes != 8 || out.Rounds != 5 {
		t.Fatalf("built %d cars, %d capacity, %d byes, %d rounds",
			out.Entrants, out.Capacity, out.Byes, out.Rounds)
	}

	// And the page becomes the bracket rather than the seeding review.
	page := get(t, s, "/championship").Body.String()
	if !strings.Contains(page, "The bracket") {
		t.Error("the bracket did not render after being built")
	}
	if strings.Contains(page, "Build the bracket") {
		t.Error("the seeding review is still offered after the bracket exists")
	}
	for _, want := range []string{"Final", "Semi-finals", "Quarter-finals"} {
		if !strings.Contains(page, want) {
			t.Errorf("the bracket does not name the %q round", want)
		}
	}
	if !strings.Contains(page, "bye &mdash; straight to the next round") {
		t.Error("byes are not shown as byes")
	}

	// The JSON the display scene reads agrees with the page.
	state, err := a.Bracket.State(ctx, champID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Entrants != 24 || state.Champion != nil {
		t.Errorf("state = %d entrants, champion %v", state.Entrants, state.Champion)
	}
}

// The state endpoint is what a TV reads, so it has to be right from the first
// request rather than after a refresh.
func TestTheBracketStateEndpointDescribesTheBracket(t *testing.T) {
	s, a, seasonID, champID := championshipFixture(t)
	ctx := context.Background()

	proposals, _ := a.Bracket.Seed(ctx, seasonID, champID)
	if _, err := a.Bracket.Generate(ctx, champID, proposals, "test"); err != nil {
		t.Fatal(err)
	}

	rec := get(t, s, "/api/bracket/state?season_id="+itoa(seasonID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var out struct {
		Seeded   bool `json:"seeded"`
		Entrants int  `json:"entrants"`
		Byes     int  `json:"byes"`
		Rounds   []struct {
			Round    int    `json:"round"`
			Name     string `json:"name"`
			Matchups []struct {
				Walkover bool `json:"walkover"`
				Ready    bool `json:"ready"`
				Top      *struct {
					Seed int    `json:"seed"`
					Car  string `json:"car"`
				} `json:"top"`
			} `json:"matchups"`
		} `json:"rounds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Seeded || out.Entrants != 24 || out.Byes != 8 {
		t.Fatalf("seeded=%v entrants=%d byes=%d", out.Seeded, out.Entrants, out.Byes)
	}
	if len(out.Rounds) != 5 {
		t.Fatalf("%d rounds, want 5", len(out.Rounds))
	}
	if out.Rounds[4].Name != "Final" {
		t.Errorf("the last round is called %q", out.Rounds[4].Name)
	}

	walkovers, ready := 0, 0
	for _, m := range out.Rounds[0].Matchups {
		if m.Walkover {
			walkovers++
		}
		if m.Ready {
			ready++
		}
	}
	if walkovers != 8 || ready != 8 {
		t.Errorf("round one has %d byes and %d races, want 8 and 8", walkovers, ready)
	}
}

// The planner endpoint answers the season-shape question without touching
// anything.
func TestThePlannerEndpointAnswersWhatIf(t *testing.T) {
	s := mustServer(t)

	rec := get(t, s, "/api/bracket/plan?races=5&places=3&wildcards=6")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var out struct {
		Entrants    int      `json:"entrants"`
		Capacity    int      `json:"capacity"`
		Byes        int      `json:"byes"`
		Warnings    []string `json:"warnings"`
		Suggestions []struct {
			Wildcards int  `json:"wildcards"`
			ByeFree   bool `json:"bye_free"`
		} `json:"suggestions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	// Five races keeping six wildcards is 21 cars in a 32 bracket: 11 byes.
	if out.Entrants != 21 || out.Capacity != 32 || out.Byes != 11 {
		t.Errorf("5 races + 6 wildcards = %d cars, %d bracket, %d byes",
			out.Entrants, out.Capacity, out.Byes)
	}
	// And it should offer the tidier answer rather than leaving it to be found.
	byeFree := 0
	for _, sg := range out.Suggestions {
		if sg.ByeFree {
			byeFree++
		}
	}
	if byeFree == 0 {
		t.Error("no bye-free alternative was suggested for a five-race season")
	}
}

// The TV scene and the loading tray both have to follow a bracket, whose next
// heat does not exist until it is armed.
func TestTheBracketSceneAndImpoundFollowTheMatchups(t *testing.T) {
	s, a, seasonID, champID := championshipFixture(t)
	ctx := context.Background()
	if err := a.Timer.Connect(ctx, "", "simulator"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Timer.RunBench(ctx); err != nil {
		t.Fatal(err)
	}
	proposals, _ := a.Bracket.Seed(ctx, seasonID, champID)
	if _, err := a.Bracket.Generate(ctx, champID, proposals, "test"); err != nil {
		t.Fatal(err)
	}
	if err := a.Race.Start(ctx, champID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Race.Stop)
	armed := a.Race.State(ctx).Heat

	rec := get(t, s, "/api/race/bracket")
	var scene struct {
		Seeded  bool  `json:"seeded"`
		OnTrack int64 `json:"on_track"`
		Rounds  []struct {
			Name     string            `json:"name"`
			Matchups []json.RawMessage `json:"matchups"`
		} `json:"rounds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &scene); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	if !scene.Seeded || len(scene.Rounds) != 5 || scene.Rounds[4].Name != "Final" {
		t.Errorf("the bracket scene has %d rounds (seeded %v)", len(scene.Rounds), scene.Seeded)
	}
	if scene.OnTrack != *armed.BracketMatchupID {
		t.Errorf("the scene marks matchup %d on the track, but %d is armed", scene.OnTrack, *armed.BracketMatchupID)
	}

	cur, next := impoundState(t, s)
	if cur == nil || next == nil {
		t.Fatalf("impound shows current %v and next %v for a bracket", cur != nil, next != nil)
	}
	cars := 0
	for _, l := range next.Lanes {
		if l.CarNumber != 0 {
			cars++
		}
	}
	if cars != 2 {
		t.Errorf("the next matchup to load has %d cars, want 2", cars)
	}
}

// The race page for a bracket must not offer to build a round-robin schedule,
// and says where the bracket is built instead.
func TestTheRacePageForABracketDoesNotOfferASchedule(t *testing.T) {
	s, a, _, champID := championshipFixture(t)
	if err := a.Race.SetRace(context.Background(), champID); err != nil {
		t.Fatal(err)
	}
	body := get(t, s, "/race").Body.String()
	if strings.Contains(body, `id="close-checkin"`) {
		t.Error("the race page offers a round-robin schedule for a bracket")
	}
	if !strings.Contains(body, "runs as a bracket") {
		t.Error("the race page does not say the championship is a bracket")
	}
}

// The reveal names the trophy due as each car comes up. At the championship
// there is one, and it has a name.
func TestTheChampionshipRevealNamesTheDAleCup(t *testing.T) {
	s, a, _, champID := championshipFixture(t)
	if err := a.Race.SetRace(context.Background(), champID); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Trophies int      `json:"trophies"`
		Names    []string `json:"trophy_names"`
	}
	rec := get(t, s, "/api/race/standings")
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Trophies != 1 || len(out.Names) != 1 || out.Names[0] != "The D'Ale Cup" {
		t.Errorf("the championship reveal offers %d trophies named %v", out.Trophies, out.Names)
	}
}
