package scoring

import (
	"math"
	"testing"
)

// The club's track: 28 feet, cars at 1:25 scale.
const (
	trackFt = 28.0
	scale   = 25
)

// These are real rows from the club's published results. If this test fails,
// the website's numbers and the app's numbers have diverged — which someone
// would notice, because the MPH figure is the number the crowd reacts to.
//
// Source: derby-site/content/races/2026/race-4/heats.csv
func TestScaleMPHMatchesPublishedResults(t *testing.T) {
	cases := []struct {
		seconds float64
		want    string
	}{
		{2.434, "196.1"},
		{2.76, "172.9"},
		{2.54, "187.9"},
		{2.475, "192.8"},
		{2.468, "193.4"},
	}
	for _, c := range cases {
		got := FormatMPH(ScaleMPH(trackFt, scale, c.seconds))
		if got != c.want {
			t.Errorf("%.3fs -> %s mph, want %s", c.seconds, got, c.want)
		}
	}
}

// Guard the track length itself. DerbyNet's default is 40 ft; the club's track
// is 28. Getting this wrong inflates every speed by 43% and nothing else would
// catch it, because the times stay correct.
func TestWrongTrackLengthIsDetectable(t *testing.T) {
	correct := ScaleMPH(28, scale, 2.434)
	derbynetDefault := ScaleMPH(40, scale, 2.434)
	if math.Abs(correct-196.1) > 0.05 {
		t.Errorf("28 ft gives %.1f, want 196.1", correct)
	}
	if math.Abs(derbynetDefault-280.1) > 0.05 {
		t.Errorf("40 ft gives %.1f, want 280.1", derbynetDefault)
	}
}

func TestScaleMPHRejectsNonsense(t *testing.T) {
	if got := ScaleMPH(trackFt, scale, 0); got != 0 {
		t.Errorf("zero time gave %v, want 0 rather than an infinity", got)
	}
	if got := ScaleMPH(trackFt, scale, -1); got != 0 {
		t.Errorf("negative time gave %v, want 0", got)
	}
	if got := ScaleMPH(0, scale, 2.4); got != 0 {
		t.Errorf("zero track length gave %v, want 0", got)
	}
}

// Faster is faster: halving the time doubles the speed.
func TestScaleMPHIsInverselyProportionalToTime(t *testing.T) {
	fast := ScaleMPH(trackFt, scale, 1.2)
	slow := ScaleMPH(trackFt, scale, 2.4)
	if math.Abs(fast-2*slow) > 1e-9 {
		t.Errorf("%.4f is not twice %.4f", fast, slow)
	}
}

// The website's CSVs trim trailing zeros on finish times: 2.760 is written
// "2.76". Matching that keeps the published tables byte-identical.
func TestFormatTimeMatchesWebsiteStyle(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{2.760, "2.76"},
		{2.434, "2.434"},
		{2.500, "2.5"},
		{3.000, "3"},
		{2.4009, "2.401"},
	}
	for _, c := range cases {
		if got := FormatTime(c.in); got != c.want {
			t.Errorf("FormatTime(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Averages trim their trailing zeros, matching the club's published standings:
// 2026 race 4 writes Greg Thrift's average as "2.43", not "2.430".
func TestFormatAverageTrimsTrailingZeros(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{2.367, "2.367"},
		{2.43, "2.43"},
		{2.400, "2.4"},
		{2.4166666666666665, "2.417"},
	}
	for _, c := range cases {
		if got := FormatAverage(c.in); got != c.want {
			t.Errorf("FormatAverage(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The website writes "194", not "194.0".
func TestFormatMPHDropsTrailingZero(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{194.0, "194"},
		{196.14, "196.1"},
		{200.0, "200"},
	}
	for _, c := range cases {
		if got := FormatMPH(c.in); got != c.want {
			t.Errorf("FormatMPH(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A full heat from the published results, end to end: times in, places and
// speeds out, matching what is on the website today.
//
// Source: derby-site/content/races/2026/race-4/heats.csv, heat 1.
func TestPublishedHeatReproduces(t *testing.T) {
	times := map[int]float64{
		1: 2.76,  // Derby Ales, CONTROL
		2: 2.54,  // Will Jaggers, Tiger Stripe
		3: 2.434, // Chris Bryan, Cracked Up
		4: 2.475, // Chance Greer, CHUD
	}
	wantPlace := map[int]int{1: 4, 2: 3, 3: 1, 4: 2}
	wantMPH := map[int]string{1: "172.9", 2: "187.9", 3: "196.1", 4: "192.8"}

	places := PlaceInHeat(times)
	for lane, seconds := range times {
		if places[lane] != wantPlace[lane] {
			t.Errorf("lane %d placed %d, want %d", lane, places[lane], wantPlace[lane])
		}
		if got := FormatMPH(ScaleMPH(trackFt, scale, seconds)); got != wantMPH[lane] {
			t.Errorf("lane %d speed %s, want %s", lane, got, wantMPH[lane])
		}
	}
}
