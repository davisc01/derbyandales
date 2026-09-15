package app

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
)

// Going back to a snapshot is the answer to a night that went badly wrong —
// a season recomputed with the wrong setting, a check-in list wiped. It has to
// actually put the old data back, and it has to be undoable itself.
func TestRestoringABackupPutsTheOldDatabaseBackOnTheNextLaunch(t *testing.T) {
	ctx := context.Background()
	paths, err := DefaultPaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	a, err := Open(ctx, paths, log)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.DB.SetSetting(ctx, "restore_marker", "before"); err != nil {
		t.Fatal(err)
	}
	b, err := a.Backup(ctx, BackupManual)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.DB.SetSetting(ctx, "restore_marker", "after"); err != nil {
		t.Fatal(err)
	}

	if err := StageRestore(ctx, a.DB, paths, "../derby.sqlite3", 20); err == nil {
		t.Error("a restore was staged from a file that is not a listed backup")
	}
	if err := StageRestore(ctx, a.DB, paths, filepath.Base(b.Path), 20); err != nil {
		t.Fatalf("StageRestore: %v", err)
	}
	if !RestorePending(paths) {
		t.Fatal("no restore is pending after staging one")
	}
	// Nothing changes until the restart: the running app keeps its data.
	if v, _ := a.DB.Setting(ctx, "restore_marker", ""); v != "after" {
		t.Errorf("staging a restore changed the live database to %q", v)
	}
	a.Close()

	a, err = Open(ctx, paths, log)
	if err != nil {
		t.Fatalf("reopening after a restore: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	if v, _ := a.DB.Setting(ctx, "restore_marker", ""); v != "before" {
		t.Errorf("after the restore the database says %q, want the backup's %q", v, "before")
	}
	if RestorePending(paths) {
		t.Error("the restore is still pending after being applied")
	}

	// The database that was replaced is itself a backup, so the restore can be
	// undone the same way.
	found := false
	for _, bk := range ListBackups(paths.Backups) {
		if bk.Reason == BackupPreRestore {
			found = true
		}
	}
	if !found {
		t.Error("no pre-restore snapshot was kept")
	}
}
