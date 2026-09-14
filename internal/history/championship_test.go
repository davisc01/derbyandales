package history

import (
	"fmt"
	"os"
	"testing"
)

// The archive spans seven years and at least four exporters. These read the
// club's own published championship files, exactly as committed.

var years = []int{2019, 2021, 2022, 2023, 2024, 2025}

func load(t *testing.T, year int) []Car {
	t.Helper()
	path := fmt.Sprintf("../../testdata/championships/%d-standings.csv", year)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	cars, err := ParseChampionship(year, body)
	if err != nil {
		t.Fatalf("%d: %v", year, err)
	}
	return cars
}

// Every year has to parse, and every car has to come out with a name and a
// driver — those two are what the eligibility rule is checked against.
func TestEveryPublishedChampionshipParses(t *testing.T) {
	total := 0
	for _, year := range years {
		cars := load(t, year)
		if len(cars) < 15 {
			t.Errorf("%d: only %d cars", year, len(cars))
		}
		for _, c := range cars {
			if c.CarName == "" {
				t.Errorf("%d: a car has no name", year)
			}
			if c.Last == "" {
				t.Errorf("%d: %q has no driver", year, c.CarName)
			}
			if c.Year != year {
				t.Errorf("car recorded under %d, want %d", c.Year, year)
			}
		}
		total += len(cars)
		t.Logf("%d: %d cars", year, len(cars))
	}
	// Six championships of roughly twenty-odd cars.
	if total < 110 {
		t.Errorf("%d cars across the whole archive, expected well over a hundred", total)
	}
}

// 2019's export wrapped two header cells across lines, so "Average\nTime" is
// one column name. Folding whitespace is what makes the rest of the row
// findable.
func TestTheYearWithAWrappedHeaderParses(t *testing.T) {
	cars := load(t, 2019)
	first := cars[0]
	if first.CarName != "Prissy" || first.Last != "Pugh" || first.First != "Ben" {
		t.Errorf("2019's first row came out as %+v", first)
	}
	if first.Place != 1 {
		t.Errorf("2019's first row placed %d", first.Place)
	}
}

// The later exports use one full-name column rather than two.
func TestAFullNameColumnIsSplit(t *testing.T) {
	cars := load(t, 2025)
	first := cars[0]
	if first.First != "Michael" || first.Last != "Sanders" {
		t.Errorf("2025's first driver came out as %q %q", first.First, first.Last)
	}
	if first.CarName != "The Pelvinator" {
		t.Errorf("2025's first car is %q", first.CarName)
	}
	if first.CarNumber != 79 {
		t.Errorf("2025's first car number is %d, want 79", first.CarNumber)
	}
}

// 2021 baked the qualifying origin into every car name — "-1" through "-4" for
// the race it qualified from, "-W" for a wildcard. That is not part of the
// name, and leaving it on would stop the same car matching years later.
func TestThe2021QualifyingSuffixIsStripped(t *testing.T) {
	cars := load(t, 2021)

	names := map[string]bool{}
	for _, c := range cars {
		names[c.CarName] = true
		if suffix.MatchString(c.CarName) {
			t.Errorf("%q still carries its qualifying suffix", c.CarName)
		}
	}
	for _, want := range []string{"Red Rocket", "Witness Me", "Dr Mantis Tobaggan"} {
		if !names[want] {
			t.Errorf("%q is not in the 2021 field", want)
		}
	}
}

// The stripping has to be narrow. A real car name that happens to contain a
// hyphen must survive it.
func TestARealHyphenatedNameSurvives(t *testing.T) {
	cars := load(t, 2024)
	found := false
	for _, c := range cars {
		if c.CarName == "Drive-By" {
			found = true
		}
	}
	if !found {
		t.Error(`the 2024 car "Drive-By" lost its hyphen`)
	}

	for in, want := range map[string]string{
		"Drive-By":             "Drive-By",
		"Red Rocket-1":         "Red Rocket",
		"Dr Mantis Tobaggan-W": "Dr Mantis Tobaggan",
		"Shitbox-w":            "Shitbox",
		"Route-66":             "Route-66", // two digits: not the suffix
		"Loose Moose":          "Loose Moose",
	} {
		if got := CleanCarName(in); got != want {
			t.Errorf("CleanCarName(%q) = %q, want %q", in, got, want)
		}
	}
}

// Cars are matched across years by name, so the comparison has to ignore the
// things people vary without meaning to.
func TestCarsAreMatchedRegardlessOfCaseAndSpacing(t *testing.T) {
	same := []string{"Loose Moose", "loose moose", "  Loose   Moose  ", "LOOSE MOOSE"}
	first := Key(same[0])
	for _, s := range same[1:] {
		if Key(s) != first {
			t.Errorf("Key(%q) = %q, want %q", s, Key(s), first)
		}
	}
	if Key("Loose Moose") == Key("Loose Goose") {
		t.Error("two different cars compare equal")
	}
}

// A file that is not a championship standings file should say so rather than
// returning nothing and looking like an empty year.
func TestSomethingElseEntirelyIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"empty":                "",
		"header only":          "Place,Car Name\n",
		"not a standings file": "Rank,Name,Total Points\n1,Chad Davis,86\n",
	} {
		if _, err := ParseChampionship(2027, []byte(body)); err == nil {
			t.Errorf("%s was accepted as a championship", name)
		}
	}
}
