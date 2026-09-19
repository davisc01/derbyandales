package records

import "testing"

var (
	r2025 = Race{Year: 2025, Number: 1}
	r2026 = Race{Year: 2026, Number: 1}
	c2025 = Race{Year: 2025, Championship: true, Number: 1}
)

func car(person string) Car {
	return Car{Person: person, Driver: person, CarName: person + "'s car", CarNumber: len(person)}
}

func run(person string, race Race, seq, lane int, t float64) Run {
	return Run{Car: car(person), Race: race, Seq: seq, Lane: lane, Time: t}
}

// The championship closes its season, so a 2025 championship run is older
// than any 2026 run and newer than every 2025 race night.
func TestRacesAreOrderedAsTheSeasonRuns(t *testing.T) {
	if !r2025.Before(c2025) || !c2025.Before(r2026) || r2026.Before(r2025) {
		t.Error("races are out of order")
	}
	if (Race{Year: 2025, Number: 2}).Before(r2025) {
		t.Error("race 2 came before race 1")
	}
}

// The record's history is what makes "broken" answerable: each entry beat all
// before it, and the last is the holder.
func TestTheFastestRunKeepsItsHistory(t *testing.T) {
	b := Compute([]Run{
		run("greg thrift", r2025, 1, 1, 2.300),
		run("anne bryan", r2025, 2, 2, 2.310), // slower: not a record
		run("chris bryan", c2025, 3, 3, 2.292),
		run("duncan breland", r2026, 1, 1, 2.293), // a thousandth short
	}, nil)
	if b.FastestRun == nil || b.FastestRun.Person != "chris bryan" {
		t.Fatalf("holder = %+v", b.FastestRun)
	}
	if len(b.RunHistory) != 2 {
		t.Fatalf("history has %d entries, want the two that set it", len(b.RunHistory))
	}
	if b.RunHistory[0].Person != "greg thrift" {
		t.Errorf("history starts with %s", b.RunHistory[0].Person)
	}
}

// Equalling the record is not breaking it, and the first to run the time
// keeps it.
func TestATiedRecordStaysWithWhoeverRanItFirst(t *testing.T) {
	b := Compute([]Run{
		run("a", r2025, 1, 1, 2.300),
		run("b", r2026, 1, 1, 2.300),
	}, nil)
	if b.FastestRun.Person != "a" || len(b.RunHistory) != 1 {
		t.Errorf("holder %s, history %d", b.FastestRun.Person, len(b.RunHistory))
	}
	if b.Lanes[0].Person != "a" {
		t.Errorf("lane record went to %s on a tie", b.Lanes[0].Person)
	}
}

func TestEachLaneHasItsOwnRecord(t *testing.T) {
	b := Compute([]Run{
		run("a", r2025, 1, 1, 2.30), run("b", r2025, 1, 2, 2.35),
		run("c", r2025, 2, 1, 2.40), run("d", r2025, 2, 2, 2.31),
	}, nil)
	if len(b.Lanes) != 2 || b.Lanes[0].Person != "a" || b.Lanes[1].Person != "d" {
		t.Errorf("lanes = %+v", b.Lanes)
	}
}

// One quick car running four quick heats would otherwise be the whole top ten.
func TestTheLeaderboardHasOneRowPerCar(t *testing.T) {
	var runs []Run
	for i := 0; i < 4; i++ {
		runs = append(runs, run("fast", r2025, i+1, i+1, 2.30+float64(i)*0.001))
	}
	runs = append(runs, run("slow", r2025, 1, 2, 2.50))
	b := Compute(runs, nil)
	if len(b.TopRuns) != 2 {
		t.Errorf("%d leaderboard rows, want one per car", len(b.TopRuns))
	}
}

func TestACareerCountsWinsPodiumsAndCupsSeparately(t *testing.T) {
	res := func(person string, race Race, place int) Result {
		return Result{Car: car(person), Race: race, Place: place, Average: 2.3 + float64(place)/100}
	}
	b := Compute(nil, []Result{
		res("a", r2025, 1), res("a", Race{Year: 2025, Number: 2}, 3),
		res("a", c2025, 1), res("b", r2025, 2),
	})
	a := b.Career[0]
	if a.Person != "a" || a.Wins != 1 || a.Podiums != 2 || a.Cups != 1 || a.Nights != 3 {
		t.Errorf("career = %+v", a)
	}
}

// A bracket is decided head to head. It has no average, and must not take the
// average record with a zero.
func TestAResultWithNoAverageTakesNoAverageRecord(t *testing.T) {
	b := Compute(nil, []Result{
		{Car: car("a"), Race: r2025, Place: 1, Average: 2.35},
		{Car: car("b"), Race: c2025, Place: 1},
	})
	if b.FastestAverage == nil || b.FastestAverage.Person != "a" {
		t.Errorf("average record = %+v", b.FastestAverage)
	}
}

func TestAHeatThatBreaksTheTrackRecordSaysSo(t *testing.T) {
	runs := []Run{
		run("old", r2025, 1, 1, 2.292),
		run("lane3", r2025, 1, 3, 2.300),
		run("new", r2026, 5, 2, 2.290),
		run("also", r2026, 5, 3, 2.291), // under the old record, but not the fastest here
	}
	n := HeatNotices(runs, r2026, 5)
	if n[2].Kind != TrackRecord || n[2].Previous.Person != "old" {
		t.Errorf("lane 2: %+v", n[2])
	}
	if n[3].Kind == TrackRecord {
		t.Error("two cars in one heat both took the track record")
	}
	if n[3].Kind != LaneRecord {
		t.Errorf("lane 3 beat its lane's record and got %q", n[3].Kind)
	}
}

// With nothing on file there is nothing to break. Without this, the first heat
// on a fresh install would set a record in every lane.
func TestTheFirstHeatEverSetsNoRecord(t *testing.T) {
	runs := []Run{run("a", r2026, 1, 1, 2.3), run("b", r2026, 1, 2, 2.4)}
	if n := HeatNotices(runs, r2026, 1); len(n) != 0 {
		t.Errorf("notices = %+v", n)
	}
}

// A heat is judged against what ran before it — not against later heats that
// have since been run, or it could never be shown again after the fact.
func TestAHeatIsJudgedOnlyAgainstWhatCameBefore(t *testing.T) {
	runs := []Run{
		run("old", r2025, 1, 1, 2.40),
		run("a", r2026, 1, 1, 2.35),
		run("b", r2026, 2, 1, 2.30),
	}
	if n := HeatNotices(runs, r2026, 1); n[1].Kind != TrackRecord {
		t.Errorf("heat 1 lost its record to a later heat: %+v", n)
	}
}

func TestAPersonalBestNeedsAnEarlierNight(t *testing.T) {
	runs := []Run{
		run("vet", r2025, 1, 1, 2.40),
		run("rookie", r2026, 1, 2, 2.45),
		run("track", r2025, 1, 3, 2.20), // keeps the track record out of it
		run("vet", r2026, 2, 3, 2.38),
		run("rookie", r2026, 2, 4, 2.42),
	}
	n := HeatNotices(runs, r2026, 2)
	if n[3].Kind != PersonalBest || n[3].Previous.Time != 2.40 {
		t.Errorf("the returning racer's best was not noticed: %+v", n[3])
	}
	if _, ok := n[4]; ok {
		t.Errorf("a first-night improvement was called a personal best: %+v", n[4])
	}
}

func TestEveryCarUnderTheOldAverageRecordIsNoticed(t *testing.T) {
	results := []Result{
		{Car: car("old"), Race: r2025, Place: 1, Average: 2.35},
		{Car: Car{Person: "a", CarNumber: 7}, Race: r2026, Place: 1, Average: 2.33},
		{Car: Car{Person: "b", CarNumber: 8}, Race: r2026, Place: 2, Average: 2.34},
		{Car: Car{Person: "c", CarNumber: 9}, Race: r2026, Place: 3, Average: 2.36},
	}
	n := AverageNotices(results, r2026)
	if len(n) != 2 || n[7].Kind != AverageRecord || n[8].PreviousAverage.Person != "old" {
		t.Errorf("notices = %+v", n)
	}
}

func TestAMisspeltNameIsShownRatherThanMerged(t *testing.T) {
	b := Compute([]Run{
		run("keith furguson", r2025, 1, 1, 2.4),
		run("keith ferguson", r2026, 1, 1, 2.4),
		run("jim seidman", r2025, 1, 2, 2.4),
		run("jeff seidman", r2025, 1, 3, 2.4),
		run("anne bryan", r2025, 1, 4, 2.4),
		run("chris bryan", r2025, 2, 1, 2.4),
	}, nil)
	if len(b.Lookalikes) != 1 {
		t.Fatalf("lookalikes = %v, want only the Furguson/Ferguson pair", b.Lookalikes)
	}
	if len(b.Career) != 0 {
		t.Error("runs alone should not make a career")
	}
}
