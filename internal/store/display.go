package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/davisc01/derbyandales/internal/model"
)

// Displays self-register. A screen opens the display URL, is given a name and a
// token, and appears in the coordinator's list. There are no IP addresses to
// configure and nothing to edit in a file — which is the whole point, because
// the person setting up the TVs is carrying an HDMI cable, not a laptop.

// Scene is what a display is currently showing.
type Scene string

const (
	SceneBlank    Scene = "blank"
	SceneRoster   Scene = "roster"
	SceneRacing   Scene = "now-racing"
	SceneReveal   Scene = "results-reveal"
	SceneAwards   Scene = "awards"
	SceneSlides   Scene = "slideshow"
	SceneBracket  Scene = "bracket"
	SceneVotingQR Scene = "voting-qr"
)

// SceneInfo describes a scene for the coordinator's picker.
type SceneInfo struct {
	Scene Scene  `json:"scene"`
	Name  string `json:"name"`
	Hint  string `json:"hint"`
	// Ready reports whether the scene has anything to show yet.
	Ready bool `json:"ready"`
}

// Scenes lists the display scenes in the order a race night uses them.
func Scenes() []SceneInfo {
	return []SceneInfo{
		{SceneBlank, "Blank", "Between segments.", true},
		{SceneRoster, "Racers", "Tonight's line-up, for the intros.", true},
		{SceneRacing, "Now racing", "Lane assignments, then the finish order.", true},
		{SceneReveal, "Results reveal", "One car at a time, slowest to fastest.", true},
		{SceneSlides, "Car photos", "Slideshow of the cars.", false},
		{SceneAwards, "Awards", "Award winners.", false},
		{SceneBracket, "Bracket", "The championship bracket.", false},
		{SceneVotingQR, "Voting", "QR code for the voting tablet.", false},
	}
}

// displayNames are the words the automatic names are built from. A display
// called "Bar TV" is easier to pick out of a list than one called
// 192.168.1.47:54322.
var (
	displayAdjectives = []string{"Left", "Right", "Front", "Back", "Corner", "Bar", "Window", "Far"}
	displayNouns      = []string{"Screen", "TV", "Display", "Monitor", "Panel"}
)

// generateName invents a friendly display name.
func generateName() string {
	pick := func(from []string) string {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(from))))
		if err != nil {
			return from[0]
		}
		return from[n.Int64()]
	}
	return pick(displayAdjectives) + " " + pick(displayNouns)
}

// generateToken returns an opaque identifier for a display.
func generateToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// RegisterDisplay creates a display, or refreshes the one with this token.
func (db *DB) RegisterDisplay(ctx context.Context, token string) (model.Display, error) {
	now := time.Now()

	if token != "" {
		d, err := db.DisplayByToken(ctx, token)
		if err == nil {
			_, err = db.ExecContext(ctx,
				`UPDATE display SET last_seen_at = ? WHERE id = ?`, now.Unix(), d.ID)
			d.LastSeenAt = now
			return d, err
		}
		if !errors.Is(err, ErrNotFound) {
			return model.Display{}, err
		}
	}

	newToken, err := generateToken()
	if err != nil {
		return model.Display{}, err
	}
	d := model.Display{
		Name:       generateName(),
		Token:      newToken,
		Page:       string(SceneBlank),
		Params:     "{}",
		LastSeenAt: now,
	}
	res, err := db.ExecContext(ctx,
		`INSERT INTO display (name, token, page, params, last_seen_at) VALUES (?,?,?,?,?)`,
		d.Name, d.Token, d.Page, d.Params, now.Unix())
	if err != nil {
		return d, fmt.Errorf("register display: %w", err)
	}
	d.ID, err = res.LastInsertId()
	return d, err
}

const displayCols = `id, name, token, page, params, last_seen_at`

func scanDisplay(sc interface{ Scan(...any) error }) (model.Display, error) {
	var d model.Display
	var lastSeen int64
	err := sc.Scan(&d.ID, &d.Name, &d.Token, &d.Page, &d.Params, &lastSeen)
	if err != nil {
		return d, err
	}
	d.LastSeenAt = fromUnix(lastSeen)
	return d, nil
}

// DisplayByToken finds a display by its token.
func (db *DB) DisplayByToken(ctx context.Context, token string) (model.Display, error) {
	row := db.QueryRowContext(ctx, `SELECT `+displayCols+` FROM display WHERE token = ?`, token)
	d, err := scanDisplay(row)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// Displays lists every known display, most recently seen first.
func (db *DB) Displays(ctx context.Context) ([]model.Display, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+displayCols+` FROM display ORDER BY last_seen_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Display
	for rows.Next() {
		d, err := scanDisplay(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetDisplayScene assigns what a display shows.
func (db *DB) SetDisplayScene(ctx context.Context, id int64, scene Scene, params string) error {
	if params == "" {
		params = "{}"
	}
	_, err := db.ExecContext(ctx,
		`UPDATE display SET page = ?, params = ? WHERE id = ?`, scene, params, id)
	return err
}

// RenameDisplay gives a display a name someone chose.
func (db *DB) RenameDisplay(ctx context.Context, id int64, name string) error {
	if name == "" {
		return fmt.Errorf("a display needs a name")
	}
	_, err := db.ExecContext(ctx, `UPDATE display SET name = ? WHERE id = ?`, name, id)
	return err
}

// ForgetDisplay removes a display that is no longer connected.
func (db *DB) ForgetDisplay(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM display WHERE id = ?`, id)
	return err
}

// TouchDisplay records that a display is still connected.
func (db *DB) TouchDisplay(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx,
		`UPDATE display SET last_seen_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

// DisplayOnlineWindow is how recently a display must have been seen to count
// as connected. It is several times the heartbeat interval so a slow network
// does not make a working screen look dead.
const DisplayOnlineWindow = 45 * time.Second
