package app

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/schedule"
	"github.com/davisc01/derbyandales/internal/store"
)

// A demo season needs races behind it, not just one to run.
//
// Season points, the auto-qualifier list and the championship bracket are all
// meaningless on the first night of a year. Without earlier races there is
// nothing on those screens to look at and nothing to rehearse, so the demo
// fabricates a season already in progress: five nights raced and scored, and a
// sixth waiting at check-in.
//
// The past races are written straight into the database rather than run through
// the timer. They are invented history, not a rehearsal of how a race night
// goes — the live race is there for that.

// demoPastRaces is how many nights the demo season has already run. Five, so
// the sixth completes a standard season and the championship becomes seedable.
const demoPastRaces = 5

// demoVenues gives each fabricated night somewhere plausible to have happened.
var demoVenues = []string{
	"Demo Brewing Co.",
	"The Testing Room",
	"Placeholder Taproom",
	"Sample Street Brewery",
	"Fixture & Firkin",
	"Golden Path Alehouse",
}

// SeedDemoSeason creates a demo season with five scored races behind it and a
// sixth ready to check in. This is what -demo produces.
func (a *App) SeedDemoSeason(ctx context.Context, year int) (model.Race, error) {
	created, err := a.seedDemoSeasonRow(ctx, year)
	if err != nil {
		return model.Race{}, err
	}

	// Deterministic, so two people comparing demo databases see the same season
	// and a bug reported against it can be reproduced.
	rng := rand.New(rand.NewSource(int64(year)))

	for n := 1; n <= demoPastRaces; n++ {
		if err := a.seedPastRace(ctx, created.ID, n, rng); err != nil {
			return model.Race{}, fmt.Errorf("demo race %d: %w", n, err)
		}
	}

	// The live race is the season's last night.
	return a.seedDemoLiveRace(ctx, created, demoPastRaces+1)
}

// seedPastRace fabricates one already-raced night and records it in the season.
func (a *App) seedPastRace(ctx context.Context, seasonID int64, number int, rng *rand.Rand) error {
	race, err := a.DB.CreateRace(ctx, model.Race{
		SeasonID: seasonID,
		Number:   number,
		Name:     fmt.Sprintf("Race %d", number),
		// Roughly monthly, working backwards from the live race.
		Date:   time.Now().AddDate(0, -(demoPastRaces + 1 - number), 0),
		Venue:  demoVenues[(number-1)%len(demoVenues)],
		Kind:   model.RacePoints,
		Status: model.StatusVoting,
	})
	if err != nil {
		return err
	}

	// Vary the field between nights. Turnout does vary, and a season where
	// every race has the identical roster would hide the fact that points
	// depend on how many people showed up.
	field := demoRacers
	if number%2 == 0 {
		field = demoRacers[:len(demoRacers)-3]
	}

	entries := make([]store.EntryView, 0, len(field))
	for _, d := range field {
		racer, err := a.DB.FindOrCreateRacer(ctx, seasonID, d.First, d.Last)
		if err != nil {
			return err
		}
		checkedIn := race.Date
		entry := model.Entry{
			RaceID:      race.ID,
			RacerID:     racer.ID,
			CarNumber:   d.Number,
			CarName:     d.Car,
			IsControl:   d.Car == "CONTROL",
			CheckedInAt: &checkedIn,
		}
		// One night with an ineligible car in it, because that is what the real
		// seasons look like: two of 2026's five races had one. It races, it is
		// timed, and it scores nothing.
		if number == 3 && d.Number == 62 {
			entry.Excluded = true
			entry.ExclusionReason = "raced in a previous championship"
		}
		created, err := a.DB.CreateEntry(ctx, entry)
		if err != nil {
			return err
		}
		entries = append(entries, store.EntryView{
			Entry: created, FirstName: d.First, LastName: d.Last,
		})
	}

	s, err := schedule.Generate(len(entries), 4)
	if err != nil {
		return err
	}
	if err := a.DB.SaveSchedule(ctx, race.ID, s, entries); err != nil {
		return err
	}

	// A fast car is fast every night.
	//
	// Giving each car a fresh random pace per race would scatter the podium
	// across the whole roster, and then no racer would ever hold more
	// championship slots than the cap allows — so the over-limit warning and
	// the substitution screen would never appear in the demo at all. A pace
	// that persists across the season, with a little variation per night, is
	// both more realistic and what makes those screens worth rehearsing.
	pace := make(map[int64]float64, len(entries))
	for i, e := range entries {
		base := seasonPace(i) + rng.Float64()*0.14
		if e.IsControl {
			base = 2.85 // the pace car is deliberately slow
		}
		pace[e.ID] = base
	}

	heats, err := a.DB.Heats(ctx, race.ID)
	if err != nil {
		return err
	}
	for _, h := range heats {
		times := map[int]float64{}
		for _, l := range h.Lanes {
			if l.EntryID == nil {
				continue
			}
			t := pace[*l.EntryID] + rng.NormFloat64()*0.03
			// One run in about eighty comes apart. The scoring rule drops the
			// slowest run, so a single bad heat should cost a car almost
			// nothing — and the demo should show that it does.
			if rng.Intn(80) == 0 {
				t += 0.4
			}
			times[l.Lane] = round3(t)
		}
		if err := a.DB.RecordHeatResults(ctx, h.ID, times); err != nil {
			return err
		}
	}

	if _, err := a.DB.FreezeRace(ctx, race.ID); err != nil {
		return err
	}
	return nil
}

// seasonPace is a car's underlying speed for the whole demo season, spread
// closely enough that the per-night variation still reorders the podium.
func seasonPace(i int) float64 {
	return 2.30 + float64(i)*0.012
}

// round3 puts a fabricated time on the millisecond, the way a timer reports it.
func round3(t float64) float64 {
	return float64(int64(t*1000+0.5)) / 1000
}
