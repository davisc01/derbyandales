package timer

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/davisc01/derbyandales/internal/scoring"
)

// DNFTime is the time a lane reads when it did not finish. The timer reports
// 0.000 and the driver rewrites it to this, so it sorts last.
const DNFTime = scoring.DNF

// LaneResult is one lane's outcome in a heat.
type LaneResult struct {
	Lane int
	Time float64
	// TimerPlace is the place the timer itself assigned, or 0 if it did not say.
	// Places are recomputed from the times; this is kept only so the test bench
	// can cross-check and report a disagreement.
	TimerPlace int
}

// DecodeLane converts a lane label into a 1-based lane number.
//
// FastTrack labels lanes 'A'..'F'. Some timers use '1'..'9' instead, so both
// are accepted.
func DecodeLane(label string) (int, error) {
	if label == "" {
		return 0, fmt.Errorf("timer: empty lane label")
	}
	c := label[0]
	switch {
	case c >= '1' && c <= '9':
		return int(c - '0'), nil
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 1, nil
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 1, nil
	default:
		return 0, fmt.Errorf("timer: unrecognised lane label %q", label)
	}
}

// DecodePlace converts a FastTrack place character into a 1-based place.
// '!' is first, '"' second, '#' third, and so on from ASCII 33. An empty or
// unrecognised character means the timer did not assign a place.
func DecodePlace(s string) int {
	if s == "" {
		return 0
	}
	c := s[0]
	if c < '!' {
		return 0
	}
	place := int(c-'!') + 1
	if place < 1 || place > 12 {
		return 0
	}
	return place
}

// ZeroesToNines rewrites a timer's "no result" reading so that it sorts last.
//
// FastTrack reports 0.000 both for a lane that never finished and for one that
// is masked out. Left alone, a car that did not finish would appear to have won
// by a mile.
func ZeroesToNines(time string) string {
	trimmed := strings.TrimSpace(time)
	if trimmed == "" {
		return trimmed
	}
	for _, c := range trimmed {
		if c != '0' && c != '.' {
			return trimmed
		}
	}
	// Same shape, all nines: 0.000 becomes 9.999, 0.0000 becomes 9.9999.
	return strings.Map(func(c rune) rune {
		if c == '0' {
			return '9'
		}
		return c
	}, trimmed)
}

// ParseLaneResult turns a LANE_RESULT event's arguments into a LaneResult.
func ParseLaneResult(args []string) (LaneResult, error) {
	if len(args) < 2 {
		return LaneResult{}, fmt.Errorf("timer: lane result needs a lane and a time, got %v", args)
	}
	lane, err := DecodeLane(args[0])
	if err != nil {
		return LaneResult{}, err
	}
	seconds, err := strconv.ParseFloat(ZeroesToNines(args[1]), 64)
	if err != nil {
		return LaneResult{}, fmt.Errorf("timer: unparseable time %q: %w", args[1], err)
	}
	r := LaneResult{Lane: lane, Time: seconds}
	if len(args) > 2 {
		r.TimerPlace = DecodePlace(args[2])
	}
	return r, nil
}

// HeatResult accumulates lane results until every expected lane has reported.
type HeatResult struct {
	// Mask has bit (lane-1) set for each lane still awaited.
	pending uint
	lanes   map[int]LaneResult
}

// NewHeatResult expects results for the lanes set in laneMask.
func NewHeatResult(laneMask uint) *HeatResult {
	return &HeatResult{pending: laneMask, lanes: make(map[int]LaneResult, 6)}
}

// Add records one lane. It reports whether this completed the heat.
//
// Results for lanes that were not expected — a masked lane reporting anyway,
// which FastTrack does — are kept but do not affect completion.
func (h *HeatResult) Add(r LaneResult) (complete bool) {
	if r.Lane < 1 {
		return false
	}
	if _, seen := h.lanes[r.Lane]; !seen {
		h.lanes[r.Lane] = r
	}
	h.pending &^= 1 << uint(r.Lane-1)
	return h.pending == 0
}

// Complete reports whether every expected lane has reported.
func (h *HeatResult) Complete() bool { return h.pending == 0 }

// Lanes returns a result for every lane that was armed, ordered by lane, with
// places recomputed from the times.
//
// A lane the timer never reported is given DNFTime rather than left out. The
// club sees bad reads — a car crosses the line and nothing comes back — and a
// lane silently missing from the results is the worst of the available
// outcomes: the heat looks complete, the car looks as though it never raced,
// and nobody notices until the standings are wrong.
//
// 9.999 is honest about it. It is what the timer itself sends for a lane that
// did not finish, it sorts last, and it is the run the drop-slowest rule throws
// away — so one bad read costs a car almost nothing, which is right, because
// the car did nothing wrong.
func (h *HeatResult) Lanes(laneMask uint) []LaneResult {
	times := make(map[int]float64)
	for lane := 1; lane <= 32; lane++ {
		if laneMask&(1<<uint(lane-1)) == 0 {
			continue
		}
		if r, ok := h.lanes[lane]; ok {
			times[lane] = r.Time
		} else {
			times[lane] = DNFTime
		}
	}
	places := scoring.PlaceInHeat(times)

	out := make([]LaneResult, 0, len(times))
	for lane := 1; lane <= 32; lane++ {
		t, ok := times[lane]
		if !ok {
			continue
		}
		r, reported := h.lanes[lane]
		if !reported {
			r = LaneResult{Lane: lane, Time: t}
		}
		r.TimerPlace = places[lane]
		out = append(out, r)
	}
	return out
}

// Missing lists the armed lanes the timer never reported, which is what a bad
// read looks like from here.
func (h *HeatResult) Missing(laneMask uint) []int {
	var out []int
	for lane := 1; lane <= 32; lane++ {
		if laneMask&(1<<uint(lane-1)) == 0 {
			continue
		}
		if _, ok := h.lanes[lane]; !ok {
			out = append(out, lane)
		}
	}
	return out
}

// TimerPlaces returns the places the timer reported, for cross-checking.
func (h *HeatResult) TimerPlaces() map[int]int {
	out := make(map[int]int, len(h.lanes))
	for lane, r := range h.lanes {
		out[lane] = r.TimerPlace
	}
	return out
}

// AllZeroes reports whether every expected lane read as a non-finish.
//
// This nearly always means the timer was triggered without cars — a hand
// through the beam, a gate knocked open. Recording it as a heat would be worse
// than useless, so racing stops and a human decides.
func (h *HeatResult) AllZeroes(laneMask uint) bool {
	any := false
	for lane, r := range h.lanes {
		if laneMask&(1<<uint(lane-1)) == 0 {
			continue
		}
		any = true
		if r.Time < scoring.DNF {
			return false
		}
	}
	return any
}
