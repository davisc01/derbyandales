package scoring

import (
	"encoding/csv"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// These tests run the club's own published results back through this package.
//
// testdata holds 2026 race 4 exactly as it appears on derbyandales.com: 99 lane
// results across 25 heats, and the 23-car standings DerbyNet produced from them.
// Reproducing both is the strongest available evidence that this implementation
// agrees with the one the club has been racing on.

const (
	heatsFixture     = "../../testdata/2026-race-4-heats.csv"
	standingsFixture = "../../testdata/2026-race-4-standings.csv"
	excludedFixture  = "../../testdata/2026-race-4-excluded.txt"
)

// excludedCars reads the entries the club left out of the published standings.
//
// Two of Justin Palmer's cars were excluded from 2026 race 4, which is what
// entry.excluded exists for. It is not a rounding detail: car 901 had the
// fastest average of the night, so the exclusion is what decided the winner.
func excludedCars(t *testing.T) map[int64]bool {
	t.Helper()
	raw, err := os.ReadFile(excludedFixture)
	if err != nil {
		t.Fatalf("read %s: %v", excludedFixture, err)
	}
	out := map[int64]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			t.Fatalf("bad car number %q in %s: %v", line, excludedFixture, err)
		}
		out[n] = true
	}
	return out
}

// knownPlaceDivergence lists cars whose place deliberately differs from the
// published file. See TestTiedCarsWerePublishedSeparately for the reasoning.
var knownPlaceDivergence = map[int64]bool{91: true}

// runsFromHeats reads the fixture into per-car runs, honouring exclusions.
func runsFromHeats(t *testing.T, rows [][]string, excluded map[int64]bool) map[int64][]Run {
	t.Helper()
	out := map[int64][]Run{}
	for _, row := range rows {
		car, err := strconv.Atoi(strings.TrimSpace(row[4]))
		if err != nil {
			t.Fatalf("bad car number %q", row[4])
		}
		if excluded[int64(car)] {
			continue
		}
		heat, _ := strconv.Atoi(strings.TrimSpace(row[0]))
		lane, _ := strconv.Atoi(strings.TrimSpace(row[1]))
		out[int64(car)] = append(out[int64(car)], Run{
			Heat: heat, Lane: lane, Time: atof(t, row[6]),
		})
	}
	return out
}

// readCSV reads a fixture, stripping the UTF-8 BOM the exported files carry.
func readCSV(t *testing.T, path string) ([]string, [][]string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := strings.TrimPrefix(string(raw), "\ufeff")

	records, err := csv.NewReader(strings.NewReader(text)).ReadAll()
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(records) < 2 {
		t.Fatalf("%s has no data rows", path)
	}
	return records[0], records[1:]
}

func atof(t *testing.T, s string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}

// Every scale speed the club has published for this race, recomputed.
func TestPublishedScaleMPHAllRows(t *testing.T) {
	_, rows := readCSV(t, heatsFixture)

	checked := 0
	for i, row := range rows {
		seconds := atof(t, row[6])
		wantMPH := strings.TrimSpace(row[7])

		got := FormatMPH(ScaleMPH(trackFt, scale, seconds))
		if got != wantMPH {
			t.Errorf("row %d (%s %s, %.3fs): got %s mph, published %s",
				i+2, row[2], row[3], seconds, got, wantMPH)
		}
		checked++
	}
	if checked < 90 {
		t.Fatalf("only checked %d rows; the fixture looks truncated", checked)
	}
	t.Logf("reproduced %d published scale speeds", checked)
}

// Every finish time, reformatted. This is what the publish step will write, so
// it has to match the existing files character for character.
func TestPublishedFinishTimeFormatting(t *testing.T) {
	_, rows := readCSV(t, heatsFixture)
	for i, row := range rows {
		published := strings.TrimSpace(row[6])
		if got := FormatTime(atof(t, published)); got != published {
			t.Errorf("row %d: FormatTime round-trip gave %q, published %q", i+2, got, published)
		}
	}
}

// Per-heat placings, recomputed from the times alone.
func TestPublishedHeatPlacings(t *testing.T) {
	_, rows := readCSV(t, heatsFixture)

	type laneRow struct {
		lane  int
		time  float64
		place int
	}
	heats := map[int][]laneRow{}
	for _, row := range rows {
		heat, _ := strconv.Atoi(strings.TrimSpace(row[0]))
		lane, _ := strconv.Atoi(strings.TrimSpace(row[1]))
		place, _ := strconv.Atoi(strings.TrimSpace(row[8]))
		heats[heat] = append(heats[heat], laneRow{lane, atof(t, row[6]), place})
	}

	for heat, lanes := range heats {
		times := make(map[int]float64, len(lanes))
		for _, l := range lanes {
			times[l.lane] = l.time
		}
		got := PlaceInHeat(times)
		for _, l := range lanes {
			if got[l.lane] != l.place {
				t.Errorf("heat %d lane %d: computed place %d, published %d",
					heat, l.lane, got[l.lane], l.place)
			}
		}
	}
}

// The whole race, scored end to end: 99 lane times in, the published standings
// out. This exercises drop-slowest, averaging, ordering and placing together.
func TestPublishedStandingsReproduce(t *testing.T) {
	_, heatRows := readCSV(t, heatsFixture)
	_, standingRows := readCSV(t, standingsFixture)

	// Car number is the identity that appears in both files.
	runsByCar := runsFromHeats(t, heatRows, excludedCars(t))

	got := Standings(runsByCar)
	byCar := make(map[int64]Result, len(got))
	for _, r := range got {
		byCar[r.EntryID] = r
	}

	if len(got) != len(standingRows) {
		t.Errorf("scored %d cars, published standings list %d", len(got), len(standingRows))
	}

	for i, row := range standingRows {
		wantPlace, _ := strconv.Atoi(strings.TrimSpace(row[0]))
		car, _ := strconv.Atoi(strings.TrimSpace(row[1]))
		name := strings.TrimSpace(row[2])
		wantHeats, _ := strconv.Atoi(strings.TrimSpace(row[4]))
		wantAvg := strings.TrimSpace(row[5])
		wantBest := atof(t, row[6])
		wantWorst := atof(t, row[7])

		r, ok := byCar[int64(car)]
		if !ok {
			t.Errorf("car %d (%s) is in the published standings but was not scored", car, name)
			continue
		}

		if r.Place != wantPlace && !knownPlaceDivergence[int64(car)] {
			t.Errorf("row %d: car %d (%s) placed %d, published %d",
				i+2, car, name, r.Place, wantPlace)
		}
		if r.Heats != wantHeats {
			t.Errorf("car %d (%s): %d heats, published %d", car, name, r.Heats, wantHeats)
		}
		if got := FormatAverage(r.Average); got != wantAvg {
			t.Errorf("car %d (%s): average %s, published %s", car, name, got, wantAvg)
		}
		if math.Abs(r.Best-wantBest) > 1e-9 {
			t.Errorf("car %d (%s): best %.3f, published %.3f", car, name, r.Best, wantBest)
		}
		if math.Abs(r.Worst-wantWorst) > 1e-9 {
			t.Errorf("car %d (%s): worst %.3f, published %.3f", car, name, r.Worst, wantWorst)
		}
	}
}

// The winner of a real race, reproduced from nothing but the lane times.
func TestPublishedWinner(t *testing.T) {
	_, heatRows := readCSV(t, heatsFixture)
	got := Standings(runsFromHeats(t, heatRows, excludedCars(t)))

	// 2026 race 4 was won by car 73, Duncan Breland's "Loose Moose", at 2.367.
	if got[0].EntryID != 73 {
		t.Errorf("winner is car %d, want car 73", got[0].EntryID)
	}
	if avg := FormatAverage(got[0].Average); avg != "2.367" {
		t.Errorf("winning average %s, want 2.367", avg)
	}
}

// The one place this implementation deliberately disagrees with the published
// file.
//
// Cars 46 and 91 ran identical times to the millisecond, so their drop-slowest
// averages are exactly equal. DerbyNet published them at places 4 and 5. The
// reason is arithmetic order, not a rule: DerbyNet computes the average in SQL
// as (SUM - MAX) / (COUNT - 1), which for car 91 lands one bit away from summing
// the kept times — a difference of about 4e-16.
//
// Separating two cars that ran the same times on floating-point noise is wrong,
// so this reports them as a tie. Everything below them is unaffected, because a
// two-way tie for 4th is followed by 6th either way.
func TestTiedCarsWerePublishedSeparately(t *testing.T) {
	_, heatRows := readCSV(t, heatsFixture)
	got := Standings(runsFromHeats(t, heatRows, excludedCars(t)))

	byCar := map[int64]Result{}
	for _, r := range got {
		byCar[r.EntryID] = r
	}

	a, b := byCar[46], byCar[91]
	if a.Average != b.Average {
		t.Fatalf("cars 46 and 91 should have identical averages, got %v and %v",
			a.Average, b.Average)
	}
	if a.Place != 4 || b.Place != 4 {
		t.Errorf("tied cars placed %d and %d, want both 4", a.Place, b.Place)
	}
	if !a.Tied || !b.Tied {
		t.Error("both cars should be marked Tied")
	}
	// The published file has 5 here; the tie is the divergence.
	if byCar[40].Place != 6 {
		t.Errorf("car 40 placed %d, want 6 — a two-way tie for 4th skips 5th",
			byCar[40].Place)
	}
}

// Excluding an entry has to actually change the result, or the flag is not
// doing its job. In this race the fastest car of the night was excluded, so
// scoring without honouring exclusions crowns the wrong winner.
func TestExclusionChangesTheWinner(t *testing.T) {
	_, heatRows := readCSV(t, heatsFixture)

	withExclusions := Standings(runsFromHeats(t, heatRows, excludedCars(t)))
	ignoringExclusions := Standings(runsFromHeats(t, heatRows, nil))

	if withExclusions[0].EntryID == ignoringExclusions[0].EntryID {
		t.Fatal("exclusions made no difference to this race; the fixture no longer " +
			"exercises the excluded-entry path")
	}
	if ignoringExclusions[0].EntryID != 901 {
		t.Errorf("ignoring exclusions, the fastest car is %d, want 901 (Justin Palmer's Shelby)",
			ignoringExclusions[0].EntryID)
	}
	if withExclusions[0].EntryID != 73 {
		t.Errorf("honouring exclusions, the winner is car %d, want 73",
			withExclusions[0].EntryID)
	}
}
