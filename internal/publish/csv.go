// Package publish writes the club's results into the Hugo site.
//
// It never runs git. Files are written into the working tree and the user
// reviews and commits them, because "the software pushed something wrong to the
// website" is a worse failure than "the software did not push anything".
//
// The formats here were taken from the committed files rather than from the
// exporters that produced them. The committed ones had been hand-trimmed, and
// it is those that the site actually renders.
package publish

import (
	"bytes"
	"encoding/csv"
	"strconv"

	"github.com/davisc01/derbyandales/internal/scoring"
)

// The site's csv-table shortcode reads the file with Go's own CSV reader, so it
// tolerates either line ending and a missing final newline. Two things it does
// not tolerate:
//
//   - A UTF-8 BOM. It survives into the first header cell, and the shortcode
//     matches column names by string equality — so `hide-columns="Over Limit"`
//     and `highlight-column="Over Limit"` both silently stop working on the
//     first column of a file written with one. The club's season page uses both.
//   - A header renamed. Same reason.
//
// So: no BOM, LF endings, a trailing newline, and headers copied exactly.

func newWriter() (*bytes.Buffer, *csv.Writer) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	// encoding/csv writes \r\n only if UseCRLF is set. It is not.
	return &buf, w
}

// --- heats.csv ------------------------------------------------------------------

// HeatRow is one car's run in one heat.
type HeatRow struct {
	Heat      int
	Lane      int
	First     string
	Last      string
	CarNumber int
	CarName   string
	Time      float64
	Place     int
}

// HeatsCSV renders the heat results.
//
// Every car that ran is here, including the pace car and any car ruled
// ineligible: this is the record of what happened on the track, not of who was
// scored. Empty lanes are left out — there is nothing to report about a bye.
func HeatsCSV(rows []HeatRow, trackFt float64, scaleDenom int) ([]byte, error) {
	buf, w := newWriter()
	if err := w.Write([]string{
		"Heat", "Lane", "FirstName", "LastName", "CarNumber", "CarName",
		"FinishTime", "Scale MPH", "FinishPlace",
	}); err != nil {
		return nil, err
	}
	for _, r := range rows {
		mph := scoring.ScaleMPH(trackFt, scaleDenom, r.Time)
		if err := w.Write([]string{
			strconv.Itoa(r.Heat),
			strconv.Itoa(r.Lane),
			r.First,
			r.Last,
			strconv.Itoa(r.CarNumber),
			r.CarName,
			scoring.FormatTime(r.Time),
			scoring.FormatMPH(mph),
			strconv.Itoa(r.Place),
		}); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// --- standings.csv --------------------------------------------------------------

// StandingRow is one car's place in a race.
type StandingRow struct {
	Place     int
	CarNumber int
	Name      string
	CarName   string
	Heats     int
	Average   float64
	Best      float64
	Worst     float64
}

// StandingsCSV renders the finishing order.
//
// One full-name column rather than first and last, which is what the club's
// standings pages use — heats.csv splits them and standings.csv does not.
func StandingsCSV(rows []StandingRow) ([]byte, error) {
	buf, w := newWriter()
	if err := w.Write([]string{
		"Place", "Car Number", "Name", "Car Name", "Heats", "Average", "Best", "Worst",
	}); err != nil {
		return nil, err
	}
	for _, r := range rows {
		if err := w.Write([]string{
			strconv.Itoa(r.Place),
			strconv.Itoa(r.CarNumber),
			r.Name,
			r.CarName,
			strconv.Itoa(r.Heats),
			scoring.FormatAverage(r.Average),
			scoring.FormatTime(r.Best),
			scoring.FormatTime(r.Worst),
		}); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// --- awards.csv -----------------------------------------------------------------

// AwardRow is one trophy.
type AwardRow struct {
	Award     string
	First     string
	Last      string
	CarNumber int
	CarName   string
}

// AwardsCSV renders the trophies.
//
// There is no "Award Type" column. The planning notes for this project said
// there was, taken from one file — 2026 race 2 — but the other four races of
// that season and every one before them use these five columns, so that is the
// club's format and the note was wrong.
func AwardsCSV(rows []AwardRow) ([]byte, error) {
	buf, w := newWriter()
	if err := w.Write([]string{
		"Award Name", "First Name", "Last Name", "Car Number", "Car Name",
	}); err != nil {
		return nil, err
	}
	for _, r := range rows {
		if err := w.Write([]string{
			r.Award, r.First, r.Last, strconv.Itoa(r.CarNumber), r.CarName,
		}); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// --- qualifiers.csv -------------------------------------------------------------

// QualifierRow is one championship-qualifying slot.
type QualifierRow struct {
	Seed      int
	Driver    string
	CarName   string
	Race      int
	Finish    int
	Average   float64
	Entries   int
	OverLimit bool
}

// QualifiersCSV renders the auto-qualifier list.
//
// "Over Limit" is exactly YES or empty, because the season page both highlights
// and hides that column by matching on the value.
func QualifiersCSV(rows []QualifierRow) ([]byte, error) {
	buf, w := newWriter()
	if err := w.Write([]string{
		"Current Seed", "Driver", "Car Name", "Race", "Race Finish",
		"Avg Time", "Total Entries", "Over Limit",
	}); err != nil {
		return nil, err
	}
	for _, r := range rows {
		over := ""
		if r.OverLimit {
			over = "YES"
		}
		if err := w.Write([]string{
			strconv.Itoa(r.Seed),
			r.Driver,
			r.CarName,
			"Race " + strconv.Itoa(r.Race),
			strconv.Itoa(r.Finish),
			// Three decimals, trailing zeros trimmed. The old tracker wrote
			// four and they were trimmed by hand before committing.
			scoring.FormatAverage(r.Average),
			strconv.Itoa(r.Entries),
			over,
		}); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// --- wildcard.csv ---------------------------------------------------------------

// WildcardRow is one racer's season points.
type WildcardRow struct {
	Rank int
	Name string
	// Points is a string because a racer who already holds every championship
	// slot they are allowed is published as "Max Entries Reached" instead of a
	// number.
	Points string
}

// WildcardCSV renders the season points standings.
func WildcardCSV(rows []WildcardRow) ([]byte, error) {
	buf, w := newWriter()
	if err := w.Write([]string{"Rank", "Name", "Total Points"}); err != nil {
		return nil, err
	}
	for _, r := range rows {
		if err := w.Write([]string{strconv.Itoa(r.Rank), r.Name, r.Points}); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}
