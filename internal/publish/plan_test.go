package publish

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Publishing writes into somebody's git working tree, so the tests here are
// mostly about restraint: what it declines to touch, and whether it says
// honestly what it is about to do.

// fakeSite builds a directory that looks enough like the club's site.
func fakeSite(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "content", "races"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func sampleRace(year, number int) RaceFiles {
	return RaceFiles{
		Page: Page{
			Year: year, Number: number, Name: "Race 1",
			Venue: "Demo Brewing Co.", Date: time.Date(year, 3, 1, 0, 0, 0, 0, time.UTC),
		},
		Heats: []HeatRow{
			{Heat: 1, Lane: 1, First: "Ada", Last: "Fairweather", CarNumber: 7, CarName: "Lightning Bug", Time: 2.401, Place: 1},
			{Heat: 1, Lane: 2, First: "Cass", Last: "Nguyen", CarNumber: 19, CarName: "Toast", Time: 2.512, Place: 2},
		},
		Standings: []StandingRow{
			{Place: 1, CarNumber: 7, Name: "Ada Fairweather", CarName: "Lightning Bug", Heats: 4, Average: 2.401, Best: 2.39, Worst: 2.46},
			{Place: 2, CarNumber: 19, Name: "Cass Nguyen", CarName: "Toast", Heats: 4, Average: 2.512, Best: 2.5, Worst: 2.58},
		},
		Awards: []AwardRow{
			{Award: "Fastest in Event", First: "Ada", Last: "Fairweather", CarNumber: 7, CarName: "Lightning Bug"},
		},
		TrackFt: 28, ScaleDenom: 25,
	}
}

func find(t *testing.T, p Plan, suffix string) File {
	t.Helper()
	for _, f := range p.Files {
		if strings.HasSuffix(f.Path, suffix) {
			return f
		}
	}
	t.Fatalf("no file ending %q in the plan", suffix)
	return File{}
}

// A mistyped setting must not scatter CSV files through a home directory.
func TestAFolderThatIsNotTheSiteIsRefused(t *testing.T) {
	cases := map[string]string{
		"empty":        "",
		"missing":      filepath.Join(t.TempDir(), "nope"),
		"not the site": t.TempDir(), // exists, but has no content/races
	}
	for name, root := range cases {
		t.Run(name, func(t *testing.T) {
			err := SiteRoot(root)
			if err == nil {
				t.Fatal("accepted a folder that is not the website")
			}
			for _, leak := range []string{"no such file", "stat ", "syscall"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("error leaks a system message: %s", err)
				}
			}
		})
	}

	if err := SiteRoot(fakeSite(t)); err != nil {
		t.Errorf("a real site folder was refused: %v", err)
	}
}

// A first publish writes everything, including the page itself.
func TestAFirstPublishCreatesThePageAndTheFiles(t *testing.T) {
	root := fakeSite(t)

	plan, err := PlanRace(root, sampleRace(2027, 1))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Changes() != 4 {
		t.Fatalf("%d files to write, want 4", plan.Changes())
	}
	for _, f := range plan.Files {
		if f.Status != StatusNew {
			t.Errorf("%s is %q on a first publish", f.Path, f.Status)
		}
	}

	written, err := Apply(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 4 {
		t.Fatalf("wrote %d files, want 4", len(written))
	}
	for _, name := range []string{"index.md", "heats.csv", "standings.csv", "awards.csv"} {
		path := filepath.Join(root, "content", "races", "2027", "race-1", name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s was not written", name)
		}
	}

	// Publishing again changes nothing, which is what makes it safe to press.
	again, err := PlanRace(root, sampleRace(2027, 1))
	if err != nil {
		t.Fatal(err)
	}
	if again.Changes() != 0 {
		for _, f := range again.Files {
			if f.Writes() {
				t.Errorf("%s would be rewritten unchanged (%s)", f.Path, f.Status)
			}
		}
	}
}

// index.md carries the summary and the photo album link, neither of which any
// export knows about. Publishing again must never flatten it.
func TestAnExistingPageIsNeverOverwritten(t *testing.T) {
	root := fakeSite(t)
	plan, _ := PlanRace(root, sampleRace(2027, 1))
	if _, err := Apply(plan); err != nil {
		t.Fatal(err)
	}

	page := filepath.Join(root, "content", "races", "2027", "race-1", "index.md")
	edited := "---\ntitle: \"Race 1\"\n---\n\nSomebody wrote this by hand.\n"
	if err := os.WriteFile(page, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	again, _ := PlanRace(root, sampleRace(2027, 1))
	f := find(t, again, "index.md")
	if f.Status != StatusKept {
		t.Errorf("index.md is %q, want %q", f.Status, StatusKept)
	}
	if f.Note == "" {
		t.Error("nothing explains why index.md is being left alone")
	}
	if _, err := Apply(again); err != nil {
		t.Fatal(err)
	}

	after, _ := os.ReadFile(page)
	if string(after) != edited {
		t.Error("a hand-edited page was overwritten")
	}
}

// The championship has no awards page. Publishing one would render an empty
// table on the site.
func TestTheChampionshipPublishesNoAwards(t *testing.T) {
	root := fakeSite(t)
	r := sampleRace(2027, 1)
	r.Page.Championship = true
	r.Page.Name = "2027 Championship"

	plan, err := PlanRace(root, r)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range plan.Files {
		if strings.HasSuffix(f.Path, "awards.csv") {
			t.Error("the championship plan includes an awards file")
		}
	}
	if len(plan.Files) != 3 {
		t.Errorf("%d files, want 3", len(plan.Files))
	}
	// And it goes in its own folder, not a numbered one.
	if !strings.Contains(plan.Files[0].Path, filepath.Join("2027", "championship")) {
		t.Errorf("the championship went to %s", plan.Files[0].Path)
	}

	page := string(IndexMD(r.Page))
	if strings.Contains(page, "awards.csv") {
		t.Error("the championship page renders an awards table")
	}
	if !strings.Contains(page, `tags: ["championship", "2027"]`) {
		t.Error("the championship page is not tagged as one")
	}
}

// Nothing is written until it has been shown, so the diff has to say what
// actually changed rather than just how much.
func TestTheDiffShowsWhichRowChanged(t *testing.T) {
	root := fakeSite(t)
	first := sampleRace(2027, 1)
	if _, err := Apply(mustPlan(t, root, first)); err != nil {
		t.Fatal(err)
	}

	// One time corrected after the night.
	second := sampleRace(2027, 1)
	second.Heats[1].Time = 2.498

	plan := mustPlan(t, root, second)
	f := find(t, plan, "heats.csv")
	if f.Status != StatusChanged {
		t.Fatalf("heats.csv is %q after a time changed", f.Status)
	}
	if f.Added != 1 || f.Removed != 1 {
		t.Errorf("one corrected time shows as +%d/-%d, want +1/-1", f.Added, f.Removed)
	}

	var added, removed string
	for _, d := range f.Diff {
		switch d.Mark {
		case '+':
			added = d.Text
		case '-':
			removed = d.Text
		}
	}
	if !strings.Contains(removed, "2.512") {
		t.Errorf("the removed line does not show the old time: %q", removed)
	}
	if !strings.Contains(added, "2.498") {
		t.Errorf("the added line does not show the new time: %q", added)
	}
	// And the scale speed moved with it, which is the point of recomputing it.
	if strings.Contains(added, "187.9") && strings.Contains(removed, "187.9") {
		t.Error("the scale speed did not change with the time")
	}
}

// A big change should still be readable. An excluded car shifts every place
// below it, and the diff should show that rather than a wall of a hundred lines.
func TestABigDiffStaysReadable(t *testing.T) {
	root := fakeSite(t)

	big := sampleRace(2027, 1)
	for i := 0; i < 60; i++ {
		big.Heats = append(big.Heats, HeatRow{
			Heat: i/4 + 2, Lane: i%4 + 1, First: "R", Last: "acer",
			CarNumber: 100 + i, CarName: "Car", Time: 2.4 + float64(i)/1000, Place: i%4 + 1,
		})
	}
	if _, err := Apply(mustPlan(t, root, big)); err != nil {
		t.Fatal(err)
	}

	changed := big
	changed.Heats = append([]HeatRow(nil), big.Heats...)
	changed.Heats[30].Time = 3.111

	plan := mustPlan(t, root, changed)
	f := find(t, plan, "heats.csv")
	if len(f.Diff) > 12 {
		t.Errorf("one changed row produced a %d-line diff", len(f.Diff))
	}
	if f.Added != 1 || f.Removed != 1 {
		t.Errorf("+%d/-%d for one changed row", f.Added, f.Removed)
	}
	// The elided middle is marked rather than silently dropped.
	elided := false
	for _, d := range f.Diff {
		if d.Text == "…" {
			elided = true
		}
	}
	if !elided {
		t.Error("the skipped lines are not marked")
	}
}

// The season page explains the seeding in the club's own words, and those words
// contain seed numbers that change with the season's shape.
func TestTheSeasonPageDescribesItsOwnShape(t *testing.T) {
	six := string(SeasonIndexMD(SeasonPage{
		Year: 2027, Races: 6, AutoQualPlaces: 3, WildcardSpots: 6, MaxEntries: 3,
	}))
	for _, want := range []string{
		"Seeds 1-6 are for individual race winners",
		"Seeds 7-18 are the remaining auto-qualifiers",
		"Seeds 19-24 are the wildcard winners",
		`highlight-rows="6"`,
		`hide-columns="Over Limit"`,
	} {
		if !strings.Contains(six, want) {
			t.Errorf("the season page does not say %q", want)
		}
	}

	// A five-race season describes itself, rather than describing 2026.
	five := string(SeasonIndexMD(SeasonPage{
		Year: 2028, Races: 5, AutoQualPlaces: 3, WildcardSpots: 1, MaxEntries: 3,
	}))
	for _, want := range []string{
		"Seeds 1-5 are for individual race winners",
		"Seeds 6-15 are the remaining auto-qualifiers",
		"Seeds 16-16 are the wildcard winners",
	} {
		if !strings.Contains(five, want) {
			t.Errorf("a five-race season page does not say %q", want)
		}
	}
}

// The app never runs git, so a publish must leave a working tree somebody can
// review — including not touching files it did not change.
func TestUnchangedFilesAreNotRewritten(t *testing.T) {
	root := fakeSite(t)
	if _, err := Apply(mustPlan(t, root, sampleRace(2027, 1))); err != nil {
		t.Fatal(err)
	}

	standings := filepath.Join(root, "content", "races", "2027", "race-1", "standings.csv")
	before, err := os.Stat(standings)
	if err != nil {
		t.Fatal(err)
	}

	// Change only the heats, then publish again.
	second := sampleRace(2027, 1)
	second.Heats[0].Time = 2.399
	written, err := Apply(mustPlan(t, root, second))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range written {
		if strings.HasSuffix(w, "standings.csv") {
			t.Error("standings.csv was rewritten though nothing in it changed")
		}
	}

	after, _ := os.Stat(standings)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("an unchanged file's timestamp moved")
	}
}

func mustPlan(t *testing.T, root string, r RaceFiles) Plan {
	t.Helper()
	p, err := PlanRace(root, r)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The committed files are a museum of exporters: BOMs, CRLF, a missing final
// newline. A diff that reported all of that as a rewrite would bury the one row
// that actually changed.
func TestLineEndingsAndBOMsDoNotDrownTheRealChange(t *testing.T) {
	root := fakeSite(t)
	r := sampleRace(2027, 1)
	if _, err := Apply(mustPlan(t, root, r)); err != nil {
		t.Fatal(err)
	}

	// Re-save the heats file the way the old exporter did: BOM, CRLF, no final
	// newline. Nothing about the table has changed.
	path := filepath.Join(root, "content", "races", "2027", "race-1", "heats.csv")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := append([]byte{0xEF, 0xBB, 0xBF},
		[]byte(strings.ReplaceAll(strings.TrimSuffix(string(body), "\n"), "\n", "\r\n"))...)
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatal(err)
	}

	// One corrected time on top of that.
	changed := sampleRace(2027, 1)
	changed.Heats[1].Time = 2.498

	f := find(t, mustPlan(t, root, changed), "heats.csv")
	if f.Added != 1 || f.Removed != 1 {
		t.Errorf("one corrected row over a legacy file shows as +%d/-%d, want +1/-1",
			f.Added, f.Removed)
	}

	// And a file that differs only by a BOM is still rewritten, because the BOM
	// is what breaks the site.
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	same := find(t, mustPlan(t, root, r), "heats.csv")
	if same.Status != StatusChanged {
		t.Errorf("a file carrying a BOM is %q, want it rewritten", same.Status)
	}
	if !strings.Contains(same.Note, "byte-order mark") {
		t.Errorf("nothing explains the rewrite: %q", same.Note)
	}

	if _, err := Apply(mustPlan(t, root, r)); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if strings.HasPrefix(string(after), "\ufeff") {
		t.Error("the byte-order mark survived the rewrite")
	}
}
