package history

import (
	"os"
	"path/filepath"
	"testing"
)

// The club records are built from six years of published race nights, written
// by four different exporters. These read real ones from testdata.

func readRace(t *testing.T, dir string) []RaceCar {
	t.Helper()
	base := filepath.Join("..", "..", "testdata", "races", dir)
	heats, err := os.ReadFile(filepath.Join(base, "heats.csv"))
	if err != nil {
		t.Fatal(err)
	}
	standings, err := os.ReadFile(filepath.Join(base, "standings.csv"))
	if err != nil {
		t.Fatal(err)
	}
	cars, err := ParseRace(heats, standings)
	if err != nil {
		t.Fatalf("ParseRace(%s): %v", dir, err)
	}
	return cars
}

func carNumbered(t *testing.T, cars []RaceCar, n int) RaceCar {
	t.Helper()
	for _, c := range cars {
		if c.CarNumber == n {
			return c
		}
	}
	t.Fatalf("car %d is missing", n)
	return RaceCar{}
}

// Every car runs each lane once. If a year's layout were misread, runs would
// land on the wrong car or nowhere, and this is where it would show.
func TestEveryArchivedCarHasItsFourRuns(t *testing.T) {
	for _, dir := range []string{"2019/race-1", "2021/race-4", "2023/race-1",
		"2025/race-1", "2025/championship", "2026/race-4"} {
		t.Run(dir, func(t *testing.T) {
			for _, c := range readRace(t, dir) {
				if len(c.Runs) != 4 {
					t.Errorf("car %d has %d runs", c.CarNumber, len(c.Runs))
				}
				lanes := map[int]bool{}
				for _, r := range c.Runs {
					lanes[r.Lane] = true
				}
				if len(lanes) != 4 {
					t.Errorf("car %d ran lanes %v", c.CarNumber, lanes)
				}
			}
		})
	}
}

// 2026 race 4: two cars raced and were left out of the standings. One of them
// ran the fastest average of the night, so reading it as a normal car would
// hand it a record it was ruled out of.
func TestACarMissingFromTheStandingsIsExcluded(t *testing.T) {
	cars := readRace(t, "2026/race-4")
	for _, n := range []int{89, 901} {
		if c := carNumbered(t, cars, n); !c.Excluded || c.Place != 0 {
			t.Errorf("car %d: excluded=%v place=%d", n, c.Excluded, c.Place)
		}
	}
	if c := carNumbered(t, cars, 73); c.Excluded || c.Place != 1 {
		t.Errorf("the winner of record came back excluded=%v place=%d", c.Excluded, c.Place)
	}
	if c := carNumbered(t, cars, 1); !c.Control {
		t.Error("the CONTROL car was not recognised as the pace car")
	}
}

// 2023 race 1 marked a disqualified car in its name, published no car numbers
// in the standings, and had racers with two cars each — so the only way to say
// which of Chris Bryan's cars was ruled out is the average each one ran.
func TestADisqualifiedCarIsFoundByItsAverage(t *testing.T) {
	cars := readRace(t, "2023/race-1")
	var dq, kept []RaceCar
	for _, c := range cars {
		if c.First == "Chris" && c.Last == "Bryan" {
			if c.Excluded {
				dq = append(dq, c)
			} else {
				kept = append(kept, c)
			}
		}
	}
	if len(dq) != 1 || len(kept) != 1 {
		t.Fatalf("Chris Bryan: %d excluded, %d kept; want one of each", len(dq), len(kept))
	}
	if dq[0].CarName != "Danger Danger" {
		t.Errorf("the excluded car is %q, want Danger Danger with the DQ mark removed", dq[0].CarName)
	}
	if kept[0].CarName != "Nuttin'" || kept[0].Place != 18 {
		t.Errorf("the kept car is %q at %d", kept[0].CarName, kept[0].Place)
	}
	for _, c := range cars {
		if c.CarName == "" {
			t.Errorf("car %d has no name, though the standings give one", c.CarNumber)
		}
	}
}

// The heat files have typos in them; the standings are the curated file.
func TestTheStandingsSpellingWins(t *testing.T) {
	c := carNumbered(t, readRace(t, "2025/race-1"), 123)
	if c.First != "Amelia" {
		t.Errorf("car 123 is driven by %q, want the standings' spelling", c.First)
	}
}

// 2021 wrote a non-finish as 9.9999. It must not become a different kind of
// slow from everybody else's 9.999.
func TestANonFinishIsAlwaysNineNineNineNine(t *testing.T) {
	for _, c := range readRace(t, "2021/race-4") {
		for _, r := range c.Runs {
			if r.Time > 9.999 {
				t.Errorf("car %d heat %d recorded %v", c.CarNumber, r.Heat, r.Time)
			}
		}
	}
}

func TestOnlyTheWholeNameIsThePaceCar(t *testing.T) {
	cases := []struct {
		first, last, car string
		want             bool
	}{
		{"Derby", "Ales", "CONTROL", true},
		{"Derby and", "Alers", "Control", true},
		{"Derby", "Ale", "", true},
		{"Justin", "Palmer", "The Other Controller", false},
		{"Duncan", "Breland", "Loose Moose", false},
	}
	for _, c := range cases {
		if got := IsPaceCar(c.first, c.last, c.car); got != c.want {
			t.Errorf("IsPaceCar(%q %q, %q) = %v", c.first, c.last, c.car, got)
		}
	}
}
