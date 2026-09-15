package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"
)

// Logs on disk.
//
// Launched from the .app there is no Terminal, and macOS sends a plain
// executable's output nowhere at all. So without a file, the first real race
// night's timer trouble would leave no trace: whatever the FastTrack actually
// said, and whatever the app made of it, gone when the window closed. One file
// per day, a fortnight kept.

// DefaultLogKeep is how many days of logs are kept.
const DefaultLogKeep = 14

// OpenLogFile opens today's log for appending, prunes old ones, and sends any
// crash report there as well — a panic is exactly the thing worth reading
// afterwards and exactly the thing that would otherwise vanish.
func OpenLogFile(paths Paths, keep int) (*os.File, error) {
	if err := os.MkdirAll(paths.Logs, 0o755); err != nil {
		return nil, err
	}
	name := filepath.Join(paths.Logs, fmt.Sprintf("derbyandales-%s.log", time.Now().Format("2006-01-02")))
	f, err := os.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	_ = debug.SetCrashOutput(f, debug.CrashOptions{})
	pruneLogs(paths.Logs, keep)
	return f, nil
}

func pruneLogs(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil || keep <= 0 {
		return
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "derbyandales-") && strings.HasSuffix(e.Name(), ".log") {
			names = append(names, e.Name())
		}
	}
	// Dated names sort in date order.
	sort.Strings(names)
	for i := 0; i+keep < len(names); i++ {
		os.Remove(filepath.Join(dir, names[i]))
	}
}
