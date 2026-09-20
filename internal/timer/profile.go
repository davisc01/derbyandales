// Package timer talks to the finish-line timer.
//
// A timer model is described declaratively by a Profile — serial settings, how
// to identify it, what to send to arm a heat, and regular expressions that turn
// its output into events. Adding a second model is then data rather than code.
// This idea is DerbyNet's and it is a good one.
package timer

import (
	"fmt"
	"regexp"
	"time"
)

// EventKind is something the timer told us, or something we concluded.
type EventKind string

const (
	// EvLaneResult carries one lane's finish: lane number, seconds, and the
	// place the timer itself assigned (0 when it did not say).
	EvLaneResult EventKind = "lane_result"

	EvGateOpen         EventKind = "gate_open"
	EvGateClosed       EventKind = "gate_closed"
	EvGateNotSupported EventKind = "gate_not_supported"

	// EvNoLaserReset means the timer's feature bits say it cannot be reset over
	// the serial line, so the reset poll must be suppressed.
	EvNoLaserReset EventKind = "no_laser_reset"

	EvIdentified     EventKind = "identified"
	EvRaceStarted    EventKind = "race_started"
	EvRaceFinished   EventKind = "race_finished"
	EvOverdue        EventKind = "overdue"
	EvLostConnection EventKind = "lost_connection"
	EvStateChanged   EventKind = "state_changed"
	EvMalfunction    EventKind = "malfunction"
)

// Event is one timer event.
type Event struct {
	Kind EventKind
	Args []string
	At   time.Time
	// Raw is the line that produced it, for the diagnostic console.
	Raw string
}

// SerialParams are the line settings a timer expects.
type SerialParams struct {
	Baud     int
	DataBits int
	StopBits int
	Parity   string // "none", "odd", "even"
}

// Detector turns a line of timer output into an event.
//
// Applying a detector removes the matched text from the line, so the same
// detector can be applied repeatedly. That is how one FastTrack result line,
// which carries every lane at once, yields one event per lane.
type Detector struct {
	Pattern *regexp.Regexp
	Event   EventKind
	// Args are 1-based capture group indices to pass through, in order.
	Args []int
}

// Apply matches once. It returns the event, the line with the matched text
// excised, and whether anything matched.
func (d Detector) Apply(line string) (Event, string, bool) {
	loc := d.Pattern.FindStringSubmatchIndex(line)
	if loc == nil {
		return Event{}, line, false
	}
	groups := d.Pattern.FindStringSubmatch(line)

	args := make([]string, 0, len(d.Args))
	for _, i := range d.Args {
		if i < len(groups) {
			args = append(args, groups[i])
		} else {
			args = append(args, "")
		}
	}
	remaining := line[:loc[0]] + line[loc[1]:]
	return Event{Kind: d.Event, Args: args, Raw: groups[0]}, remaining, true
}

// HeatPrep describes how to tell the timer which lanes are in use.
type HeatPrep struct {
	// Unmask re-enables every lane, e.g. "MG".
	Unmask string
	// MaskPrefix plus FirstLane+offset masks one lane, e.g. "M" + 'A'.
	MaskPrefix string
	FirstLane  byte
	// Reset, when set, is sent to arm the timer for a new heat.
	Reset string
}

// GateWatcher describes how to ask whether the start gate is closed.
type GateWatcher struct {
	// Command is polled, e.g. "RG".
	Command string
	// Detectors are applied only to lines arriving shortly after Command.
	//
	// This window matters. The FastTrack gate patterns are loose — "0$" would
	// match any line ending in zero, including a serial number — so they must
	// only be live when a reply is actually expected.
	Detectors []Detector
}

// Prober identifies a timer.
type Prober struct {
	// Command is written to elicit an identifying reply, e.g. "RV".
	Command string
	// Responses must each match, in order, across the lines that come back.
	Responses []*regexp.Regexp
}

// Profile is a complete description of one timer model.
type Profile struct {
	Name string
	Key  string

	Serial   SerialParams
	MaxLanes int
	// EOL is appended to every command written. FastTrack wants nothing.
	EOL string

	Prober Prober

	// Setup commands are sent once after identification.
	Setup []string
	// SetupDetectors are applied to the replies to Setup commands, which is how
	// a timer's feature bits are read.
	SetupDetectors []Detector

	// Matchers are applied to every line received during normal operation.
	Matchers []Detector

	HeatPrep    HeatPrep
	GateWatcher GateWatcher

	// ResetDuringMark, when set, is polled while a heat is armed. FastTrack has
	// no dedicated reset command; it is reset by holding the laser gate line.
	ResetDuringMark string

	// RemoteStart releases the cars, where an automatic gate is fitted.
	RemoteStart string

	// ForceResults makes the timer report what it has rather than waiting for
	// the race to end on its own. It is how a heat that has gone quiet is told
	// apart from one the timer never started: a timer holding times gives them
	// up, and a timer with no race to report says nothing at all.
	ForceResults string
}

// MaskCommands returns the commands that leave exactly the given lanes active.
//
// laneMask is a bitmask where bit (lane-1) set means the lane is in use. Note
// this is the opposite sense from the schedule's byes, and from DerbyNet's
// stored unused-lane-mask, so the conversion happens in exactly one place.
func (p *Profile) MaskCommands(laneMask uint, laneCount int) []string {
	if p.HeatPrep.Unmask == "" {
		return nil
	}
	if laneCount <= 0 || laneCount > p.MaxLanes {
		laneCount = p.MaxLanes
	}
	cmds := []string{p.HeatPrep.Unmask}
	for lane := 0; lane < laneCount; lane++ {
		if laneMask&(1<<uint(lane)) == 0 {
			cmds = append(cmds, fmt.Sprintf("%s%c",
				p.HeatPrep.MaskPrefix, p.HeatPrep.FirstLane+byte(lane)))
		}
	}
	return cmds
}
