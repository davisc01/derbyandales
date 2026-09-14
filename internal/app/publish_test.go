package app

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/davisc01/derbyandales/internal/store"
)

// Publishing a real race from the database into a scratch site, and reading the
// files back the way the website's shortcode does.

func scratchSite(t *testing.T, a *App) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "content", "races"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := a.DB.SetSetting(context.Background(), store.KeyDerbySitePath, root); err != nil {
		t.Fatal(err)
	}
	return root
}

// readPublished parses a written file the way the site's shortcode does.
func readPublished(t *testing.T, root, path string) [][]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if strings.HasPrefix(string(raw), "\ufeff") {
		t.Fatalf("%s was written with a BOM, which breaks the site's column matching", path)
	}
	rows, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	if err != nil {
		t.Fatalf("%s does not parse: %v", path, err)
	}
	return rows
}

func TestPublishingARaceWritesTheWholePage(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()
	root := scratchSite(t, a)

	runWholeRace(t, a, raceID, 0, 0)

	plan, err := a.Publish.PlanRace(ctx, raceID)
	if err != nil {
		t.Fatalf("PlanRace: %v", err)
	}
	if plan.Changes() != 4 {
		t.Fatalf("%d files to write, want 4", plan.Changes())
	}
	written, err := a.Publish.Apply(ctx, plan, "coordinator")
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 4 {
		t.Fatalf("wrote %d files", len(written))
	}

	dir := filepath.Join("content", "races", "2027", "race-1")

	// heats.csv is every recorded run, and one row per lane that had a car.
	heats := readPublished(t, root, filepath.Join(dir, "heats.csv"))
	if heats[0][0] != "Heat" || heats[0][7] != "Scale MPH" {
		t.Errorf("heats header = %v", heats[0])
	}
	dbHeats, _ := a.DB.Heats(ctx, raceID)
	runs := 0
	for _, h := range dbHeats {
		for _, l := range h.Lanes {
			if l.EntryID != nil && l.FinishTime != nil {
				runs++
			}
		}
	}
	if len(heats)-1 != runs {
		t.Errorf("published %d heat rows, the race recorded %d", len(heats)-1, runs)
	}

	// standings.csv leaves out the ineligible car but keeps the pace car.
	standings := readPublished(t, root, filepath.Join(dir, "standings.csv"))
	names := map[string]bool{}
	for _, r := range standings[1:] {
		names[r[3]] = true
	}
	if !names["CONTROL"] {
		t.Error("the pace car is missing from the standings; it races and is ranked")
	}
	entries, _ := a.DB.Entries(ctx, raceID)
	for _, e := range entries {
		if e.Excluded && names[e.CarName] {
			t.Errorf("the ineligible car %q was published in the standings", e.CarName)
		}
	}

	// awards.csv is the three speed trophies, and the pace car takes none.
	awards := readPublished(t, root, filepath.Join(dir, "awards.csv"))
	if len(awards)-1 != 3 {
		t.Fatalf("%d awards published, want 3", len(awards)-1)
	}
	if awards[1][0] != "1st" {
		t.Errorf("the first award is %q", awards[1][0])
	}
	for _, r := range awards[1:] {
		if r[4] == "CONTROL" {
			t.Errorf("the pace car took the %q trophy", r[0])
		}
	}

	// index.md is written once, with the club's sections in it.
	page, err := os.ReadFile(filepath.Join(root, dir, "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Awards", "## Standings", "## Heat Results",
		`csv-table file="heats.csv" download="true"`, `tags: ["race", "2027"]`} {
		if !strings.Contains(string(page), want) {
			t.Errorf("the page does not contain %q", want)
		}
	}
}

// The pace car can and does finish high on a thin night. It is ranked, and it
// takes nothing.
func TestThePaceCarIsSkippedForTrophiesEvenWhenItPlacesWell(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()

	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	// Make the pace car the fastest thing on the track.
	entries, _ := a.DB.Entries(ctx, raceID)
	var control int64
	for _, e := range entries {
		if e.IsControl {
			control = e.ID
		}
	}
	heats, _ := a.DB.Heats(ctx, raceID)
	for _, h := range heats {
		times := map[int]float64{}
		for _, l := range h.Lanes {
			if l.EntryID == nil {
				continue
			}
			if *l.EntryID == control {
				times[l.Lane] = 1.900
			} else {
				// Distinct paces: a tie for a trophy would (rightly) block the
				// trophies, which is not what this test is about.
				times[l.Lane] = 2.400 + float64(*l.EntryID)*0.003
			}
		}
		a.DB.RecordHeatResults(ctx, h.ID, times)
	}

	standings, _ := a.DB.Standings(ctx, raceID)
	if standings[0].Entry.ID != control {
		t.Fatal("the pace car is not first; this test is not exercising anything")
	}

	awards, err := a.DB.SpeedAwards(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(awards) != 3 {
		t.Fatalf("%d speed awards, want 3", len(awards))
	}
	for _, aw := range awards {
		if aw.Entry.IsControl {
			t.Errorf("the pace car took %q while placing 1st", aw.Name)
		}
	}
	// And the trophy went to the fastest car that can take one.
	if awards[0].Entry.ID != standings[1].Entry.ID {
		t.Errorf("the 1st-place trophy went to car %d, want the fastest eligible car %d",
			awards[0].Entry.CarNumber, standings[1].Entry.CarNumber)
	}

}

// The championship has no awards page, and goes in its own folder.
func TestPublishingTheChampionship(t *testing.T) {
	a, seasonID := fullSeason(t)
	ctx := context.Background()
	root := scratchSite(t, a)

	champID := championshipWithField(t, a, seasonID, nil)
	proposals, _ := a.Bracket.Seed(ctx, seasonID, champID)
	if _, err := a.Bracket.Generate(ctx, champID, proposals, "test"); err != nil {
		t.Fatal(err)
	}

	// Race it, so there are results to publish.
	seedOf := map[int64]int{}
	seeds, _ := a.DB.Seeds(ctx, champID)
	for _, s := range seeds {
		seedOf[s.Entry.ID] = s.Seed
	}
	for {
		m, err := a.DB.NextMatchup(ctx, champID)
		if err != nil {
			break
		}
		heat, err := a.DB.CreateMatchupHeat(ctx, champID, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		times := map[int]float64{}
		for _, l := range heat.Lanes {
			if l.EntryID == nil {
				continue
			}
			times[l.Lane] = 2.30 + float64(seedOf[*l.EntryID])*0.005
		}
		if err := a.DB.RecordHeatResults(ctx, heat.ID, times); err != nil {
			t.Fatal(err)
		}
		if err := a.Bracket.RecordResult(ctx, champID, m.ID); err != nil {
			t.Fatalf("recording round %d: %v", m.Round, err)
		}
	}
	if _, err := a.DB.Champion(ctx, champID); err != nil {
		t.Fatalf("the championship did not finish: %v", err)
	}

	plan, err := a.Publish.PlanRace(ctx, champID)
	if err != nil {
		t.Fatalf("PlanRace: %v", err)
	}
	for _, f := range plan.Files {
		if strings.Contains(f.Path, "awards.csv") {
			t.Error("the championship published an awards file")
		}
		if !strings.Contains(f.Path, filepath.Join("2027", "championship")) {
			t.Errorf("the championship wrote to %s", f.Path)
		}
	}
	if _, err := a.Publish.Apply(ctx, plan, "coordinator"); err != nil {
		t.Fatal(err)
	}

	heats := readPublished(t, root, filepath.Join("content", "races", "2027", "championship", "heats.csv"))
	if len(heats) < 2 {
		t.Fatal("the championship published no heats")
	}
	// Bracket heats are two cars, so every heat number appears exactly twice.
	counts := map[string]int{}
	for _, r := range heats[1:] {
		counts[r[0]]++
	}
	for heat, n := range counts {
		if n != 2 {
			t.Errorf("heat %s has %d cars in it, want 2", heat, n)
		}
	}
}

// The season page is the one the wildcard chase is read off all year.
func TestPublishingTheSeasonStandings(t *testing.T) {
	a, seasonID := seasonFixture(t)
	ctx := context.Background()
	root := scratchSite(t, a)

	plan, err := a.Publish.PlanSeason(ctx, seasonID)
	if err != nil {
		t.Fatalf("PlanSeason: %v", err)
	}
	if _, err := a.Publish.Apply(ctx, plan, "coordinator"); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join("content", "races", "2027", "season-standings")
	quals := readPublished(t, root, filepath.Join(dir, "qualifiers.csv"))
	if quals[0][0] != "Current Seed" || quals[0][7] != "Over Limit" {
		t.Errorf("qualifiers header = %v", quals[0])
	}
	for i, r := range quals[1:] {
		if r[0] != strconv.Itoa(i+1) {
			t.Fatalf("row %d has seed %q; seeds must be contiguous", i, r[0])
		}
		if !strings.HasPrefix(r[3], "Race ") {
			t.Errorf("race column is %q, want \"Race N\"", r[3])
		}
		// The site both highlights and hides this column by matching the value.
		if r[7] != "" && r[7] != "YES" {
			t.Errorf("Over Limit is %q, want YES or empty", r[7])
		}
	}

	wild := readPublished(t, root, filepath.Join(dir, "wildcard.csv"))
	if wild[0][2] != "Total Points" {
		t.Errorf("wildcard header = %v", wild[0])
	}

	// The page explains the seeding using this season's own numbers.
	page, err := os.ReadFile(filepath.Join(root, dir, "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "Seeds 1-6 are for individual race winners") {
		t.Error("the season page does not explain the seeding")
	}
}

// A mistyped website folder must be caught before anything is written.
func TestPublishingRefusesAFolderThatIsNotTheSite(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()
	runWholeRace(t, a, raceID, 0, 0)

	for _, path := range []string{"", t.TempDir()} {
		a.DB.SetSetting(ctx, store.KeyDerbySitePath, path)
		if _, err := a.Publish.PlanRace(ctx, raceID); err == nil {
			t.Errorf("publishing to %q was allowed", path)
		}
	}
}

// Publishing a race that has not been run has nothing to say.
func TestPublishingARaceWithNoResults(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()
	scratchSite(t, a)

	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	_, err := a.Publish.PlanRace(ctx, raceID)
	if err == nil {
		t.Fatal("a race with no results was published")
	}
	if !strings.Contains(err.Error(), "no results") {
		t.Errorf("error = %q, want it to explain why", err)
	}
}
