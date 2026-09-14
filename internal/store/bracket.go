package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/davisc01/derbyandales/internal/bracket"
	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/season"
)

// The championship, stored.
//
// Seeding is two problems wearing one name. The first is the order, which is
// decided entirely by the season and is in internal/season. The second is which
// car actually goes in each slot, which cannot be known until people turn up —
// a racer holding three qualifying slots brings three cars, a wildcard racer
// brings whatever they have, and somebody always drops out. That second half is
// here, because it needs the championship's own check-in list.

// SeedProposal is one championship place with the car it has been matched to,
// or an explanation of why it has not been.
type SeedProposal struct {
	season.Candidate

	// EntryID is the championship entry filling this slot, or zero.
	EntryID int64
	// CarNumber and Car describe that entry, for the review screen.
	CarNumber int
	Car       string

	// Why says how the match was made, or why it could not be.
	Why string
	// Exact marks a match on both the racer and the car that qualified, which
	// is the case nobody needs to check.
	Exact bool
}

// Matched reports whether a car has been found for this slot.
func (p SeedProposal) Matched() bool { return p.EntryID != 0 }

// ProposeSeeding works out the championship field and matches each place to a
// car that has checked in.
//
// The matching is deliberately conservative. An exact match is the same racer
// with the same car that qualified, and that is left alone. Anything else — a
// racer who brought a different car, a wildcard racer, a slot with nobody to
// fill it — is reported with a reason so a person decides. Getting this wrong
// puts somebody in the wrong half of the bracket, which is not the kind of
// mistake that surfaces until the semi-final.
func (db *DB) ProposeSeeding(ctx context.Context, seasonID, championshipID int64) ([]SeedProposal, error) {
	race, err := db.Race(ctx, championshipID)
	if err != nil {
		return nil, err
	}
	if race.Kind != model.RaceChampionship {
		return nil, fmt.Errorf("%s is a points race, not the championship", raceName(race))
	}

	s, err := db.Season(ctx, seasonID)
	if err != nil {
		return nil, err
	}
	qualifiers, err := db.Qualifiers(ctx, seasonID)
	if err != nil {
		return nil, err
	}
	standings, err := db.Wildcard(ctx, seasonID)
	if err != nil {
		return nil, err
	}
	field := season.Field(qualifiers, standings, s.WildcardSpots)

	entries, err := db.RacingEntries(ctx, championshipID)
	if err != nil {
		return nil, err
	}
	// The pace car does not race the championship, and an excluded car is
	// ineligible by definition.
	available := make([]EntryView, 0, len(entries))
	for _, e := range entries {
		if e.EarnsPoints() {
			available = append(available, e)
		}
	}

	// Which qualifying car each championship entry corresponds to, by racer and
	// car name. Car names are how people identify their cars to each other, so
	// they are the strongest signal available.
	used := map[int64]bool{}
	out := make([]SeedProposal, 0, len(field))

	// Two passes, so an exact match is never beaten to its car by a looser one
	// earlier in the list.
	proposals := make([]SeedProposal, len(field))
	for i, c := range field {
		proposals[i] = SeedProposal{Candidate: c}
	}
	for i := range proposals {
		c := proposals[i].Candidate
		if c.Origin != season.OriginQualifier {
			continue
		}
		for _, e := range available {
			if used[e.ID] || e.RacerID != c.RacerID {
				continue
			}
			if !sameCar(e.CarName, c.CarName) {
				continue
			}
			used[e.ID] = true
			proposals[i].EntryID = e.ID
			proposals[i].CarNumber = e.CarNumber
			proposals[i].Car = e.CarName
			proposals[i].Exact = true
			proposals[i].Why = "the car that qualified"
			break
		}
	}
	for i := range proposals {
		if proposals[i].Matched() {
			continue
		}
		c := proposals[i].Candidate
		for _, e := range available {
			if used[e.ID] || e.RacerID != c.RacerID {
				continue
			}
			used[e.ID] = true
			proposals[i].EntryID = e.ID
			proposals[i].CarNumber = e.CarNumber
			proposals[i].Car = e.CarName
			if c.Origin == season.OriginWildcard {
				proposals[i].Why = "the car they brought"
			} else {
				proposals[i].Why = fmt.Sprintf("a different car — %s qualified", c.CarName)
			}
			break
		}
		if !proposals[i].Matched() {
			proposals[i].Why = c.Driver + " has not checked in"
		}
	}

	out = append(out, proposals...)
	return out, nil
}

// sameCar compares car names the way a person would: ignoring case and the
// spaces around them.
func sameCar(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// SaveSeeding writes the championship field, replacing whatever was there.
//
// Only matched places are stored, and they are renumbered 1..n in the order
// given. A racer who did not turn up therefore shrinks the field rather than
// leaving a hole in it — which is the right answer, because a hole would be a
// bye that nobody earned.
func (db *DB) SaveSeeding(ctx context.Context, championshipID int64, proposals []SeedProposal) (int, error) {
	seen := map[int64]bool{}
	for _, p := range proposals {
		if !p.Matched() {
			continue
		}
		if seen[p.EntryID] {
			return 0, fmt.Errorf("car %d is seeded twice", p.CarNumber)
		}
		seen[p.EntryID] = true
	}

	n := 0
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM bracket_seed WHERE race_id = ?`, championshipID); err != nil {
			return err
		}
		seed := 0
		for _, p := range proposals {
			if !p.Matched() {
				continue
			}
			seed++
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO bracket_seed (race_id, entry_id, seed, origin) VALUES (?,?,?,?)`,
				championshipID, p.EntryID, seed, string(p.Origin)); err != nil {
				return err
			}
		}
		n = seed
		return nil
	})
	return n, err
}

// SeedView is one stored seed with its car.
type SeedView struct {
	Seed   int
	Origin season.Origin
	Entry  EntryView
	// HasBye is derived from the seed number, never stored. The old model
	// inferred bye, qualifier and wildcard from one seed range apiece, so a bye
	// racer could not also be labelled an auto-qualifier — which is what they
	// are.
	HasBye bool
}

// Seeds lists the championship field in seed order.
func (db *DB) Seeds(ctx context.Context, championshipID int64) ([]SeedView, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT bs.seed, bs.origin, `+entryCols+`
		FROM bracket_seed bs
		JOIN entry e ON e.id = bs.entry_id
		JOIN racer r ON r.id = e.racer_id
		WHERE bs.race_id = ?
		ORDER BY bs.seed`, championshipID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SeedView
	for rows.Next() {
		var v SeedView
		var origin string
		var e EntryView
		var photoID, checkedIn sql.NullInt64
		var isControl, excluded int
		if err := rows.Scan(&v.Seed, &origin,
			&e.ID, &e.RaceID, &e.RacerID, &e.CarNumber, &e.CarName, &photoID,
			&isControl, &excluded, &e.ExclusionReason, &checkedIn, &e.Note,
			&e.FirstName, &e.LastName); err != nil {
			return nil, err
		}
		e.PhotoID = nullInt(photoID)
		e.IsControl = isControl != 0
		e.Excluded = excluded != 0
		e.CheckedInAt = fromUnixPtr(checkedIn)
		v.Origin = season.Origin(origin)
		v.Entry = e
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Byes follow from the field size, so they are worked out here rather than
	// remembered.
	if len(out) >= 2 {
		byes := bracket.Capacity(len(out)) - len(out)
		for i := range out {
			out[i].HasBye = out[i].Seed <= byes
		}
	}
	return out, nil
}

// --- the bracket itself ----------------------------------------------------------

// MatchupView is one matchup with the cars in it.
type MatchupView struct {
	model.BracketMatchup
	Top    *EntryView
	Bottom *EntryView
	Winner *EntryView

	// TopSeed and BottomSeed are 0 when the slot is empty or unresolved.
	TopSeed    int
	BottomSeed int

	// Heat is the heat this matchup races, once it has one.
	HeatNumber int
}

// Ready reports whether both cars are known, so this matchup can be raced.
func (m MatchupView) Ready() bool {
	return m.TopEntryID != nil && m.BottomEntryID != nil && m.WinnerEntryID == nil
}

// Walkover reports a matchup with one car in it, which advances without racing.
func (m MatchupView) Walkover() bool {
	return (m.TopEntryID == nil) != (m.BottomEntryID == nil)
}

// TopWon and BottomWon say which side went through, for marking the page.
func (m MatchupView) TopWon() bool {
	return m.WinnerEntryID != nil && m.TopEntryID != nil && *m.WinnerEntryID == *m.TopEntryID
}

func (m MatchupView) BottomWon() bool {
	return m.WinnerEntryID != nil && m.BottomEntryID != nil && *m.WinnerEntryID == *m.BottomEntryID
}

// Only returns the single car in a walkover, and OnlySeed its seed. Two methods
// rather than two return values, because a template cannot take the second.
func (m MatchupView) Only() *EntryView {
	if m.Top != nil {
		return m.Top
	}
	return m.Bottom
}

func (m MatchupView) OnlySeed() int {
	if m.Top != nil {
		return m.TopSeed
	}
	return m.BottomSeed
}

// Upset reports a decided matchup won by the higher seed number, which is the
// thing the room reacts to.
func (m MatchupView) Upset() bool {
	if m.WinnerEntryID == nil || m.TopSeed == 0 || m.BottomSeed == 0 {
		return false
	}
	if m.TopWon() {
		return m.TopSeed > m.BottomSeed
	}
	return m.BottomSeed > m.TopSeed
}

// GenerateBracket builds the matchups from the stored seeding.
//
// Walkovers are resolved as it goes, so a bye racer appears in round two the
// moment the bracket exists rather than after somebody presses something.
func (db *DB) GenerateBracket(ctx context.Context, championshipID int64) (*bracket.Bracket, error) {
	seeds, err := db.Seeds(ctx, championshipID)
	if err != nil {
		return nil, err
	}
	if len(seeds) < 2 {
		return nil, errors.New("seed the championship first — there are not enough cars for a bracket")
	}

	// Regenerating would discard results, and the bracket is the part of the
	// night people are watching.
	run, err := db.bracketHasResults(ctx, championshipID)
	if err != nil {
		return nil, err
	}
	if run {
		return nil, errors.New("the championship has already started, so the bracket cannot be rebuilt")
	}

	b, err := bracket.Build(len(seeds))
	if err != nil {
		return nil, err
	}
	bySeed := make(map[int]int64, len(seeds))
	for _, s := range seeds {
		bySeed[s.Seed] = s.Entry.ID
	}

	err = db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM bracket_matchup WHERE race_id = ?`, championshipID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM heat WHERE race_id = ? AND phase = ?`,
			championshipID, model.PhaseBracket); err != nil {
			return err
		}

		// Round one first, so the walkovers can be pushed up as they are found.
		ids := map[[2]int]int64{} // round, position -> matchup id
		for _, m := range b.Matchups {
			var top, bottom any
			if m.Round == 1 {
				if id, ok := bySeed[m.Top]; ok {
					top = id
				}
				if id, ok := bySeed[m.Bottom]; ok {
					bottom = id
				}
			}
			res, err := tx.ExecContext(ctx, `
				INSERT INTO bracket_matchup (race_id, round, position, top_entry_id, bottom_entry_id)
				VALUES (?,?,?,?,?)`, championshipID, m.Round, m.Position, top, bottom)
			if err != nil {
				return fmt.Errorf("round %d position %d: %w", m.Round, m.Position, err)
			}
			id, err := res.LastInsertId()
			if err != nil {
				return err
			}
			ids[[2]int{m.Round, m.Position}] = id
		}

		// Walk the byes up into round two. A bye is not a race, so it should
		// never look like one that is waiting to happen.
		for _, m := range b.Round(1) {
			if !m.Walkover() {
				continue
			}
			seed := m.Top
			if seed == bracket.Bye {
				seed = m.Bottom
			}
			entryID := bySeed[seed]
			id := ids[[2]int{1, m.Position}]
			if _, err := tx.ExecContext(ctx,
				`UPDATE bracket_matchup SET winner_entry_id = ? WHERE id = ?`, entryID, id); err != nil {
				return err
			}
			if err := placeWinner(ctx, tx, championshipID, b, 1, m.Position, entryID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return b, nil
}

// placeWinner writes a winner into the slot it feeds in the round above.
func placeWinner(ctx context.Context, tx *sql.Tx, raceID int64, b *bracket.Bracket,
	round, position int, entryID int64) error {

	nextRound, nextPosition, top, ok := bracket.Feeds(round, position, b.Rounds)
	if !ok {
		return nil // the final: there is nowhere above it
	}
	col := "bottom_entry_id"
	if top {
		col = "top_entry_id"
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE bracket_matchup SET `+col+` = ? WHERE race_id = ? AND round = ? AND position = ?`,
		entryID, raceID, nextRound, nextPosition)
	return err
}

func (db *DB) bracketHasResults(ctx context.Context, championshipID int64) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM bracket_matchup bm
		JOIN heat h ON h.id = bm.heat_id
		JOIN heat_lane hl ON hl.heat_id = h.id
		WHERE bm.race_id = ? AND hl.finish_time IS NOT NULL`, championshipID).Scan(&n)
	return n > 0, err
}

// Matchups lists the bracket in round then position order, with the cars
// resolved.
func (db *DB) Matchups(ctx context.Context, championshipID int64) ([]MatchupView, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT bm.id, bm.race_id, bm.round, bm.position,
		       bm.top_entry_id, bm.bottom_entry_id, bm.winner_entry_id, bm.heat_id,
		       COALESCE(h.number, 0)
		FROM bracket_matchup bm
		LEFT JOIN heat h ON h.id = bm.heat_id
		WHERE bm.race_id = ?
		ORDER BY bm.round, bm.position`, championshipID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MatchupView
	for rows.Next() {
		var m MatchupView
		var top, bottom, winner, heatID sql.NullInt64
		if err := rows.Scan(&m.ID, &m.RaceID, &m.Round, &m.Position,
			&top, &bottom, &winner, &heatID, &m.HeatNumber); err != nil {
			return nil, err
		}
		m.TopEntryID = nullInt(top)
		m.BottomEntryID = nullInt(bottom)
		m.WinnerEntryID = nullInt(winner)
		m.HeatID = nullInt(heatID)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	entries, err := db.Entries(ctx, championshipID)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]EntryView, len(entries))
	for _, e := range entries {
		byID[e.ID] = e
	}
	seeds, err := db.Seeds(ctx, championshipID)
	if err != nil {
		return nil, err
	}
	seedOf := make(map[int64]int, len(seeds))
	for _, s := range seeds {
		seedOf[s.Entry.ID] = s.Seed
	}

	look := func(id *int64) *EntryView {
		if id == nil {
			return nil
		}
		if e, ok := byID[*id]; ok {
			return &e
		}
		return nil
	}
	for i := range out {
		out[i].Top = look(out[i].TopEntryID)
		out[i].Bottom = look(out[i].BottomEntryID)
		out[i].Winner = look(out[i].WinnerEntryID)
		if out[i].TopEntryID != nil {
			out[i].TopSeed = seedOf[*out[i].TopEntryID]
		}
		if out[i].BottomEntryID != nil {
			out[i].BottomSeed = seedOf[*out[i].BottomEntryID]
		}
	}
	return out, nil
}

// NextMatchup returns the next matchup waiting to be raced: both cars known,
// no winner yet, lowest round then lowest position.
func (db *DB) NextMatchup(ctx context.Context, championshipID int64) (MatchupView, error) {
	all, err := db.Matchups(ctx, championshipID)
	if err != nil {
		return MatchupView{}, err
	}
	for _, m := range all {
		if m.Ready() {
			return m, nil
		}
	}
	return MatchupView{}, ErrNotFound
}

// Champion returns the winner of the final, once there is one.
func (db *DB) Champion(ctx context.Context, championshipID int64) (EntryView, error) {
	all, err := db.Matchups(ctx, championshipID)
	if err != nil {
		return EntryView{}, err
	}
	rounds := 0
	for _, m := range all {
		if m.Round > rounds {
			rounds = m.Round
		}
	}
	for _, m := range all {
		if m.Round == rounds && m.Position == 1 && m.Winner != nil {
			return *m.Winner, nil
		}
	}
	return EntryView{}, ErrNotFound
}

// CreateMatchupHeat makes the two-lane heat a matchup races on.
//
// The lanes come from the season, defaulting to 1 and 2. Which two does not
// matter to the result — the bracket is head to head, not on times — but it has
// to be the same two every time so the crowd knows where to look, and it has to
// be settable because a lane can be damaged.
func (db *DB) CreateMatchupHeat(ctx context.Context, championshipID, matchupID int64) (HeatView, error) {
	m, err := db.Matchup(ctx, matchupID)
	if err != nil {
		return HeatView{}, err
	}
	if m.TopEntryID == nil || m.BottomEntryID == nil {
		return HeatView{}, errors.New("that matchup is still waiting for a car")
	}
	if m.HeatID != nil {
		return db.Heat(ctx, *m.HeatID)
	}

	race, err := db.Race(ctx, championshipID)
	if err != nil {
		return HeatView{}, err
	}
	s, err := db.Season(ctx, race.SeasonID)
	if err != nil {
		return HeatView{}, err
	}

	var heatID int64
	err = db.Tx(ctx, func(tx *sql.Tx) error {
		var next int
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(number), 0) + 1 FROM heat WHERE race_id = ?`,
			championshipID).Scan(&next); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO heat (race_id, number, phase, bracket_matchup_id, status)
			VALUES (?,?,?,?,?)`,
			championshipID, next, model.PhaseBracket, matchupID, model.HeatPending)
		if err != nil {
			return err
		}
		if heatID, err = res.LastInsertId(); err != nil {
			return err
		}

		// Every lane exists, but only the bracket pair carries a car. The rest
		// run empty and the timer masks them.
		for lane := 1; lane <= s.LaneCount; lane++ {
			var entryID any
			switch lane {
			case s.BracketLaneA:
				entryID = *m.TopEntryID
			case s.BracketLaneB:
				entryID = *m.BottomEntryID
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO heat_lane (heat_id, lane, entry_id) VALUES (?,?,?)`,
				heatID, lane, entryID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE bracket_matchup SET heat_id = ? WHERE id = ?`, heatID, matchupID)
		return err
	})
	if err != nil {
		return HeatView{}, err
	}
	return db.Heat(ctx, heatID)
}

// SwapMatchupLanes puts the two cars the other way round.
//
// Offered because a racer will ask, and refusing would cost more goodwill than
// it is worth. A heat already built for this matchup is discarded and rebuilt
// on the next arm, so the swap actually reaches the track.
func (db *DB) SwapMatchupLanes(ctx context.Context, matchupID int64) error {
	m, err := db.Matchup(ctx, matchupID)
	if err != nil {
		return err
	}
	if m.WinnerEntryID != nil {
		return errors.New("that matchup has already been raced")
	}

	return db.Tx(ctx, func(tx *sql.Tx) error {
		if m.HeatID != nil {
			var run int
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM heat_lane WHERE heat_id = ? AND finish_time IS NOT NULL`,
				*m.HeatID).Scan(&run); err != nil {
				return err
			}
			if run > 0 {
				return errors.New("that matchup has already been run — re-run it instead")
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM heat WHERE id = ?`, *m.HeatID); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE bracket_matchup
			SET top_entry_id = ?, bottom_entry_id = ?, heat_id = NULL
			WHERE id = ?`,
			ptrArg(m.BottomEntryID), ptrArg(m.TopEntryID), matchupID)
		return err
	})
}

// RecordMatchupWinner decides a matchup and moves the winner up.
func (db *DB) RecordMatchupWinner(ctx context.Context, championshipID, matchupID, winnerID int64) error {
	m, err := db.Matchup(ctx, matchupID)
	if err != nil {
		return err
	}
	if m.TopEntryID == nil || m.BottomEntryID == nil {
		return errors.New("that matchup is still waiting for a car")
	}
	if *m.TopEntryID != winnerID && *m.BottomEntryID != winnerID {
		return errors.New("that car is not in this matchup")
	}

	seeds, err := db.Seeds(ctx, championshipID)
	if err != nil {
		return err
	}
	b, err := bracket.Build(len(seeds))
	if err != nil {
		return err
	}

	return db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE bracket_matchup SET winner_entry_id = ? WHERE id = ?`, winnerID, matchupID); err != nil {
			return err
		}
		return placeWinner(ctx, tx, championshipID, b, m.Round, m.Position, winnerID)
	})
}

// Matchup loads one matchup.
func (db *DB) Matchup(ctx context.Context, id int64) (model.BracketMatchup, error) {
	var m model.BracketMatchup
	var top, bottom, winner, heatID sql.NullInt64
	err := db.QueryRowContext(ctx, `
		SELECT id, race_id, round, position, top_entry_id, bottom_entry_id, winner_entry_id, heat_id
		FROM bracket_matchup WHERE id = ?`, id).
		Scan(&m.ID, &m.RaceID, &m.Round, &m.Position, &top, &bottom, &winner, &heatID)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	if err != nil {
		return m, err
	}
	m.TopEntryID = nullInt(top)
	m.BottomEntryID = nullInt(bottom)
	m.WinnerEntryID = nullInt(winner)
	m.HeatID = nullInt(heatID)
	return m, nil
}
