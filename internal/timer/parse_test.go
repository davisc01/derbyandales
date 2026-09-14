package timer

import (
	"math"
	"testing"
)

// The exact result line from a real Micro Wizard K3, taken from DerbyNet's
// recorded trace (timer/testing/testdata/fasttrack-mark-set.playback).
//
// Every detail here is load-bearing: six lanes on a four-lane race, two of them
// masked and still reporting, place characters counting up from ASCII 33, and
// a double space before F.
const realK3Result = `A=4.009" B=3.261! C=4.129$ D=4.013# E=0.000  F=0.000`

// One line carries every lane, so the detector has to match repeatedly. It does
// that by excising what it matched — get that wrong and you either loop forever
// or only ever see lane A.
func TestOneResultLineYieldsEveryLane(t *testing.T) {
	det := FastTrack().Matchers[0]

	var got []LaneResult
	line := realK3Result
	for {
		ev, remaining, ok := det.Apply(line)
		if !ok {
			break
		}
		if remaining == line {
			t.Fatal("detector matched without consuming anything; this would loop forever")
		}
		line = remaining
		r, err := ParseLaneResult(ev.Args)
		if err != nil {
			t.Fatalf("parse %v: %v", ev.Args, err)
		}
		got = append(got, r)
	}

	if len(got) != 6 {
		t.Fatalf("got %d lane results, want 6", len(got))
	}

	want := []struct {
		lane  int
		time  float64
		place int
	}{
		{1, 4.009, 2},
		{2, 3.261, 1},
		{3, 4.129, 4},
		{4, 4.013, 3},
		{5, 9.999, 0}, // masked: 0.000 becomes 9.999, no place character
		{6, 9.999, 0},
	}
	for i, w := range want {
		if got[i].Lane != w.lane {
			t.Errorf("result %d: lane %d, want %d", i, got[i].Lane, w.lane)
		}
		if math.Abs(got[i].Time-w.time) > 1e-9 {
			t.Errorf("lane %d: time %v, want %v", w.lane, got[i].Time, w.time)
		}
		if got[i].TimerPlace != w.place {
			t.Errorf("lane %d: timer place %d, want %d", w.lane, got[i].TimerPlace, w.place)
		}
	}
}

// The places the timer reports must agree with the order of the times it
// reports. If they ever disagree, something is wrong with the hardware and the
// test bench should say so rather than quietly trusting one of them.
func TestTimerPlacesAgreeWithTimes(t *testing.T) {
	det := FastTrack().Matchers[0]
	line := realK3Result

	byLane := map[int]LaneResult{}
	for {
		ev, remaining, ok := det.Apply(line)
		if !ok {
			break
		}
		line = remaining
		r, _ := ParseLaneResult(ev.Args)
		byLane[r.Lane] = r
	}

	// Lanes 1-4 raced; B was fastest at 3.261 and the timer called it first.
	order := []int{2, 1, 4, 3} // by time: 3.261, 4.009, 4.013, 4.129
	for i, lane := range order {
		if got := byLane[lane].TimerPlace; got != i+1 {
			t.Errorf("lane %d: timer said place %d, but its time ranks %d",
				lane, got, i+1)
		}
	}
}

// A lane that never finished reads as zero. Left alone it would look like the
// fastest run of the night.
func TestZeroesToNines(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0.000", "9.999"},
		{"0.0000", "9.9999"},
		{"0.00", "9.99"},
		{"2.434", "2.434"},
		{"0.001", "0.001"}, // a real, very fast reading is left alone
		{"", ""},
	}
	for _, c := range cases {
		if got := ZeroesToNines(c.in); got != c.want {
			t.Errorf("ZeroesToNines(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDecodeLane(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{in: "A", want: 1}, {in: "B", want: 2}, {in: "F", want: 6},
		{in: "a", want: 1},
		{in: "1", want: 1}, {in: "4", want: 4},
		{in: "", wantErr: true},
		{in: "-", wantErr: true},
	}
	for _, c := range cases {
		got, err := DecodeLane(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("DecodeLane(%q) should have failed", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("DecodeLane(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("DecodeLane(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestDecodePlace(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"!", 1}, {`"`, 2}, {"#", 3}, {"$", 4},
		{"", 0},
		{" ", 0},
	}
	for _, c := range cases {
		if got := DecodePlace(c.in); got != c.want {
			t.Errorf("DecodePlace(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// Masking is the one place where the sense of a lane bitmask flips, so it gets
// its own test. A four-car heat on a six-lane timer must silence lanes 5 and 6,
// or they report 0.000 and look like two cars that failed to finish.
func TestMaskCommands(t *testing.T) {
	p := FastTrack()

	// All four lanes in use on a six-lane timer.
	got := p.MaskCommands(0b1111, 6)
	want := []string{"MG", "ME", "MF"}
	assertCommands(t, got, want)

	// Lane 3 empty because of a bye.
	got = p.MaskCommands(0b1011, 4)
	assertCommands(t, got, []string{"MG", "MC"})

	// Every lane in use: nothing to mask.
	assertCommands(t, p.MaskCommands(0b1111, 4), []string{"MG"})

	// Head-to-head bracket heat on lanes 1 and 2.
	assertCommands(t, p.MaskCommands(0b0011, 4), []string{"MG", "MC", "MD"})
}

func assertCommands(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("command %d = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// The feature bits say whether the timer can be reset over the serial line.
// Sending a reset to one that cannot is harmless; failing to notice one that
// can is how heats stop arming halfway through a race.
func TestFeatureBitsDetectMissingLaserReset(t *testing.T) {
	det := FastTrack().SetupDetectors[0]

	// The bits are written most-significant first, so the third character is
	// bit 6, "laser reset from computer". These two strings differ only there.
	if _, _, ok := det.Apply("1001 0111"); !ok {
		t.Error("bit 6 clear should be detected as a timer without laser reset")
	}
	if _, _, ok := det.Apply("1011 0111"); ok {
		t.Error("bit 6 set means reset is available; it must not be flagged as missing")
	}
	// The same reply without the separating space, which some firmware sends.
	if _, _, ok := det.Apply("10010111"); !ok {
		t.Error("an unspaced feature reply should parse the same way")
	}
}

// The gate patterns are loose by necessity — "0$" matches any line ending in
// zero. This documents why they must only be applied inside the reply window.
func TestGatePatternsAreLooseAndNeedWindowing(t *testing.T) {
	gate := FastTrack().GateWatcher.Detectors

	var open, closed Detector
	for _, d := range gate {
		switch d.Event {
		case EvGateOpen:
			open = d
		case EvGateClosed:
			closed = d
		}
	}

	if _, _, ok := open.Apply("RG0"); !ok {
		t.Error("RG0 should read as gate open")
	}
	if _, _, ok := closed.Apply("RG1"); !ok {
		t.Error("RG1 should read as gate closed")
	}

	// The reason for the window: a serial number ending in zero also matches.
	if _, _, ok := open.Apply("K3 Version 1.05A  Serial Number 15980"); !ok {
		t.Skip("pattern no longer matches a bare trailing zero; windowing may be unnecessary")
	} else {
		t.Log("confirmed: the gate-open pattern matches a serial number ending in 0, " +
			"which is why gate detectors are only live just after an RG")
	}
}
