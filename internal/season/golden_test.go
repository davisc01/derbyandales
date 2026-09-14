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
	for _, s := range Qualifiers(finishes, nil, r) {
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
func TestAutoQualifiersMatchThePublishedSeeding(t *testing.T) {
	got := Qualifiers(loadSeason(t), nil, clubRules)
	want := readCSV(t, qualifiersFixture)

	// The published file carries one extra hand-added row, seeded "9*": the
	// standby for an over-limit racer, shown alongside the slot rather than
	// replacing it, because mid-season the substitution was not yet committed.
	// The app records substitutions properly, so that row has no counterpart
	// here — see TestSubstitutionReplacesAnOverLimitSlot.
	var rows [][]string
	for _, row := range want[1:] {
		if strings.HasSuffix(row[0], "*") {
			continue
		}
		rows = append(rows, row)
	}
	if len(rows) == len(want)-1 {
		t.Fatal("the fixture no longer contains the 9* standby row this test accounts for")
	}

	if len(got) != len(rows) {
		t.Fatalf("produced %d qualifiers, published %d", len(got), len(rows))
	}
	for i, row := range rows {
		s := got[i]
		seed := atoi(t, row[0])
		switch {
		case s.Seed != seed:
			t.Errorf("row %d: seed %d, published %d", i+1, s.Seed, seed)
		case s.Driver != row[1]:
			t.Errorf("seed %d: %s, published %s", seed, s.Driver, row[1])
		case s.CarName != row[2]:
			t.Errorf("seed %d: car %q, published %q", seed, s.CarName, row[2])
		case fmt.Sprintf("Race %d", s.RaceNumber) != row[3]:
			t.Errorf("seed %d: %s, published %s", seed, fmt.Sprintf("Race %d", s.RaceNumber), row[3])
		case s.Place != atoi(t, row[4]):
			t.Errorf("seed %d: finished %d, published %s", seed, s.Place, row[4])
		case s.Entries != atoi(t, row[6]):
			t.Errorf("seed %d: %d entries, published %s", seed, s.Entries, row[6])
		case s.OverLimit != (row[7] == "YES"):
			t.Errorf("seed %d: over limit %v, published %q", seed, s.OverLimit, row[7])
		}
	}
}

// The club's 2026 snapshot has one racer holding four of fifteen slots, which
// is the situation substitution exists for.
func TestSubstitutionReplacesAnOverLimitSlot(t *testing.T) {
	finishes := loadSeason(t)
	before := Qualifiers(finishes, nil, clubRules)

	// Find the lowest-seeded slot held by an over-limit racer: the one the
	// coordinator would give up.
	var give Slot
	for _, s := range before {
		if s.OverLimit {
			give = s
		}
	}
	if give.EntryID == 0 {
		t.Fatal("the fixture no longer contains an over-limit racer")
	}

	candidates := SubstituteCandidates(finishes, give.RaceID, nil, clubRules)
	if len(candidates) == 0 {
		t.Fatal("no substitute available from that race")
	}
	take := candidates[0]
	if take.Place <= clubRules.AutoQualPlaces {
		t.Errorf("candidate finished %d, which already qualifies", take.Place)
	}

	after := Qualifiers(finishes, []Substitution{{give.EntryID, take.EntryID}}, clubRules)
	if len(after) != len(before) {
		t.Fatalf("field changed size: %d slots, was %d", len(after), len(before))
	}

	var found *Slot
	for i := range after {
		if after[i].EntryID == give.EntryID {
			t.Errorf("the substituted-out entry is still seeded at %d", after[i].Seed)
		}
		if after[i].EntryID == take.EntryID {
			found = &after[i]
		}
	}
	if found == nil {
		t.Fatal("the substitute is not in the field")
	}
	if found.SubstitutedFor == nil || found.SubstitutedFor.EntryID != give.EntryID {
		t.Error("the slot does not record who it replaced")
	}
	for _, s := range after {
		if s.RacerID == give.RacerID && s.OverLimit {
			t.Errorf("%s is still over the limit with %d entries", s.Driver, s.Entries)
		}
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
