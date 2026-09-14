package publish

import (
	"bytes"
	"encoding/csv"
	"os"
	"strconv"
	"strings"
	"testing"
)

// These rebuild the club's committed files from their own contents and compare
// byte for byte.
//
// Reading a file and writing it back may look circular, but it is not: the
// committed files carry a UTF-8 BOM, CRLF line endings and in one case no
// trailing newline, and they were hand-trimmed to three decimal places from an
// exporter that wrote four. What is being asserted is that this package
// produces the same *table* with none of that, which is what the site's
// shortcode needs and what every future file will look like.

const (
	heatsFixture     = "../../testdata/2026-race-4-heats.csv"
	standingsFixture = "../../testdata/2026-race-4-standings.csv"
	qualFixture      = "../../testdata/2026-qualifiers.csv"
	wildcardFixture  = "../../testdata/2026-wildcard.csv"
)

// readFixture returns a published file as records, with the BOM stripped.
func readFixture(t *testing.T, path string) [][]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	raw = bytes.TrimPrefix(raw, []byte("\ufeff"))
	rows, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return rows
}

// compare asserts that generated output matches a fixture cell for cell, and
// says exactly where it does not.
func compare(t *testing.T, got []byte, want [][]string, label string) {
	t.Helper()
	rows, err := csv.NewReader(bytes.NewReader(got)).ReadAll()
	if err != nil {
		t.Fatalf("%s: the generated file does not parse: %v", label, err)
	}
	if len(rows) != len(want) {
		t.Fatalf("%s: %d rows, the published file has %d", label, len(rows), len(want))
	}
	for i := range want {
		if len(rows[i]) != len(want[i]) {
			t.Errorf("%s row %d: %d columns, want %d\n got %q\nwant %q",
				label, i, len(rows[i]), len(want[i]), rows[i], want[i])
			continue
		}
		for j := range want[i] {
			if rows[i][j] != want[i][j] {
				t.Errorf("%s row %d column %q: got %q, published %q",
					label, i, want[0][j], rows[i][j], want[i][j])
			}
		}
	}
}

func atof(t *testing.T, s string) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		t.Fatalf("bad number %q: %v", s, err)
	}
	return f
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		t.Fatalf("bad integer %q: %v", s, err)
	}
	return n
}

// 99 lane results across 25 heats, with the scale speeds the site shows.
func TestHeatsFileReproducesThePublishedOne(t *testing.T) {
	want := readFixture(t, heatsFixture)

	var rows []HeatRow
	for _, r := range want[1:] {
		rows = append(rows, HeatRow{
			Heat: atoi(t, r[0]), Lane: atoi(t, r[1]),
			First: r[2], Last: r[3],
			CarNumber: atoi(t, r[4]), CarName: r[5],
			Time: atof(t, r[6]), Place: atoi(t, r[8]),
		})
	}

	// 28 ft at 1:25 — the club's track. The MPH column is recomputed here
	// rather than copied, so it is genuinely being checked.
	got, err := HeatsCSV(rows, 28, 25)
	if err != nil {
		t.Fatal(err)
	}
	compare(t, got, want, "heats.csv")
}

func TestStandingsFileReproducesThePublishedOne(t *testing.T) {
	want := readFixture(t, standingsFixture)

	var rows []StandingRow
	for _, r := range want[1:] {
		rows = append(rows, StandingRow{
			Place: atoi(t, r[0]), CarNumber: atoi(t, r[1]),
			Name: r[2], CarName: r[3], Heats: atoi(t, r[4]),
			Average: atof(t, r[5]), Best: atof(t, r[6]), Worst: atof(t, r[7]),
		})
	}
	got, err := StandingsCSV(rows)
	if err != nil {
		t.Fatal(err)
	}
	compare(t, got, want, "standings.csv")
}

func TestQualifiersFileReproducesThePublishedOne(t *testing.T) {
	want := readFixture(t, qualFixture)

	// The published file carries one hand-added row seeded "9*", a standby for
	// an over-limit racer. The app records substitutions properly, so it has no
	// counterpart here.
	var fixture [][]string
	fixture = append(fixture, want[0])
	var rows []QualifierRow
	for _, r := range want[1:] {
		if strings.HasSuffix(r[0], "*") {
			continue
		}
		fixture = append(fixture, r)
		rows = append(rows, QualifierRow{
			Seed: atoi(t, r[0]), Driver: r[1], CarName: r[2],
			Race:   atoi(t, strings.TrimPrefix(r[3], "Race ")),
			Finish: atoi(t, r[4]), Average: atof(t, r[5]),
			Entries: atoi(t, r[6]), OverLimit: r[7] == "YES",
		})
	}
	if len(fixture) == len(want) {
		t.Fatal("the fixture no longer contains the 9* standby row this test accounts for")
	}

	got, err := QualifiersCSV(rows)
	if err != nil {
		t.Fatal(err)
	}
	compare(t, got, fixture, "qualifiers.csv")
}

func TestWildcardFileReproducesThePublishedOne(t *testing.T) {
	want := readFixture(t, wildcardFixture)

	var rows []WildcardRow
	for _, r := range want[1:] {
		rows = append(rows, WildcardRow{Rank: atoi(t, r[0]), Name: r[1], Points: r[2]})
	}
	got, err := WildcardCSV(rows)
	if err != nil {
		t.Fatal(err)
	}
	compare(t, got, want, "wildcard.csv")

	// The literal string a maxed-out racer is published with has to survive.
	if !bytes.Contains(got, []byte("Max Entries Reached")) {
		t.Error("\"Max Entries Reached\" did not survive into the file")
	}
}

// The site's shortcode matches column names by string equality, so a BOM in the
// first header cell silently breaks hide-columns and highlight-column — both of
// which the club's season page uses on this very file.
func TestFilesAreWrittenWithoutABOM(t *testing.T) {
	files := map[string][]byte{}

	heats, _ := HeatsCSV([]HeatRow{{Heat: 1, Lane: 1, First: "A", Last: "B", CarNumber: 7, CarName: "C", Time: 2.5, Place: 1}}, 28, 25)
	files["heats.csv"] = heats
	standings, _ := StandingsCSV([]StandingRow{{Place: 1, CarNumber: 7, Name: "A B", CarName: "C", Heats: 4, Average: 2.5, Best: 2.4, Worst: 2.6}})
	files["standings.csv"] = standings
	awards, _ := AwardsCSV([]AwardRow{{Award: "1st", First: "A", Last: "B", CarNumber: 7, CarName: "C"}})
	files["awards.csv"] = awards
	quals, _ := QualifiersCSV([]QualifierRow{{Seed: 1, Driver: "A B", CarName: "C", Race: 1, Finish: 1, Average: 2.5, Entries: 1}})
	files["qualifiers.csv"] = quals
	wild, _ := WildcardCSV([]WildcardRow{{Rank: 1, Name: "A B", Points: "40"}})
	files["wildcard.csv"] = wild

	for name, body := range files {
		if bytes.HasPrefix(body, []byte("\ufeff")) {
			t.Errorf("%s starts with a BOM", name)
		}
		if bytes.Contains(body, []byte("\r")) {
			t.Errorf("%s contains a carriage return", name)
		}
		if !bytes.HasSuffix(body, []byte("\n")) {
			t.Errorf("%s does not end with a newline", name)
		}
	}
}

// The five headers the site's pages are written against. Renaming any of them
// breaks a column that a shortcode matches by name.
func TestHeadersAreExactlyWhatTheSiteExpects(t *testing.T) {
	cases := []struct {
		name, want string
		body       func() ([]byte, error)
	}{
		{"heats.csv", "Heat,Lane,FirstName,LastName,CarNumber,CarName,FinishTime,Scale MPH,FinishPlace",
			func() ([]byte, error) { return HeatsCSV(nil, 28, 25) }},
		{"standings.csv", "Place,Car Number,Name,Car Name,Heats,Average,Best,Worst",
			func() ([]byte, error) { return StandingsCSV(nil) }},
		{"awards.csv", "Award Name,First Name,Last Name,Car Number,Car Name",
			func() ([]byte, error) { return AwardsCSV(nil) }},
		{"qualifiers.csv", "Current Seed,Driver,Car Name,Race,Race Finish,Avg Time,Total Entries,Over Limit",
			func() ([]byte, error) { return QualifiersCSV(nil) }},
		{"wildcard.csv", "Rank,Name,Total Points",
			func() ([]byte, error) { return WildcardCSV(nil) }},
	}
	for _, c := range cases {
		body, err := c.body()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got := strings.SplitN(string(body), "\n", 2)[0]
		if got != c.want {
			t.Errorf("%s header:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// A car name with a comma or a quote in it has to survive. The club has had
// "I Wish a Bitch Would..." already; a comma is a matter of time.
func TestAwkwardCarNamesAreQuotedProperly(t *testing.T) {
	got, err := StandingsCSV([]StandingRow{
		{Place: 1, CarNumber: 7, Name: "A B", CarName: `Comma, Chameleon`, Heats: 4, Average: 2.5, Best: 2.4, Worst: 2.6},
		{Place: 2, CarNumber: 8, Name: "C D", CarName: `The "Fast" One`, Heats: 4, Average: 2.6, Best: 2.5, Worst: 2.7},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(bytes.NewReader(got)).ReadAll()
	if err != nil {
		t.Fatalf("the file does not parse: %v", err)
	}
	if rows[1][3] != "Comma, Chameleon" {
		t.Errorf("a comma in a car name did not survive: %q", rows[1][3])
	}
	if rows[2][3] != `The "Fast" One` {
		t.Errorf("quotes in a car name did not survive: %q", rows[2][3])
	}
}
