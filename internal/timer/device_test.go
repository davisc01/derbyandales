package timer

import (
	"context"
	"strings"
	"testing"
	"time"
)

// These run the whole driver against the simulator, which speaks the real
// FastTrack protocol — echoes, '*' acknowledgements, feature bits, gate
// readings and an unterminated result line. Everything except the copper is
// exercised.

func newTestDevice(t *testing.T, opts SimOptions) (*Device, *Simulator) {
	t.Helper()
	sim := NewSimulator(opts)
	dev := Open(SimulatorProfile(), sim, Options{})
	t.Cleanup(func() { dev.Close() })
	return dev, sim
}

// subscribe opens an event subscription for the life of the test.
func subscribe(t *testing.T, dev *Device) <-chan Event {
	t.Helper()
	events, cancel := dev.Subscribe()
	t.Cleanup(cancel)
	return events
}

// waitFor drains events until one of the wanted kinds arrives.
func waitFor(t *testing.T, dev *Device, timeout time.Duration, kinds ...EventKind) Event {
	t.Helper()
	return waitOn(t, subscribe(t, dev), dev, timeout, kinds...)
}

// waitOn is waitFor against an existing subscription, for tests that must be
// listening before the triggering action happens.
func waitOn(t *testing.T, events <-chan Event, dev *Device, timeout time.Duration, kinds ...EventKind) Event {
	t.Helper()
	want := make(map[EventKind]bool, len(kinds))
	for _, k := range kinds {
		want[k] = true
	}
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("event channel closed while waiting for %v", kinds)
			}
			if want[ev.Kind] {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out after %v waiting for %v (state %s)", timeout, kinds, dev.State())
			return Event{}
		}
	}
}

func TestIdentifyReadsTheTimersVersion(t *testing.T) {
	dev, sim := newTestDevice(t, DefaultSimOptions())

	identity, err := dev.Identify(context.Background())
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if !strings.Contains(identity, "Micro Wizard") || !strings.Contains(identity, "K3") {
		t.Errorf("identity = %q, want the manufacturer and model", identity)
	}
	if !strings.Contains(identity, "15985") {
		t.Errorf("identity = %q, want the serial number", identity)
	}
	if cmds := sim.Commands(); len(cmds) == 0 || cmds[0] != "RV" {
		t.Errorf("probe sent %v, want RV first", cmds)
	}
}

// Probing a port with nothing on it must fail promptly rather than hang, or
// scanning for the timer would stall on the first Bluetooth device it finds.
func TestIdentifyTimesOutOnASilentPort(t *testing.T) {
	dev := Open(SimulatorProfile(), &silentPort{closed: make(chan struct{})}, Options{})
	t.Cleanup(func() { dev.Close() })

	start := time.Now()
	if _, err := dev.Identify(context.Background()); err == nil {
		t.Fatal("expected a timeout on a silent port")
	}
	if elapsed := time.Since(start); elapsed > ProbeTimeout+time.Second {
		t.Errorf("took %v to give up, want about %v", elapsed, ProbeTimeout)
	}
}

// silentPort accepts writes and never answers.
type silentPort struct{ closed chan struct{} }

func (p *silentPort) Write(b []byte) (int, error) { return len(b), nil }
func (p *silentPort) Read(b []byte) (int, error)  { <-p.closed; return 0, nil }
func (p *silentPort) Close() error {
	select {
	case <-p.closed:
	default:
		close(p.closed)
	}
	return nil
}

func TestSetupSendsTheExpectedCommands(t *testing.T) {
	dev, sim := newTestDevice(t, DefaultSimOptions())
	if err := dev.Setup(); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	assertSent(t, sim, "RE", "N1", "N2", "RF")
}

// A four-car heat on a six-lane timer must silence the spare lanes, or they
// report 0.000 and look like two cars that failed to finish.
func TestArmingMasksUnusedLanes(t *testing.T) {
	dev, sim := newTestDevice(t, SimOptions{Lanes: 6, Seed: 1, ResultDelay: 10 * time.Millisecond})

	if err := dev.ArmHeat(0b001111, 6); err != nil {
		t.Fatalf("ArmHeat: %v", err)
	}
	assertSent(t, sim, "MG", "ME", "MF")

	masked := sim.MaskedLanes()
	if len(masked) != 2 || masked[0] != 5 || masked[1] != 6 {
		t.Errorf("masked lanes %v, want [5 6]", masked)
	}
	if dev.State() != StateMark {
		t.Errorf("state = %s, want mark", dev.State())
	}
}

// The whole cycle: arm, stage the cars, drop the gate, collect results.
func TestFullHeatCycle(t *testing.T) {
	opts := DefaultSimOptions()
	opts.ResultDelay = 20 * time.Millisecond
	dev, sim := newTestDevice(t, opts)

	// Subscribe before anything can happen, or a fast simulator finishes the
	// heat before the listener exists.
	events := subscribe(t, dev)

	if err := dev.ArmHeat(0b1111, 4); err != nil {
		t.Fatal(err)
	}

	// Stage the cars. The gate must be polled for the driver to notice, and the
	// reading has to persist to survive the debounce.
	sim.CloseGate()
	pollUntil(t, dev, StateSet, 3*time.Second)

	// Drop the gate.
	sim.OpenGate()
	pollUntil(t, dev, StateRunning, 3*time.Second)

	waitOn(t, events, dev, 3*time.Second, EvRaceFinished)

	results := dev.Finish()
	if len(results) != 4 {
		t.Fatalf("got %d lane results, want 4", len(results))
	}
	seen := map[int]bool{}
	for _, r := range results {
		if r.Lane < 1 || r.Lane > 4 {
			t.Errorf("unexpected lane %d", r.Lane)
		}
		if seen[r.Lane] {
			t.Errorf("lane %d reported twice", r.Lane)
		}
		seen[r.Lane] = true
		if r.Time < 2.0 || r.Time > 3.0 {
			t.Errorf("lane %d time %v is outside the simulated range", r.Lane, r.Time)
		}
		if r.TimerPlace < 1 || r.TimerPlace > 4 {
			t.Errorf("lane %d place %d", r.Lane, r.TimerPlace)
		}
	}
	if dev.State() != StateIdle {
		t.Errorf("state after Finish = %s, want idle", dev.State())
	}
}

// FastTrack does not terminate its final line. Without the inferred line end,
// the last result of every heat would simply never arrive.
func TestUnterminatedResultLineStillArrives(t *testing.T) {
	opts := DefaultSimOptions()
	opts.UnterminatedResults = true // the real behaviour
	opts.ResultDelay = 10 * time.Millisecond
	dev, sim := newTestDevice(t, opts)
	events := subscribe(t, dev)

	if err := dev.ArmHeat(0b1111, 4); err != nil {
		t.Fatal(err)
	}
	sim.CloseGate()
	pollUntil(t, dev, StateSet, 3*time.Second)
	sim.OpenGate()

	waitOn(t, events, dev, 3*time.Second, EvRaceFinished)
	if got := len(dev.Finish()); got != 4 {
		t.Errorf("got %d results from an unterminated line, want 4", got)
	}
}

// A lane that never finishes reads as 9.999 and still completes the heat, so
// one stuck car does not hang the race.
func TestNonFinishingLaneStillCompletesTheHeat(t *testing.T) {
	opts := DefaultSimOptions()
	opts.DNFLanes = []int{3}
	opts.ResultDelay = 10 * time.Millisecond
	dev, sim := newTestDevice(t, opts)
	events := subscribe(t, dev)

	if err := dev.ArmHeat(0b1111, 4); err != nil {
		t.Fatal(err)
	}
	sim.CloseGate()
	pollUntil(t, dev, StateSet, 3*time.Second)
	sim.OpenGate()
	waitOn(t, events, dev, 3*time.Second, EvRaceFinished)

	results := dev.Finish()
	if len(results) != 4 {
		t.Fatalf("got %d results, want 4", len(results))
	}
	for _, r := range results {
		if r.Lane == 3 {
			if r.Time < 9.0 {
				t.Errorf("lane 3 did not finish but reads %v; it should sort last", r.Time)
			}
			if r.TimerPlace != 4 {
				t.Errorf("the non-finishing lane placed %d, want last", r.TimerPlace)
			}
		}
	}
}

// Results arriving with nothing armed mean someone rolled a car down the track
// between heats. Recording them would overwrite a finished result.
func TestResultsAreRefusedWhenNothingIsArmed(t *testing.T) {
	opts := DefaultSimOptions()
	dev, sim := newTestDevice(t, opts)
	events := subscribe(t, dev)

	sim.EmitResults()

	ev := waitOn(t, events, dev, 3*time.Second, EvMalfunction)
	if !strings.Contains(strings.Join(ev.Args, " "), "no heat is armed") {
		t.Errorf("malfunction said %q, want an explanation that nothing was armed", ev.Args)
	}
	if dev.State() != StateIdle {
		t.Errorf("state = %s, want idle", dev.State())
	}
}

// A timer whose feature bits say it cannot be reset over serial must not be
// sent the reset command.
func TestNoLaserResetSuppressesTheResetPoll(t *testing.T) {
	opts := DefaultSimOptions()
	opts.NoLaserReset = true
	dev, sim := newTestDevice(t, opts)
	events := subscribe(t, dev)

	if err := dev.Setup(); err != nil {
		t.Fatal(err)
	}
	waitOn(t, events, dev, 3*time.Second, EvNoLaserReset)

	if dev.CanResetOverSerial() {
		t.Error("the timer said it has no laser reset; the poll should be suppressed")
	}
	before := len(sim.Commands())
	if err := dev.PollReset(); err != nil {
		t.Fatal(err)
	}
	if after := len(sim.Commands()); after != before {
		t.Errorf("PollReset sent something despite the feature being absent: %v",
			sim.Commands()[before:])
	}
}

// On a track with a powered release gate, the reset line is what launches the
// cars. Polling it would start the race by itself.
func TestAutomaticGateSuppressesTheResetPoll(t *testing.T) {
	sim := NewSimulator(DefaultSimOptions())
	dev := Open(SimulatorProfile(), sim, Options{AutomaticGateRelease: true})
	t.Cleanup(func() { dev.Close() })

	if dev.CanResetOverSerial() {
		t.Fatal("with an automatic gate fitted, the reset poll must be suppressed")
	}
	before := len(sim.Commands())
	if err := dev.PollReset(); err != nil {
		t.Fatal(err)
	}
	if after := len(sim.Commands()); after != before {
		t.Error("PollReset sent the reset command; this would release the cars")
	}
}

func TestRemoteStartReleasesTheCars(t *testing.T) {
	opts := DefaultSimOptions()
	opts.ResultDelay = 10 * time.Millisecond
	sim := NewSimulator(opts)
	dev := Open(SimulatorProfile(), sim, Options{AutomaticGateRelease: true})
	t.Cleanup(func() { dev.Close() })
	events := subscribe(t, dev)

	if err := dev.ArmHeat(0b1111, 4); err != nil {
		t.Fatal(err)
	}
	sim.CloseGate()
	pollUntil(t, dev, StateSet, 3*time.Second)

	if err := dev.RemoteStart(); err != nil {
		t.Fatalf("RemoteStart: %v", err)
	}
	assertSent(t, sim, "LG")
	waitOn(t, events, dev, 3*time.Second, EvRaceFinished)
}

// A timer with the gate feature switched off answers "X". Racing still works,
// but there is no "ready" indication, and the bench should be able to say so.
func TestGateNotSupportedIsDetected(t *testing.T) {
	opts := DefaultSimOptions()
	opts.GateUnsupported = true
	dev, _ := newTestDevice(t, opts)
	events := subscribe(t, dev)

	if err := dev.PollGate(); err != nil {
		t.Fatal(err)
	}
	waitOn(t, events, dev, 3*time.Second, EvGateNotSupported)
	if dev.GateKnowable() {
		t.Error("the timer said the gate feature is off; GateKnowable should be false")
	}
}

// Losing the cable mid-session must be noticed, not silently ignored.
func TestLostConnectionIsReported(t *testing.T) {
	dev, sim := newTestDevice(t, DefaultSimOptions())
	events := subscribe(t, dev)
	sim.Close()
	waitOn(t, events, dev, 3*time.Second, EvLostConnection)
}

func TestTraceRecordsBothDirections(t *testing.T) {
	trace := NewTrace()
	sim := NewSimulator(DefaultSimOptions())
	dev := Open(SimulatorProfile(), sim, Options{Trace: trace})
	t.Cleanup(func() { dev.Close() })

	if _, err := dev.Identify(context.Background()); err != nil {
		t.Fatal(err)
	}

	lines := trace.Lines()
	var sent, received int
	for _, l := range lines {
		if l.Out {
			sent++
		} else {
			received++
		}
	}
	if sent == 0 {
		t.Error("trace recorded nothing sent")
	}
	if received == 0 {
		t.Error("trace recorded nothing received")
	}
	if !strings.Contains(trace.String(), "Micro Wizard") {
		t.Errorf("trace should contain the timer's reply:\n%s", trace.String())
	}
}

// --- helpers -----------------------------------------------------------------

// pollUntil polls the gate the way the race loop does, until the state changes.
func pollUntil(t *testing.T, dev *Device, want State, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if dev.State() == want {
			return
		}
		if err := dev.PollGate(); err != nil {
			t.Fatalf("PollGate: %v", err)
		}
		time.Sleep(60 * time.Millisecond)
	}
	t.Fatalf("state is %s after %v, want %s", dev.State(), timeout, want)
}

func assertSent(t *testing.T, sim *Simulator, want ...string) {
	t.Helper()
	got := sim.Commands()
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("command %q was never sent; sent %v", w, got)
		}
	}
}
