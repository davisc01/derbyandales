package scoring

import (
	"strconv"
	"strings"
)

// Scale speed is the number the crowd actually reacts to. A 2.4 second run down
// a 28 foot track is about 8 mph in reality, which sounds like nothing; at 1:25
// scale it is nearly 200 mph, which sounds like a lot.
//
//	feet per second        = track_length / time
//	real miles per hour    = fps * 3600 / 5280
//	scale miles per hour   = real mph * scale denominator
//
// Verified against the club's published results: a 2.434 s run on the 28 ft
// track at 1:25 gives 196.1, matching 2026 race 4 exactly.

// SecondsPerHour and FeetPerMile are spelled out rather than folded into one
// magic constant, so the formula can be checked by eye.
const (
	secondsPerHour = 3600.0
	feetPerMile    = 5280.0
)

// ScaleMPH converts a finish time into scale miles per hour.
// It returns 0 for a non-positive time rather than an infinity.
func ScaleMPH(trackLengthFt float64, scaleDenom int, seconds float64) float64 {
	if seconds <= 0 || trackLengthFt <= 0 {
		return 0
	}
	feetPerSecond := trackLengthFt / seconds
	return feetPerSecond * secondsPerHour * float64(scaleDenom) / feetPerMile
}

// FormatTime renders a finish time the way the club's website does: three
// decimal places with trailing zeros trimmed, so 2.760 prints as "2.76".
func FormatTime(seconds float64) string {
	return trimTrailingZeros(strconv.FormatFloat(seconds, 'f', 3, 64))
}

// FormatAverage renders an average time at the precision the standings are
// published at.
//
// Trailing zeros are trimmed, matching the club's existing files: 2026 race 4
// writes Greg Thrift's average as "2.43", not "2.430".
func FormatAverage(seconds float64) string {
	return trimTrailingZeros(strconv.FormatFloat(seconds, 'f', 3, 64))
}

// FormatMPH renders a scale speed the way the website does: one decimal place,
// with a trailing ".0" dropped, so 194.0 prints as "194".
func FormatMPH(mph float64) string {
	return trimTrailingZeros(strconv.FormatFloat(mph, 'f', 1, 64))
}

// trimTrailingZeros removes trailing zeros after a decimal point, and the point
// itself if nothing is left after it.
func trimTrailingZeros(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}
