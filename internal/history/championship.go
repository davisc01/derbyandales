// Package history reads the club's own published archive back in.
//
// It exists for one rule: a car that has raced in a previous championship
// cannot race in another. That is checked by hand at check-in today, from
// memory, which works until the person who remembers is not there — which is
// the whole reason this project exists.
//
// The archive is not a clean dataset. It spans seven years and at least four
// different exporters, so the column names move around, one year's header is
// split across lines, and one year has qualifying information baked into the
// car names. All of that is handled here rather than anywhere it could spread.
package history

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/davisc01/derbyandales/internal/model"
)

// Car is one car that raced in a championship.
type Car struct {
	Year      int
	First     string
	Last      string
	CarName   string
	CarNumber int
	Place     int
}

// Driver renders the racer's full name.
func (c Car) Driver() string {
	return model.Racer{FirstName: c.First, LastName: c.Last}.FullName()
}

// Column aliases, because seven years of exporters never agreed on a name.
var (
	firstNames  = []string{"First Name", "FirstName"}
	lastNames   = []string{"Last Name", "LastName"}
	wholeNames  = []string{"Name", "Driver"}
	carNames    = []string{"Car Name", "CarName"}
	carNumbers  = []string{"Car#", "Car Number", "CarNumber"}
	placeNames  = []string{"Place"}
	headerSpace = regexp.MustCompile(`\s+`)
)

// suffix is the qualifying origin the 2021 export appended to every car name:
// "-1" through "-4" for the race a car qualified from and "-W" for a wildcard.
// It is not part of the name, and leaving it on would stop "Red Rocket" from
// matching "Red Rocket-1" seven years later.
//
// Deliberately narrow. The 2024 championship contains a car called "Drive-By",
// which must survive untouched.
var suffix = regexp.MustCompile(`-(?:[0-9]|[Ww])$`)

// CleanCarName is how a car name is compared across years: trimmed, with the
// 2021 qualifying suffix removed.
func CleanCarName(name string) string {
	return strings.TrimSpace(suffix.ReplaceAllString(strings.TrimSpace(name), ""))
}

// Key is the comparable identity of a car: its name, folded for case and
// spacing, so "Loose  Moose" and "loose moose" are the same car.
func Key(name string) string {
	return strings.ToLower(headerSpace.ReplaceAllString(CleanCarName(name), " "))
}

// ParseChampionship reads one year's published championship standings.
func ParseChampionship(year int, body []byte) ([]Car, error) {
	body = bytes.TrimPrefix(body, []byte{0xEF, 0xBB, 0xBF})

	r := csv.NewReader(bytes.NewReader(body))
	// Row lengths vary within a file in some years, where a trailing comma was
	// left on one line. Reading loosely and picking columns by name is the only
	// thing that survives an archive like this.
	r.FieldsPerRecord = -1

	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("the %d championship file could not be read: %w", year, err)
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("the %d championship file has no rows in it", year)
	}

	// The 2019 export wrapped two header cells across lines, so a header name
	// can contain a newline. Fold all whitespace before matching.
	index := map[string]int{}
	for i, name := range rows[0] {
		clean := strings.TrimSpace(headerSpace.ReplaceAllString(name, " "))
		index[clean] = i
	}
	col := func(row []string, names []string) string {
		for _, n := range names {
			if i, ok := index[n]; ok && i < len(row) {
				return strings.TrimSpace(row[i])
			}
		}
		return ""
	}

	if col(rows[0], carNames) == "" && index["Car Name"] == 0 {
		// Nothing matched at all: this is not a standings file.
		if _, ok := index["Car Name"]; !ok {
			if _, ok := index["CarName"]; !ok {
				return nil, fmt.Errorf("the %d championship file has no car name column", year)
			}
		}
	}

	var out []Car
	for _, row := range rows[1:] {
		car := Car{Year: year}
		car.CarName = CleanCarName(col(row, carNames))
		if car.CarName == "" {
			continue // a blank line, or the stray row 2019 starts with
		}

		car.First = col(row, firstNames)
		car.Last = col(row, lastNames)
		if car.First == "" && car.Last == "" {
			// Later exports use one full-name column. Split on the last space:
			// the club writes "Chris Bryan", not "Bryan, Chris".
			whole := col(row, wholeNames)
			if i := strings.LastIndex(whole, " "); i > 0 {
				car.First, car.Last = whole[:i], whole[i+1:]
			} else {
				car.Last = whole
			}
		}
		car.CarNumber, _ = strconv.Atoi(col(row, carNumbers))
		car.Place, _ = strconv.Atoi(col(row, placeNames))

		out = append(out, car)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the %d championship file has no cars in it", year)
	}
	return out, nil
}
