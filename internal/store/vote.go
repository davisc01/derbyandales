package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/davisc01/derbyandales/internal/model"
)

// Voting happens on one shared tablet at the check-in table, staffed, during
// the intermission. There is deliberately no dedupe: a per-device limit would
// block every voter after the first.
//
// Votes are rows rather than a counter on the car. That is what makes undo,
// void and an audit trail possible — the old system incremented a column, so a
// misclick was unrecoverable.

// Vote category keys. These are stable identifiers; the labels are what people
// see and may be reworded freely.
const (
	VoteTheme  = "theme"
	VoteDesign = "design"
)

// AwardNameForVote maps a category to the award it produces.
//
// These strings go straight into the website's awards.csv, so they must match
// what is already published there: "Best Theme" and "Best Design".
var AwardNameForVote = map[string]string{
	VoteTheme:  "Best Theme",
	VoteDesign: "Best Design",
}

// AwardTypeVoted is the award type the site uses for the voted trophies.
const AwardTypeVoted = "Design Trophy"

// defaultCategories are created for every race, in ballot order.
var defaultCategories = []struct {
	Key, Label string
}{
	{VoteTheme, "Best Themed Car"},
	{VoteDesign, "Best Overall Design"},
}

// EnsureVoteCategories creates a race's ballot questions if they do not exist.
// Safe to call repeatedly.
func (db *DB) EnsureVoteCategories(ctx context.Context, raceID int64) error {
	for i, c := range defaultCategories {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO vote_category (race_id, key, label, enabled, sort)
			VALUES (?,?,?,1,?)
			ON CONFLICT(race_id, key) DO NOTHING`,
			raceID, c.Key, c.Label, i); err != nil {
			return fmt.Errorf("create vote category %s: %w", c.Key, err)
		}
	}
	return nil
}

const voteCategoryCols = `id, race_id, key, label, enabled, winner_entry_id`

func scanVoteCategory(sc interface{ Scan(...any) error }) (model.VoteCategory, error) {
	var c model.VoteCategory
	var enabled int
	var winner sql.NullInt64
	if err := sc.Scan(&c.ID, &c.RaceID, &c.Key, &c.Label, &enabled, &winner); err != nil {
		return c, err
	}
	c.Enabled = enabled != 0
	c.WinnerEntryID = nullInt(winner)
	return c, nil
}

// VoteCategories lists a race's ballot questions in ballot order.
func (db *DB) VoteCategories(ctx context.Context, raceID int64) ([]model.VoteCategory, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+voteCategoryCols+` FROM vote_category WHERE race_id = ? ORDER BY sort, id`, raceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.VoteCategory
	for rows.Next() {
		c, err := scanVoteCategory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// VoteCategory loads one ballot question.
func (db *DB) VoteCategory(ctx context.Context, id int64) (model.VoteCategory, error) {
	row := db.QueryRowContext(ctx, `SELECT `+voteCategoryCols+` FROM vote_category WHERE id = ?`, id)
	c, err := scanVoteCategory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// SetVoteCategoryEnabled turns a ballot question on or off. A race with no
// themed cars simply switches the theme question off, replacing a configuration
// flag in the old system that silently did nothing.
func (db *DB) SetVoteCategoryEnabled(ctx context.Context, id int64, enabled bool) error {
	_, err := db.ExecContext(ctx,
		`UPDATE vote_category SET enabled = ? WHERE id = ?`, boolInt(enabled), id)
	return err
}

// --- casting -----------------------------------------------------------------

// CastVote records one tap on the tablet.
func (db *DB) CastVote(ctx context.Context, categoryID, entryID int64) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO vote (category_id, entry_id, cast_at) VALUES (?,?,?)`,
		categoryID, entryID, time.Now().Unix())
	if err != nil {
		return 0, fmt.Errorf("record vote: %w", err)
	}
	return res.LastInsertId()
}

// UndoLastVote voids the most recent vote in a category.
//
// A misclick is the common case — someone taps the wrong tile and says so
// immediately — so this is one action rather than a hunt through a list.
func (db *DB) UndoLastVote(ctx context.Context, categoryID int64) (model.Vote, error) {
	var v model.Vote
	var castAt int64
	err := db.QueryRowContext(ctx, `
		SELECT id, category_id, entry_id, cast_at FROM vote
		WHERE category_id = ? AND voided = 0
		ORDER BY cast_at DESC, id DESC LIMIT 1`, categoryID).
		Scan(&v.ID, &v.CategoryID, &v.EntryID, &castAt)
	if errors.Is(err, sql.ErrNoRows) {
		return v, errors.New("there are no votes to undo")
	}
	if err != nil {
		return v, err
	}
	v.CastAt = fromUnix(castAt)

	if _, err := db.ExecContext(ctx, `UPDATE vote SET voided = 1 WHERE id = ?`, v.ID); err != nil {
		return v, err
	}
	v.Voided = true
	return v, nil
}

// VoidVote strikes out one specific vote.
func (db *DB) VoidVote(ctx context.Context, voteID int64) error {
	_, err := db.ExecContext(ctx, `UPDATE vote SET voided = 1 WHERE id = ?`, voteID)
	return err
}

// ResetVoteCategory voids every vote in a category and clears its winner.
//
// Votes are voided rather than deleted: if a reset turns out to have been a
// mistake, the evidence is still there.
func (db *DB) ResetVoteCategory(ctx context.Context, categoryID int64) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE vote SET voided = 1 WHERE category_id = ?`, categoryID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE vote_category SET winner_entry_id = NULL WHERE id = ?`, categoryID)
		return err
	})
}

// --- tallies -----------------------------------------------------------------

// TallyRow is one car's standing in a ballot question.
type TallyRow struct {
	Entry EntryView
	Votes int
	// Leading marks every car on the top count. More than one means a tie the
	// coordinator has to break.
	Leading bool
}

// Tally counts the live votes in a category, most votes first.
//
// Every eligible car is listed, including those with no votes, so the
// coordinator sees the whole field rather than only the popular end of it.
func (db *DB) Tally(ctx context.Context, categoryID int64) ([]TallyRow, error) {
	category, err := db.VoteCategory(ctx, categoryID)
	if err != nil {
		return nil, err
	}
	entries, err := db.BallotEntries(ctx, category.RaceID)
	if err != nil {
		return nil, err
	}

	counts := map[int64]int{}
	rows, err := db.QueryContext(ctx,
		`SELECT entry_id, COUNT(*) FROM vote
		 WHERE category_id = ? AND voided = 0 GROUP BY entry_id`, categoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var entryID int64
		var n int
		if err := rows.Scan(&entryID, &n); err != nil {
			return nil, err
		}
		counts[entryID] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]TallyRow, 0, len(entries))
	top := 0
	for _, e := range entries {
		n := counts[e.ID]
		if n > top {
			top = n
		}
		out = append(out, TallyRow{Entry: e, Votes: n})
	}

	// Most votes first, then by car number so the order is stable between
	// refreshes rather than jumping about while people are looking at it.
	sortTally(out)

	if top > 0 {
		for i := range out {
			out[i].Leading = out[i].Votes == top
		}
	}
	return out, nil
}

func sortTally(rows []TallyRow) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0; j-- {
			a, b := rows[j-1], rows[j]
			if a.Votes > b.Votes || (a.Votes == b.Votes && a.Entry.CarNumber <= b.Entry.CarNumber) {
				break
			}
			rows[j-1], rows[j] = rows[j], rows[j-1]
		}
	}
}

// BallotEntries lists the cars that can be voted for.
//
// The pace car is not a competitor and is left off. Ineligible cars are left on:
// they are barred from the standings, not from having a nice paint job — and
// they are physically on the table where people are looking.
func (db *DB) BallotEntries(ctx context.Context, raceID int64) ([]EntryView, error) {
	all, err := db.Entries(ctx, raceID)
	if err != nil {
		return nil, err
	}
	out := make([]EntryView, 0, len(all))
	for _, e := range all {
		if e.IsControl {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// VoteCount reports how many live votes a category has.
func (db *DB) VoteCount(ctx context.Context, categoryID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM vote WHERE category_id = ? AND voided = 0`, categoryID).Scan(&n)
	return n, err
}

// --- winners -----------------------------------------------------------------

// DeclareVoteWinner records the winner of a ballot question and writes the
// award it produces.
//
// Declaring is explicit rather than automatic because a tie needs a person.
// The old system had the coordinator read the top row of an HTML table and
// retype it into another application.
func (db *DB) DeclareVoteWinner(ctx context.Context, categoryID, entryID int64) error {
	category, err := db.VoteCategory(ctx, categoryID)
	if err != nil {
		return err
	}
	entry, err := db.Entry(ctx, entryID)
	if err != nil {
		return err
	}
	if entry.RaceID != category.RaceID {
		return errors.New("that car is not in this race")
	}
	if entry.IsControl {
		return errors.New("the pace car cannot win a trophy")
	}

	name := AwardNameForVote[category.Key]
	if name == "" {
		name = category.Label
	}

	return db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE vote_category SET winner_entry_id = ? WHERE id = ?`,
			entryID, categoryID); err != nil {
			return err
		}
		// One award per category: replace rather than accumulate, so changing
		// your mind does not leave two winners on the website.
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM award WHERE race_id = ? AND name = ? AND source = ?`,
			category.RaceID, name, model.AwardVote); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO award (race_id, name, award_type, entry_id, sort, source)
			 VALUES (?,?,?,?,?,?)`,
			category.RaceID, name, AwardTypeVoted, entryID, 100, model.AwardVote)
		return err
	})
}

// ClearVoteWinner undoes a declaration.
func (db *DB) ClearVoteWinner(ctx context.Context, categoryID int64) error {
	category, err := db.VoteCategory(ctx, categoryID)
	if err != nil {
		return err
	}
	name := AwardNameForVote[category.Key]
	if name == "" {
		name = category.Label
	}
	return db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE vote_category SET winner_entry_id = NULL WHERE id = ?`, categoryID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`DELETE FROM award WHERE race_id = ? AND name = ? AND source = ?`,
			category.RaceID, name, model.AwardVote)
		return err
	})
}

// --- awards ------------------------------------------------------------------

// AwardView is an award with the car that won it.
type AwardView struct {
	model.Award
	Entry EntryView
}

// Awards lists a race's awards in presentation order.
func (db *DB) Awards(ctx context.Context, raceID int64) ([]AwardView, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT a.id, a.race_id, a.name, a.award_type, a.entry_id, a.sort, a.source
		FROM award a WHERE a.race_id = ? ORDER BY a.sort, a.id`, raceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AwardView
	for rows.Next() {
		var a AwardView
		var entryID sql.NullInt64
		if err := rows.Scan(&a.ID, &a.RaceID, &a.Name, &a.AwardType,
			&entryID, &a.Sort, &a.Source); err != nil {
			return nil, err
		}
		a.EntryID = nullInt(entryID)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range out {
		if out[i].EntryID == nil {
			continue
		}
		if entry, err := db.Entry(ctx, *out[i].EntryID); err == nil {
			out[i].Entry = entry
		}
	}
	return out, nil
}
