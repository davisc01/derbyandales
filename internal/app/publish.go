package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/davisc01/derbyandales/internal/bus"
	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/publish"
	"github.com/davisc01/derbyandales/internal/season"
	"github.com/davisc01/derbyandales/internal/store"
)

// PublishController writes results into the club's website.
//
// It never runs git. The site is a working tree somebody reviews and commits,
// and the failure this design is protecting against is not a refused write —
// it is a silent one that turns up in a commit weeks later with nobody able to
// say where it came from.
type PublishController struct {
	app *App
}

// NewPublishController returns a controller bound to the application.
func NewPublishController(a *App) *PublishController { return &PublishController{app: a} }

// SitePath is the configured website folder.
func (pc *PublishController) SitePath(ctx context.Context) (string, error) {
	path, err := pc.app.DB.Setting(ctx, store.KeyDerbySitePath, "")
	if err != nil {
		return "", err
	}
	path = strings.TrimSpace(path)
	if err := publish.SiteRoot(path); err != nil {
		return path, err
	}
	return path, nil
}

// PlanRace works out what publishing one race night would write.
func (pc *PublishController) PlanRace(ctx context.Context, raceID int64) (publish.Plan, error) {
	root, err := pc.SitePath(ctx)
	if err != nil {
		return publish.Plan{}, err
	}

	race, err := pc.app.DB.Race(ctx, raceID)
	if err != nil {
		return publish.Plan{}, err
	}
	s, err := pc.app.DB.Season(ctx, race.SeasonID)
	if err != nil {
		return publish.Plan{}, err
	}

	files := publish.RaceFiles{
		Page: publish.Page{
			Year:         s.Year,
			Number:       race.Number,
			Name:         race.Name,
			Venue:        race.Venue,
			Date:         race.Date,
			Championship: race.Kind == model.RaceChampionship,
		},
		TrackFt:    s.TrackLengthFt,
		ScaleDenom: s.ScaleDenom,
	}

	if files.Heats, err = pc.heatRows(ctx, raceID); err != nil {
		return publish.Plan{}, err
	}
	if len(files.Heats) == 0 {
		return publish.Plan{}, fmt.Errorf("%s has no results yet", raceLabel(race))
	}
	// Half a bracket has no finishing order to publish: most of the field has
	// no place until the cars above them are decided.
	if race.Bracket() {
		if _, err := pc.app.DB.Champion(ctx, raceID); err != nil {
			return publish.Plan{}, fmt.Errorf("%s has not been won yet", raceLabel(race))
		}
	}
	if files.Standings, err = pc.standingRows(ctx, raceID); err != nil {
		return publish.Plan{}, err
	}
	if !files.Page.Championship {
		if files.Awards, err = pc.awardRows(ctx, raceID); err != nil {
			return publish.Plan{}, err
		}
	}
	return publish.PlanRace(root, files)
}

// heatRows collects every recorded run, including the pace car and any car
// ruled ineligible. This file is the record of what happened on the track.
func (pc *PublishController) heatRows(ctx context.Context, raceID int64) ([]publish.HeatRow, error) {
	heats, err := pc.app.DB.Heats(ctx, raceID)
	if err != nil {
		return nil, err
	}
	var out []publish.HeatRow
	for _, h := range heats {
		for _, l := range h.Lanes {
			// A bye is an empty lane. There is nothing to report about it.
			if l.EntryID == nil || l.FinishTime == nil {
				continue
			}
			place := 0
			if l.FinishPlace != nil {
				place = *l.FinishPlace
			}
			out = append(out, publish.HeatRow{
				Heat: h.Number, Lane: l.Lane,
				First: l.DriverFirst, Last: l.DriverLast,
				CarNumber: l.CarNumber, CarName: l.CarName,
				Time: *l.FinishTime, Place: place,
			})
		}
	}
	return out, nil
}

func (pc *PublishController) standingRows(ctx context.Context, raceID int64) ([]publish.StandingRow, error) {
	standings, err := pc.app.DB.Standings(ctx, raceID)
	if err != nil {
		return nil, err
	}
	var out []publish.StandingRow
	for _, st := range standings {
		// A car with no usable run has no place. Publishing it as 0 would read
		// as a result rather than as an absence.
		if st.Place == 0 {
			continue
		}
		out = append(out, publish.StandingRow{
			Place: st.Place, CarNumber: st.Entry.CarNumber,
			Name: st.Entry.FullName(), CarName: st.Entry.CarName,
			Heats: st.Heats, Average: st.Average, Best: st.Best, Worst: st.Worst,
		})
	}
	return out, nil
}

func (pc *PublishController) awardRows(ctx context.Context, raceID int64) ([]publish.AwardRow, error) {
	awards, err := pc.app.DB.RaceAwards(ctx, raceID)
	if err != nil {
		return nil, err
	}
	var out []publish.AwardRow
	for _, a := range awards {
		if a.EntryID == nil {
			continue // not decided yet
		}
		out = append(out, publish.AwardRow{
			Award: a.Name,
			First: a.Entry.FirstName, Last: a.Entry.LastName,
			CarNumber: a.Entry.CarNumber, CarName: a.Entry.CarName,
		})
	}
	return out, nil
}

// PlanSeason works out what publishing the season standings would write.
func (pc *PublishController) PlanSeason(ctx context.Context, seasonID int64) (publish.Plan, error) {
	root, err := pc.SitePath(ctx)
	if err != nil {
		return publish.Plan{}, err
	}
	s, err := pc.app.DB.Season(ctx, seasonID)
	if err != nil {
		return publish.Plan{}, err
	}

	qualifiers, err := pc.app.DB.Qualifiers(ctx, seasonID)
	if err != nil {
		return publish.Plan{}, err
	}
	standings, err := pc.app.DB.Wildcard(ctx, seasonID)
	if err != nil {
		return publish.Plan{}, err
	}
	if len(qualifiers) == 0 && len(standings) == 0 {
		return publish.Plan{}, errors.New("no races have been recorded for this season yet")
	}

	files := publish.SeasonFiles{
		Page: publish.SeasonPage{
			Year: s.Year, Races: s.RaceCount, AutoQualPlaces: s.AutoQualPlaces,
			WildcardSpots: s.WildcardSpots, MaxEntries: s.MaxChampionshipEntry,
		},
	}
	for _, q := range qualifiers {
		files.Qualifiers = append(files.Qualifiers, publish.QualifierRow{
			Seed: q.Seed, Driver: q.Driver, CarName: q.CarName,
			Race: q.RaceNumber, Finish: q.Place, Average: q.Average,
			Entries: q.Entries, OverLimit: q.OverLimit,
		})
	}
	for _, row := range standings {
		files.Wildcard = append(files.Wildcard, publish.WildcardRow{
			Rank: row.Rank, Name: row.Name, Points: season.FormatPoints(row),
		})
	}
	return publish.PlanSeason(root, files)
}

// Apply writes a plan and records what it wrote.
func (pc *PublishController) Apply(ctx context.Context, plan publish.Plan, actor string) ([]string, error) {
	if plan.Changes() == 0 {
		return nil, nil
	}
	written, err := publish.Apply(plan)
	if len(written) > 0 {
		_ = pc.app.DB.Audit(ctx, actor, "publish",
			fmt.Sprintf("%s: %s", plan.Label, strings.Join(written, ", ")))
		pc.app.Bus.Publish(bus.TopicSystem, "published", map[string]any{
			"label": plan.Label,
			"files": written,
		})
		pc.app.Log.Info("published", "what", plan.Label, "files", len(written))
	}
	return written, err
}

// raceLabel is what to call a race in a message someone reads at the venue.
func raceLabel(r model.Race) string {
	if r.Name != "" {
		return r.Name
	}
	if r.Kind == model.RaceChampionship {
		return "the championship"
	}
	return fmt.Sprintf("race %d", r.Number)
}
