package timer

import (
	"os"
	"strings"
	"testing"
)

// The club's own timer, recorded over the serial port on 2026-09-20: a Micro
// Wizard K1 from 2004, firmware 1.09D, serial 29596.
//
// It is here because this timer disagrees with the K3 the rest of the tests are
// written against, and every disagreement has already cost a race night or an
// afternoon: it refuses the enhanced format, refuses to report its start
// switch, and answers the unmask command without a newline.
const k1TracePath = "testdata/fasttrack-k1-2004.trace"

// replies returns what the timer said, in order, from a saved trace.
func replies(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(k1TracePath)
	if err != nil {
		t.Fatalf("reading the recorded session: %v", err)
	}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		i := strings.Index(line, "<< ")
		if i < 0 {
			continue
		}
		text := line[i+3:]
		// The trace annotates a line the reader had to complete itself.
		if j := strings.Index(text, "   (no newline"); j >= 0 {
			text = text[:j]
		}
		out = append(out, strings.TrimSpace(text))
	}
	return out
}

func sawReply(t *testing.T, want string) {
	t.Helper()
	for _, r := range replies(t) {
		if r == want {
			return
		}
	}
	t.Fatalf("the recorded session has no %q; re-record it if the hardware changed", want)
}

// This timer answers the gate query "X". Nothing in the software may treat that
// as a gate reading, and the operator must not be told to check wiring.
func TestRecordedK1RefusesTheGateQuery(t *testing.T) {
	sawReply(t, "X")

	p := FastTrack()
	if !gateNotSupportedReply(p, "X") {
		t.Error(`"X" must be recognised as the timer refusing the gate query`)
	}
	for _, det := range p.GateWatcher.Detectors {
		if det.Event == EvGateNotSupported {
			continue
		}
		if _, _, ok := det.Apply("X"); ok {
			t.Errorf(`"X" also matched the %s detector; it would be read as a gate state`, det.Event)
		}
	}
}

// "AC" is the unmask command's acknowledgement and arrives with no newline, so
// the reader only completes it after NewlineTimeout. It must not look like a
// result, or a stray lane appears in a heat nobody ran.
func TestRecordedK1UnmaskAcknowledgementIsNotAResult(t *testing.T) {
	sawReply(t, "AC")

	for _, det := range FastTrack().Matchers {
		if _, _, ok := det.Apply("AC"); ok {
			t.Errorf("%q matched the %s detector", "AC", det.Event)
		}
	}
}

// RM's mode line ends in "1" and N2's refusal is "X" — both would be read as
// gate readings if the gate detectors were live when they arrived. They are
// only live in the window after a poll, which is why setup runs before polling
// starts.
func TestRecordedK1SetupRepliesWouldReadAsGateStates(t *testing.T) {
	const modeLine = "0 000000 0 0 1"
	sawReply(t, modeLine)

	var matched bool
	for _, det := range FastTrack().GateWatcher.Detectors {
		if _, _, ok := det.Apply(modeLine); ok {
			matched = true
		}
	}
	if !matched {
		t.Skip("the mode line no longer collides with the gate patterns")
	}
	// It does collide, so the only protection is the window. Assert the window
	// is shut by default: a freshly opened device must not be listening.
	dev, _ := newTestDevice(t, DefaultSimOptions())
	dev.mu.Lock()
	window := dev.gateWindow
	dev.mu.Unlock()
	if !window.IsZero() {
		t.Error("the gate detectors are live before anything has polled the gate")
	}
}

// The identity this timer reports has to satisfy the prober, or it cannot be
// recognised at all. Its copyright line differs from the K3's.
func TestRecordedK1Identifies(t *testing.T) {
	sawReply(t, "Copyright (C) 2004 Micro Wizard")
	sawReply(t, "K1 Version 1.09D Serial Number 29596")

	joined := "Copyright (C) 2004 Micro Wizard K1 Version 1.09D Serial Number 29596"
	for _, re := range FastTrack().Prober.Responses {
		if !re.MatchString("Copyright (C) 2004 Micro Wizard") &&
			!re.MatchString("K1 Version 1.09D Serial Number 29596") {
			t.Errorf("prober pattern %v matches neither line this timer sends", re)
		}
	}
	if got := SummariseIdentity(joined); got != "Micro Wizard K1, serial 29596" {
		t.Errorf("SummariseIdentity = %q", got)
	}
}
