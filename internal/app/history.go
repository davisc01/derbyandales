package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/davisc01/derbyandales/internal/history"
	"github.com/davisc01/derbyandales/internal/model"
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

// ImportedRace is what one race night's import did.
type ImportedRace struct {
	Label   string
	Runs    int
	Problem string
}

// ImportRaces reads every published race night — heat times and standings —
// out of the website folder. They are what the club records go back through.
//
// Like the championships, it only reads, and re-running it replaces each night.
func (a *App) ImportRaces(ctx context.Context) ([]ImportedRace, error) {
	root, err := a.Publish.SitePath(ctx)
	if err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(root, "content", "races", "*", "*", "heats.csv"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)

	var out []ImportedRace
	races, runs := 0, 0
	for _, path := range paths {
		// .../content/races/<year>/<race-N or championship>/heats.csv
		dir := filepath.Dir(path)
		year, err := strconv.Atoi(filepath.Base(filepath.Dir(dir)))
		if err != nil {
			continue
		}
		kind, number, ok := raceFolder(filepath.Base(dir))
		if !ok {
			continue
		}
		label := fmt.Sprintf("%d %s", year, filepath.Base(dir))

		heats, err := os.ReadFile(path)
		if err != nil {
			out = append(out, ImportedRace{Label: label, Problem: "the heat results could not be read"})
			continue
		}
		standings, err := os.ReadFile(filepath.Join(dir, "standings.csv"))
		if err != nil {
			out = append(out, ImportedRace{Label: label, Problem: "there are no standings beside the heats"})
			continue
		}
		cars, err := history.ParseRace(heats, standings)
		if err != nil {
			out = append(out, ImportedRace{Label: label, Problem: err.Error()})
			continue
		}
		n, err := a.DB.ImportArchiveRace(ctx, year, kind, number, cars)
		if err != nil {
			return out, err
		}
		out = append(out, ImportedRace{Label: label, Runs: n})
		races++
		runs += n
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no race results found under %s", filepath.Join("content", "races"))
	}

	_ = a.DB.Audit(ctx, "coordinator", "history.import_races",
		fmt.Sprintf("%d race nights, %d runs", races, runs))
	a.Log.Info("imported past race nights", "races", races, "runs", runs)
	return out, nil
}

// raceFolder reads the site's folder name for a race night.
func raceFolder(name string) (model.RaceKind, int, bool) {
	if name == "championship" {
		// The championship is race number 1 of its kind, as it is here.
		return model.RaceChampionship, 1, true
	}
	n, err := strconv.Atoi(strings.TrimPrefix(name, "race-"))
	if err != nil || !strings.HasPrefix(name, "race-") {
		return "", 0, false
	}
	return model.RacePoints, n, true
}
