package schedule

import (
	"testing"
)

// The club's rule, checked across every field size a race night could plausibly
// produce. This is the single most important test in the package: a violation
// means a racer is called to two lanes at once, or never races a lane at all.
func TestEveryCarRunsEachLaneExactlyOnce(t *testing.T) {
	for cars := 2; cars <= 60; cars++ {
		s, err := Generate(cars, 4)
		if err != nil {
			t.Fatalf("cars=%d: %v", cars, err)
		}
		if err := Validate(s); err != nil {
			t.Errorf("cars=%d: %v", cars, err)
		}
		if got := s.RunsPerCar(); got != 4 {
			t.Errorf("cars=%d: RunsPerCar = %d, want 4", cars, got)
		}
	}
}

// n cars produce exactly n heats — no padding, once there are at least as many
// cars as lanes.
func TestHeatCountEqualsCarCount(t *testing.T) {
	for cars := 4; cars <= 40; cars++ {
		s, err := Generate(cars, 4)
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Heats) != cars {
			t.Errorf("cars=%d: got %d heats, want %d", cars, len(s.Heats), cars)
		}
	}
}

// Fewer cars than lanes still has to fill the track, so the remainder are byes.
func TestFewerCarsThanLanesUsesByes(t *testing.T) {
	s, err := Generate(3, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(s); err != nil {
		t.Fatal(err)
	}
	if len(s.Heats) != 4 {
		t.Errorf("got %d heats, want 4 (padded to the lane count)", len(s.Heats))
	}
	byes := 0
	for _, heat := range s.Heats {
		for _, car := range heat {
			if car == Bye {
				byes++
			}
		}
	}
	// One virtual car's worth of slots: 4 heats, one bye each.
	if byes != 4 {
		t.Errorf("got %d byes, want 4", byes)
	}
}

// The analytic score in scoreOffsets works from differences rather than by
// building the schedule. If it disagrees with a direct count, the search is
// optimizing the wrong thing.
func TestAnalyticScoreMatchesDirectCount(t *testing.T) {
	for cars := 4; cars <= 40; cars++ {
		s, err := Generate(cars, 4)
		if err != nil {
			t.Fatal(err)
		}
		if got := RepeatCount(s); got != s.RepeatedMeetings {
			t.Errorf("cars=%d: direct count %d, analytic score %d", cars, got, s.RepeatedMeetings)
		}
	}
}

// A perfect schedule is one where no two cars ever meet twice. Four lanes needs
// six distinct pairwise differences, so it becomes possible once the field is
// large enough.
func TestLargeFieldsAreScheduledPerfectly(t *testing.T) {
	for cars := 13; cars <= 48; cars++ {
		s, err := Generate(cars, 4)
		if err != nil {
			t.Fatal(err)
		}
		if !s.Perfect() {
			t.Errorf("cars=%d: %d repeated meetings, expected a perfect schedule",
				cars, s.RepeatedMeetings)
		}
	}
}

// The club's real field size. DerbyNet ships [2,3,4] for this case; we search
// instead. The test asserts the property that matters — a perfect schedule —
// and separately confirms our scorer agrees that DerbyNet's answer is perfect
// too, which is what validates the scorer against a known-good implementation.
func TestTwentyFourCarsMatchesDerbyNetQuality(t *testing.T) {
	s, err := Generate(24, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(s); err != nil {
		t.Fatal(err)
	}
	if !s.Perfect() {
		t.Errorf("24 cars: %d repeated meetings, want 0", s.RepeatedMeetings)
	}
	if len(s.Heats) != 24 {
		t.Errorf("got %d heats, want 24", len(s.Heats))
	}

	// DerbyNet's published generator for 4 lanes / 24 cars is steps [2,3,4],
	// i.e. offsets [0,2,5,9]. Our scorer must agree it is perfect.
	if got, _ := scoreOffsets([]int{0, 2, 5, 9}, 24); got != 0 {
		t.Errorf("scoreOffsets on DerbyNet's [0,2,5,9] = %d, want 0", got)
	}
}

// Racing in back-to-back heats is the thing racers actually complain about:
// there is no time to collect the car and get back to the start line.
//
// From 15 cars up this is always avoidable, and the schedule must avoid it.
// The club has never run a race smaller than about 18 cars.
func TestNoCarRacesInConsecutiveHeats(t *testing.T) {
	for cars := 15; cars <= 60; cars++ {
		s, err := Generate(cars, 4)
		if err != nil {
			t.Fatal(err)
		}
		if n := BackToBack(s); n != 0 {
			t.Errorf("cars=%d: %d back-to-back runs, want 0 (min gap %d)",
				cars, n, MinGap(s))
		}
	}
}

// Below 15 cars, back-to-back racing is not a scheduling failure — it is
// arithmetic. A perfect four-lane offset set has six distinct pairwise
// differences, which is twelve counting sign. Once the field is small enough
// those twelve cover every nonzero residue, so *every* pair of heats shares a
// car and some back-to-back racing is unavoidable.
//
// This test pins that reasoning down. If someone later "fixes" the small-field
// case, this tells them what they are actually trading away.
func TestSmallFieldsCannotAvoidBackToBack(t *testing.T) {
	for _, cars := range []int{13, 14} {
		if free := bestFreeStepsAmongPerfectSets(cars, 4); free != 0 {
			t.Errorf("cars=%d: found a perfect offset set with %d free steps; "+
				"zero back-to-back may now be achievable", cars, free)
		}
	}
	// 15 is the first field size where it becomes possible, which is why
	// TestNoCarRacesInConsecutiveHeats starts there.
	if free := bestFreeStepsAmongPerfectSets(15, 4); free == 0 {
		t.Error("cars=15: expected at least one perfect offset set with a free step")
	}
}

// bestFreeStepsAmongPerfectSets reports the most free running-order steps
// available from any offset set that also mixes opponents perfectly.
func bestFreeStepsAmongPerfectSets(n, lanes int) int {
	best := 0
	candidate := make([]int, lanes)
	forEachCombination(n-1, lanes-1, func(rest []int) bool {
		candidate[0] = 0
		copy(candidate[1:], rest)
		if repeats, free := scoreOffsets(candidate, n); repeats == 0 && free > best {
			best = free
		}
		return true
	})
	return best
}

// The scheduler should report the back-to-back count honestly so the check-in
// screen can warn rather than quietly producing a schedule people will grumble
// about.
func TestBackToBackIsReportedForSmallFields(t *testing.T) {
	s, err := Generate(13, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(s); err != nil {
		t.Fatal(err)
	}
	if BackToBack(s) == 0 {
		t.Error("expected BackToBack to report the unavoidable back-to-back runs at 13 cars")
	}
}

// Small fields cannot avoid it — with 5 cars and 4 lanes, almost every car is in
// almost every heat. The scheduler should still produce something valid.
func TestSmallFieldsAreStillValid(t *testing.T) {
	for cars := 2; cars <= 8; cars++ {
		s, err := Generate(cars, 4)
		if err != nil {
			t.Fatalf("cars=%d: %v", cars, err)
		}
		if err := Validate(s); err != nil {
			t.Errorf("cars=%d: %v", cars, err)
		}
	}
}

// Runs should be spread through the night rather than clustered, so nobody is
// finished after ten minutes and nobody is still waiting at the end.
func TestRunsAreSpreadThroughTheSchedule(t *testing.T) {
	const cars = 24
	s, err := Generate(cars, 4)
	if err != nil {
		t.Fatal(err)
	}

	// Track when each car's runs happen. With 24 cars over 24 heats, a car's
	// four runs should not all fall in the same third of the night.
	positions := make([][]int, cars)
	for h, heat := range s.Heats {
		for _, car := range heat {
			if car != Bye {
				positions[car] = append(positions[car], h)
			}
		}
	}
	third := len(s.Heats) / 3
	for car, pos := range positions {
		if len(pos) != 4 {
			t.Fatalf("car %d has %d runs", car, len(pos))
		}
		if pos[len(pos)-1]-pos[0] < third {
			t.Errorf("car %d runs all bunched into heats %v", car, pos)
		}
	}
}

// The same inputs must always produce the same schedule, or a regenerate would
// silently reshuffle a race that has already been announced.
func TestScheduleIsDeterministic(t *testing.T) {
	for _, cars := range []int{7, 18, 24, 37} {
		a, err := Generate(cars, 4)
		if err != nil {
			t.Fatal(err)
		}
		b, err := Generate(cars, 4)
		if err != nil {
			t.Fatal(err)
		}
		if len(a.Heats) != len(b.Heats) {
			t.Fatalf("cars=%d: heat counts differ", cars)
		}
		for h := range a.Heats {
			for lane := range a.Heats[h] {
				if a.Heats[h][lane] != b.Heats[h][lane] {
					t.Fatalf("cars=%d: heat %d lane %d differs between runs", cars, h+1, lane+1)
				}
			}
		}
	}
}

// Other lane counts are not what the club races, but the generator should not
// be silently wrong if the track ever changes.
func TestOtherLaneCounts(t *testing.T) {
	for _, lanes := range []int{2, 3, 5, 6} {
		for cars := lanes; cars <= 30; cars++ {
			s, err := Generate(cars, lanes)
			if err != nil {
				t.Fatalf("lanes=%d cars=%d: %v", lanes, cars, err)
			}
			if err := Validate(s); err != nil {
				t.Errorf("lanes=%d cars=%d: %v", lanes, cars, err)
			}
			if s.RunsPerCar() != lanes {
				t.Errorf("lanes=%d cars=%d: RunsPerCar = %d", lanes, cars, s.RunsPerCar())
			}
		}
	}
}

func TestGenerateRejectsImpossibleInputs(t *testing.T) {
	if _, err := Generate(1, 4); err == nil {
		t.Error("one car should be rejected: there is no race")
	}
	if _, err := Generate(0, 4); err == nil {
		t.Error("zero cars should be rejected")
	}
	if _, err := Generate(10, 0); err == nil {
		t.Error("zero lanes should be rejected")
	}
}

// The step form is how DerbyNet's tables express a generator; it should round
// trip through the offsets we actually search over.
func TestGeneratorStepsMatchOffsets(t *testing.T) {
	s, err := Generate(24, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Generator) != 3 {
		t.Fatalf("got %d steps, want 3", len(s.Generator))
	}
	pos := s.Offsets[0]
	for i, step := range s.Generator {
		pos = (pos + step) % 24
		if pos != s.Offsets[i+1] {
			t.Errorf("step %d: rebuilt offset %d, want %d", i, pos, s.Offsets[i+1])
		}
	}
}

func TestCombinations(t *testing.T) {
	cases := []struct{ n, k, want int }{
		{23, 3, 1771}, // the club's 24-car search space
		{5, 0, 1},
		{5, 5, 1},
		{5, 6, 0},
		{10, 3, 120},
	}
	for _, c := range cases {
		if got := combinations(c.n, c.k); got != c.want {
			t.Errorf("combinations(%d,%d) = %d, want %d", c.n, c.k, got, c.want)
		}
	}
}

func TestForEachCombinationEnumeratesAllSubsets(t *testing.T) {
	var got [][]int
	forEachCombination(5, 3, func(c []int) bool {
		got = append(got, append([]int(nil), c...))
		return true
	})
	if len(got) != 10 { // C(5,3)
		t.Fatalf("got %d combinations, want 10", len(got))
	}
	// Strictly increasing, within range, lexicographic.
	for _, c := range got {
		for i := 1; i < len(c); i++ {
			if c[i] <= c[i-1] {
				t.Errorf("%v is not strictly increasing", c)
			}
		}
		if c[0] < 1 || c[len(c)-1] > 5 {
			t.Errorf("%v out of range", c)
		}
	}
	if got[0][0] != 1 || got[0][1] != 2 || got[0][2] != 3 {
		t.Errorf("first combination = %v, want [1 2 3]", got[0])
	}
}

func TestForEachCombinationStopsEarly(t *testing.T) {
	n := 0
	forEachCombination(20, 3, func([]int) bool {
		n++
		return n < 5
	})
	if n != 5 {
		t.Errorf("visited %d combinations, want 5", n)
	}
}

// Scheduling happens while a room full of people waits, so it needs to be fast.
func BenchmarkGenerate24Cars(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := Generate(24, 4); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGenerate60Cars(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := Generate(60, 4); err != nil {
			b.Fatal(err)
		}
	}
}
