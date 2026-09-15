package season

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

// These tests rebuild the club's own published 2026 season standings from its
// own published race results.
//
// 2026 is a five-race snapshot: race 6 had not been run when the site was last
// updated, which is why the qualifier list is 15 long rather than 18. That does
// not weaken the fixture — it exercises the case where the season is only part
// way through, which is what the screen will show on most race nights.
//
// The published numbers were produced by the old tracker, which counted the
// CONTROL pace car in the field size. Reproducing them therefore means running
// with CountControl on. The corrected default is asserted separately, against
// the difference it is supposed to make.

const (
	wildcardFixture   = "../../testdata/2026-wildcard.csv"
	qualifiersFixture = "../../testdata/2026-qualifiers.csv"
)

// clubRules are the club's settings for 2026.
var clubRules = Rules{AutoQualPlaces: 3, MaxEntries: 3}

// loadSeason reads the five published race standings as Finishes.
//
// Racer identity comes from the driver name here because that is all the
// published file carries. The application keys on a racer row instead, which is
// the point of that table: the old tracker keyed season points on this same
// name text, so a typo silently split a racer's season in two.
func loadSeason(t *testing.T) []Finish {
	t.Helper()
	racerIDs := map[string]int64{}
	var out []Finish

	for race := 1; race <= 5; race++ {
		path := fmt.Sprintf("../../testdata/2026-race-%d-standings.csv", race)
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		rows, err := csv.NewReader(f).ReadAll()
		f.Close()
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if len(rows) < 2 {
			t.Fatalf("%s has no rows", path)
		}

		// The DerbyNet export carries a UTF-8 BOM, and races 2, 3 and 5 were
		// published without the Heats column, so the headers are read rather
		// than assumed.
		header := map[string]int{}
		for i, name := range rows[0] {
			header[strings.TrimPrefix(strings.TrimSpace(name), "\ufeff")] = i
		}
		col := func(row []string, name string) string {
			i, ok := header[name]
			if !ok || i >= len(row) {
				return ""
			}
			return strings.TrimSpace(row[i])
		}

		for _, row := range rows[1:] {
			name := col(row, "Name")
			if _, ok := racerIDs[name]; !ok {
				racerIDs[name] = int64(len(racerIDs) + 1)
			}
			carName := col(row, "Car Name")
			out = append(out, Finish{
				// Car numbers repeat between races, so the entry id is
				// synthesised the way the database would assign one.
				EntryID:    int64(len(out) + 1),
				RacerID:    racerIDs[name],
				RaceID:     int64(race),
				RaceNumber: race,
				Driver:     name,
				CarName:    carName,
				Place:      atoi(t, col(row, "Place")),
				Average:    atof(t, col(row, "Average")),
				IsControl:  strings.EqualFold(carName, "CONTROL"),
			})
		}
	}
	return out
}

// wildcardFrom runs the whole season through the points and ranking rules.
func wildcardFrom(t *testing.T, finishes []Finish, r Rules) []WildcardRow {
	t.Helper()

	earned := map[int64]int{}
	names := map[int64]string{}
	// A racer exists for the season only if they entered a car that is not the
	// pace car. Without this the CONTROL car's driver appears as a competitor.
	competed := map[int64]bool{}

	for race := 1; race <= 5; race++ {
		var perRace []Finish
		for _, f := range finishes {
			if f.RaceNumber == race {
				perRace = append(perRace, f)
			}
		}
		for _, a := range RacePoints(perRace, r) {
			earned[a.RacerID] += a.Points
		}
		for _, f := range perRace {
			names[f.RacerID] = f.Driver
			if !f.IsControl {
				competed[f.RacerID] = true
			}
		}
	}

	slots := map[int64]int{}
	for _, s := range Qualifiers(finishes, r) {
		slots[s.RacerID]++
	}

	var racers []RacerPoints
	for id, name := range names {
		if !competed[id] {
			continue
		}
		racers = append(racers, RacerPoints{
			RacerID: id, Name: name, Earned: earned[id], Slots: slots[id],
		})
	}
	return Wildcard(racers, r)
}

// The published standings are what the club raced on. If this stops matching,
// the points rule has drifted from the one the members know.
func TestWildcardStandingsMatchThePublishedFile(t *testing.T) {
	r := clubRules
	r.CountControl = true // the behaviour that produced the published file

	got := wildcardFrom(t, loadSeason(t), r)
	want := readCSV(t, wildcardFixture)

	// The published file lists "Derby Ales" — the pace car's driver — as a
	// racer on zero points. There is no such person, so that row is expected to
	// be missing and every rank after it shifts up by one.
	var filtered [][]string
	for _, row := range want[1:] {
		if row[1] == "Derby Ales" {
			continue
		}
		filtered = append(filtered, row)
	}
	if len(filtered) == len(want)-1 {
		t.Fatal("the fixture no longer contains the pace car row this test accounts for")
	}

	if len(got) != len(filtered) {
		t.Fatalf("produced %d racers, the published file has %d (after removing the pace car)",
			len(got), len(filtered))
	}
	for i, row := range filtered {
		g := got[i]
		if g.Name != row[1] {
			t.Errorf("rank %d: %s, published %s", i+1, g.Name, row[1])
			continue
		}
		if FormatPoints(g) != row[2] {
			t.Errorf("%s: %s points, published %s", g.Name, FormatPoints(g), row[2])
		}
	}
}

// The correction: the pace car is not a competitor, so it must not inflate the
// field size. A racer loses exactly one point per race they scored in, which is
// small, consistent, and exactly what the club's published rule says should
// have happened all along.
func TestExcludingTheControlCarCostsOnePointPerScoringRace(t *testing.T) {
	finishes := loadSeason(t)

	old := clubRules
	old.CountControl = true
	before := wildcardFrom(t, finishes, old)
	after := wildcardFrom(t, finishes, clubRules)

	// How many races each racer actually scored in, which is the expected drop.
	scoring := map[int64]int{}
	for race := 1; race <= 5; race++ {
		var perRace []Finish
		for _, f := range finishes {
			if f.RaceNumber == race {
				perRace = append(perRace, f)
			}
		}
		for _, a := range RacePoints(perRace, clubRules) {
			if a.Points > 0 {
				scoring[a.RacerID]++
			}
		}
	}

	byID := map[int64]WildcardRow{}
	for _, row := range after {
		byID[row.RacerID] = row
	}
	checked := 0
	for _, was := range before {
		now, ok := byID[was.RacerID]
		if !ok {
			t.Fatalf("%s disappeared from the standings", was.Name)
		}
		if want := was.Total - scoring[was.RacerID]; now.Total != want {
			t.Errorf("%s: %d points, want %d (was %d, scored in %d races)",
				was.Name, now.Total, want, was.Total, scoring[was.RacerID])
		}
		if scoring[was.RacerID] > 0 {
			checked++
		}
	}
	if checked < 10 {
		t.Fatalf("only %d racers scored; the fixture is not exercising this", checked)
	}
}

// The auto-qualifier list is the seeding order for the championship, so its
// order is not cosmetic.
//
// The published 2026 file was made by hand before the club's pass-down rule was
// applied, and shows its working: Chris Bryan holds four places, flagged over
// the limit, and Greg Thrift — 4th in race 5 — is listed as a "9*" standby
// beside them. Chris already held three places when race 5 was run, so his
// second car to finish in the top three there (Sprocket, 3rd) cannot take a
// place and it passes to 4th. The expected list is therefore the published one
// with that standby resolved: Greg in at seed 9, Sprocket out, Chris on three.
func TestAutoQualifiersMatchThePublishedSeeding(t *testing.T) {
	got := Qualifiers(loadSeason(t), clubRules)
	want := readCSV(t, qualifiersFixture)

	var rows [][]string
	var standby []string
	for _, row := range want[1:] {
		if strings.HasSuffix(row[0], "*") {
			standby = row
			continue
		}
		rows = append(rows, row)
	}
	if standby == nil {
		t.Fatal("the fixture no longer contains the 9* standby row this test resolves")
	}

	// Resolve it: the standby takes the place of the over-limit racer's last
	// qualifying finish in the standby's race.
	replaced := -1
	for i, row := range rows {
		if row[7] == "YES" && row[3] == standby[3] {
			replaced = i
		}
	}
	if replaced < 0 {
		t.Fatal("the fixture has no over-limit place in the standby's race")
	}
	capped := rows[replaced][1]
	standby[0] = rows[replaced][0]
	rows[replaced] = standby
	for _, row := range rows {
		if row[1] == capped {
			row[6] = strconv.Itoa(clubRules.MaxEntries)
		}
		row[7] = ""
	}

	if len(got) != len(rows) {
		t.Fatalf("produced %d qualifiers, want %d", len(got), len(rows))
	}
	for i, row := range rows {
		s := got[i]
		seed := atoi(t, row[0])
		switch {
		case s.Seed != seed:
			t.Errorf("row %d: seed %d, want %d", i+1, s.Seed, seed)
		case s.Driver != row[1]:
			t.Errorf("seed %d: %s, want %s", seed, s.Driver, row[1])
		case s.CarName != row[2]:
			t.Errorf("seed %d: car %q, want %q", seed, s.CarName, row[2])
		case fmt.Sprintf("Race %d", s.RaceNumber) != row[3]:
			t.Errorf("seed %d: %s, want %s", seed, fmt.Sprintf("Race %d", s.RaceNumber), row[3])
		case s.Place != atoi(t, row[4]):
			t.Errorf("seed %d: finished %d, want %s", seed, s.Place, row[4])
		case s.Entries != atoi(t, row[6]):
			t.Errorf("seed %d: %d entries, want %s", seed, s.Entries, row[6])
		}
	}

	// And the place records why it passed down, for the season screen.
	for _, s := range got {
		if s.Driver != standby[1] {
			continue
		}
		if len(s.PassedOver) != 1 || s.PassedOver[0].Driver != capped || s.PassedOver[0].Reason != PassAtCap {
			t.Errorf("%s's place does not record passing over %s at the cap: %+v", s.Driver, capped, s.PassedOver)
		}
	}
}

// A car that qualified is not allowed to race again. If one does anyway, it
// cannot take a second place: the place goes to the next car.
func TestACarThatAlreadyQualifiedCannotQualifyAgain(t *testing.T) {
	r := Rules{AutoQualPlaces: 1, MaxEntries: 3}
	finishes := []Finish{
		{EntryID: 1, RacerID: 1, RaceNumber: 1, Driver: "A", CarName: "Rocket", Place: 1, Average: 2.3},
		{EntryID: 2, RacerID: 2, RaceNumber: 1, Driver: "B", CarName: "Slug", Place: 2, Average: 2.4},
		{EntryID: 3, RacerID: 1, RaceNumber: 2, Driver: "A", CarName: "  rocket ", Place: 1, Average: 2.3},
		{EntryID: 4, RacerID: 2, RaceNumber: 2, Driver: "B", CarName: "Slug", Place: 2, Average: 2.4},
	}
	got := Qualifiers(finishes, r)
	if len(got) != 2 {
		t.Fatalf("%d places, want one per race", len(got))
	}
	for _, s := range got {
		if s.RaceNumber == 2 {
			if s.EntryID != 4 {
				t.Errorf("race 2's place went to entry %d, want the runner-up", s.EntryID)
			}
			if len(s.PassedOver) != 1 || s.PassedOver[0].Reason != PassAlreadyQualified {
				t.Errorf("race 2's place does not record why it passed down: %+v", s.PassedOver)
			}
			if !s.RaceTop {
				t.Error("the best qualifier from race 2 is not seeded as that race's top car")
			}
		}
	}
}

// A racer on the cap still races and still finishes where they finish; only the
// place moves. And they are never shown over the cap, because they never are.
func TestARacerAtTheCapPassesTheirPlaceDown(t *testing.T) {
	r := Rules{AutoQualPlaces: 1, MaxEntries: 2}
	var finishes []Finish
	for race := 1; race <= 3; race++ {
		finishes = append(finishes,
			Finish{EntryID: int64(race*10 + 1), RacerID: 1, RaceNumber: race, Driver: "Fast",
				CarName: fmt.Sprintf("Car %d", race), Place: 1, Average: 2.3},
			Finish{EntryID: int64(race*10 + 2), RacerID: 2, RaceNumber: race, Driver: "Next",
				CarName: fmt.Sprintf("Other %d", race), Place: 2, Average: 2.5})
	}
	got := Qualifiers(finishes, r)
	fast := 0
	for _, s := range got {
		if s.RacerID == 1 {
			fast++
			if s.Entries != 2 {
				t.Errorf("Fast holds %d entries, want the cap of 2", s.Entries)
			}
		}
		if s.RaceNumber == 3 && s.RacerID != 2 {
			t.Errorf("race 3's place went to racer %d, want the runner-up", s.RacerID)
		}
	}
	if fast != 2 {
		t.Errorf("Fast has %d places, want 2", fast)
	}
}

// --- fixture helpers ----------------------------------------------------------

func readCSV(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return rows
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		t.Fatalf("bad integer %q: %v", s, err)
	}
	return n
}

func atof(t *testing.T, s string) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		t.Fatalf("bad number %q: %v", s, err)
	}
	return f
}
