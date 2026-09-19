package records

import "testing"

// A night is its counted runs' average. The championship is not ranked, and a
// run-off does not move its night.
func TestRaceNightsAreRankedByTheirAverageRun(t *testing.T) {
	r1, r2 := Race{Year: 2026, Number: 1}, Race{Year: 2026, Number: 2}
	champ := Race{Year: 2026, Championship: true, Number: 1}
	got := RaceSpeeds([]Run{
		{Race: r1, Time: 2.40}, {Race: r1, Time: 2.50},
		{Race: r2, Time: 2.30}, {Race: r2, Time: 2.40},
		{Race: r2, Time: 1.00, RunOff: true},
		{Race: champ, Time: 2.00},
	})
	if len(got) != 2 {
		t.Fatalf("%d nights ranked, want the two points races", len(got))
	}
	if got[0].Race != r2 || got[0].Rank != 1 || got[0].Runs != 2 {
		t.Errorf("fastest = %+v, want race 2 on its two scheduled runs", got[0])
	}
	if got[1].Average < 2.449 || got[1].Average > 2.451 {
		t.Errorf("race 1 average = %v", got[1].Average)
	}
}

func TestNightsWithTheSameAverageShareARank(t *testing.T) {
	a, b, c := Race{Year: 2025, Number: 1}, Race{Year: 2025, Number: 2}, Race{Year: 2025, Number: 3}
	got := RaceSpeeds([]Run{{Race: a, Time: 2.4}, {Race: b, Time: 2.4}, {Race: c, Time: 2.5}})
	if got[0].Rank != 1 || got[1].Rank != 1 || got[2].Rank != 3 {
		t.Errorf("ranks = %d %d %d, want 1 1 3", got[0].Rank, got[1].Rank, got[2].Rank)
	}
}
