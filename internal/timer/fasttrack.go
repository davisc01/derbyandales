package timer

import "regexp"

// Micro Wizard FastTrack command set.
//
// These are the timer's own protocol, taken from Micro Wizard's documentation
// as reproduced in DerbyNet's driver. The laser-gate pin is the subtle one:
// on a track with an automatic release gate fitted, the same line that resets
// the timer also releases the cars, so the reset poll has to be suppressed or
// it will start the race by itself.
const (
	ftReadVersion  = "RV" // identify: manufacturer, model, firmware, serial
	ftReturnFeats  = "RF" // eight feature bits
	ftResetElim    = "RE" // leave eliminator mode
	ftFormatNew    = "N1" // "new" result format
	ftFormatEnh    = "N2" // enhanced: five-digit times plus start-switch status
	ftUnmaskLanes  = "MG" // re-enable every lane
	ftMaskPrefix   = "M"  // + 'A'..'F' masks that lane
	ftReadGate     = "RG" // read the start switch
	ftResetLaser   = "LR" // hold the laser line until the switch releases
	ftPulseLaser   = "LG" // pulse the laser line for one second (releases cars)
	ftForceResults = "RA" // give up waiting and report what you have
)

// Result lines look like:
//
//	A=4.009" B=3.261! C=4.129$ D=4.013# E=0.000  F=0.000
//
// Lane letter, time, and a trailing character giving the place the timer
// assigned: '!' is first, '"' second, and so on up from ASCII 33. A lane that
// did not finish — or is masked out — reports 0.000 with no place character.
var ftResultPattern = regexp.MustCompile(` *([A-Z])=(\d+\.\d+)([^ ]?)`)

// The feature bits come back as eight characters. Bit 6 is "laser reset from
// computer"; this pattern matches a zero there, meaning the timer cannot be
// reset over the serial line.
//
//	8th Sequence of Finish (K3 only)   4th Eliminator mode
//	7th Countdown clock                3rd Reverse lanes
//	6th Laser reset from computer      2nd Mask lanes
//	5th Force end of race              1st Serial race data
var ftNoLaserResetPattern = regexp.MustCompile(`^[01][01]0[01] *[01][01][01]1$`)

// FastTrack returns the profile for Micro Wizard's K- and Q-series timers,
// which is what the club races on.
func FastTrack() *Profile {
	return &Profile{
		Name: "FastTrack K- or Q-series",
		Key:  "fasttrack-k",

		// 9600 8N1, and no terminator on outgoing commands.
		Serial:   SerialParams{Baud: 9600, DataBits: 8, StopBits: 1, Parity: "none"},
		MaxLanes: 6,
		EOL:      "",

		Prober: Prober{
			Command: ftReadVersion,
			// Real replies:
			//   Copyright (c) Micro Wizard 2002-2005
			//   K3 Version 1.05A  Serial Number 15985
			Responses: []*regexp.Regexp{
				regexp.MustCompile(`Micro Wizard|MICRO WIZARD`),
				regexp.MustCompile(`^K|Model: Q`),
			},
		},

		Setup: []string{ftResetElim, ftFormatNew, ftFormatEnh, ftReturnFeats},
		SetupDetectors: []Detector{
			{Pattern: ftNoLaserResetPattern, Event: EvNoLaserReset},
		},

		Matchers: []Detector{
			// Lane, time, and the timer's own place character. DerbyNet discards
			// the place and recomputes from the times; so do we, but we keep it
			// so the test bench can cross-check the two and say so if they
			// disagree.
			{Pattern: ftResultPattern, Event: EvLaneResult, Args: []int{1, 2, 3}},
		},

		HeatPrep: HeatPrep{
			Unmask:     ftUnmaskLanes,
			MaskPrefix: ftMaskPrefix,
			FirstLane:  'A',
		},

		GateWatcher: GateWatcher{
			Command: ftReadGate,
			Detectors: []Detector{
				// These are deliberately loose, which is why they are only applied
				// inside the reply window — "0$" alone would match a serial number.
				{Pattern: regexp.MustCompile(`^RG0|0$`), Event: EvGateOpen},
				{Pattern: regexp.MustCompile(`^RG1|1$`), Event: EvGateClosed},
				// "X" means the timer has the feature switched off.
				{Pattern: regexp.MustCompile(`^X$`), Event: EvGateNotSupported},
			},
		},

		ResetDuringMark: ftResetLaser,
		RemoteStart:     ftPulseLaser,
		ForceResults:    ftForceResults,
	}
}

// Profiles returns every timer model this build knows about.
func Profiles() []*Profile {
	return []*Profile{FastTrack()}
}

// ProfileByKey looks up a profile, including the simulator.
func ProfileByKey(key string) *Profile {
	if key == SimulatorKey {
		return SimulatorProfile()
	}
	for _, p := range Profiles() {
		if p.Key == key {
			return p
		}
	}
	return nil
}
