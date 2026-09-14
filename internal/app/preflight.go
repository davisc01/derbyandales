package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/davisc01/derbyandales/internal/store"
	"github.com/davisc01/derbyandales/internal/timer"
)

// Preflight answers one question before a race night starts: is anything going
// to bite us? Each check reports a verdict plus a human-readable detail, and
// where possible an action the operator can take right now.

// Verdict is the outcome of a single check.
type Verdict string

const (
	Pass    Verdict = "pass"
	Warn    Verdict = "warn"
	Fail    Verdict = "fail"
	Skipped Verdict = "skipped"
	// Browser means the check can only be answered by a browser (camera
	// permissions, for instance), so the UI runs it client-side.
	Browser Verdict = "browser"
)

// Check is one preflight result.
type Check struct {
	Name    string  `json:"name"`
	Verdict Verdict `json:"verdict"`
	Detail  string  `json:"detail"`
	// Action is a URL the operator can follow to fix or re-run this check.
	Action string `json:"action,omitempty"`
	// ActionLabel is the button text for Action.
	ActionLabel string `json:"action_label,omitempty"`
}

// minFreeBytes is the point below which we start warning. A race night's photos
// are a few hundred MB at most, so this is generous.
const minFreeBytes = 2 << 30 // 2 GiB

// staleBackup is how old the newest snapshot may be before we mention it.
const staleBackup = 24 * time.Hour

// RunPreflight executes every server-side check.
func RunPreflight(ctx context.Context, db *store.DB, paths Paths, displaysOnline int, tc *TimerController) []Check {
	checks := []Check{
		checkDatabase(ctx, db, paths),
		checkDisk(paths),
		checkPhotos(paths),
		checkBackups(paths),
		checkDerbySite(ctx, db),
		checkDisplays(displaysOnline),
	}
	checks = append(checks, timerCheck(tc))
	// Camera permission is a browser-side fact; the page fills this in.
	checks = append(checks, Check{
		Name:    "Camera",
		Verdict: Browser,
		Detail:  "Checked in the browser.",
	})
	return checks
}

func checkDatabase(ctx context.Context, db *store.DB, paths Paths) Check {
	c := Check{Name: "Database"}
	var journal string
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journal); err != nil {
		c.Verdict, c.Detail = Fail, fmt.Sprintf("Cannot query database: %v", err)
		return c
	}
	var integrity string
	if err := db.QueryRowContext(ctx, `PRAGMA quick_check(1)`).Scan(&integrity); err != nil {
		c.Verdict, c.Detail = Fail, fmt.Sprintf("Integrity check failed: %v", err)
		return c
	}
	if integrity != "ok" {
		c.Verdict, c.Detail = Fail, "Integrity check reported: "+integrity
		return c
	}
	size := int64(0)
	if fi, err := os.Stat(paths.DB); err == nil {
		size = fi.Size()
	}
	c.Verdict = Pass
	c.Detail = fmt.Sprintf("%s · %s · integrity ok", humanBytes(size), journal)
	return c
}

func checkDisk(paths Paths) Check {
	c := Check{Name: "Disk space"}
	var st unix.Statfs_t
	if err := unix.Statfs(paths.Root, &st); err != nil {
		c.Verdict, c.Detail = Warn, fmt.Sprintf("Cannot determine free space: %v", err)
		return c
	}
	free := int64(st.Bavail) * int64(st.Bsize)
	c.Detail = humanBytes(free) + " free"
	if free < minFreeBytes {
		c.Verdict = Warn
		c.Detail += " — low; photos and backups may fail"
		return c
	}
	c.Verdict = Pass
	return c
}

func checkPhotos(paths Paths) Check {
	c := Check{Name: "Photo storage"}
	probe := filepath.Join(paths.Photos, ".write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		c.Verdict, c.Detail = Fail, fmt.Sprintf("Not writable: %v", err)
		return c
	}
	os.Remove(probe)

	count := 0
	if entries, err := os.ReadDir(paths.Photos); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				count++
			}
		}
	}
	c.Verdict = Pass
	c.Detail = fmt.Sprintf("writable · %d photo(s) stored", count)
	return c
}

func checkBackups(paths Paths) Check {
	c := Check{Name: "Backups", Action: "/api/backup", ActionLabel: "Back up now"}
	all := ListBackups(paths.Backups)
	if len(all) == 0 {
		c.Verdict, c.Detail = Warn, "No snapshots yet"
		return c
	}
	age := time.Since(all[0].Taken)
	c.Detail = fmt.Sprintf("%d snapshot(s) · newest %s ago (%s)",
		len(all), age.Round(time.Minute), all[0].Reason)
	if age > staleBackup {
		c.Verdict = Warn
		return c
	}
	c.Verdict = Pass
	return c
}

func checkDerbySite(ctx context.Context, db *store.DB) Check {
	c := Check{Name: "Website path", Action: "/settings", ActionLabel: "Set path"}
	path, err := db.Setting(ctx, store.KeyDerbySitePath, "")
	if err != nil {
		c.Verdict, c.Detail = Warn, err.Error()
		return c
	}
	if path == "" {
		c.Verdict, c.Detail = Warn, "Not configured — publishing is unavailable"
		return c
	}
	// A Hugo site without content/ is almost certainly the wrong directory.
	if fi, err := os.Stat(filepath.Join(path, "content")); err != nil || !fi.IsDir() {
		c.Verdict, c.Detail = Fail, path+" has no content/ directory"
		return c
	}
	probe := filepath.Join(path, ".derbyandales-write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		c.Verdict, c.Detail = Fail, fmt.Sprintf("%s is not writable: %v", path, err)
		return c
	}
	os.Remove(probe)
	c.Verdict, c.Detail = Pass, path
	return c
}

func checkDisplays(online int) Check {
	c := Check{Name: "Displays", Action: "/displays", ActionLabel: "Manage"}
	switch {
	case online == 0:
		c.Verdict, c.Detail = Warn, "No screens connected yet"
	case online == 1:
		c.Verdict, c.Detail = Pass, "1 client connected"
	default:
		c.Verdict, c.Detail = Pass, fmt.Sprintf("%d clients connected", online)
	}
	return c
}

// humanBytes formats a byte count for a status line.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// timerCheck reports the Timer Test Bench state.
//
// This is the check that matters most on race night, so it is deliberately
// blunt: a bench that has never run, or ran two hours ago, is not evidence that
// the timer works now.
func timerCheck(tc *TimerController) Check {
	c := Check{Name: "Timer", Action: "/timer/test", ActionLabel: "Open test bench"}
	if tc == nil {
		c.Verdict, c.Detail = Fail, "Timer controller unavailable."
		return c
	}

	status := tc.Status()
	switch {
	case status.Bench == nil:
		c.Verdict = Warn
		c.Detail = "Never tested. Run the timer test before closing check-in."

	case status.BenchStale:
		age := time.Since(status.Bench.StartedAt).Round(time.Minute)
		c.Verdict = Warn
		c.Detail = fmt.Sprintf("Last tested %v ago — too long to trust. Re-run the test.", age)

	case status.Bench.OverriddenBy != "":
		c.Verdict = Warn
		c.Detail = "Failing checks were overridden: " + status.Bench.OverrideReason

	case !status.Bench.Passed():
		c.Verdict = Fail
		var names []string
		for _, f := range status.Bench.Failures() {
			names = append(names, f.Name)
		}
		c.Detail = "Failed: " + strings.Join(names, ", ")

	default:
		c.Verdict = Pass
		detail := status.Bench.Profile
		if status.Identity != "" {
			detail = timer.SummariseIdentity(status.Identity)
		}
		if status.Port != "" {
			detail += " on " + status.Port
		}
		if status.Simulated {
			detail += " (simulated — not real hardware)"
			c.Verdict = Warn
		}
		c.Detail = detail
	}
	return c
}
