package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/davisc01/derbyandales/internal/bracket"
	"github.com/davisc01/derbyandales/internal/bus"
	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/scoring"
	"github.com/davisc01/derbyandales/internal/store"
)

// BracketController runs the championship.
//
// A bracket matchup is a two-car heat like any other, so the timer, the gate
// and the displays all work the way they already do. What is different is what
// happens afterwards: instead of a time going into an average, a winner moves
// up the page, and eventually somebody has won the season.
type BracketController struct {
	app *App
}

// NewBracketController returns a controller bound to the application.
func NewBracketController(a *App) *BracketController { return &BracketController{app: a} }

// State is everything the championship screen and the bracket display need.
type State struct {
	RaceID   int64  `json:"race_id"`
	RaceName string `json:"race_name"`

	Seeded   bool `json:"seeded"`
	Entrants int  `json:"entrants"`
	Capacity int  `json:"capacity"`
	Byes     int  `json:"byes"`
	Rounds   int  `json:"rounds"`

	Seeds    []store.SeedView    `json:"-"`
	Matchups []store.MatchupView `json:"-"`

	// Next is the matchup waiting to be raced, if there is one.
	Next *store.MatchupView `json:"-"`

	// Champion is set once the final has been decided.
	Champion *store.EntryView `json:"-"`

	// Remaining counts the matchups still to be raced. Byes are resolved when
	// the bracket is built, so they are not in it: what is left is the number
	// of times two cars still have to go down the track, which is what someone
	// deciding whether to get another drink actually wants to know.
	Remaining int `json:"remaining"`
}

// State gathers the championship in one pass.
func (bc *BracketController) State(ctx context.Context, championshipID int64) (State, error) {
	var st State
	if championshipID == 0 {
		return st, nil
	}
	race, err := bc.app.DB.Race(ctx, championshipID)
	if err != nil {
		return st, err
	}
	st.RaceID = race.ID
	st.RaceName = race.Name

	if st.Seeds, err = bc.app.DB.Seeds(ctx, championshipID); err != nil {
		return st, err
	}
	st.Entrants = len(st.Seeds)
	st.Seeded = st.Entrants >= 2
	if st.Seeded {
		st.Capacity = bracket.Capacity(st.Entrants)
		st.Byes = st.Capacity - st.Entrants
		b, err := bracket.Build(st.Entrants)
		if err == nil {
			st.Rounds = b.Rounds
		}
	}

	if st.Matchups, err = bc.app.DB.Matchups(ctx, championshipID); err != nil {
		return st, err
	}
	for i := range st.Matchups {
		m := st.Matchups[i]
		if m.WinnerEntryID == nil {
			st.Remaining++
		}
		if st.Next == nil && m.Ready() {
			st.Next = &st.Matchups[i]
		}
	}

	if champ, err := bc.app.DB.Champion(ctx, championshipID); err == nil {
		st.Champion = &champ
	}
	return st, nil
}

// Seed proposes the championship field, matching each place to a car that has
// checked in.
func (bc *BracketController) Seed(ctx context.Context, seasonID, championshipID int64) ([]store.SeedProposal, error) {
	return bc.app.DB.ProposeSeeding(ctx, seasonID, championshipID)
}

// Generate saves a seeding and builds the bracket from it.
//
// A snapshot first: this is the point where a season's results turn into the
// night's running order, and getting it back by hand would mean re-typing
// twenty-four seeds.
func (bc *BracketController) Generate(ctx context.Context, championshipID int64,
	proposals []store.SeedProposal, actor string) (State, error) {

	var st State
	if _, err := bc.app.Backup(ctx, BackupBracketSeed); err != nil {
		bc.app.Log.Warn("snapshot before seeding failed", "err", err)
	}

	n, err := bc.app.DB.SaveSeeding(ctx, championshipID, proposals)
	if err != nil {
		return st, err
	}
	b, err := bc.app.DB.GenerateBracket(ctx, championshipID)
	if err != nil {
		return st, err
	}

	_ = bc.app.DB.Audit(ctx, actor, "bracket.generate",
		fmt.Sprintf("%d cars, %d byes, %d rounds", n, b.Byes, b.Rounds))
	bc.app.Bus.Publish(bus.TopicBracket, "generated", map[string]any{
		"race_id":  championshipID,
		"entrants": n,
		"byes":     b.Byes,
		"rounds":   b.Rounds,
	})
	return bc.State(ctx, championshipID)
}

// ArmNext builds the heat for the next matchup and arms the timer on it.
func (bc *BracketController) ArmNext(ctx context.Context, championshipID int64) error {
	m, err := bc.app.DB.NextMatchup(ctx, championshipID)
	if errors.Is(err, store.ErrNotFound) {
		return errors.New("every matchup that can be raced has been")
	}
	if err != nil {
		return err
	}
	return bc.ArmMatchup(ctx, championshipID, m.ID)
}

// ArmMatchup races one specific matchup.
func (bc *BracketController) ArmMatchup(ctx context.Context, championshipID, matchupID int64) error {
	heat, err := bc.app.DB.CreateMatchupHeat(ctx, championshipID, matchupID)
	if err != nil {
		return err
	}
	return bc.app.Race.ArmHeat(ctx, heat.ID)
}

// RecordResult decides a matchup from the times its heat recorded.
//
// The bracket is head to head: the faster car goes through, and the time itself
// only matters for the screen. A dead heat has to be settled by running it
// again, because there is no second criterion to fall back on and inventing one
// would be worse than asking.
func (bc *BracketController) RecordResult(ctx context.Context, championshipID, matchupID int64) error {
	m, err := bc.app.DB.Matchup(ctx, matchupID)
	if err != nil {
		return err
	}
	if m.HeatID == nil {
		return errors.New("that matchup has not been raced")
	}
	heat, err := bc.app.DB.Heat(ctx, *m.HeatID)
	if err != nil {
		return err
	}

	winner, err := headToHead(heat, m)
	if err != nil {
		return err
	}
	return bc.declare(ctx, championshipID, m, winner)
}

// Declare settles a matchup by hand, for a dead heat or a car that broke.
func (bc *BracketController) Declare(ctx context.Context, championshipID, matchupID, winnerID int64, actor, reason string) error {
	m, err := bc.app.DB.Matchup(ctx, matchupID)
	if err != nil {
		return err
	}
	if err := bc.declare(ctx, championshipID, m, winnerID); err != nil {
		return err
	}
	_ = bc.app.DB.Audit(ctx, actor, "bracket.declare",
		fmt.Sprintf("round %d position %d decided by hand: %s", m.Round, m.Position, reason))
	return nil
}

func (bc *BracketController) declare(ctx context.Context, championshipID int64,
	m model.BracketMatchup, winnerID int64) error {

	if err := bc.app.DB.RecordMatchupWinner(ctx, championshipID, m.ID, winnerID); err != nil {
		return err
	}

	winner, _ := bc.app.DB.Entry(ctx, winnerID)
	seeds, _ := bc.app.DB.Seeds(ctx, championshipID)
	seedOf := map[int64]int{}
	for _, s := range seeds {
		seedOf[s.Entry.ID] = s.Seed
	}
	loserID := *m.TopEntryID
	if loserID == winnerID {
		loserID = *m.BottomEntryID
	}

	// An upset is when the higher seed number wins, and it is the thing the
	// room reacts to, so it is published rather than left to be noticed.
	upset := seedOf[winnerID] > seedOf[loserID]

	bc.app.Bus.Publish(bus.TopicBracket, "result", map[string]any{
		"race_id":  championshipID,
		"matchup":  m.ID,
		"round":    m.Round,
		"position": m.Position,
		"winner":   winnerID,
		"seed":     seedOf[winnerID],
		"upset":    upset,
		"car":      winner.CarName,
		"driver":   winner.FullName(),
	})

	if champ, err := bc.app.DB.Champion(ctx, championshipID); err == nil {
		if err := bc.app.DB.SetRaceStatus(ctx, championshipID, model.StatusComplete); err != nil {
			bc.app.Log.Warn("marking the championship complete failed", "err", err)
		}
		if _, err := bc.app.Backup(ctx, BackupRaceComplete); err != nil {
			bc.app.Log.Warn("snapshot after the championship failed", "err", err)
		}
		bc.app.Log.Info("champion", "car", champ.CarName, "driver", champ.FullName(),
			"seed", seedOf[champ.ID])
		// A screen already showing the bracket shows the champion the moment
		// this is published, so that counts as presenting it. Without this, a
		// bracket left up all night would never tick off the run of show.
		if displays, err := bc.app.DB.Displays(ctx); err == nil {
			for _, d := range displays {
				if d.Page == string(store.SceneBracket) && time.Since(d.LastSeenAt) < store.DisplayOnlineWindow {
					_ = bc.app.DB.RecordSceneShown(ctx, championshipID, store.SceneBracket)
					break
				}
			}
		}
		bc.app.Bus.Publish(bus.TopicBracket, "champion", map[string]any{
			"race_id": championshipID,
			"entry":   champ.ID,
			"car":     champ.CarName,
			"driver":  champ.FullName(),
			"seed":    seedOf[champ.ID],
			"at":      time.Now(),
		})
	}
	return nil
}

// SwapLanes puts a pending matchup's two cars the other way round.
func (bc *BracketController) SwapLanes(ctx context.Context, matchupID int64, actor string) error {
	if err := bc.app.DB.SwapMatchupLanes(ctx, matchupID); err != nil {
		return err
	}
	_ = bc.app.DB.Audit(ctx, actor, "bracket.swap", fmt.Sprintf("matchup %d", matchupID))
	return nil
}

// MPH is the scale speed of a bracket run, for the screen.
func (bc *BracketController) MPH(ctx context.Context, seasonID int64, seconds float64) float64 {
	s, err := bc.app.DB.Season(ctx, seasonID)
	if err != nil {
		return 0
	}
	return scoring.ScaleMPH(s.TrackLengthFt, s.ScaleDenom, seconds)
}
