package history

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Reading one published race night back in: its heats and its standings.
//
// This is what the club records are built from. The heat times are the
// evidence; the standings say who counted. Neither file is enough alone — the
// 2021-2024 heat files carry no car names, and no standings file says which
// cars raced and were left out.

// RaceCar is one car's night, as the archive has it.
type RaceCar struct {
	First     string
	Last      string
	CarName   string
	CarNumber int
	// Place is the published finishing place, or zero for a car that raced and
	// was not in the standings.
	Place int

	// Control is the club's pace car. It is equipment, not a racer, and holds
	// no record.
	Control bool
	// Excluded is a car that raced but was ruled out of the results. The
	// archive says so in one of two ways: the car is missing from the
	// standings altogether (2026), or " - DQ" was added to its name (2023).
	Excluded bool

	Runs []RaceRun
}

// Driver renders the racer's full name.
func (c RaceCar) Driver() string {
	return strings.TrimSpace(c.First + " " + c.Last)
}

// RaceRun is one trip down one lane.
type RaceRun struct {
	Heat int
	Lane int
	// Time is in seconds. A car that did not finish is 9.999, whatever the
	// exporter wrote for it — 2021 wrote 9.9999, and a 0 would otherwise read
	// as the fastest run ever recorded.
	Time float64
}

var (
	heatNumbers = []string{"Heat", "Heat#"}
	laneNumbers = []string{"Lane"}
	runTimes    = []string{"Times", "Time", "FinishTime"}
	averages    = []string{"Average", "Avg Time", "Avg. Time", "Average Time"}

	// dqMark is how 2023 recorded a disqualified car: in its name.
	dqMark = regexp.MustCompile(`(?i)\s*-\s*DQ$`)
)

// table is a CSV read loosely and addressed by column name, because no two
// years of the archive agree on either the names or the widths.
type table struct {
	index map[string]int
	rows  [][]string
}

func readTable(body []byte) (table, error) {
	body = bytes.TrimPrefix(body, []byte{0xEF, 0xBB, 0xBF})
	r := csv.NewReader(bytes.NewReader(body))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return table{}, err
	}
	if len(rows) == 0 {
		return table{}, fmt.Errorf("it is empty")
	}
	t := table{index: map[string]int{}}
	for i, name := range rows[0] {
		// 2019 wrapped header cells across lines.
		t.index[strings.TrimSpace(headerSpace.ReplaceAllString(name, " "))] = i
	}
	for _, row := range rows[1:] {
		blank := true
		for _, c := range row {
			if strings.TrimSpace(c) != "" {
				blank = false
				break
			}
		}
		if !blank {
			t.rows = append(t.rows, row)
		}
	}
	return t, nil
}

func (t table) has(names []string) bool {
	for _, n := range names {
		if _, ok := t.index[n]; ok {
			return true
		}
	}
	return false
}

func (t table) get(row []string, names []string) string {
	for _, n := range names {
		if i, ok := t.index[n]; ok && i < len(row) {
			return strings.TrimSpace(row[i])
		}
	}
	return ""
}

// name reads a driver from either a split or a whole-name layout.
func (t table) name(row []string) (first, last string) {
	first, last = t.get(row, firstNames), t.get(row, lastNames)
	if first == "" && last == "" {
		whole := t.get(row, wholeNames)
		if i := strings.LastIndex(whole, " "); i > 0 {
			return whole[:i], whole[i+1:]
		}
		return "", whole
	}
	return first, last
}

// PersonKey is how a racer is recognised across years: the full name, folded
// for case and spacing. It is deliberately no looser than that. A misspelling
// splits a racer in two, and that is shown to a person to put right rather
// than guessed at — two real people can share a surname and a first initial.
func PersonKey(first, last string) string {
	return strings.ToLower(headerSpace.ReplaceAllString(strings.TrimSpace(first+" "+last), " "))
}

// IsPaceCar recognises the club's CONTROL car. Its driver has been written as
// "Derby Ales", "Derby Ale" and "Derby and Alers" over the years; its car name
// is "CONTROL" in any case. Only the whole name counts, because 2026 has a real
// car called "The Other Controller".
func IsPaceCar(first, last, carName string) bool {
	if Key(carName) == "control" {
		return true
	}
	f, l := strings.ToLower(strings.TrimSpace(first)), strings.ToLower(strings.TrimSpace(last))
	return strings.HasPrefix(f, "derby") && strings.HasPrefix(l, "ale")
}

// ParseRace reads one race night from its published heats and standings.
func ParseRace(heatsCSV, standingsCSV []byte) ([]RaceCar, error) {
	heats, err := readTable(heatsCSV)
	if err != nil {
		return nil, fmt.Errorf("the heat results could not be read: %v", err)
	}
	standings, err := readTable(standingsCSV)
	if err != nil {
		return nil, fmt.Errorf("the standings could not be read: %v", err)
	}
	if !heats.has(runTimes) || !heats.has(carNumbers) || !heats.has(laneNumbers) {
		return nil, fmt.Errorf("the heat results have no times, lanes or car numbers in them")
	}
	if !standings.has(placeNames) {
		return nil, fmt.Errorf("the standings have no place column")
	}

	// Every car's runs, keyed by car number, which every year's heat file has.
	var cars []*RaceCar
	byNumber := map[int]*RaceCar{}
	for _, row := range heats.rows {
		num, err := strconv.Atoi(heats.get(row, carNumbers))
		if err != nil {
			continue
		}
		heat, _ := strconv.Atoi(heats.get(row, heatNumbers))
		lane, _ := strconv.Atoi(heats.get(row, laneNumbers))
		t, err := strconv.ParseFloat(heats.get(row, runTimes), 64)
		if err != nil || t <= 0 || t > 9.999 {
			t = 9.999
		}
		car := byNumber[num]
		if car == nil {
			car = &RaceCar{CarNumber: num}
			car.First, car.Last = heats.name(row)
			car.CarName = CleanCarName(heats.get(row, carNames))
			byNumber[num] = car
			cars = append(cars, car)
		}
		car.Runs = append(car.Runs, RaceRun{Heat: heat, Lane: lane, Time: t})
	}
	if len(cars) == 0 {
		return nil, fmt.Errorf("the heat results have no runs in them")
	}

	// Then who counted. Match on car number where the standings have one; 2023
	// race 1 does not, and there several racers brought two cars, so a name
	// alone cannot say which is which — the average can, because the published
	// average was computed from exactly these runs.
	type standing struct {
		first, last, car string
		place            int
		average          float64
		dq               bool
		used             bool
	}
	var rows []*standing
	byNum := map[int]*standing{}
	for _, row := range standings.rows {
		place, err := strconv.Atoi(standings.get(row, placeNames))
		if err != nil {
			continue
		}
		s := &standing{place: place}
		s.first, s.last = standings.name(row)
		name := standings.get(row, carNames)
		s.dq = dqMark.MatchString(name)
		s.car = CleanCarName(dqMark.ReplaceAllString(name, ""))
		s.average, _ = strconv.ParseFloat(standings.get(row, averages), 64)
		rows = append(rows, s)
		if n, err := strconv.Atoi(standings.get(row, carNumbers)); err == nil && standings.has(carNumbers) {
			byNum[n] = s
		}
	}

	for _, car := range cars {
		var match *standing
		if standings.has(carNumbers) {
			match = byNum[car.CarNumber]
		} else {
			avg := dropSlowest(car.Runs)
			key := PersonKey(car.First, car.Last)
			var named []*standing
			for _, s := range rows {
				if !s.used && PersonKey(s.first, s.last) == key {
					named = append(named, s)
				}
			}
			if len(named) == 1 {
				match = named[0]
			} else {
				for _, s := range named {
					if math.Abs(s.average-avg) < 0.0006 {
						match = s
						break
					}
				}
			}
		}

		if match == nil {
			// Raced, and not in the results: ruled out.
			car.Excluded = true
		} else {
			match.used = true
			car.Place = match.place
			car.Excluded = match.dq
			// The standings are the curated file. The heat files have typos in
			// them ("Ameila") and, for four years, no car names at all.
			if match.first != "" || match.last != "" {
				car.First, car.Last = match.first, match.last
			}
			if match.car != "" {
				car.CarName = match.car
			}
		}
		if car.Excluded {
			car.Place = 0
		}
		car.Control = IsPaceCar(car.First, car.Last, car.CarName)
	}

	out := make([]RaceCar, 0, len(cars))
	for _, c := range cars {
		sort.Slice(c.Runs, func(i, j int) bool { return c.Runs[i].Heat < c.Runs[j].Heat })
		out = append(out, *c)
	}
	return out, nil
}

// dropSlowest is the club's average, used only to tell one racer's two cars
// apart in a year whose standings carry no car numbers.
func dropSlowest(runs []RaceRun) float64 {
	if len(runs) == 0 {
		return 0
	}
	times := make([]float64, 0, len(runs))
	for _, r := range runs {
		times = append(times, r.Time)
	}
	sort.Float64s(times)
	if len(times) > 1 {
		times = times[:len(times)-1]
	}
	sum := 0.0
	for _, t := range times {
		sum += t
	}
	return sum / float64(len(times))
}
