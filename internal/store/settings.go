package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/davisc01/derbyandales/internal/model"
)

// Application-level setting keys. Anything that varies per season lives on the
// season row instead, so these are genuinely global.
const (
	KeyDerbySitePath   = "derby_site_path"   // path to the Hugo site clone
	KeyCoordinatorPIN  = "coordinator_pin"   // scheduling, results editing, publishing
	KeyCrewPIN         = "crew_pin"          // check-in, voting admin
	KeyAutoAdvanceSecs = "auto_advance_secs" // pause between heats
	KeyTimerPort       = "timer_port"        // remembered serial device
	KeyTimerProfile    = "timer_profile"     // e.g. fasttrack-k, simulator
	KeyReverseLanes    = "reverse_lanes"     // physical lane wiring is mirrored
	KeyActiveSeasonID  = "active_season_id"  // what the UI opens on
	KeyHTTPPort        = "http_port"
	KeyHTTPSPort       = "https_port"
	KeyBackupKeep      = "backup_keep" // how many snapshots to retain
)

// Setting reads a setting, returning def when unset.
func (db *DB) Setting(ctx context.Context, key, def string) (string, error) {
	var v string
	err := db.QueryRowContext(ctx, `SELECT value FROM setting WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return def, nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// SettingInt reads an integer setting, returning def when unset or unparseable.
func (db *DB) SettingInt(ctx context.Context, key string, def int) (int, error) {
	s, err := db.Setting(ctx, key, "")
	if err != nil || s == "" {
		return def, err
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def, nil
	}
	return n, nil
}

// SettingBool reads a boolean setting.
func (db *DB) SettingBool(ctx context.Context, key string, def bool) (bool, error) {
	s, err := db.Setting(ctx, key, "")
	if err != nil || s == "" {
		return def, err
	}
	return s == "1" || s == "true", nil
}

// SetSetting writes a setting.
func (db *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO setting (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// AllSettings returns every stored setting.
func (db *DB) AllSettings(ctx context.Context) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT key, value FROM setting ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// Audit records a decision worth being able to explain later. Overrides of a
// failed preflight check go here, which is the whole point: a jammed gate switch
// should never block a race, it should just leave a trace.
func (db *DB) Audit(ctx context.Context, actor, action, detail string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO audit (at, actor, action, detail) VALUES (?, ?, ?, ?)`,
		time.Now().Unix(), actor, action, detail)
	return err
}

// RecentAudit returns the newest audit entries, most recent first.
func (db *DB) RecentAudit(ctx context.Context, limit int) ([]model.AuditEntry, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, at, actor, action, detail FROM audit ORDER BY at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AuditEntry
	for rows.Next() {
		var e model.AuditEntry
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.Actor, &e.Action, &e.Detail); err != nil {
			return nil, err
		}
		e.At = fromUnix(at)
		out = append(out, e)
	}
	return out, rows.Err()
}
