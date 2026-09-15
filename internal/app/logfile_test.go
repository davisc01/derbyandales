package app

import (
	"os"
	"path/filepath"
	"testing"
)

// Launched from the .app there is nowhere else for the log to go, so it has to
// land in a file — and a season of race nights must not fill the disk.
func TestLogsGoToADatedFileAndOldOnesArePruned(t *testing.T) {
	paths, err := DefaultPaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Logs, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, day := range []string{"2026-01-01", "2026-01-02", "2026-01-03", "2026-01-04"} {
		os.WriteFile(filepath.Join(paths.Logs, "derbyandales-"+day+".log"), []byte("x"), 0o644)
	}

	f, err := OpenLogFile(paths, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("hello\n"); err != nil {
		t.Fatal(err)
	}

	entries, _ := os.ReadDir(paths.Logs)
	if len(entries) != 3 {
		t.Errorf("%d log files kept, want 3", len(entries))
	}
	if _, err := os.Stat(filepath.Join(paths.Logs, "derbyandales-2026-01-01.log")); err == nil {
		t.Error("the oldest log was not pruned")
	}
	if _, err := os.Stat(f.Name()); err != nil {
		t.Errorf("today's log was pruned: %v", err)
	}
}
