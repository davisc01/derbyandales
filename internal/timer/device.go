package timer

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

// Device drives one timer: it owns the port, applies the profile, runs the
// state machine, and publishes events.
type Device struct {
	profile *Profile
	port    Port
	reader  *Reader
	machine *Machine

	mu       sync.Mutex
	identity string
	laneMask uint
	// gateWindow is when the gate detectors stop being live. The FastTrack gate
	// patterns match loosely, so they are only applied to lines arriving just
	// after an RG.
	gateWindow time.Time
	// laserReset is cleared when the timer's feature bits say it cannot be
	// reset over the serial line.
	laserReset bool
	// autoGate suppresses the reset poll entirely: on a track with an automatic
	// release gate, the reset line is what launches the cars.
	autoGate bool
	traced   *Trace

	// Events are fanned out to every subscriber. A single shared channel would
	// mean two consumers — the app's event pump and a bench check, say — racing
	// for each event and each seeing only some of them. That failure is silent
	// and intermittent, which is the worst kind on a race night.
	subsMu  sync.Mutex
	subs    map[int]chan Event
	nextSub int

	done   chan struct{}
	closed sync.Once

	// tap receives a copy of every line, for Identify. There is exactly one
	// reader of the port — the run loop — because two goroutines reading the
	// same channel steal lines from each other, which shows up as a timer that
	// intermittently refuses to be identified.
	tapMu sync.Mutex
	tap   chan<- string
}

// Options configure a Device.
type Options struct {
	// AutomaticGateRelease must be set when the track has a powered release
	// gate fitted. It suppresses the reset poll, which would otherwise start
	// the race by itself.
	AutomaticGateRelease bool
	// Trace, when set, records every byte in and out.
	Trace *Trace
}

// Open attaches to an already-open port using the given profile. It does not
// probe; call Identify for that.
func Open(p *Profile, port Port, opts Options) *Device {
	d := &Device{
		profile:    p,
		port:       port,
		reader:     NewReader(port),
		machine:    NewMachine(),
		laserReset: true,
		autoGate:   opts.AutomaticGateRelease,
		traced:     opts.Trace,
		subs:       make(map[int]chan Event),
		done:       make(chan struct{}),
	}
	d.machine.OnTransition = func(from, to State) {
		d.emit(Event{Kind: EvStateChanged, Args: []string{string(from), string(to)}})
	}
	go d.run()
	return d
}

// Subscribe returns a channel of timer events and a function to release it.
//
// Every subscriber sees every event. The returned cancel must be called, or the
// subscription leaks for the life of the device.
func (d *Device) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 128)

	d.subsMu.Lock()
	d.nextSub++
	id := d.nextSub
	d.subs[id] = ch
	d.subsMu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			d.subsMu.Lock()
			if existing, ok := d.subs[id]; ok {
				delete(d.subs, id)
				close(existing)
			}
			d.subsMu.Unlock()
		})
	}
}

// Profile returns the device's profile.
func (d *Device) Profile() *Profile { return d.profile }

// State reports the racing state.
func (d *Device) State() State { return d.machine.State() }

// GateClosed reports the debounced gate reading.
func (d *Device) GateClosed() bool { return d.machine.GateClosed() }

// GateKnowable reports whether this timer can report its gate at all.
func (d *Device) GateKnowable() bool { return d.machine.GateKnowable() }

// Identity is what the timer said when asked who it is.
func (d *Device) Identity() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.identity
}

// CanResetOverSerial reports whether the reset poll is available and safe.
func (d *Device) CanResetOverSerial() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.laserReset && !d.autoGate
}

// ResetSuppressionReason explains why the reset poll is unavailable, or "" when
// it is available. The two reasons need different responses from the operator,
// so they are distinguished rather than lumped together.
func (d *Device) ResetSuppressionReason() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case d.autoGate:
		return "the track has an automatic release gate, and the reset line is what " +
			"launches the cars"
	case !d.laserReset:
		return "this timer's feature bits say it cannot be reset over the serial line"
	default:
		return ""
	}
}

// Send writes a raw command, appending the profile's terminator.
func (d *Device) Send(cmd string) error {
	if d.traced != nil {
		d.traced.Sent(cmd)
	}
	_, err := d.port.Write([]byte(cmd + d.profile.EOL))
	return err
}

// Close stops the device.
func (d *Device) Close() error {
	d.closed.Do(func() { close(d.done) })
	return d.reader.Close()
}

// run pumps lines through the detectors.
func (d *Device) run() {
	defer d.closeSubscribers()
	for {
		select {
		case <-d.done:
			return

		case err := <-d.reader.Err():
			d.emit(Event{Kind: EvLostConnection, Args: []string{err.Error()}})
			return

		case line, ok := <-d.reader.Lines():
			if !ok {
				d.emit(Event{Kind: EvLostConnection, Args: []string{"port closed"}})
				return
			}
			if d.traced != nil {
				d.traced.Received(line.Text, line.Inferred)
			}
			d.forwardToTap(line.Text)
			d.dispatch(line)
		}
	}
}

// forwardToTap gives Identify a copy of the line without competing for it.
func (d *Device) forwardToTap(text string) {
	d.tapMu.Lock()
	tap := d.tap
	d.tapMu.Unlock()
	if tap == nil {
		return
	}
	select {
	case tap <- text:
	default:
	}
}

// dispatch applies every applicable detector to one line.
//
// Each match is excised from the line and matching repeats, which is how a
// single FastTrack result line carrying every lane produces one event per lane.
func (d *Device) dispatch(line Line) {
	text := line.Text

	d.mu.Lock()
	gateLive := !d.gateWindow.IsZero() && time.Now().Before(d.gateWindow)
	d.mu.Unlock()

	detectors := make([]Detector, 0, 8)
	if gateLive {
		detectors = append(detectors, d.profile.GateWatcher.Detectors...)
	}
	detectors = append(detectors, d.profile.SetupDetectors...)
	detectors = append(detectors, d.profile.Matchers...)

	for progress := true; progress && strings.TrimSpace(text) != ""; {
		progress = false
		for _, det := range detectors {
			ev, remaining, ok := det.Apply(text)
			if !ok {
				continue
			}
			text = remaining
			progress = true
			ev.At = line.At
			d.handle(ev)
			break
		}
	}
}

// handle turns a detected event into state changes and published events.
func (d *Device) handle(ev Event) {
	switch ev.Kind {
	case EvGateClosed:
		if d.machine.GateReading(true) {
			d.emit(ev)
		}
	case EvGateOpen:
		if d.machine.GateReading(false) {
			d.emit(ev)
		}
	case EvGateNotSupported:
		d.machine.GateNotSupported()
		d.emit(ev)

	case EvNoLaserReset:
		d.mu.Lock()
		d.laserReset = false
		d.mu.Unlock()
		d.emit(ev)

	case EvLaneResult:
		r, err := ParseLaneResult(ev.Args)
		if err != nil {
			d.emit(Event{Kind: EvMalfunction, Args: []string{err.Error()}, At: ev.At})
			return
		}
		complete, accepted := d.machine.AddResult(r)
		if !accepted {
			// A result with nothing armed means someone rolled a car down the
			// track between heats. Recording it would overwrite a finished
			// result, so it is dropped — but said out loud.
			d.emit(Event{
				Kind: EvMalfunction,
				Args: []string{fmt.Sprintf("result for lane %d ignored: no heat is armed", r.Lane)},
				At:   ev.At,
			})
			return
		}
		d.emit(ev)
		if complete {
			d.emit(Event{Kind: EvRaceFinished, At: ev.At})
		}

	default:
		d.emit(ev)
	}
}

func (d *Device) emit(ev Event) {
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	d.subsMu.Lock()
	defer d.subsMu.Unlock()
	for _, ch := range d.subs {
		select {
		case ch <- ev:
		default:
			// Never block the reader on a consumer that has stopped listening.
			// A dropped event costs a stale display; a blocked reader costs the
			// heat.
		}
	}
}

// closeSubscribers releases every subscription when the device shuts down, so
// consumers see their channel close rather than waiting forever.
func (d *Device) closeSubscribers() {
	d.subsMu.Lock()
	defer d.subsMu.Unlock()
	for id, ch := range d.subs {
		delete(d.subs, id)
		close(ch)
	}
}

// Identify probes the timer and records what it says.
func (d *Device) Identify(ctx context.Context) (string, error) {
	if d.profile.Prober.Command == "" {
		return "", fmt.Errorf("timer: %s cannot be probed automatically", d.profile.Name)
	}

	lines, stop := d.collect()
	defer stop()

	if err := d.Send(d.profile.Prober.Command); err != nil {
		return "", fmt.Errorf("timer: probe write failed: %w", err)
	}

	deadline := time.NewTimer(ProbeTimeout)
	defer deadline.Stop()

	var seen []string
	matched := 0
	for matched < len(d.profile.Prober.Responses) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", fmt.Errorf("timer: no reply to %s after %v (saw %q)",
				d.profile.Prober.Command, ProbeTimeout, strings.Join(seen, " | "))
		case line := <-lines:
			if line == d.profile.Prober.Command {
				continue // the timer echoing back what we just sent
			}
			seen = append(seen, line)
			if d.profile.Prober.Responses[matched].MatchString(line) {
				matched++
			}
		}
	}

	identity := strings.Join(seen, " ")
	d.mu.Lock()
	d.identity = identity
	d.mu.Unlock()
	d.emit(Event{Kind: EvIdentified, Args: []string{identity}})
	return identity, nil
}

// ProbeTimeout is how long to wait for a timer to identify itself.
const ProbeTimeout = 2 * time.Second

// collect registers a tap so Identify sees raw lines.
//
// It deliberately does not read the port itself: the run loop is the only
// reader, and it forwards a copy here. Two goroutines reading the same channel
// would race for each line, losing some of them.
func (d *Device) collect() (<-chan string, func()) {
	out := make(chan string, 32)

	d.tapMu.Lock()
	d.tap = out
	d.tapMu.Unlock()

	var once sync.Once
	return out, func() {
		once.Do(func() {
			d.tapMu.Lock()
			d.tap = nil
			d.tapMu.Unlock()
		})
	}
}

// Setup sends the profile's setup commands.
func (d *Device) Setup() error {
	for _, cmd := range d.profile.Setup {
		if err := d.Send(cmd); err != nil {
			return fmt.Errorf("timer: setup command %s: %w", cmd, err)
		}
		// FastTrack has no flow control; give it a moment between commands.
		time.Sleep(60 * time.Millisecond)
	}
	return nil
}

// ArmHeat masks off unused lanes and arms the timer.
func (d *Device) ArmHeat(laneMask uint, laneCount int) error {
	d.mu.Lock()
	d.laneMask = laneMask
	d.mu.Unlock()

	for _, cmd := range d.profile.MaskCommands(laneMask, laneCount) {
		if err := d.Send(cmd); err != nil {
			return err
		}
		time.Sleep(40 * time.Millisecond)
	}
	if d.profile.HeatPrep.Reset != "" {
		if err := d.Send(d.profile.HeatPrep.Reset); err != nil {
			return err
		}
	}
	d.machine.Arm(laneMask)
	return nil
}

// PollGate asks the timer for the gate state and opens the detector window.
func (d *Device) PollGate() error {
	if d.profile.GateWatcher.Command == "" {
		return nil
	}
	d.mu.Lock()
	d.gateWindow = time.Now().Add(gateReplyWindow)
	d.mu.Unlock()
	return d.Send(d.profile.GateWatcher.Command)
}

// gateReplyWindow is how long after asking the gate detectors stay live.
const gateReplyWindow = 150 * time.Millisecond

// PollReset sends the reset command while a heat is armed, if it is safe.
func (d *Device) PollReset() error {
	if d.profile.ResetDuringMark == "" || !d.CanResetOverSerial() {
		return nil
	}
	return d.Send(d.profile.ResetDuringMark)
}

// RemoteStart releases the cars, where a powered gate is fitted.
func (d *Device) RemoteStart() error {
	if d.profile.RemoteStart == "" {
		return fmt.Errorf("timer: %s has no remote start", d.profile.Name)
	}
	return d.Send(d.profile.RemoteStart)
}

// Finish completes the armed heat and returns its results.
func (d *Device) Finish() []LaneResult { return d.machine.Finish() }

// Disarm abandons the armed heat.
func (d *Device) Disarm() { d.machine.Disarm() }

// Machine exposes the state machine, for the race controller.
func (d *Device) Machine() *Machine { return d.machine }

// Silent reports how long the port has been quiet.
func (d *Device) Silent() time.Duration { return d.reader.Silent() }

// --- port discovery ----------------------------------------------------------

// PortInfo describes a candidate serial port.
type PortInfo struct {
	Name string
	// Likely marks ports that look like a USB-serial adapter rather than a
	// built-in device such as Bluetooth.
	Likely bool
}

// ListPorts returns the serial ports on this machine, most likely first.
//
// macOS exposes both /dev/tty.* and /dev/cu.* for each device. The cu ("call
// up") node is the one that does not block waiting for carrier detect, so it
// is the only one worth offering.
func ListPorts() ([]PortInfo, error) {
	names, err := serial.GetPortsList()
	if err != nil {
		return nil, fmt.Errorf("timer: list serial ports: %w", err)
	}

	var out []PortInfo
	for _, name := range names {
		base := filepath.Base(name)
		if strings.HasPrefix(base, "tty.") {
			continue
		}
		out = append(out, PortInfo{Name: name, Likely: likelyTimerPort(base)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Likely != out[j].Likely {
			return out[i].Likely
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// likelyTimerPort recognises the USB-serial adapters these timers ship with.
func likelyTimerPort(base string) bool {
	lower := strings.ToLower(base)
	for _, hint := range []string{"usbserial", "usbmodem", "slab_usbtouart", "ftdi", "wchusbserial"} {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

// OpenPort opens a serial port with the profile's line settings.
func OpenPort(name string, p *Profile) (Port, error) {
	mode := &serial.Mode{
		BaudRate: p.Serial.Baud,
		DataBits: p.Serial.DataBits,
		StopBits: serial.OneStopBit,
		Parity:   serial.NoParity,
	}
	switch p.Serial.StopBits {
	case 2:
		mode.StopBits = serial.TwoStopBits
	}
	switch strings.ToLower(p.Serial.Parity) {
	case "odd":
		mode.Parity = serial.OddParity
	case "even":
		mode.Parity = serial.EvenParity
	}

	port, err := serial.Open(name, mode)
	if err != nil {
		return nil, fmt.Errorf("timer: open %s: %w", name, err)
	}
	// Reads must return promptly so the reader can infer unterminated lines.
	if err := port.SetReadTimeout(NewlineTimeout); err != nil {
		port.Close()
		return nil, fmt.Errorf("timer: set read timeout on %s: %w", name, err)
	}
	return port, nil
}
