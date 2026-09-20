package app

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	// BackupBracketSeed is taken before the championship field is committed,
	// which is where a whole season's results become the night's running order.
	BackupBracketSeed BackupReason = "bracket-seed"
	BackupManual      BackupReason = "manual"
	// BackupDemoClear is taken before the demo data is deleted. The data
	// itself is fabricated, but a night spent rehearsing on it is not, and
	// somebody who clears it on race day at the wrong moment needs a way back.
	BackupDemoClear BackupReason = "demo-clear"
	// BackupPreRestore is the database as it stood when somebody chose to go
	// back to an older snapshot — so going back can itself be undone.
	BackupPreRestore BackupReason = "pre-restore"
)

// Backup is one snapshot on disk.
type Backup struct {
	Path   string
	Reason BackupReason
	Taken  time.Time
	Size   int64
}

// Name is the snapshot's file name, which is how a restore refers to it.
func (b Backup) Name() string { return filepath.Base(b.Path) }

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

// Restoring a backup.
//
// It happens across a restart rather than in place. Every controller reads the
// database through one shared handle, and swapping that handle while a heat is
// being recorded or a display is polling would be a race the software cannot
// win. So a restore is staged: the chosen snapshot is copied aside, the current
// database is snapshotted, and the app stops. On the next launch, before the
// database is opened, the staged copy takes its place.

// pendingRestoreName is the staged copy, kept in the data folder rather than in
// backups/ so pruning cannot delete it before the restart.
const pendingRestoreName = "restore-pending.sqlite3"

// sqliteHeader is how every SQLite database file begins.
const sqliteHeader = "SQLite format 3\x00"

// StageRestore prepares a backup to replace the database on the next launch.
// name is the snapshot's file name as listed; anything else is refused, so a
// request cannot point the restore at an arbitrary file.
func StageRestore(ctx context.Context, db *store.DB, paths Paths, name string, keep int) error {
	var chosen *Backup
	for _, b := range ListBackups(paths.Backups) {
		if filepath.Base(b.Path) == name {
			b := b
			chosen = &b
		}
	}
	if chosen == nil {
		return fmt.Errorf("there is no backup called %s", name)
	}
	if err := checkSQLite(chosen.Path); err != nil {
		return err
	}

	staged := filepath.Join(paths.Root, pendingRestoreName)
	if err := copyFile(chosen.Path, staged); err != nil {
		return fmt.Errorf("copying the backup: %w", err)
	}
	// Taken after the copy: a snapshot prunes the oldest backups, and the one
	// chosen may well be among them.
	if _, err := Snapshot(ctx, db, paths, BackupPreRestore, keep); err != nil {
		os.Remove(staged)
		return fmt.Errorf("could not save the current database first, so nothing was changed: %w", err)
	}
	return nil
}

// RestorePending reports whether a restore is waiting for the next launch.
func RestorePending(paths Paths) bool {
	_, err := os.Stat(filepath.Join(paths.Root, pendingRestoreName))
	return err == nil
}

// CancelRestore throws a staged restore away.
func CancelRestore(paths Paths) error {
	err := os.Remove(filepath.Join(paths.Root, pendingRestoreName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// applyPendingRestore swaps a staged backup in. It runs before the database is
// opened, so nothing else can be holding it.
func applyPendingRestore(paths Paths) (bool, error) {
	staged := filepath.Join(paths.Root, pendingRestoreName)
	if _, err := os.Stat(staged); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err := checkSQLite(staged); err != nil {
		return false, err
	}
	// The write-ahead log belongs to the database being replaced. Left behind,
	// SQLite would replay it over the restored file.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(paths.DB + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	if err := os.Rename(staged, paths.DB); err != nil {
		return false, err
	}
	return true, nil
}

func checkSQLite(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, len(sqliteHeader))
	if _, err := io.ReadFull(f, head); err != nil || string(head) != sqliteHeader {
		return fmt.Errorf("%s is not a database backup", filepath.Base(path))
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".partial"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
