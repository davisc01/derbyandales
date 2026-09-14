package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/store"
)

// Demo data exists so the displays, the race screen and the timer bench can all
// be exercised before check-in is built. It is also how someone learns the
// software before a race night, which is the whole point of the project.
//
// It is never created automatically: it takes an explicit flag or button, and
// the season is named so nobody mistakes it for real results.

// demoRacers are invented names, deliberately not the club's real racers.
var demoRacers = []struct {
	First, Last, Car string
	Number           int
}{
	{"Derby", "Ales", "CONTROL", 1},
	{"Ada", "Fairweather", "Lightning Bug", 7},
	{"Bertie", "Colhoun", "Slab Jack", 12},
	{"Cass", "Nguyen", "Toast", 19},
	{"Dario", "Oyelaran", "Wet Paint", 23},
	{"Edie", "Marchetti", "Hot Glue", 31},
	{"Femi", "Blackwood", "Second Breakfast", 40},
	{"Greta", "Sandoval", "Unsafe At Any Speed", 44},
	{"Hal", "Pemberton", "Rolling Blackout", 51},
	{"Ines", "Kovač", "The Long Way", 58},
	{"Jonah", "Whitlock", "Bad Idea", 62},
	{"Kit", "Abernathy", "Structural Concern", 66},
	{"Lena", "Moreau", "Pigeon", 73},
	{"Mo", "Achterberg", "Deadline", 77},
	{"Nils", "Baptiste", "Gravy Boat", 81},
	{"Orla", "Fitzgerald", "Regrettable", 84},
	{"Piet", "Okonkwo", "Committee Decision", 88},
	{"Quinn", "Ravensworth", "Sensible Shoes", 91},
	{"Rosa", "Lindqvist", "Escape Velocity", 95},
	{"Sam", "Thibodeaux", "Final Warning", 99},
}

// SeedDemoRace creates a season and one race with a field of cars, ready to
// check in. For a season with nights already behind it, see SeedDemoSeason.
func (a *App) SeedDemoRace(ctx context.Context, year int) (model.Race, error) {
	created, err := a.seedDemoSeasonRow(ctx, year)
	if err != nil {
		return model.Race{}, err
	}
	return a.seedDemoLiveRace(ctx, created, 1)
}

// seedDemoSeasonRow creates the season itself, named so nobody mistakes it for
// real results.
func (a *App) seedDemoSeasonRow(ctx context.Context, year int) (model.Season, error) {
	s := store.DefaultSeason(year)
	s.Name = fmt.Sprintf("%d Season (demo data)", year)

	created, err := a.DB.CreateSeason(ctx, s)
	if err != nil {
		return created, fmt.Errorf("create demo season: %w", err)
	}
	return created, nil
}

// seedDemoLiveRace creates the race that is waiting to be run, with every car
// already checked in.
func (a *App) seedDemoLiveRace(ctx context.Context, created model.Season, number int) (model.Race, error) {
	race, err := a.DB.CreateRace(ctx, model.Race{
		SeasonID: created.ID,
		Number:   number,
		Name:     fmt.Sprintf("Race %d", number),
		Date:     time.Now(),
		Venue:    demoVenues[(number-1)%len(demoVenues)],
		Kind:     model.RacePoints,
		Status:   model.StatusCheckin,
	})
	if err != nil {
		return race, fmt.Errorf("create demo race: %w", err)
	}

	for _, d := range demoRacers {
		racer, err := a.DB.FindOrCreateRacer(ctx, created.ID, d.First, d.Last)
		if err != nil {
			return race, err
		}
		now := time.Now()
		entry := model.Entry{
			RaceID:      race.ID,
			RacerID:     racer.ID,
			CarNumber:   d.Number,
			CarName:     d.Car,
			IsControl:   d.Car == "CONTROL",
			CheckedInAt: &now,
		}
		// One ineligible entry, so the excluded path is visible rather than
		// theoretical — it is the flag that decided a real race.
		if d.Number == 62 {
			entry.Excluded = true
			entry.ExclusionReason = "raced in a previous championship"
		}
		if _, err := a.DB.CreateEntry(ctx, entry); err != nil {
			return race, fmt.Errorf("create demo entry %d: %w", d.Number, err)
		}
	}

	_ = a.DB.Audit(ctx, "system", "demo.seed",
		fmt.Sprintf("%s with %d cars", race.Name, len(demoRacers)))
	a.Log.Info("demo data created", "season", created.Name, "race", race.Name,
		"cars", len(demoRacers))
	return race, nil
}

// HasDemoData reports whether a demo season already exists, so seeding twice
// does not quietly make a second one.
func (a *App) HasDemoData(ctx context.Context) bool {
	seasons, err := a.DB.Seasons(ctx)
	if err != nil {
		return false
	}
	for _, s := range seasons {
		if strings.Contains(s.Name, "demo data") {
			return true
		}
	}
	return false
}
