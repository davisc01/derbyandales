package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/davisc01/derbyandales/internal/bus"
	"github.com/davisc01/derbyandales/internal/store"
	"github.com/davisc01/derbyandales/internal/timer"
)

// KeyLastBench stores the most recent Timer Test Bench result, so it survives a
// restart. Losing it would mean re-running the bench after every app launch,
// which is exactly the friction that makes people skip checks.
const KeyLastBench = "timer_last_bench"

// TimerController owns the connection to the finish-line timer.
//
// One connection, one owner. The race controller, the test bench and the status
// page all go through here, so two things can never hold the serial port at
// once — which on a real port fails in confusing ways.
type TimerController struct {
	app *App

	mu  sync.Mutex
	dev *timer.Device
	// sim is set only when the simulated timer is connected. It lets the UI
	// stand in for the person at the track, so the whole bench — including the
	// checks that need a gate opened or a car rolled — can be learned before
	// race night rather than during it.
	sim        *timer.Simulator
	trace      *timer.Trace
	port       string
	profileKey string
	lastBench  *timer.Result
	// activeBench is the run in progress, so the interactive checks append to
	// the same result rather than each starting a fresh one.
	activeBench *timer.Bench

	stopPolling context.CancelFunc
	// busy is held while an interactive bench check is running, so the gate
	// poller does not fight it for the port.
	busy bool
}

// NewTimerController returns a disconnected controller.
func NewTimerController(a *App) *TimerController {
	tc := &TimerController{app: a}
	tc.loadLastBench(context.Background())
	return tc
}

// TimerStatus is what the UI shows.
type TimerStatus struct {
	Connected   bool          `json:"connected"`
	Simulated   bool          `json:"simulated"`
	Profile     string        `json:"profile"`
	ProfileKey  string        `json:"profile_key"`
	Port        string        `json:"port"`
	Identity    string        `json:"identity"`
	State       string        `json:"state"`
	GateClosed  bool          `json:"gate_closed"`
	GateKnown   bool          `json:"gate_known"`
	CanReset    bool          `json:"can_reset"`
	ResetReason string        `json:"reset_reason,omitempty"`
	Silent      time.Duration `json:"silent_ms"`
	TraceLines  int           `json:"trace_lines"`

	// Bench is the last test result, with its age.
	Bench      *timer.Result `json:"bench,omitempty"`
	BenchStale bool          `json:"bench_stale"`
	BenchReady bool          `json:"bench_ready"`
}

// Status reports the current state.
func (tc *TimerController) Status() TimerStatus {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	s := TimerStatus{
		Port:       tc.port,
		ProfileKey: tc.profileKey,
		Simulated:  tc.profileKey == timer.SimulatorKey,
		Bench:      tc.lastBench,
		BenchStale: tc.lastBench.Stale(timer.MaxBenchAge),
		BenchReady: tc.lastBench.Ready() && !tc.lastBench.Stale(timer.MaxBenchAge),
		TraceLines: tc.trace.Len(),
	}
	if tc.dev == nil {
		s.State = string(timer.StateIdle)
		return s
	}
	s.Connected = true
	s.Profile = tc.dev.Profile().Name
	s.Identity = tc.dev.Identity()
	s.State = string(tc.dev.State())
	s.GateClosed = tc.dev.GateClosed()
	s.GateKnown = tc.dev.GateKnowable()
	s.CanReset = tc.dev.CanResetOverSerial()
	s.ResetReason = tc.dev.ResetSuppressionReason()
	s.Silent = tc.dev.Silent() / time.Millisecond
	return s
}

// Ports lists the serial ports that might have a timer on them.
func (tc *TimerController) Ports() ([]timer.PortInfo, error) { return timer.ListPorts() }

// Connect opens the timer. An empty port with the simulator profile connects to
// the simulated device, which is how the bench can be demonstrated and the app
// developed with no hardware present.
func (tc *TimerController) Connect(ctx context.Context, portName, profileKey string) error {
	profile := timer.ProfileByKey(profileKey)
	if profile == nil {
		return fmt.Errorf("unknown timer profile %q", profileKey)
	}

	tc.Disconnect()

	autoGate, _ := tc.app.DB.SettingBool(ctx, store.KeyAutoGateRelease, false)
	trace := timer.NewTrace()

	var port timer.Port
	var sim *timer.Simulator
	if profileKey == timer.SimulatorKey {
		sim = timer.NewSimulator(timer.DefaultSimOptions())
		port = sim
		portName = ""
	} else {
		p, err := timer.OpenPort(portName, profile)
		if err != nil {
			return err
		}
		port = p
	}

	dev := timer.Open(profile, port, timer.Options{
		AutomaticGateRelease: autoGate,
		Trace:                trace,
	})

	tc.mu.Lock()
	tc.dev = dev
	tc.sim = sim
	tc.trace = trace
	tc.port = portName
	tc.profileKey = profileKey
	tc.activeBench = nil
	tc.mu.Unlock()

	// Remember the choice so the next launch reconnects without being asked.
	_ = tc.app.DB.SetSetting(ctx, store.KeyTimerPort, portName)
	_ = tc.app.DB.SetSetting(ctx, store.KeyTimerProfile, profileKey)

	go tc.pump(dev)
	tc.startPolling()

	tc.app.Bus.Publish(bus.TopicTimer, "connected", tc.Status())
	return nil
}

// Disconnect closes the timer.
func (tc *TimerController) Disconnect() {
	tc.mu.Lock()
	dev := tc.dev
	stop := tc.stopPolling
	tc.dev = nil
	tc.sim = nil
	tc.activeBench = nil
	tc.stopPolling = nil
	tc.mu.Unlock()

	if stop != nil {
		stop()
	}
	if dev != nil {
		dev.Close()
		tc.app.Bus.Publish(bus.TopicTimer, "disconnected", nil)
	}
}

// Simulator returns the simulated timer, or nil when real hardware is in use.
func (tc *TimerController) Simulator() *timer.Simulator {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.sim
}

// Device returns the connected device, or nil.
func (tc *TimerController) Device() *timer.Device {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.dev
}

// Trace returns the recorded serial session.
func (tc *TimerController) Trace() *timer.Trace {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.trace
}

// SaveTrace writes the session to the traces directory and returns the path.
//
// This is how a hardware quirk becomes a regression test instead of tribal
// knowledge.
func (tc *TimerController) SaveTrace() (string, error) {
	trace := tc.Trace()
	if trace == nil || trace.Len() == 0 {
		return "", fmt.Errorf("nothing recorded yet")
	}
	name := fmt.Sprintf("timer-%s.trace", time.Now().Format("20060102-150405"))
	path := filepath.Join(tc.app.Paths.Traces, name)
	if err := trace.Save(path); err != nil {
		return "", err
	}
	return path, nil
}

// pump forwards timer events onto the application bus, so every browser sees
// them at once.
func (tc *TimerController) pump(dev *timer.Device) {
	events, unsubscribe := dev.Subscribe()
	defer unsubscribe()

	for ev := range events {
		tc.app.Bus.Publish(bus.TopicTimer, string(ev.Kind), map[string]any{
			"kind":  string(ev.Kind),
			"args":  ev.Args,
			"raw":   ev.Raw,
			"at":    ev.At,
			"state": string(dev.State()),
		})
		if ev.Kind == timer.EvLostConnection {
			tc.app.Log.Warn("timer connection lost", "detail", ev.Args)
			return
		}
	}
}

// startPolling reads the gate in the background so the UI shows it live.
func (tc *TimerController) startPolling() {
	ctx, cancel := context.WithCancel(context.Background())

	tc.mu.Lock()
	tc.stopPolling = cancel
	dev := tc.dev
	tc.mu.Unlock()
	if dev == nil {
		cancel()
		return
	}

	go func() {
		ticker := time.NewTicker(timer.GatePollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				tc.mu.Lock()
				busy := tc.busy
				tc.mu.Unlock()
				if busy {
					// An interactive check has the port; do not talk over it.
					continue
				}
				if err := dev.PollGate(); err != nil {
					return
				}
			}
		}
	}()
}

// hold marks the port busy for the duration of an interactive check.
func (tc *TimerController) hold() func() {
	tc.mu.Lock()
	tc.busy = true
	tc.mu.Unlock()
	return func() {
		tc.mu.Lock()
		tc.busy = false
		tc.mu.Unlock()
	}
}

// --- bench -------------------------------------------------------------------

// laneCount returns the active season's lane count, defaulting to four.
func (tc *TimerController) laneCount(ctx context.Context) int {
	seasons, err := tc.app.DB.Seasons(ctx)
	if err != nil || len(seasons) == 0 {
		return 4
	}
	return seasons[0].LaneCount
}

// LaneNumbers lists the season's lanes, for the UI to offer.
func (tc *TimerController) LaneNumbers(ctx context.Context) []int {
	n := tc.laneCount(ctx)
	out := make([]int, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, i)
	}
	return out
}

// RunBench performs the automatic checks.
func (tc *TimerController) RunBench(ctx context.Context) (timer.Result, error) {
	dev := tc.Device()
	if dev == nil {
		return timer.Result{}, fmt.Errorf("no timer connected")
	}
	defer tc.hold()()

	tc.mu.Lock()
	port, trace := tc.port, tc.trace
	tc.mu.Unlock()

	b := timer.NewBench(dev, port, tc.laneCount(ctx), trace)
	tc.setBench(b)

	result := b.RunAutomatic(ctx)
	tc.saveBench(ctx, result)
	return result, nil
}

func (tc *TimerController) setBench(b *timer.Bench) {
	tc.mu.Lock()
	tc.activeBench = b
	tc.mu.Unlock()
}

func (tc *TimerController) activeOrNewBench(ctx context.Context) (*timer.Bench, error) {
	dev := tc.Device()
	if dev == nil {
		return nil, fmt.Errorf("no timer connected")
	}
	tc.mu.Lock()
	b := tc.activeBench
	port, trace := tc.port, tc.trace
	tc.mu.Unlock()

	if b == nil {
		b = timer.NewBench(dev, port, tc.laneCount(ctx), trace)
		tc.setBench(b)
	}
	return b, nil
}

// WatchGate runs the interactive gate check.
func (tc *TimerController) WatchGate(ctx context.Context, timeout time.Duration) (timer.Check, error) {
	b, err := tc.activeOrNewBench(ctx)
	if err != nil {
		return timer.Check{}, err
	}
	defer tc.hold()()
	c := b.WatchGate(ctx, timeout)
	tc.saveBench(ctx, b.Result())
	return c, nil
}

// CheckLaneMapping runs the interactive lane-wiring check.
func (tc *TimerController) CheckLaneMapping(ctx context.Context, lane int, timeout time.Duration) (timer.Check, error) {
	b, err := tc.activeOrNewBench(ctx)
	if err != nil {
		return timer.Check{}, err
	}
	defer tc.hold()()
	c := b.CheckLaneMapping(ctx, lane, timeout)
	tc.saveBench(ctx, b.Result())
	return c, nil
}

// RunTestHeat runs a practice heat whose results are discarded.
func (tc *TimerController) RunTestHeat(ctx context.Context, timeout time.Duration) (timer.Check, error) {
	b, err := tc.activeOrNewBench(ctx)
	if err != nil {
		return timer.Check{}, err
	}
	defer tc.hold()()
	c := b.RunTestHeat(ctx, timeout)
	tc.saveBench(ctx, b.Result())
	return c, nil
}

// Override records a coordinator deciding to race despite a failed check.
//
// A jammed gate switch must never prevent a race from happening; it should just
// make the decision explicit and leave a trace.
func (tc *TimerController) Override(ctx context.Context, actor, reason string) error {
	tc.mu.Lock()
	result := tc.lastBench
	tc.mu.Unlock()

	if result == nil {
		return fmt.Errorf("run the timer test first")
	}
	if reason == "" {
		return fmt.Errorf("a reason is required")
	}

	updated := *result
	updated.OverriddenBy = actor
	updated.OverrideReason = reason
	tc.saveBench(ctx, updated)

	_ = tc.app.DB.Audit(ctx, actor, "timer.bench.override", reason)
	tc.app.Log.Warn("timer test overridden", "actor", actor, "reason", reason)
	return nil
}

// LastBench returns the most recent result.
func (tc *TimerController) LastBench() *timer.Result {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.lastBench
}

// saveBench stores and announces a result.
func (tc *TimerController) saveBench(ctx context.Context, r timer.Result) {
	tc.mu.Lock()
	tc.lastBench = &r
	tc.mu.Unlock()

	if encoded, err := json.Marshal(r); err == nil {
		_ = tc.app.DB.SetSetting(ctx, KeyLastBench, string(encoded))
	}
	tc.app.Bus.Publish(bus.TopicTimer, "bench", r)
}

// loadLastBench restores the stored result at startup.
func (tc *TimerController) loadLastBench(ctx context.Context) {
	raw, err := tc.app.DB.Setting(ctx, KeyLastBench, "")
	if err != nil || raw == "" {
		return
	}
	var r timer.Result
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return
	}
	tc.mu.Lock()
	tc.lastBench = &r
	tc.mu.Unlock()
}
