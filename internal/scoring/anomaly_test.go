package scoring

import (
	"math/rand"
	"testing"
)

// The anomaly check exists to catch a heat that was a fault rather than a
// result. Its two failure modes are opposite and both bad: missing a broken
// heat leaves wrong times in the standings, and flagging ordinary racing
// teaches the coordinator to ignore it.

// fourCarRace builds a plausible race: every car four runs, one per lane.
func fourCarRace(cars int, seed int64) map[int64][]Run {
	rng := rand.New(rand.NewSource(seed))
	out := map[int64][]Run{}
	for c := 1; c <= cars; c++ {
		pace := 2.30 + rng.Float64()*0.30
		for lane := 1; lane <= 4; lane++ {
			out[int64(c)] = append(out[int64(c)], Run{
				Heat: (c-1+lane)%cars + 1,
				Lane: lane,
				Time: pace + rng.NormFloat64()*0.03,
			})
		}
	}
	return out
}

// A sticky gate, a knock to the track, someone leaning on the rail: whatever it
// was, every car in that heat had its worst night's run there.
func TestAHeatWhereEveryCarRanItsWorstIsFlagged(t *testing.T) {
	runs := map[int64][]Run{
		1: {{Heat: 1, Time: 2.40}, {Heat: 2, Time: 2.90}, {Heat: 3, Time: 2.41}, {Heat: 4, Time: 2.39}},
		2: {{Heat: 1, Time: 2.50}, {Heat: 2, Time: 2.99}, {Heat: 3, Time: 2.51}, {Heat: 4, Time: 2.52}},
		3: {{Heat: 1, Time: 2.35}, {Heat: 2, Time: 2.88}, {Heat: 3, Time: 2.36}, {Heat: 4, Time: 2.34}},
		4: {{Heat: 1, Time: 2.60}, {Heat: 2, Time: 3.10}, {Heat: 3, Time: 2.61}, {Heat: 4, Time: 2.59}},
	}

	got := HeatAnomalies(runs)
	if len(got) != 1 {
		t.Fatalf("flagged %d heats, want 1: %+v", len(got), got)
	}
	if got[0].Heat != 2 {
		t.Errorf("flagged heat %d, want 2", got[0].Heat)
	}
	if got[0].Kind != AllSlowest {
		t.Errorf("kind = %q, want %q", got[0].Kind, AllSlowest)
	}
	if got[0].Cars != 4 {
		t.Errorf("based on %d cars, want 4", got[0].Cars)
	}
	if got[0].OneIn != 256 {
		t.Errorf("odds reported as 1 in %v, want 1 in 256", got[0].OneIn)
	}
}

// The other direction matters just as much and is easier to miss, because the
// times look good. A gate that released early or a timer that started late
// makes every car in the heat look like its best self.
func TestAHeatWhereEveryCarRanItsBestIsFlagged(t *testing.T) {
	runs := map[int64][]Run{
		1: {{Heat: 5, Time: 2.10}, {Heat: 6, Time: 2.40}, {Heat: 7, Time: 2.41}, {Heat: 8, Time: 2.39}},
		2: {{Heat: 5, Time: 2.15}, {Heat: 6, Time: 2.50}, {Heat: 7, Time: 2.51}, {Heat: 8, Time: 2.52}},
		3: {{Heat: 5, Time: 2.05}, {Heat: 6, Time: 2.35}, {Heat: 7, Time: 2.36}, {Heat: 8, Time: 2.34}},
	}

	got := HeatAnomalies(runs)
	if len(got) != 1 || got[0].Heat != 5 {
		t.Fatalf("did not flag heat 5: %+v", got)
	}
	if got[0].Kind != AllFastest {
		t.Errorf("kind = %q, want %q", got[0].Kind, AllFastest)
	}
	// Three cars with four runs each: one in sixty-four, and the flag should say
	// so rather than presenting it as the same finding as a four-car heat.
	if got[0].OneIn != 64 {
		t.Errorf("odds reported as 1 in %v, want 1 in 64", got[0].OneIn)
	}
}

// The important test. A check that fires on ordinary racing is worse than no
// check, because the one time it matters nobody will believe it.
func TestOrdinaryRacingIsNotFlagged(t *testing.T) {
	races, flagged := 200, 0
	for seed := int64(0); seed < int64(races); seed++ {
		flagged += len(HeatAnomalies(fourCarRace(24, seed)))
	}
	// The maths says about one race in five throws a coincidence: 24 heats at
	// one in 256, in either direction. Anything far above that means the test
	// is measuring something other than chance.
	if flagged > races/3 {
		t.Errorf("%d flags across %d clean races — too noisy to trust", flagged, races)
	}
	t.Logf("%d raw flags across %d clean 24-car races", flagged, races)
}

// The flag a coordinator is told to act on has to be quiet on clean racing,
// because the one night it fires is the night they need to believe it.
func TestClearIsAlmostNeverRaisedByOrdinaryRacing(t *testing.T) {
	races, clear := 500, 0
	for seed := int64(0); seed < int64(races); seed++ {
		for _, a := range HeatAnomalies(fourCarRace(24, seed)) {
			if a.Clear {
				clear++
			}
		}
	}
	// Measured at about one clean race in four hundred. Allowing five here
	// leaves room for the simulation to wander without letting a regression
	// that reintroduces the noise pass.
	if clear > 5 {
		t.Errorf("%d clear faults across %d clean races", clear, races)
	}
	t.Logf("%d clear flags across %d clean 24-car races", clear, races)
}

// Cars vary in how consistent they are, and the check must not get noisier for
// a scrappy field. This is why the bar is each car's own spread rather than a
// threshold in seconds.
func TestTheClearFlagDoesNotDependOnHowConsistentTheFieldIs(t *testing.T) {
	for _, consistency := range []float64{0.01, 0.03, 0.06} {
		races, clear := 500, 0
		for seed := int64(0); seed < int64(races); seed++ {
			rng := rand.New(rand.NewSource(seed))
			runs := map[int64][]Run{}
			for c := 1; c <= 24; c++ {
				pace := 2.30 + rng.Float64()*0.30
				for lane := 1; lane <= 4; lane++ {
					runs[int64(c)] = append(runs[int64(c)], Run{
						Heat: (c-1+lane)%24 + 1,
						Lane: lane,
						Time: pace + rng.NormFloat64()*consistency,
					})
				}
			}
			for _, a := range HeatAnomalies(runs) {
				if a.Clear {
					clear++
				}
			}
		}
		if clear > 5 {
			t.Errorf("run-to-run spread %.0f ms: %d clear flags across %d clean races",
				consistency*1000, clear, races)
		}
		t.Logf("run-to-run spread %.0f ms: %d clear flags in %d clean races",
			consistency*1000, clear, races)
	}
}

// One car having a bad trip is racing, not a fault. It must not drag the heat
// into the list.
func TestOneSlowCarIsNotAnAnomaly(t *testing.T) {
	runs := map[int64][]Run{
		1: {{Heat: 1, Time: 2.40}, {Heat: 2, Time: 2.99}, {Heat: 3, Time: 2.41}, {Heat: 4, Time: 2.39}},
		2: {{Heat: 1, Time: 2.50}, {Heat: 2, Time: 2.51}, {Heat: 3, Time: 2.52}, {Heat: 4, Time: 2.90}},
		3: {{Heat: 1, Time: 2.35}, {Heat: 2, Time: 2.36}, {Heat: 3, Time: 2.90}, {Heat: 4, Time: 2.34}},
		4: {{Heat: 1, Time: 2.60}, {Heat: 2, Time: 2.61}, {Heat: 3, Time: 2.59}, {Heat: 4, Time: 2.95}},
	}
	if got := HeatAnomalies(runs); len(got) != 0 {
		t.Errorf("flagged %+v on ordinary racing", got)
	}
}

// A two-car heat is a one-in-sixteen coincidence, which turns up somewhere in
// most races. Flagging it would train the coordinator to ignore the list.
func TestATwoCarHeatIsNotEnoughEvidence(t *testing.T) {
	runs := map[int64][]Run{
		1: {{Heat: 1, Time: 2.90}, {Heat: 2, Time: 2.40}, {Heat: 3, Time: 2.41}, {Heat: 4, Time: 2.39}},
		2: {{Heat: 1, Time: 2.99}, {Heat: 2, Time: 2.50}, {Heat: 3, Time: 2.51}, {Heat: 4, Time: 2.52}},
	}
	if got := HeatAnomalies(runs); len(got) != 0 {
		t.Errorf("flagged a two-car heat: %+v", got)
	}
}

// A struck-out lane is already the coordinator saying "that run was not real",
// so it must not be evidence either way. Here it is the only thing that would
// make the heat look unanimous.
func TestAStruckOutRunIsNotEvidence(t *testing.T) {
	runs := map[int64][]Run{
		1: {{Heat: 1, Time: 2.90}, {Heat: 2, Time: 2.40}, {Heat: 3, Time: 2.41}, {Heat: 4, Time: 2.39}},
		2: {{Heat: 1, Time: 2.99}, {Heat: 2, Time: 2.50}, {Heat: 3, Time: 2.51}, {Heat: 4, Time: 2.52}},
		// Struck out, so this car's heat-1 run cannot count towards heat 1 —
		// which leaves only two cars there, below the bar.
		3: {{Heat: 1, Time: 3.10, Ignored: true}, {Heat: 2, Time: 2.35}, {Heat: 3, Time: 2.36}, {Heat: 4, Time: 2.34}},
	}
	if got := HeatAnomalies(runs); len(got) != 0 {
		t.Errorf("counted a struck-out run as evidence: %+v", got)
	}
}

// A struck-out run must not skew the car's own best and worst either. Here the
// ignored run would have been the car's slowest; with it gone, its heat-1 run
// is, and the heat is unanimous.
func TestAStruckOutRunDoesNotDefineTheCarsWorst(t *testing.T) {
	runs := map[int64][]Run{
		1: {{Heat: 1, Time: 2.90}, {Heat: 2, Time: 2.40}, {Heat: 3, Time: 2.41}, {Heat: 4, Time: 2.39}},
		2: {{Heat: 1, Time: 2.99}, {Heat: 2, Time: 2.50}, {Heat: 3, Time: 2.51}, {Heat: 4, Time: 2.52}},
		3: {{Heat: 1, Time: 2.88}, {Heat: 2, Time: 2.35}, {Heat: 3, Time: 2.36},
			{Heat: 9, Time: 3.50, Ignored: true}},
	}
	got := HeatAnomalies(runs)
	if len(got) != 1 || got[0].Heat != 1 {
		t.Fatalf("heat 1 was not flagged: %+v", got)
	}
}

// The margin is what separates a fault from a coincidence, so it has to be the
// smallest of the cars' gaps — the weakest link in the evidence, not the
// most convenient one.
func TestTheMarginIsTheWeakestCarsGap(t *testing.T) {
	// The cars' fastest heats are deliberately spread apart, so heat 1 is the
	// only unanimous one in either direction.
	runs := map[int64][]Run{
		// Slowest by 0.48
		1: {{Heat: 1, Time: 2.90}, {Heat: 2, Time: 2.40}, {Heat: 3, Time: 2.42}, {Heat: 4, Time: 2.41}},
		// Slowest by 0.40
		2: {{Heat: 1, Time: 2.90}, {Heat: 2, Time: 2.50}, {Heat: 3, Time: 2.48}, {Heat: 4, Time: 2.49}},
		// Slowest by only 0.01 — the weakest link
		3: {{Heat: 1, Time: 2.36}, {Heat: 2, Time: 2.35}, {Heat: 3, Time: 2.30}, {Heat: 4, Time: 2.33}},
	}
	got := HeatAnomalies(runs)
	if len(got) != 1 {
		t.Fatalf("flagged %d heats, want 1", len(got))
	}
	if d := got[0].Margin - 0.01; d > 1e-9 || d < -1e-9 {
		t.Errorf("margin = %.4f, want 0.01 — the smallest gap, not the largest", got[0].Margin)
	}
	// One car barely last is not an outlier against its own form, so this is
	// not a heat to re-run on the strength of the evidence.
	if got[0].Clear {
		t.Error("a heat resting on a 0.01 s gap was reported as clear")
	}
}

// A real fault moves everyone well clear of their own form, and that is the
// case the coordinator should be pointed at first.
func TestAGenuineFaultIsReportedAsClear(t *testing.T) {
	runs := map[int64][]Run{
		1: {{Heat: 1, Time: 2.80}, {Heat: 2, Time: 2.40}, {Heat: 3, Time: 2.41}, {Heat: 4, Time: 2.39}},
		2: {{Heat: 1, Time: 2.90}, {Heat: 2, Time: 2.50}, {Heat: 3, Time: 2.51}, {Heat: 4, Time: 2.52}},
		3: {{Heat: 1, Time: 2.75}, {Heat: 2, Time: 2.35}, {Heat: 3, Time: 2.36}, {Heat: 4, Time: 2.34}},
	}
	got := HeatAnomalies(runs)
	if len(got) != 1 {
		t.Fatalf("flagged %d heats, want 1", len(got))
	}
	if !got[0].Clear {
		t.Errorf("a heat where every car lost ~0.4 s was not reported as clear: %+v", got[0])
	}
	if got[0].Margin < 0.35 {
		t.Errorf("margin = %.3f, want about 0.39", got[0].Margin)
	}
}

// Clear faults sort above coincidences, so the heat to re-run is at the top of
// the list rather than somewhere in it.
func TestClearFaultsAreListedAboveCoincidences(t *testing.T) {
	runs := map[int64][]Run{
		// Heat 1: everyone barely slowest — a coincidence. Their fastest heats
		// differ, so heat 1 is the only unanimous one here.
		1: {{Heat: 1, Time: 2.41}, {Heat: 2, Time: 2.40}, {Heat: 3, Time: 2.38}, {Heat: 4, Time: 2.39}},
		2: {{Heat: 1, Time: 2.53}, {Heat: 2, Time: 2.52}, {Heat: 3, Time: 2.51}, {Heat: 4, Time: 2.50}},
		3: {{Heat: 1, Time: 2.37}, {Heat: 2, Time: 2.36}, {Heat: 3, Time: 2.34}, {Heat: 4, Time: 2.35}},
		// Heat 9: three other cars, all half a second off — a fault.
		4: {{Heat: 9, Time: 2.90}, {Heat: 10, Time: 2.40}, {Heat: 11, Time: 2.41}, {Heat: 12, Time: 2.39}},
		5: {{Heat: 9, Time: 3.00}, {Heat: 10, Time: 2.50}, {Heat: 11, Time: 2.51}, {Heat: 12, Time: 2.52}},
		6: {{Heat: 9, Time: 2.85}, {Heat: 10, Time: 2.35}, {Heat: 11, Time: 2.36}, {Heat: 12, Time: 2.34}},
	}
	got := HeatAnomalies(runs)
	if len(got) != 2 {
		t.Fatalf("flagged %d heats, want 2: %+v", len(got), got)
	}
	if got[0].Heat != 9 || !got[0].Clear {
		t.Errorf("the clear fault is not first: %+v", got)
	}
	if got[1].Clear {
		t.Error("a 0.01 s coincidence was reported as clear")
	}
}

// A car with one run is its own best and worst. Counting it would make every
// heat it appears in look remarkable while saying nothing at all.
func TestACarWithASingleRunIsNotEvidence(t *testing.T) {
	runs := map[int64][]Run{
		1: {{Heat: 1, Time: 2.90}, {Heat: 2, Time: 2.40}, {Heat: 3, Time: 2.41}, {Heat: 4, Time: 2.39}},
		2: {{Heat: 1, Time: 2.99}, {Heat: 2, Time: 2.50}, {Heat: 3, Time: 2.51}, {Heat: 4, Time: 2.52}},
		3: {{Heat: 1, Time: 2.88}},
	}
	if got := HeatAnomalies(runs); len(got) != 0 {
		t.Errorf("a single-run car was treated as evidence: %+v", got)
	}
}

// A whole heat that did not finish is the clearest fault there is, and DNFs
// rewrite to 9.999 — which is every car's slowest by construction.
func TestAHeatWhereNobodyFinishedIsFlagged(t *testing.T) {
	runs := map[int64][]Run{
		1: {{Heat: 1, Time: DNF}, {Heat: 2, Time: 2.40}, {Heat: 3, Time: 2.41}, {Heat: 4, Time: 2.39}},
		2: {{Heat: 1, Time: DNF}, {Heat: 2, Time: 2.50}, {Heat: 3, Time: 2.51}, {Heat: 4, Time: 2.52}},
		3: {{Heat: 1, Time: DNF}, {Heat: 2, Time: 2.35}, {Heat: 3, Time: 2.36}, {Heat: 4, Time: 2.34}},
		4: {{Heat: 1, Time: DNF}, {Heat: 2, Time: 2.60}, {Heat: 3, Time: 2.61}, {Heat: 4, Time: 2.59}},
	}
	got := HeatAnomalies(runs)
	if len(got) != 1 || got[0].Heat != 1 || got[0].Kind != AllSlowest {
		t.Fatalf("a heat nobody finished was not flagged: %+v", got)
	}
}

// If two heats are flagged, the least likely one is the one to re-run first.
func TestTheLeastLikelyHeatIsListedFirst(t *testing.T) {
	runs := map[int64][]Run{
		// Heat 1 has four cars; heat 2 has three of them plus nobody else.
		1: {{Heat: 1, Time: 2.90}, {Heat: 2, Time: 2.39}, {Heat: 3, Time: 2.41}, {Heat: 4, Time: 2.42}},
		2: {{Heat: 1, Time: 2.99}, {Heat: 2, Time: 2.50}, {Heat: 3, Time: 2.51}, {Heat: 4, Time: 2.52}},
		3: {{Heat: 1, Time: 2.88}, {Heat: 2, Time: 2.34}, {Heat: 3, Time: 2.36}, {Heat: 4, Time: 2.37}},
		4: {{Heat: 1, Time: 3.10}, {Heat: 5, Time: 2.60}, {Heat: 6, Time: 2.61}, {Heat: 7, Time: 2.59}},
	}
	got := HeatAnomalies(runs)
	if len(got) < 2 {
		t.Fatalf("expected both heats flagged, got %+v", got)
	}
	if got[0].OneIn < got[1].OneIn {
		t.Errorf("listed 1 in %v before 1 in %v", got[0].OneIn, got[1].OneIn)
	}
}
