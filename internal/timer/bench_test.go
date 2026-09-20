package timer

import (
	"context"
	"strings"
	"testing"
	"time"
)

func benchCheck(t *testing.T, r Result, id CheckID) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("check %q missing from result (have %d checks)", id, len(r.Checks))
	return Check{}
}

// A healthy timer should sail through every automatic check.
func TestBenchPassesOnAHealthyTimer(t *testing.T) {
	dev, _ := newTestDevice(t, DefaultSimOptions())
	b := NewBench(dev, "", 4, nil)

	r := b.RunAutomatic(context.Background())

	for _, id := range []CheckID{CheckIdentify, CheckFeatures, CheckLaneCount, CheckMask, CheckLatency} {
		c := benchCheck(t, r, id)
		if c.Verdict != VerdictPass {
			t.Errorf("%s: verdict %q, detail %q", id, c.Verdict, c.Detail)
		}
	}

	// The interactive ones need someone at the track and must be left pending
	// rather than claimed as passing.
	for _, id := range []CheckID{CheckGate, CheckLaneMapping, CheckTestHeat} {
		c := benchCheck(t, r, id)
		if c.Verdict != VerdictPending {
			t.Errorf("%s: verdict %q, want pending", id, c.Verdict)
		}
		if !c.Interactive {
			t.Errorf("%s should be marked interactive", id)
		}
	}
}

// The identify check must name the model and serial number, because that is
// what tells you whether you are talking to the timer you think you are.
func TestBenchIdentifiesTheModel(t *testing.T) {
	dev, _ := newTestDevice(t, DefaultSimOptions())
	b := NewBench(dev, "/dev/cu.usbserial-1420", 4, nil)

	c := benchCheck(t, b.RunAutomatic(context.Background()), CheckIdentify)
	if !strings.Contains(c.Detail, "K3") {
		t.Errorf("detail %q should name the model", c.Detail)
	}
	if !strings.Contains(c.Detail, "15985") {
		t.Errorf("detail %q should include the serial number", c.Detail)
	}
}

// A timer that does not answer must fail, and say what to check.
func TestBenchFailsWhenTheTimerIsSilent(t *testing.T) {
	dev := Open(SimulatorProfile(), &silentPort{closed: make(chan struct{})}, Options{})
	t.Cleanup(func() { dev.Close() })

	b := NewBench(dev, "/dev/cu.usbserial-1420", 4, nil)
	r := b.RunAutomatic(context.Background())

	c := benchCheck(t, r, CheckIdentify)
	if c.Verdict != VerdictFail {
		t.Fatalf("verdict %q, want fail", c.Verdict)
	}
	if !strings.Contains(c.Detail, "cable") {
		t.Errorf("detail %q should suggest what to check", c.Detail)
	}
	if r.Passed() {
		t.Error("a run with a failed check must not report as passed")
	}
	if r.Ready() {
		t.Error("a failed run must not be ready to race without an override")
	}
}

// The feature bits are decoded into English so the operator does not have to
// count binary digits at a brewery.
func TestBenchDecodesFeatureBits(t *testing.T) {
	dev, _ := newTestDevice(t, DefaultSimOptions())
	b := NewBench(dev, "", 4, nil)

	c := benchCheck(t, b.RunAutomatic(context.Background()), CheckFeatures)
	if !strings.Contains(c.Evidence, "Laser reset from computer") {
		t.Errorf("evidence should name the features in English:\n%s", c.Evidence)
	}
	if !strings.Contains(c.Evidence, "Mask lanes") {
		t.Errorf("evidence should mention lane masking:\n%s", c.Evidence)
	}
}

// Racing four lanes on a timer that only supports two is a setup error worth
// catching before check-in rather than at heat one.
func TestBenchFailsOnTooManyLanes(t *testing.T) {
	dev, _ := newTestDevice(t, DefaultSimOptions())
	dev.Profile().MaxLanes = 2

	b := NewBench(dev, "", 4, nil)
	c := benchCheck(t, b.RunAutomatic(context.Background()), CheckLaneCount)
	if c.Verdict != VerdictFail {
		t.Errorf("verdict %q, want fail — the season wants more lanes than the timer has", c.Verdict)
	}
}

// A timer that cannot be reset over serial is still a usable timer; the check
// should skip with an explanation, not fail.
func TestBenchSkipsResetWithAReason(t *testing.T) {
	opts := DefaultSimOptions()
	opts.NoLaserReset = true
	dev, _ := newTestDevice(t, opts)

	b := NewBench(dev, "", 4, nil)
	r := b.RunAutomatic(context.Background())

	c := benchCheck(t, r, CheckReset)
	if c.Verdict != VerdictSkipped {
		t.Errorf("verdict %q, want skipped", c.Verdict)
	}
	if !strings.Contains(c.Detail, "feature bits") {
		t.Errorf("detail %q should explain which of the two reasons applies", c.Detail)
	}
	if !r.Passed() {
		t.Error("a skipped reset must not fail the run — the timer still works")
	}
}

// The other reason for skipping is an automatic release gate, and it needs a
// different explanation because the consequence is different.
func TestBenchExplainsAutomaticGateSuppression(t *testing.T) {
	sim := NewSimulator(DefaultSimOptions())
	dev := Open(SimulatorProfile(), sim, Options{AutomaticGateRelease: true})
	t.Cleanup(func() { dev.Close() })

	b := NewBench(dev, "", 4, nil)
	c := benchCheck(t, b.RunAutomatic(context.Background()), CheckReset)

	if c.Verdict != VerdictSkipped {
		t.Errorf("verdict %q, want skipped", c.Verdict)
	}
	if !strings.Contains(c.Detail, "launches the cars") {
		t.Errorf("detail %q should explain why the reset is dangerous here", c.Detail)
	}
}

// Correct wiring: a car down lane 3 is reported as lane 3.
func TestLaneMappingPassesWhenWiringIsCorrect(t *testing.T) {
	opts := DefaultSimOptions()
	dev, sim := newTestDevice(t, opts)
	b := NewBench(dev, "", 4, nil)

	go func() {
		time.Sleep(150 * time.Millisecond)
		sim.EmitSingleLane(3, 2.45)
	}()

	c := b.CheckLaneMapping(context.Background(), 3, 3*time.Second)
	if c.Verdict != VerdictPass {
		t.Fatalf("verdict %q, detail %q", c.Verdict, c.Detail)
	}
}

// Reversed wiring is the failure this check exists for. Without it, a whole
// race gets recorded backwards before anyone notices.
func TestLaneMappingCatchesReversedWiring(t *testing.T) {
	dev, sim := newTestDevice(t, DefaultSimOptions())
	b := NewBench(dev, "", 4, nil)

	// The operator was asked for lane 3; the timer reports lane 2, because the
	// lanes are wired in the opposite order.
	go func() {
		time.Sleep(150 * time.Millisecond)
		sim.EmitSingleLane(2, 2.45)
	}()

	c := b.CheckLaneMapping(context.Background(), 3, 3*time.Second)
	if c.Verdict != VerdictFail {
		t.Fatalf("verdict %q, want fail — the wiring is reversed", c.Verdict)
	}
	if !strings.Contains(c.Detail, "lane 2") || !strings.Contains(c.Detail, "lane 3") {
		t.Errorf("detail %q should name both the expected and reported lane", c.Detail)
	}
	if !strings.Contains(c.Detail, "reversed lanes") {
		t.Errorf("detail %q should suggest the fix", c.Detail)
	}
}

// No car at all is a different failure from the wrong lane, and says so.
func TestLaneMappingFailsWhenNothingRuns(t *testing.T) {
	dev, _ := newTestDevice(t, DefaultSimOptions())
	b := NewBench(dev, "", 4, nil)

	c := b.CheckLaneMapping(context.Background(), 3, 400*time.Millisecond)
	if c.Verdict != VerdictFail {
		t.Fatalf("verdict %q, want fail", c.Verdict)
	}
	if !strings.Contains(c.Detail, "finish beam") {
		t.Errorf("detail %q should suggest what went wrong", c.Detail)
	}
}

// The test heat exercises the whole path and discards the result.
func TestBenchTestHeatRunsAndDiscards(t *testing.T) {
	opts := DefaultSimOptions()
	opts.ResultDelay = 50 * time.Millisecond
	dev, sim := newTestDevice(t, opts)
	b := NewBench(dev, "", 4, nil)

	go func() {
		time.Sleep(200 * time.Millisecond)
		sim.CloseGate()
		time.Sleep(700 * time.Millisecond)
		sim.OpenGate()
	}()

	c := b.RunTestHeat(context.Background(), 5*time.Second)
	if c.Verdict != VerdictPass {
		t.Fatalf("verdict %q, detail %q", c.Verdict, c.Detail)
	}
	if !strings.Contains(c.Evidence, "lane 1") {
		t.Errorf("evidence should show the per-lane times:\n%s", c.Evidence)
	}
	// Discarded: the device is back to idle with nothing retained.
	if dev.State() != StateIdle {
		t.Errorf("state after the test heat = %s, want idle", dev.State())
	}
}

func TestBenchTestHeatFailsWhenNothingHappens(t *testing.T) {
	dev, _ := newTestDevice(t, DefaultSimOptions())
	b := NewBench(dev, "", 4, nil)

	c := b.RunTestHeat(context.Background(), 500*time.Millisecond)
	if c.Verdict != VerdictFail {
		t.Errorf("verdict %q, want fail", c.Verdict)
	}
}

// The gate check needs to see it move both ways.
func TestGateWatchSeesBothTransitions(t *testing.T) {
	dev, sim := newTestDevice(t, DefaultSimOptions())
	b := NewBench(dev, "", 4, nil)

	go func() {
		time.Sleep(300 * time.Millisecond)
		sim.CloseGate()
		time.Sleep(1200 * time.Millisecond)
		sim.OpenGate()
	}()

	c := b.WatchGate(context.Background(), 6*time.Second)
	if c.Verdict != VerdictPass {
		t.Fatalf("verdict %q, detail %q", c.Verdict, c.Detail)
	}
	if !strings.Contains(c.Detail, "closed") || !strings.Contains(c.Detail, "open") {
		t.Errorf("detail %q should report both transitions", c.Detail)
	}
}

// A gate that never moves is the commonest race-night failure there is.
func TestGateWatchFailsWhenTheGateNeverMoves(t *testing.T) {
	dev, _ := newTestDevice(t, DefaultSimOptions())
	b := NewBench(dev, "", 4, nil)

	c := b.WatchGate(context.Background(), 900*time.Millisecond)
	if c.Verdict != VerdictFail {
		t.Fatalf("verdict %q, want fail", c.Verdict)
	}
	if !strings.Contains(c.Detail, "wiring") {
		t.Errorf("detail %q should suggest what to check", c.Detail)
	}
}

func TestGateWatchSkipsWhenUnsupported(t *testing.T) {
	opts := DefaultSimOptions()
	opts.GateUnsupported = true
	dev, _ := newTestDevice(t, opts)
	events := subscribe(t, dev)

	if err := dev.PollGate(); err != nil {
		t.Fatal(err)
	}
	waitOn(t, events, dev, 2*time.Second, EvGateNotSupported)

	b := NewBench(dev, "", 4, nil)
	c := b.WatchGate(context.Background(), time.Second)
	if c.Verdict != VerdictSkipped {
		t.Errorf("verdict %q, want skipped", c.Verdict)
	}
	if !strings.Contains(c.Detail, "no 'ready' indication") {
		t.Errorf("detail %q should explain the consequence", c.Detail)
	}
}

// A jammed gate switch must never stop a race from happening. It should make
// the decision explicit instead.
func TestOverrideMakesAFailedRunReady(t *testing.T) {
	r := Result{
		StartedAt: time.Now(),
		Checks:    []Check{{ID: CheckGate, Verdict: VerdictFail, Detail: "gate never moved"}},
	}
	if r.Ready() {
		t.Fatal("a failed run is not ready")
	}
	if len(r.Failures()) != 1 {
		t.Errorf("Failures() returned %d, want 1", len(r.Failures()))
	}

	r.OverriddenBy = "coordinator"
	r.OverrideReason = "gate switch jammed; starting heats by hand"
	if !r.Ready() {
		t.Error("an explicitly overridden run should be ready to race")
	}
	if r.Passed() {
		t.Error("an override must not make the run claim to have passed")
	}
}

// Equipment gets unplugged between the afternoon setup and the first heat.
func TestResultGoesStale(t *testing.T) {
	fresh := &Result{StartedAt: time.Now(), Checks: []Check{{Verdict: VerdictPass}}}
	if fresh.Stale(MaxBenchAge) {
		t.Error("a result from just now is not stale")
	}

	old := &Result{StartedAt: time.Now().Add(-MaxBenchAge - time.Minute)}
	if !old.Stale(MaxBenchAge) {
		t.Error("a result older than the window should be stale")
	}

	var missing *Result
	if !missing.Stale(MaxBenchAge) {
		t.Error("never having run the bench counts as stale")
	}
	if missing.Ready() {
		t.Error("never having run the bench is not ready")
	}
}

// An empty run has not proved anything, so it must not read as passing.
func TestEmptyResultDoesNotPass(t *testing.T) {
	var r Result
	if r.Passed() {
		t.Error("a result with no checks must not report as passed")
	}
}

// The club's K1 echoed the gate query and answered nothing, and the bench
// reported it as "the timer did not answer a status request" — which sends
// somebody to change a cable that is fine. An echo is proof the timer is
// listening; only the start switch is missing.
func TestBenchTellsAnEchoedGateQueryFromASilentTimer(t *testing.T) {
	opts := DefaultSimOptions()
	opts.GateSilent = true
	dev, _ := newTestDevice(t, opts)
	b := NewBench(dev, "", 4, nil)

	r := b.RunAutomatic(context.Background())

	if c := benchCheck(t, r, CheckLatency); c.Verdict != VerdictPass {
		t.Errorf("response time: verdict %q, detail %q — the timer answers, "+
			"so the link is fine", c.Verdict, c.Detail)
	}

	c := benchCheck(t, r, CheckStartSwitch)
	if c.Verdict != VerdictWarn {
		t.Fatalf("start switch: verdict %q, want warn", c.Verdict)
	}
	if !strings.Contains(c.Detail, "echoes") {
		t.Errorf("detail %q should say the timer is answering, just not about the gate", c.Detail)
	}
	if !strings.Contains(c.Evidence, "echoed back") {
		t.Errorf("evidence %q should count the echoes rather than report silence", c.Evidence)
	}
	if !strings.Contains(c.Detail, "Racing still works") {
		t.Errorf("detail %q should say what it costs, which is not the race", c.Detail)
	}
	if r.Passed() && !r.Ready() {
		t.Error("an unreadable gate must not stop the night")
	}

	// And the interactive gate check must now skip with the reason rather than
	// blame the wiring.
	if dev.GateKnowable() {
		t.Fatal("the gate should be recorded as unreadable")
	}
	g := b.WatchGate(context.Background(), 300*time.Millisecond)
	if g.Verdict != VerdictSkipped {
		t.Errorf("gate check: verdict %q, detail %q, want skipped", g.Verdict, g.Detail)
	}
}

// "X" is a different sentence to say to the operator than silence is. The
// club's 2004 K1 answers X, and it also refuses N2 — the enhanced format that
// carries the start-switch status — so on that timer this is firmware, not a
// setting anyone can turn back on. Saying "switched off" would send somebody
// hunting through a menu that has no such entry.
func TestBenchReportsATimerThatWillNotReportItsStartSwitch(t *testing.T) {
	opts := DefaultSimOptions()
	opts.GateUnsupported = true
	dev, _ := newTestDevice(t, opts)
	b := NewBench(dev, "", 4, nil)

	c := benchCheck(t, b.RunAutomatic(context.Background()), CheckStartSwitch)
	if c.Verdict != VerdictWarn {
		t.Fatalf("verdict %q, want warn", c.Verdict)
	}
	if !strings.Contains(c.Detail, "will not report its start switch") {
		t.Errorf("detail %q should say what the timer will not do", c.Detail)
	}
	if !strings.Contains(c.Detail, "not a setting") {
		t.Errorf("detail %q should not leave the operator looking for a setting", c.Detail)
	}
	if dev.GateKnowable() {
		t.Error("an X reply must leave the gate recorded as unreadable")
	}
}

// A healthy timer reports its start switch, and says so.
func TestBenchPassesStartSwitchOnAHealthyTimer(t *testing.T) {
	dev, _ := newTestDevice(t, DefaultSimOptions())
	b := NewBench(dev, "", 4, nil)

	c := benchCheck(t, b.RunAutomatic(context.Background()), CheckStartSwitch)
	if c.Verdict != VerdictPass {
		t.Fatalf("verdict %q, detail %q", c.Verdict, c.Detail)
	}
}

// "Reset accepted" used to come from the write succeeding, which it does with
// the far end unplugged. The verdict has to come from the timer's answer.
func TestBenchResetVerdictComesFromTheTimersAnswer(t *testing.T) {
	dev, _ := newTestDevice(t, DefaultSimOptions())
	c := benchCheck(t, NewBench(dev, "", 4, nil).RunAutomatic(context.Background()), CheckReset)
	if c.Verdict != VerdictPass {
		t.Fatalf("verdict %q, detail %q", c.Verdict, c.Detail)
	}
	if !strings.Contains(c.Evidence, "*") {
		t.Errorf("evidence %q should show what came back", c.Evidence)
	}

	opts := DefaultSimOptions()
	opts.NoResetAck = true
	quiet, _ := newTestDevice(t, opts)
	c = benchCheck(t, NewBench(quiet, "", 4, nil).RunAutomatic(context.Background()), CheckReset)
	if c.Verdict != VerdictWarn {
		t.Errorf("verdict %q, want warn — nothing came back, so nothing is known", c.Verdict)
	}
}

// Asking the timer to report early is the only way to tell a heat the timer is
// sitting on from one it never started, on a timer whose gate cannot be read.
func TestForceResultsIsSentToTheTimer(t *testing.T) {
	dev, sim := newTestDevice(t, DefaultSimOptions())

	if err := dev.ForceResults(); err != nil {
		t.Fatalf("ForceResults: %v", err)
	}
	assertSent(t, sim, ftForceResults)
}

// A timer with no such command must say so rather than appear to have asked.
func TestForceResultsRefusedWhenTheTimerHasNoSuchCommand(t *testing.T) {
	p := SimulatorProfile()
	p.ForceResults = ""
	sim := NewSimulator(DefaultSimOptions())
	dev := Open(p, sim, Options{})
	t.Cleanup(func() { dev.Close() })

	if err := dev.ForceResults(); err == nil {
		t.Fatal("expected an error, not a silent no-op")
	}
}

// A result arriving with nothing armed is dropped on purpose — it would
// otherwise overwrite a finished heat — but the times have to survive into the
// message, or a heat that really ran vanishes with nobody able to say what the
// cars did.
func TestDroppedResultSaysWhatTheCarsDid(t *testing.T) {
	dev, sim := newTestDevice(t, DefaultSimOptions())
	events := subscribe(t, dev)

	// Nothing is armed: the machine is idle.
	sim.EmitSingleLane(3, 2.456)

	// The timer reports every lane on one line, so there is a message per lane
	// and the one that matters is the lane a car actually ran in.
	var said []string
	deadline := time.After(2 * time.Second)
	for looking := true; looking; {
		select {
		case ev := <-events:
			if ev.Kind == EvMalfunction {
				said = append(said, strings.Join(ev.Args, " "))
			}
		case <-deadline:
			looking = false
		}
	}

	all := strings.Join(said, " | ")
	if !strings.Contains(all, "lane 3 reported 2.456s") {
		t.Errorf("the dropped time for lane 3 is missing from:\n%s", all)
	}
	if !strings.Contains(all, "no heat is armed") {
		t.Errorf("nothing said why the result was dropped:\n%s", all)
	}
}

// The recovery that matters: a timer holding a race hands the times over when
// asked, instead of the heat being lost because nobody could make it report.
func TestForceResultsMakesAHeldRaceReport(t *testing.T) {
	opts := DefaultSimOptions()
	// Long enough that the timer would never report on its own within the test:
	// anything that arrives, arrives because it was asked for.
	opts.ResultDelay = 30 * time.Second
	dev, sim := newTestDevice(t, opts)
	events := subscribe(t, dev)

	if err := dev.ArmHeat(0b1111, 4); err != nil {
		t.Fatalf("ArmHeat: %v", err)
	}
	sim.CloseGate()
	time.Sleep(600 * time.Millisecond)
	sim.OpenGate()

	if err := dev.ForceResults(); err != nil {
		t.Fatalf("ForceResults: %v", err)
	}
	waitOn(t, events, dev, 3*time.Second, EvRaceFinished)

	lanes, missing := dev.Finish()
	if len(lanes) != 4 {
		t.Fatalf("got %d lanes, want 4", len(lanes))
	}
	if len(missing) != 0 {
		t.Errorf("no lane should be missing: %v", missing)
	}
}

// And a timer with no race in flight says nothing, which is what tells the
// coordinator the heat never started rather than that it is still coming.
func TestForceResultsIsSilentWhenNoRaceHasRun(t *testing.T) {
	dev, _ := newTestDevice(t, DefaultSimOptions())
	events := subscribe(t, dev)

	if err := dev.ForceResults(); err != nil {
		t.Fatalf("ForceResults: %v", err)
	}

	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Kind == EvLaneResult || ev.Kind == EvRaceFinished {
				t.Fatalf("the timer reported %s with no race having run", ev.Kind)
			}
		case <-deadline:
			return
		}
	}
}
