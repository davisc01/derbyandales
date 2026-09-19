package scoring

import "testing"

// The wrap-up is read by the people who run the track. Each of these is a
// question they ask of it at the end of a night.

func nightRuns() []NightRun {
	// Four heats, four lanes. Lane 4 is slow and has a non-finish; car 1 is
	// the pace car and car 9 is excluded.
	return []NightRun{
		{Heat: 1, Lane: 1, Time: 2.40, EntryID: 1},
		{Heat: 1, Lane: 2, Time: 2.35, EntryID: 2, Counts: true},
		{Heat: 1, Lane: 3, Time: 2.36, EntryID: 3, Counts: true},
		{Heat: 1, Lane: 4, Time: 9.999, EntryID: 4, Counts: true},

		{Heat: 2, Lane: 1, Time: 2.30, EntryID: 9}, // excluded, fastest of all
		{Heat: 2, Lane: 2, Time: 2.33, EntryID: 3, Counts: true},
		{Heat: 2, Lane: 3, Time: 2.37, EntryID: 4, Counts: true},
		{Heat: 2, Lane: 4, Time: 2.45, EntryID: 2, Counts: true},

		{Heat: 3, Lane: 1, Time: 2.34, EntryID: 4, Counts: true},
		{Heat: 3, Lane: 2, Time: 2.341, EntryID: 2, Counts: true},
		{Heat: 3, Lane: 3, Time: 2.38, EntryID: 3, Counts: true},
		{Heat: 3, Lane: 4, Time: 2.50, EntryID: 1},
	}
}

func TestLanesAreSortedFastestFirstWithoutTheirNonFinishes(t *testing.T) {
	s := Night(nightRuns())
	if s.Lanes[len(s.Lanes)-1].Lane != 4 {
		t.Errorf("the slow lane is not last: %+v", s.Lanes)
	}
	var four LaneStat
	for _, l := range s.Lanes {
		if l.Lane == 4 {
			four = l
		}
	}
	// 9.999 left in would put lane 4 at about 4.98.
	if four.DNFs != 1 || four.Average < 2.47 || four.Average > 2.48 {
		t.Errorf("lane 4 = %+v, want one DNF and an average of the two finishes", four)
	}
	if s.DNFs != 1 || s.Heats != 3 || s.Runs != 12 {
		t.Errorf("totals = %d heats, %d runs, %d DNFs", s.Heats, s.Runs, s.DNFs)
	}
}

// Lane wins are about the track, so the excluded car's win still counts for
// its lane — but it does not get to be the fastest heat of the night.
func TestAnExcludedCarWinsItsLaneAHeatButNoHighlight(t *testing.T) {
	s := Night(nightRuns())
	wins := map[int]int{}
	for _, l := range s.Lanes {
		wins[l.Lane] = l.Wins
	}
	if wins[1] != 2 || wins[2] != 1 {
		t.Errorf("lane wins = %v", wins)
	}
	if len(s.FastestHeats) != 3 {
		t.Fatalf("%d fastest heats", len(s.FastestHeats))
	}
	if top := s.FastestHeats[0]; top.EntryID == 9 || top.Time != 2.33 {
		t.Errorf("fastest heat = %+v, want car 3's 2.33 in heat 2", top)
	}
}

func TestTheClosestFinishIsBetweenTwoCountedCars(t *testing.T) {
	s := Night(nightRuns())
	if s.Closest == nil || s.Closest.Heat != 3 || s.Closest.Winner != 4 || s.Closest.RunnerUp != 2 {
		t.Fatalf("closest = %+v, want heat 3's thousandth", s.Closest)
	}
}

// A car that did not finish once is not consistent, however close its other
// runs were.
func TestTheSteadiestCarFinishedEveryRun(t *testing.T) {
	s := Night(nightRuns())
	if s.Steadiest == nil || s.Steadiest.EntryID != 3 {
		t.Errorf("steadiest = %+v, want car 3", s.Steadiest)
	}
}

func TestANightWithNothingRunIsEmpty(t *testing.T) {
	s := Night(nil)
	if s.Heats != 0 || s.Closest != nil || s.Steadiest != nil || len(s.FastestHeats) != 0 {
		t.Errorf("stats = %+v", s)
	}
}

// Seven wins from five expected is luck, and the review must say so rather
// than send somebody off to shim a lane that is fine.
func TestALaneWinningALittleOftenIsNotUnusual(t *testing.T) {
	var runs []NightRun
	for h := 1; h <= 20; h++ {
		winner := 1 + h%4
		if h <= 7 {
			winner = 4
		} else if winner == 4 {
			winner = 1
		}
		for lane := 1; lane <= 4; lane++ {
			tm := 2.40
			if lane == winner {
				tm = 2.30
			}
			runs = append(runs, NightRun{Heat: h, Lane: lane, Time: tm, EntryID: int64(lane), Counts: true})
		}
	}
	s := Night(runs)
	for _, l := range s.Lanes {
		if l.Expected != 5 {
			t.Errorf("lane %d expected %v wins, want 5 from 20 four-car heats", l.Lane, l.Expected)
		}
		if l.Lane == 4 && (l.Wins < 7 || l.Unusual) {
			t.Errorf("lane 4 = %+v, want its wins and no flag", l)
		}
	}
}

// A lane that wins every heat is a lane problem, whatever else is true.
func TestALaneWinningEveryHeatIsUnusual(t *testing.T) {
	var runs []NightRun
	for h := 1; h <= 20; h++ {
		for lane := 1; lane <= 4; lane++ {
			tm := 2.40 + float64(lane)*0.01
			runs = append(runs, NightRun{Heat: h, Lane: lane, Time: tm, EntryID: int64(lane), Counts: true})
		}
	}
	for _, l := range Night(runs).Lanes {
		if l.Lane == 1 && !l.Unusual {
			t.Errorf("lane 1 won all 20 and was not flagged: %+v", l)
		}
		if l.Lane != 1 && l.Wins == 0 && !l.Unusual {
			// Winning none of twenty from five expected is about 1 in 150.
			t.Errorf("lane %d won nothing and was not flagged: %+v", l.Lane, l)
		}
	}
}
