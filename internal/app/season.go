package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/davisc01/derbyandales/internal/bus"
	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/season"
	"github.com/davisc01/derbyandales/internal/store"
)

// SeasonController owns everything that spans a whole year rather than one
// night: wildcard points, the auto-qualifier list, and the corrections a
// coordinator makes to either.
//
// It is thin on purpose. The rules live in internal/season where they can be
// tested against the club's published results, and the rows live in the store.
// What is here is the part that has to be decided rather than calculated:
// when a result is frozen, and which changes are worth writing to the audit log.
type SeasonController struct {
	app *App
}

// NewSeasonController returns a controller bound to the application.
func NewSeasonController(a *App) *SeasonController { return &SeasonController{app: a} }

// FreezeRace records a finished race into the season standings.
//
// Called when the last heat lands. A failure here must never look like a
// failure to finish the race — the racing happened, and the points can always
// be recomputed afterwards — so the caller logs rather than propagates.
func (sc *SeasonController) FreezeRace(ctx context.Context, raceID int64) error {
	awards, err := sc.app.DB.FreezeRace(ctx, raceID)
	if err != nil {
		return err
	}

	scored := 0
	for _, a := range awards {
		if a.Points > 0 {
			scored++
		}
	}
	_ = sc.app.DB.Audit(ctx, "system", "season.freeze",
		fmt.Sprintf("race %d: %d cars, %d scored points", raceID, len(awards), scored))

	race, err := sc.app.DB.Race(ctx, raceID)
	if err != nil {
		return err
	}
	sc.announce(race.SeasonID, "frozen", map[string]any{
		"race_id": raceID,
		"cars":    len(awards),
		"scored":  scored,
	})
	return nil
}

// Recompute re-freezes every finished race in a season.
//
// This is the escape hatch for the case the freeze exists to prevent: a setting
// was wrong, or a result was corrected after the night. It is deliberately an
// action somebody takes, with an audit entry naming them, rather than something
// that happens quietly when a checkbox changes.
func (sc *SeasonController) Recompute(ctx context.Context, seasonID int64, actor string) (int, error) {
	races, err := sc.app.DB.Races(ctx, seasonID)
	if err != nil {
		return 0, err
	}

	// A snapshot first: this rewrites every points row in the season.
	if _, err := sc.app.Backup(ctx, BackupSeasonRecompute); err != nil {
		sc.app.Log.Warn("snapshot before recompute failed", "err", err)
	}

	done := 0
	for _, r := range races {
		if r.Kind != model.RacePoints || !racedOut(r) {
			continue
		}
		if _, err := sc.app.DB.FreezeRace(ctx, r.ID); err != nil {
			return done, err
		}
		done++
	}

	_ = sc.app.DB.Audit(ctx, actor, "season.recompute",
		fmt.Sprintf("season %d: %d races recomputed", seasonID, done))
	sc.announce(seasonID, "recomputed", map[string]any{"races": done})
	return done, nil
}

// racedOut reports whether a race's heats are all behind it.
//
// A race moves to "voting" the moment the last heat lands and only reaches
// "complete" once the awards are done, which can be days later. The points are
// settled at the first of those, not the second, so both count here.
func racedOut(r model.Race) bool {
	return r.Status == model.StatusVoting || r.Status == model.StatusComplete
}

// Overview is everything the season screen shows, gathered in one pass so the
// page cannot render a qualifier list and a points table computed a moment
// apart from each other.
type Overview struct {
	Season model.Season
	Rules  season.Rules

	Races []RaceStanding

	Qualifiers  []season.Slot
	Wildcard    []season.WildcardRow
	Adjustments []store.AdjustmentView

	// Entrants is the championship field these two lists produce as things
	// stand, and Expected is what a full season produces. They differ while the
	// season is part way through, which is the normal state of this screen.
	//
	// QualifyingPlaces is how many of those are won by finishing top of a race
	// rather than on points: it is what the count of qualifiers is heading for.
	Entrants         int
	Expected         int
	QualifyingPlaces int

	// OverLimit is how many slots are held by racers above the entry cap. Each
	// one needs a substitution before the bracket can be seeded.
	OverLimit int
}

// RaceStanding says where one race has got to, from the season's point of view.
type RaceStanding struct {
	Race     model.Race
	Frozen   bool
	FrozenAt time.Time
	Cars     int
	Scored   int
}

// Season gathers the whole picture for one year.
func (sc *SeasonController) Season(ctx context.Context, seasonID int64) (Overview, error) {
	var o Overview
	var err error

	if o.Season, err = sc.app.DB.Season(ctx, seasonID); err != nil {
		return o, err
	}
	if o.Rules, err = sc.app.DB.Rules(ctx, seasonID); err != nil {
		return o, err
	}
	if o.Qualifiers, err = sc.app.DB.Qualifiers(ctx, seasonID); err != nil {
		return o, err
	}
	if o.Wildcard, err = sc.app.DB.Wildcard(ctx, seasonID); err != nil {
		return o, err
	}
	if o.Adjustments, err = sc.app.DB.Adjustments(ctx, seasonID); err != nil {
		return o, err
	}

	finishes, err := sc.app.DB.SeasonFinishes(ctx, seasonID)
	if err != nil {
		return o, err
	}
	cars := map[int64]int{}
	scored := map[int64]int{}
	for _, f := range finishes {
		cars[f.RaceID]++
		if f.Points > 0 {
			scored[f.RaceID]++
		}
	}

	races, err := sc.app.DB.Races(ctx, seasonID)
	if err != nil {
		return o, err
	}
	for _, r := range races {
		if r.Kind != model.RacePoints {
			continue
		}
		rs := RaceStanding{Race: r, Cars: cars[r.ID], Scored: scored[r.ID]}
		rs.Frozen = rs.Cars > 0
		if rs.Frozen {
			rs.FrozenAt, _ = sc.app.DB.FrozenAt(ctx, r.ID)
		}
		o.Races = append(o.Races, rs)
	}

	o.QualifyingPlaces = o.Season.RaceCount * o.Season.AutoQualPlaces
	o.Entrants = len(o.Qualifiers) + o.Season.WildcardSpots
	o.Expected = o.QualifyingPlaces + o.Season.WildcardSpots
	for _, s := range o.Qualifiers {
		if s.OverLimit {
			o.OverLimit++
		}
	}
	return o, nil
}

// --- corrections -----------------------------------------------------------------

// Adjust records a manual points correction against a racer.
func (sc *SeasonController) Adjust(ctx context.Context, seasonID, racerID int64, points int, reason, actor string) error {
	a, err := sc.app.DB.AddAdjustment(ctx, model.Adjustment{
		SeasonID: seasonID, RacerID: racerID, Points: points, Reason: reason,
	})
	if err != nil {
		return err
	}
	_ = sc.app.DB.Audit(ctx, actor, "season.adjust",
		fmt.Sprintf("racer %d: %+d points — %s", racerID, a.Points, a.Reason))
	sc.announce(seasonID, "adjusted", map[string]any{"racer_id": racerID, "points": a.Points})
	return nil
}

// RemoveAdjustment deletes a correction.
func (sc *SeasonController) RemoveAdjustment(ctx context.Context, seasonID, id int64, actor string) error {
	if err := sc.app.DB.DeleteAdjustment(ctx, id); err != nil {
		return err
	}
	_ = sc.app.DB.Audit(ctx, actor, "season.adjust.remove", fmt.Sprintf("adjustment %d", id))
	sc.announce(seasonID, "adjusted", map[string]any{"removed": id})
	return nil
}

// Substitute hands an over-limit racer's slot to another car from the same race.
func (sc *SeasonController) Substitute(ctx context.Context, seasonID, originalEntryID, substituteEntryID int64, actor string) error {
	if err := sc.app.DB.Substitute(ctx, seasonID, originalEntryID, substituteEntryID); err != nil {
		return err
	}
	_ = sc.app.DB.Audit(ctx, actor, "season.substitute",
		fmt.Sprintf("entry %d takes the slot held by entry %d", substituteEntryID, originalEntryID))
	sc.announce(seasonID, "seeding", map[string]any{
		"original":   originalEntryID,
		"substitute": substituteEntryID,
	})
	return nil
}

// UndoSubstitution puts the original slot back.
func (sc *SeasonController) UndoSubstitution(ctx context.Context, seasonID, originalEntryID int64, actor string) error {
	if err := sc.app.DB.UndoSubstitution(ctx, seasonID, originalEntryID); err != nil {
		return err
	}
	_ = sc.app.DB.Audit(ctx, actor, "season.substitute.undo", fmt.Sprintf("entry %d", originalEntryID))
	sc.announce(seasonID, "seeding", map[string]any{"restored": originalEntryID})
	return nil
}

// CurrentSeasonID reports the season the UI should open on: the one chosen in
// settings, otherwise the newest.
func (sc *SeasonController) CurrentSeasonID(ctx context.Context) (int64, error) {
	if id, _ := sc.app.DB.SettingInt(ctx, store.KeyActiveSeasonID, 0); id > 0 {
		if _, err := sc.app.DB.Season(ctx, int64(id)); err == nil {
			return int64(id), nil
		}
	}
	seasons, err := sc.app.DB.Seasons(ctx)
	if err != nil {
		return 0, err
	}
	if len(seasons) == 0 {
		return 0, errors.New("no season has been created yet")
	}
	return seasons[0].ID, nil
}

func (sc *SeasonController) announce(seasonID int64, kind string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["season_id"] = seasonID
	sc.app.Bus.Publish(bus.TopicSeason, kind, data)
}
