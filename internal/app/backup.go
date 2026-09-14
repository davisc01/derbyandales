package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/davisc01/derbyandales/internal/store"
)

// Backups are taken at the points where losing data would actually hurt:
// opening a race, closing check-in, and completing a race. They use SQLite's
// VACUUM INTO, which produces a consistent snapshot without stopping writers —
// so a backup can never stall a heat.

// BackupReason labels why a snapshot was taken, and becomes part of its filename.
type BackupReason string

const (
	BackupStartup      BackupReason = "startup"
	BackupRaceOpen     BackupReason = "race-open"
	BackupCheckinClose BackupReason = "checkin-close"
	BackupRaceComplete BackupReason = "race-complete"
	// BackupIntermission is taken when racing pauses halfway through. It is a
	// known-quiet moment, which is when a snapshot is cheapest and most useful.
	BackupIntermission BackupReason = "intermission"
	// BackupSeasonRecompute is taken before rewriting every points row in a
	// season, which is the one action here that can change published history.
	BackupSeasonRecompute BackupReason = "season-recompute"
	BackupManual          BackupReason = "manual"
)

// Backup is one snapshot on disk.
type Backup struct {
	Path   string
	Reason BackupReason
	Taken  time.Time
	Size   int64
}

// Snapshot writes a consistent copy of the database and prunes old ones,
// keeping the newest `keep`.
func Snapshot(ctx context.Context, db *store.DB, paths Paths, reason BackupReason, keep int) (Backup, error) {
	if err := os.MkdirAll(paths.Backups, 0o755); err != nil {
		return Backup{}, err
	}
	now := time.Now()
	name := fmt.Sprintf("derby-%s-%s.sqlite3", now.Format("20060102-150405"), reason)
	dest := filepath.Join(paths.Backups, name)

	// VACUUM INTO takes its own read snapshot; concurrent writes are unaffected.
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, dest); err != nil {
		return Backup{}, fmt.Errorf("snapshot to %s: %w", dest, err)
	}

	b := Backup{Path: dest, Reason: reason, Taken: now}
	if fi, err := os.Stat(dest); err == nil {
		b.Size = fi.Size()
	}
	if keep > 0 {
		pruneBackups(paths.Backups, keep)
	}
	return b, nil
}

// ListBackups returns the snapshots on disk, newest first.
func ListBackups(dir string) []Backup {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Backup
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "derby-") || !strings.HasSuffix(e.Name(), ".sqlite3") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Backup{
			Path:   filepath.Join(dir, e.Name()),
			Reason: reasonFromName(e.Name()),
			Taken:  info.ModTime(),
			Size:   info.Size(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Taken.After(out[j].Taken) })
	return out
}

// reasonFromName recovers the reason label from a snapshot filename.
func reasonFromName(name string) BackupReason {
	base := strings.TrimSuffix(name, ".sqlite3")
	parts := strings.SplitN(base, "-", 4) // derby, date, time, reason
	if len(parts) < 4 {
		return BackupManual
	}
	return BackupReason(parts[3])
}

// pruneBackups deletes all but the newest `keep` snapshots.
func pruneBackups(dir string, keep int) {
	all := ListBackups(dir)
	for i := keep; i < len(all); i++ {
		os.Remove(all[i].Path)
	}
}
