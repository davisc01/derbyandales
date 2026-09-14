package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/davisc01/derbyandales/internal/history"
)

// Importing the club's own archive.
//
// The previous-championship rule has always been checked from memory. The
// evidence for it is sitting on the club's website — seven years of published
// championship standings — so it is read from there rather than typed in.

// ImportedYear is what one year's import did.
type ImportedYear struct {
	Year int
	Cars int
	// Problem explains a year that could not be read, rather than leaving it
	// silently missing from a list somebody is about to trust.
	Problem string
}

// ImportChampionships reads every published championship out of the website
// folder and records the cars that raced in them.
//
// It reads; it never writes to the site. Re-running it replaces each year, so
// it is safe to run again after the archive is corrected — which matters,
// because it is.
func (a *App) ImportChampionships(ctx context.Context) ([]ImportedYear, error) {
	root, err := a.Publish.SitePath(ctx)
	if err != nil {
		return nil, err
	}

	pattern := filepath.Join(root, "content", "races", "*", "championship", "standings.csv")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no championship results found under %s",
			filepath.Join("content", "races"))
	}
	sort.Strings(paths)

	var out []ImportedYear
	for _, path := range paths {
		// .../content/races/<year>/championship/standings.csv
		yearDir := filepath.Base(filepath.Dir(filepath.Dir(path)))
		year, err := strconv.Atoi(yearDir)
		if err != nil {
			continue // not a year folder
		}

		body, err := os.ReadFile(path)
		if err != nil {
			out = append(out, ImportedYear{Year: year, Problem: "could not be read"})
			continue
		}
		cars, err := history.ParseChampionship(year, body)
		if err != nil {
			out = append(out, ImportedYear{Year: year, Problem: err.Error()})
			continue
		}
		n, err := a.DB.ImportChampionship(ctx, year, cars)
		if err != nil {
			return out, err
		}
		out = append(out, ImportedYear{Year: year, Cars: n})
	}

	years, cars := 0, 0
	for _, y := range out {
		if y.Problem == "" {
			years++
			cars += y.Cars
		}
	}
	_ = a.DB.Audit(ctx, "coordinator", "history.import",
		fmt.Sprintf("%d championships, %d cars", years, cars))
	a.Log.Info("imported past championships", "years", years, "cars", cars)
	return out, nil
}
