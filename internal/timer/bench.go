package timer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// The Timer Test Bench answers one question before a race night starts: is the
// timer going to work?
//
// Today the equivalent is restarting a service and hoping. Every check here
// reports pass, fail or skipped together with the raw bytes that produced the
// verdict, so "the timer is being weird" becomes something diagnosable.

// Verdict is the outcome of one check.
type Verdict string

const (
	VerdictPass    Verdict = "pass"
	VerdictWarn    Verdict = "warn"
	VerdictFail    Verdict = "fail"
	VerdictSkipped Verdict = "skipped"
	VerdictPending Verdict = "pending"
)

// CheckID names a check so the UI can address it.
type CheckID string

const (
	CheckPort        CheckID = "port"
	CheckIdentify    CheckID = "identify"
	CheckFeatures    CheckID = "features"
	CheckLaneCount   CheckID = "lanes"
	CheckMask        CheckID = "mask"
	CheckReset       CheckID = "reset"
	CheckLatency     CheckID = "latency"
	CheckStartSwitch CheckID = "start_switch"
	CheckGate        CheckID = "gate"
	CheckLaneMapping CheckID = "lane_mapping"
	CheckTestHeat    CheckID = "test_heat"
)

// Check is one bench result.
type Check struct {
	ID      CheckID `json:"id"`
	Name    string  `json:"name"`
	Verdict Verdict `json:"verdict"`
	Detail  string  `json:"detail"`
	// Evidence is the raw exchange that produced the verdict.
	Evidence string `json:"evidence,omitempty"`
	// Interactive marks checks that need someone at the track.
	Interactive bool `json:"interactive"`
}

// Result is a complete bench run.
type Result struct {
	StartedAt time.Time `json:"started_at"`
	Profile   string    `json:"profile"`
	Port      string    `json:"port"`
	Checks    []Check   `json:"checks"`
	// OverriddenBy and OverrideReason record a coordinator deciding to race
	// anyway. A jammed gate switch must never stop a race from happening; it
	// should just make the decision explicit.
	OverriddenBy   string `json:"overridden_by,omitempty"`
	OverrideReason string `json:"override_reason,omitempty"`
}

// Passed reports whether nothing failed. Skipped and warned checks are fine:
// a timer without a laser reset is still a working timer.
func (r *Result) Passed() bool {
	for _, c := range r.Checks {
		if c.Verdict == VerdictFail {
			return false
		}
	}
	return len(r.Checks) > 0
}

// Ready reports whether a race may start: either everything passed, or a
// coordinator took responsibility for the failures.
func (r *Result) Ready() bool {
	if r == nil {
		return false
	}
	return r.Passed() || r.OverriddenBy != ""
}

// Stale reports whether the result is too old to trust. Equipment gets
// unplugged between the afternoon setup and the evening's first heat.
func (r *Result) Stale(maxAge time.Duration) bool {
	if r == nil {
		return true
	}
	return time.Since(r.StartedAt) > maxAge
}

// Failures lists the checks that failed.
func (r *Result) Failures() []Check {
	var out []Check
	for _, c := range r.Checks {
		if c.Verdict == VerdictFail {
			out = append(out, c)
		}
	}
	return out
}

// MaxBenchAge is how long a bench result stays trusted.
const MaxBenchAge = 2 * time.Hour

// Bench runs the checks against a device.
type Bench struct {
	dev       *Device
	port      string
	laneCount int
	trace     *Trace

	mu     sync.Mutex
	result Result
}

// NewBench prepares a bench run. laneCount is the season's lane count, which
// the timer is checked against.
func NewBench(dev *Device, port string, laneCount int, trace *Trace) *Bench {
	return &Bench{
		dev:       dev,
		port:      port,
		laneCount: laneCount,
		trace:     trace,
		result: Result{
			StartedAt: time.Now(),
			Profile:   dev.Profile().Name,
			Port:      port,
		},
	}
}

// Result returns the run so far.
func (b *Bench) Result() Result {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.result
	out.Checks = append([]Check(nil), b.result.Checks...)
	return out
}

func (b *Bench) record(c Check) Check {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, existing := range b.result.Checks {
		if existing.ID == c.ID {
			b.result.Checks[i] = c
			return c
		}
	}
	b.result.Checks = append(b.result.Checks, c)
	return c
}

// RunAutomatic performs every check that needs no one at the track.
//
// The interactive ones — gate, lane mapping, test heat — are left pending and
// driven from the UI, because they need someone to open a gate or roll a car.
func (b *Bench) RunAutomatic(ctx context.Context) Result {
	b.checkPort()
	identified := b.checkIdentify(ctx)
	if identified {
		b.checkFeatures(ctx)
		b.checkLaneCount()
		b.checkMask()
		b.checkReset(ctx)
		b.checkLatency(ctx)
		b.checkStartSwitch(ctx)
	}

	// Placeholders so the UI can show what is still outstanding.
	for _, c := range []Check{
		{ID: CheckGate, Name: "Start gate", Interactive: true,
			Detail: "Open and close the gate to test."},
		{ID: CheckLaneMapping, Name: "Lane mapping", Interactive: true,
			Detail: "Roll a car down a named lane to confirm the wiring."},
		{ID: CheckTestHeat, Name: "Test heat", Interactive: true,
			Detail: "Run a practice heat. The results are discarded."},
	} {
		b.mu.Lock()
		found := false
		for _, existing := range b.result.Checks {
			if existing.ID == c.ID {
				found = true
				break
			}
		}
		b.mu.Unlock()
		if found {
			continue
		}
		// Do not leave a check pending that cannot be satisfied. The club's
		// timer will not report its gate at any price, so asking somebody to
		// go and open one is asking them to fail: they work the gate, nothing
		// happens, and they are left wondering what they did wrong. Skipping
		// it with the reason is the honest version, and it lets the run reach
		// a finished state instead of sitting on a check nobody can pass.
		if c.ID == CheckGate && !b.dev.GateKnowable() {
			c.Verdict = VerdictSkipped
			c.Detail = "This timer cannot report its gate, so there is nothing to " +
				"watch. " + gateConsequence
			b.record(c)
			continue
		}
		c.Verdict = VerdictPending
		b.record(c)
	}

	return b.Result()
}

func (b *Bench) checkPort() {
	c := Check{ID: CheckPort, Name: "Serial port"}
	if b.port == "" {
		c.Verdict = VerdictSkipped
		c.Detail = "Simulated timer — no serial port in use."
		b.record(c)
		return
	}
	ports, err := ListPorts()
	if err != nil {
		c.Verdict, c.Detail = VerdictWarn, "Could not list serial ports: "+err.Error()
		b.record(c)
		return
	}
	names := make([]string, 0, len(ports))
	for _, p := range ports {
		names = append(names, p.Name)
	}
	c.Verdict = VerdictPass
	c.Detail = "Using " + b.port
	c.Evidence = "Available: " + strings.Join(names, ", ")
	b.record(c)
}

func (b *Bench) checkIdentify(ctx context.Context) bool {
	c := Check{ID: CheckIdentify, Name: "Timer responds"}

	identity, err := b.dev.Identify(ctx)
	if err != nil {
		c.Verdict = VerdictFail
		c.Detail = "The timer did not answer. Check the cable, the power, and that " +
			"nothing else has the port open."
		c.Evidence = err.Error()
		b.record(c)
		return false
	}
	c.Verdict = VerdictPass
	c.Detail = SummariseIdentity(identity)
	c.Evidence = identity
	b.record(c)
	return true
}

// SummariseIdentity pulls the model and serial number out of a version reply,
// so the status line reads "Micro Wizard K3, serial 15985" rather than three
// lines of copyright notice.
func SummariseIdentity(identity string) string {
	fields := strings.Fields(identity)
	model, serial := "", ""
	for i, f := range fields {
		if model == "" && len(f) >= 2 && (f[0] == 'K' || f[0] == 'Q') &&
			f[1] >= '0' && f[1] <= '9' {
			model = f
		}
		if strings.EqualFold(f, "Number") && i+1 < len(fields) {
			serial = fields[i+1]
		}
	}
	switch {
	case model != "" && serial != "":
		return fmt.Sprintf("Micro Wizard %s, serial %s", model, serial)
	case model != "":
		return "Micro Wizard " + model
	default:
		return strings.TrimSpace(identity)
	}
}

// featureBits names the eight bits an RF reply carries, most significant first.
var featureBits = []string{
	"Sequence of finish (K3 only)",
	"Countdown clock",
	"Laser reset from computer",
	"Force end of race",
	"Eliminator mode",
	"Reverse lanes",
	"Mask lanes",
	"Serial race data",
}

func (b *Bench) checkFeatures(ctx context.Context) {
	c := Check{ID: CheckFeatures, Name: "Timer features"}

	reply, _, err := b.ask(ctx, ftReturnFeats, 1500*time.Millisecond, func(line string) bool {
		return isFeatureReply(line)
	})
	if err != nil {
		c.Verdict = VerdictWarn
		c.Detail = "The timer did not report its features; assuming defaults."
		c.Evidence = err.Error()
		b.record(c)
		return
	}

	bits := strings.ReplaceAll(strings.TrimSpace(reply), " ", "")
	var have, missing []string
	for i, name := range featureBits {
		if i >= len(bits) {
			break
		}
		if bits[i] == '1' {
			have = append(have, name)
		} else {
			missing = append(missing, name)
		}
	}

	c.Verdict = VerdictPass
	c.Detail = fmt.Sprintf("%d of %d features available", len(have), len(featureBits))
	c.Evidence = "Reply: " + reply + "\n\nAvailable:\n  " + strings.Join(have, "\n  ")
	if len(missing) > 0 {
		c.Evidence += "\n\nNot available:\n  " + strings.Join(missing, "\n  ")
	}
	// Masking is how a bye lane is silenced. Without it, an empty lane reports
	// 0.000 and looks like a car that failed to finish.
	if len(bits) >= 7 && bits[6] == '0' {
		c.Verdict = VerdictWarn
		c.Detail += " — this timer cannot mask lanes, so byes will report as non-finishes"
	}
	b.record(c)
}

func isFeatureReply(line string) bool {
	s := strings.ReplaceAll(strings.TrimSpace(line), " ", "")
	if len(s) != 8 {
		return false
	}
	for _, c := range s {
		if c != '0' && c != '1' {
			return false
		}
	}
	return true
}

func (b *Bench) checkLaneCount() {
	c := Check{ID: CheckLaneCount, Name: "Lane count"}
	max := b.dev.Profile().MaxLanes
	switch {
	case b.laneCount <= 0:
		c.Verdict, c.Detail = VerdictWarn, "The season has no lane count set."
	case b.laneCount > max:
		c.Verdict = VerdictFail
		c.Detail = fmt.Sprintf("The season expects %d lanes but this timer supports %d.",
			b.laneCount, max)
	default:
		c.Verdict = VerdictPass
		c.Detail = fmt.Sprintf("Racing %d lanes on a timer that supports %d.", b.laneCount, max)
	}
	b.record(c)
}

func (b *Bench) checkMask() {
	c := Check{ID: CheckMask, Name: "Lane masking"}
	p := b.dev.Profile()
	if p.HeatPrep.Unmask == "" {
		c.Verdict = VerdictSkipped
		c.Detail = "This timer has no lane masking."
		b.record(c)
		return
	}

	// Mask every lane in turn, then restore. A failure here shows up later as
	// bye lanes reporting as non-finishes.
	var sent []string
	for lane := 0; lane < b.laneCount; lane++ {
		mask := uint(1) << uint(lane)
		for _, cmd := range p.MaskCommands(^mask&((1<<uint(b.laneCount))-1), b.laneCount) {
			if err := b.dev.Send(cmd); err != nil {
				c.Verdict, c.Detail = VerdictFail, "Could not send "+cmd+": "+err.Error()
				b.record(c)
				return
			}
			sent = append(sent, cmd)
			time.Sleep(30 * time.Millisecond)
		}
	}
	if err := b.dev.Send(p.HeatPrep.Unmask); err != nil {
		c.Verdict, c.Detail = VerdictFail, "Could not restore all lanes: "+err.Error()
		b.record(c)
		return
	}
	sent = append(sent, p.HeatPrep.Unmask)

	c.Verdict = VerdictPass
	c.Detail = fmt.Sprintf("Masked and restored each of %d lanes.", b.laneCount)
	c.Evidence = "Sent: " + strings.Join(sent, " ")
	b.record(c)
}

// resetAckTimeout is how long to wait for the timer to acknowledge a reset.
// A FastTrack answers with "*" straight away; this is generous.
const resetAckTimeout = 600 * time.Millisecond

func (b *Bench) checkReset(ctx context.Context) {
	c := Check{ID: CheckReset, Name: "Heat reset"}
	p := b.dev.Profile()

	switch {
	case p.ResetDuringMark == "":
		c.Verdict = VerdictSkipped
		c.Detail = "This timer needs no reset command."
	case !b.dev.CanResetOverSerial():
		// Being specific matters: these two reasons need different responses
		// from whoever is running the race.
		c.Verdict = VerdictSkipped
		c.Detail = "Reset is suppressed because " + b.dev.ResetSuppressionReason() + "."
	default:
		// The verdict comes from what the timer said back, not from the write
		// succeeding. A write to a serial port succeeds with the far end
		// unplugged, so "Reset accepted" from a successful write is a green
		// tick that means nothing.
		ack, _, err := b.ask(ctx, p.ResetDuringMark, resetAckTimeout, func(string) bool { return true })
		var sendErr errSend
		switch {
		case err == nil:
			c.Verdict = VerdictPass
			c.Detail = "The timer acknowledged the reset."
			c.Evidence = "Sent " + p.ResetDuringMark + ", received " + ack
		case errors.As(err, &sendErr):
			c.Verdict, c.Detail = VerdictFail, "Could not send "+p.ResetDuringMark+": "+err.Error()
		default:
			c.Verdict = VerdictWarn
			c.Detail = "Sent " + p.ResetDuringMark + ", but the timer did not acknowledge it."
			c.Evidence = err.Error()
		}
	}
	b.record(c)
}

// checkLatency times the round trip on the one command every timer of this
// make answers.
//
// It deliberately does not use the gate query: that is an option a timer may
// have switched off, and measuring the link with it reports a disabled feature
// as a slow cable. Start-switch reporting has its own check.
func (b *Bench) checkLatency(ctx context.Context) {
	c := Check{ID: CheckLatency, Name: "Response time"}

	probe := b.dev.Profile().Prober.Command
	if probe == "" {
		c.Verdict = VerdictSkipped
		c.Detail = "This timer has no command to time."
		b.record(c)
		return
	}

	const samples = 5
	var total time.Duration
	var worst time.Duration
	for i := 0; i < samples; i++ {
		_, elapsed, err := b.ask(ctx, probe, time.Second, func(string) bool { return true })
		if err != nil {
			c.Verdict = VerdictWarn
			c.Detail = "The timer stopped answering. It identified itself a moment " +
				"ago, so check the cable and the USB adapter before racing."
			c.Evidence = err.Error()
			b.record(c)
			return
		}
		total += elapsed
		if elapsed > worst {
			worst = elapsed
		}
	}
	avg := total / samples

	c.Detail = fmt.Sprintf("average %s, worst %s", formatLatency(avg), formatLatency(worst))
	c.Evidence = fmt.Sprintf("%d round trips on %s", samples, probe)
	switch {
	case worst > 500*time.Millisecond:
		c.Verdict = VerdictWarn
		c.Detail += " — slow enough to delay results; suspect the USB adapter or cable"
	default:
		c.Verdict = VerdictPass
	}
	b.record(c)
}

// gateNotSupportedReply reports whether a reply is the profile's way of saying
// the start-switch option is switched off — "X" on a FastTrack.
func gateNotSupportedReply(p *Profile, line string) bool {
	for _, det := range p.GateWatcher.Detectors {
		if det.Event != EvGateNotSupported {
			continue
		}
		if _, _, ok := det.Apply(line); ok {
			return true
		}
	}
	return false
}

// startSwitchTimeout is how long to wait for a reply to the gate query.
const startSwitchTimeout = time.Second

// gateConsequence says what an unreadable gate costs, which is less than it
// sounds: racing works, because a heat is finished by the results arriving.
const gateConsequence = "Racing still works — a heat finishes when the lanes " +
	"report — but there will be no 'ready to race' indication on the screens, " +
	"and a heat the timer says nothing about has to be re-run or its times " +
	"typed in, because closing the gate can no longer end it."

// checkStartSwitch asks whether this timer reports its start switch at all.
//
// It is an option on a FastTrack, not a given: DerbyNet records a K1 that
// answers "X" to RG, meaning the option is switched off, and the feature bits
// do not cover it either way. A timer that echoes the query and then says
// nothing is the same story. Finding that out here is the difference between
// "this timer does not report its gate" and sending somebody out to re-check
// wiring that was fine all along.
func (b *Bench) checkStartSwitch(ctx context.Context) {
	c := Check{ID: CheckStartSwitch, Name: "Start-switch reporting"}
	cmd := b.dev.Profile().GateWatcher.Command
	if cmd == "" {
		c.Verdict = VerdictSkipped
		c.Detail = "This timer has no way to be asked about its gate."
		b.record(c)
		return
	}

	reply, _, err := b.ask(ctx, cmd, startSwitchTimeout, func(string) bool { return true })

	var echoOnly errEchoOnly
	switch {
	case err == nil && gateNotSupportedReply(b.dev.Profile(), reply):
		// The reply is read here rather than through the gate detectors. Those
		// patterns are loose enough that "0$" matches a serial number, which is
		// why they are only live for a moment after the poller asks — and this
		// check must not be the thing that widens that window.
		b.dev.GateUnreadable()
		c.Verdict = VerdictWarn
		c.Detail = "This timer will not report its start switch: it answers the " +
			"gate query with X. On a FastTrack older than the enhanced result " +
			"format that is the firmware, not a setting to switch on. " + gateConsequence
		c.Evidence = "Sent " + cmd + ", received " + reply
	case err == nil:
		c.Verdict = VerdictPass
		c.Detail = "The timer reports its start switch."
		c.Evidence = "Sent " + cmd + ", received " + reply
	case errors.As(err, &echoOnly):
		// Alive, and with nothing to say about the gate. Record it, so the
		// gate check below skips with a reason rather than failing.
		b.dev.GateUnreadable()
		c.Verdict = VerdictWarn
		c.Detail = "The timer echoes the gate query but never answers it, so it " +
			"cannot tell us about its start switch. " + gateConsequence
		c.Evidence = err.Error()
	default:
		c.Verdict = VerdictWarn
		c.Detail = "The timer said nothing at all to the gate query."
		c.Evidence = err.Error()
	}
	b.record(c)
}

// portQuietTime is how long the port must be silent before a question is asked
// while a line is still being assembled. It is longer than NewlineTimeout on
// purpose: that unterminated line is what would otherwise land on top of the
// next answer.
const portQuietTime = NewlineTimeout + 50*time.Millisecond

// settleCap bounds the wait, so a timer that never stops talking cannot hang
// the bench. A check that asks anyway and times out reports something; one
// that waits forever reports nothing.
const settleCap = 2 * time.Second

// settle waits for the port to be quiet for portQuietTime.
func (b *Bench) settle() {
	deadline := time.Now().Add(settleCap)
	for time.Now().Before(deadline) {
		quiet := b.dev.Silent()
		if quiet >= portQuietTime {
			return
		}
		time.Sleep(portQuietTime - quiet)
	}
}

// ask sends a command and waits for a line satisfying want. It reports how long
// the timer took, measured from the send, so the wait for a quiet port is not
// counted as the timer being slow.
func (b *Bench) ask(ctx context.Context, cmd string, timeout time.Duration, want func(string) bool) (string, time.Duration, error) {
	lines, stop := b.dev.collect()
	defer stop()

	// Let the port fall quiet, then discard whatever it was still saying.
	//
	// The club's K1 answers the unmask command with an unterminated "AC", so
	// bytes that arrived before this question are still being assembled and
	// would be handed over as its answer — which is how the reset check came
	// to report "received ACLR", the previous command's acknowledgement glued
	// to this one's echo. Waiting on the last byte in, rather than on whether
	// the reader is holding one, is what catches it: at the moment the next
	// command goes out the "AC" has arrived but not yet been framed.
	b.settle()
	drain(lines)

	sentAt := time.Now()
	if err := b.dev.Send(cmd); err != nil {
		return "", 0, errSend{cmd: cmd, err: err}
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	var seen []string
	echoes := 0
	for {
		select {
		case <-ctx.Done():
			return "", 0, ctx.Err()
		case <-deadline.C:
			// An echo and nothing else is a different fault from silence, and
			// the two need different responses: the timer is alive and has
			// simply got nothing to say about this command. Counting the echoes
			// rather than dropping them is what tells them apart — reported as
			// `saw ""`, they look identical.
			if echoes > 0 && len(seen) == 0 {
				return "", 0, errEchoOnly{cmd: cmd, timeout: timeout, echoes: echoes}
			}
			return "", 0, fmt.Errorf("no reply to %s within %v (saw %q)",
				cmd, timeout, strings.Join(seen, " | "))
		case line := <-lines:
			if line == cmd {
				echoes++
				continue // the timer's echo of what we sent
			}
			seen = append(seen, line)
			if want(line) {
				return line, time.Since(sentAt), nil
			}
		}
	}
}

// drain discards lines the port handed over before the question was asked.
func drain(lines <-chan string) {
	for {
		select {
		case <-lines:
		default:
			return
		}
	}
}

// errSend reports a command that could not be written to the port at all,
// which is a different verdict from one the timer did not answer.
type errSend struct {
	cmd string
	err error
}

func (e errSend) Error() string { return "send " + e.cmd + ": " + e.err.Error() }
func (e errSend) Unwrap() error { return e.err }

// errEchoOnly reports a command the timer echoed back and then never answered.
type errEchoOnly struct {
	cmd     string
	timeout time.Duration
	echoes  int
}

func (e errEchoOnly) Error() string {
	if e.echoes == 1 {
		return fmt.Sprintf("%s was echoed back within %v but never answered", e.cmd, e.timeout)
	}
	return fmt.Sprintf("%s was echoed back %d times within %v but never answered",
		e.cmd, e.echoes, e.timeout)
}

// --- interactive checks ------------------------------------------------------

// WatchGate observes the gate while someone opens and closes it.
//
// This is the single most common race-night failure, and it also measures how
// long the switch bounces — so a gate that needs a longer debounce is found on
// a test trip rather than on heat one.
func (b *Bench) WatchGate(ctx context.Context, timeout time.Duration) Check {
	c := Check{ID: CheckGate, Name: "Start gate", Interactive: true}

	if !b.dev.GateKnowable() {
		c.Verdict = VerdictSkipped
		c.Detail = "This timer cannot report its gate. Racing still works, but there " +
			"will be no 'ready' indication on the screens."
		return b.record(c)
	}

	deadline := time.Now().Add(timeout)
	var transitions []string
	var readings int
	firstChange, lastChange := time.Time{}, time.Time{}
	lastRaw := b.dev.GateClosed()
	bounce := time.Duration(0)

	for time.Now().Before(deadline) && len(transitions) < 2 {
		select {
		case <-ctx.Done():
			c.Verdict, c.Detail = VerdictSkipped, "Cancelled."
			return b.record(c)
		default:
		}

		if err := b.dev.PollGate(); err != nil {
			c.Verdict, c.Detail = VerdictFail, "Could not read the gate: "+err.Error()
			return b.record(c)
		}
		time.Sleep(GatePollInterval)
		readings++

		raw := b.dev.GateClosed()
		if raw != lastRaw {
			now := time.Now()
			if firstChange.IsZero() {
				firstChange = now
			}
			if !lastChange.IsZero() && now.Sub(lastChange) < MinGateTime {
				bounce += now.Sub(lastChange)
			}
			lastChange = now
			state := "open"
			if raw {
				state = "closed"
			}
			transitions = append(transitions, state)
			lastRaw = raw
		}
	}

	switch {
	case len(transitions) == 0:
		c.Verdict = VerdictFail
		c.Detail = fmt.Sprintf("The gate never changed after %d readings. Check the "+
			"start-switch wiring.", readings)
	case len(transitions) == 1:
		c.Verdict = VerdictWarn
		c.Detail = fmt.Sprintf("Saw the gate go %s, but not back again.", transitions[0])
	default:
		c.Verdict = VerdictPass
		c.Detail = fmt.Sprintf("Gate went %s then %s.", transitions[0], transitions[1])
		if bounce > 0 {
			c.Detail += fmt.Sprintf(" Switch bounced for %v; the debounce window is %v.",
				bounce.Round(time.Millisecond), MinGateTime)
		}
	}
	c.Evidence = fmt.Sprintf("%d readings, transitions: %s", readings, strings.Join(transitions, " -> "))
	return b.record(c)
}

// CheckLaneMapping asks for a car down one named lane and confirms the timer
// agrees which lane that was.
//
// Reversed lane wiring is a real failure mode — DerbyNet ships a setting for
// it — and without an explicit check it is only discovered after a whole race
// has been recorded backwards.
func (b *Bench) CheckLaneMapping(ctx context.Context, expectLane int, timeout time.Duration) Check {
	c := Check{ID: CheckLaneMapping, Name: "Lane mapping", Interactive: true}

	// Every lane is armed, not just the expected one. If the wiring is reversed,
	// arming a single lane would mask the input the car actually trips, and the
	// check would report silence rather than the mismatch it is looking for.
	laneMask := uint(1)<<uint(b.laneCount) - 1
	if err := b.dev.ArmHeat(laneMask, b.laneCount); err != nil {
		c.Verdict, c.Detail = VerdictFail, "Could not arm the timer: "+err.Error()
		return b.record(c)
	}
	defer b.dev.Disarm()

	reported, err := b.awaitAnyLane(ctx, timeout)
	if err != nil {
		c.Verdict = VerdictFail
		c.Detail = fmt.Sprintf("No result from lane %d. Either the car did not trip the "+
			"finish beam, or that lane is not reporting.", expectLane)
		c.Evidence = err.Error()
		return b.record(c)
	}

	if reported != expectLane {
		c.Verdict = VerdictFail
		c.Detail = fmt.Sprintf("A car sent down lane %d was reported as lane %d. The lanes "+
			"are wired in a different order than the software expects — turn on "+
			"reversed lanes, or re-check the wiring.", expectLane, reported)
	} else {
		c.Verdict = VerdictPass
		c.Detail = fmt.Sprintf("A car in lane %d was reported as lane %d.", expectLane, reported)
	}
	c.Evidence = fmt.Sprintf("expected lane %d, timer reported lane %d", expectLane, reported)
	return b.record(c)
}

// awaitAnyLane waits for the next lane result, whatever lane it is for.
func (b *Bench) awaitAnyLane(ctx context.Context, timeout time.Duration) (int, error) {
	events, unsubscribe := b.dev.Subscribe()
	defer unsubscribe()

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-deadline.C:
			return 0, fmt.Errorf("no lane result within %v", timeout)
		case ev, ok := <-events:
			if !ok {
				return 0, fmt.Errorf("timer disconnected")
			}
			if ev.Kind != EvLaneResult {
				continue
			}
			r, err := ParseLaneResult(ev.Args)
			if err != nil {
				continue
			}
			// A masked lane reporting 0.000 is not a car going down the track.
			if r.Time >= 9.0 {
				continue
			}
			return r.Lane, nil
		}
	}
}

// RunTestHeat arms a real heat and reports what comes back. The results are
// discarded — this never touches a race.
func (b *Bench) RunTestHeat(ctx context.Context, timeout time.Duration) Check {
	c := Check{ID: CheckTestHeat, Name: "Test heat", Interactive: true}

	events, unsubscribe := b.dev.Subscribe()
	defer unsubscribe()

	laneMask := uint(1)<<uint(b.laneCount) - 1
	if err := b.dev.ArmHeat(laneMask, b.laneCount); err != nil {
		c.Verdict, c.Detail = VerdictFail, "Could not arm the timer: "+err.Error()
		return b.record(c)
	}
	defer b.dev.Disarm()

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	polling := time.NewTicker(GatePollInterval)
	defer polling.Stop()

	for {
		select {
		case <-ctx.Done():
			c.Verdict, c.Detail = VerdictSkipped, "Cancelled."
			return b.record(c)

		case <-deadline.C:
			c.Verdict = VerdictFail
			c.Detail = fmt.Sprintf("No results within %v. If the cars ran, the finish "+
				"beam is not reporting.", timeout)
			return b.record(c)

		case <-polling.C:
			// Only worth asking of a timer that answers. On a 9600 baud line a
			// pointless poll every quarter second is traffic competing with the
			// result line we are waiting for.
			if b.dev.GateKnowable() {
				_ = b.dev.PollGate()
			}

		case ev, ok := <-events:
			if !ok {
				c.Verdict, c.Detail = VerdictFail, "The timer disconnected mid-heat."
				return b.record(c)
			}
			if ev.Kind != EvRaceFinished {
				continue
			}

			results, _ := b.dev.Finish()
			var lines []string
			finishers := 0
			for _, r := range results {
				note := ""
				if r.Time >= 9.0 {
					note = "  (no finish)"
				} else {
					finishers++
				}
				lines = append(lines, fmt.Sprintf("lane %d  %.3fs  place %d%s",
					r.Lane, r.Time, r.TimerPlace, note))
			}

			switch {
			case finishers == 0:
				c.Verdict = VerdictFail
				c.Detail = "The timer triggered but no lane recorded a finish."
			case finishers < len(results):
				c.Verdict = VerdictWarn
				c.Detail = fmt.Sprintf("%d of %d lanes finished.", finishers, len(results))
			default:
				c.Verdict = VerdictPass
				c.Detail = fmt.Sprintf("All %d lanes finished.", finishers)
			}
			c.Evidence = strings.Join(lines, "\n")
			return b.record(c)
		}
	}
}

// formatLatency renders a round-trip time readably at any scale. A simulated
// timer answers in microseconds; a struggling USB adapter takes hundreds of
// milliseconds, and both need to be legible.
func formatLatency(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000)
	case d < time.Second:
		return d.Round(time.Millisecond).String()
	default:
		return d.Round(10 * time.Millisecond).String()
	}
}
