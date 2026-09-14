package app

import (
	"context"
	"crypto/x509"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davisc01/derbyandales/internal/store"
	"github.com/davisc01/derbyandales/internal/timer"
)

// openForTest opens a database file directly, used to prove a snapshot is a
// real, readable database rather than just a file of the right size.
func openForTest(t *testing.T, path string) (*store.DB, error) {
	t.Helper()
	db, err := store.Open(context.Background(), path)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { db.Close() })
	return db, nil
}

func testApp(t *testing.T) *App {
	t.Helper()
	paths, err := DefaultPaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := Open(context.Background(), paths, log)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

func TestOpenCreatesEverything(t *testing.T) {
	a := testApp(t)
	for _, dir := range []string{a.Paths.Photos, a.Paths.Renders, a.Paths.Backups, a.Paths.Certs, a.Paths.Traces} {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			t.Errorf("%s was not created", dir)
		}
	}
	if _, err := os.Stat(a.Paths.DB); err != nil {
		t.Errorf("database file missing: %v", err)
	}
	// Open takes a startup snapshot, so there should already be one.
	if got := ListBackups(a.Paths.Backups); len(got) != 1 {
		t.Errorf("startup snapshots = %d, want 1", len(got))
	}
}

func TestSnapshotPrunesToKeep(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := Snapshot(ctx, a.DB, a.Paths, BackupManual, 3); err != nil {
			t.Fatalf("snapshot %d: %v", i, err)
		}
		// Filenames carry a whole-second timestamp, so space them out.
		time.Sleep(1100 * time.Millisecond)
	}
	got := ListBackups(a.Paths.Backups)
	if len(got) != 3 {
		t.Fatalf("kept %d snapshots, want 3", len(got))
	}
	// Newest first.
	for i := 1; i < len(got); i++ {
		if got[i].Taken.After(got[i-1].Taken) {
			t.Error("ListBackups is not newest-first")
		}
	}
}

func TestSnapshotIsRestorable(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	if err := a.DB.SetSetting(ctx, "marker", "before-snapshot"); err != nil {
		t.Fatal(err)
	}
	b, err := Snapshot(ctx, a.DB, a.Paths, BackupCheckinClose, 10)
	if err != nil {
		t.Fatal(err)
	}
	if b.Size == 0 {
		t.Error("snapshot is empty")
	}
	// Change the live database; the snapshot must still hold the old value.
	if err := a.DB.SetSetting(ctx, "marker", "after-snapshot"); err != nil {
		t.Fatal(err)
	}

	restored, err := openForTest(t, b.Path)
	if err != nil {
		t.Fatalf("reopen snapshot: %v", err)
	}
	if got, _ := restored.Setting(ctx, "marker", ""); got != "before-snapshot" {
		t.Errorf("snapshot marker = %q, want before-snapshot", got)
	}
}

func TestBackupReasonSurvivesFilename(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	b, err := Snapshot(ctx, a.DB, a.Paths, BackupRaceComplete, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, got := range ListBackups(a.Paths.Backups) {
		if got.Path == b.Path {
			found = true
			if got.Reason != BackupRaceComplete {
				t.Errorf("reason = %q, want %q", got.Reason, BackupRaceComplete)
			}
		}
	}
	if !found {
		t.Errorf("snapshot %s not listed", filepath.Base(b.Path))
	}
}

// The certificate exists so a check-in iPad gets a secure context and therefore
// a camera. If it does not cover the machine's LAN address, it is useless.
func TestCertCoversLocalhostAndLAN(t *testing.T) {
	paths, err := DefaultPaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	cert, err := EnsureCert(paths)
	if err != nil {
		t.Fatalf("EnsureCert: %v", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}

	if err := leaf.VerifyHostname("localhost"); err != nil {
		t.Errorf("cert does not cover localhost: %v", err)
	}
	covered := map[string]bool{}
	for _, ip := range leaf.IPAddresses {
		covered[ip.String()] = true
	}
	if !covered["127.0.0.1"] {
		t.Error("cert does not cover 127.0.0.1")
	}
	for _, ip := range LANAddrs() {
		if !covered[ip.String()] {
			t.Errorf("cert does not cover LAN address %s", ip)
		}
	}
	if time.Until(leaf.NotAfter) < 300*24*time.Hour {
		t.Errorf("cert expires in %v; it should outlast a season", time.Until(leaf.NotAfter))
	}
}

func TestEnsureCertReusesStoredCert(t *testing.T) {
	paths, _ := DefaultPaths(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	first, err := EnsureCert(paths)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EnsureCert(paths)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := x509.ParseCertificate(first.Certificate[0])
	s, _ := x509.ParseCertificate(second.Certificate[0])
	if f.SerialNumber.Cmp(s.SerialNumber) != 0 {
		t.Error("certificate was regenerated when the stored one was still good")
	}
}

func TestBuildURLs(t *testing.T) {
	u := BuildURLs(8080, 8443)
	if u.Local != "http://localhost:8080" {
		t.Errorf("Local = %q", u.Local)
	}
	if len(u.Secure) != len(u.Insecure) {
		t.Errorf("secure/insecure mismatch: %d vs %d", len(u.Secure), len(u.Insecure))
	}
	// There should be at least the hostname entry even on a machine with no
	// routable LAN address.
	if len(u.Secure) == 0 && LocalHostname() != "" {
		t.Error("expected at least the .local hostname")
	}
}

func TestLANAddrsAreUsableIPv4(t *testing.T) {
	for _, ip := range LANAddrs() {
		if ip.To4() == nil {
			t.Errorf("%s is not IPv4", ip)
		}
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			t.Errorf("%s should have been filtered out", ip)
		}
		if ip.Equal(net.IPv4zero) {
			t.Error("0.0.0.0 should not be advertised")
		}
	}
}

func TestPreflightReportsHonestly(t *testing.T) {
	a := testApp(t)
	checks := a.Preflight(context.Background())

	byName := map[string]Check{}
	for _, c := range checks {
		byName[c.Name] = c
	}

	if got := byName["Database"].Verdict; got != Pass {
		t.Errorf("Database verdict = %q (%s)", got, byName["Database"].Detail)
	}
	if got := byName["Photo storage"].Verdict; got != Pass {
		t.Errorf("Photo storage verdict = %q", got)
	}
	// Nothing has been configured, so this must warn rather than pass.
	if got := byName["Website path"].Verdict; got != Warn {
		t.Errorf("unconfigured Website path verdict = %q, want warn", got)
	}
	// The bench has never run, so this must warn rather than pass. Claiming a
	// working timer without having tested one is the lie that matters most.
	if got := byName["Timer"].Verdict; got != Warn {
		t.Errorf("Timer verdict = %q, want warn", got)
	}
	if detail := byName["Timer"].Detail; !strings.Contains(detail, "Never tested") {
		t.Errorf("Timer detail = %q, want it to say the bench has not run", detail)
	}
}

// The timer preflight line is what stands between a broken timer and a room
// full of people waiting, so each of its states is pinned down.
func TestTimerPreflightStates(t *testing.T) {
	cases := []struct {
		name     string
		bench    *timer.Result
		want     Verdict
		contains string
	}{
		{
			name: "never run",
			want: Warn, contains: "Never tested",
		},
		{
			name: "stale",
			bench: &timer.Result{
				StartedAt: time.Now().Add(-timer.MaxBenchAge - time.Hour),
				Checks:    []timer.Check{{Verdict: timer.VerdictPass}},
			},
			want: Warn, contains: "too long to trust",
		},
		{
			name: "failing",
			bench: &timer.Result{
				StartedAt: time.Now(),
				Checks: []timer.Check{
					{Name: "Start gate", Verdict: timer.VerdictFail},
				},
			},
			want: Fail, contains: "Start gate",
		},
		{
			name: "overridden",
			bench: &timer.Result{
				StartedAt:      time.Now(),
				Checks:         []timer.Check{{Name: "Start gate", Verdict: timer.VerdictFail}},
				OverriddenBy:   "coordinator",
				OverrideReason: "switch jammed",
			},
			want: Warn, contains: "switch jammed",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := testApp(t)
			if c.bench != nil {
				a.Timer.mu.Lock()
				a.Timer.lastBench = c.bench
				a.Timer.mu.Unlock()
			}
			var got Check
			for _, check := range a.Preflight(context.Background()) {
				if check.Name == "Timer" {
					got = check
				}
			}
			if got.Verdict != c.want {
				t.Errorf("verdict = %q, want %q (detail: %s)", got.Verdict, c.want, got.Detail)
			}
			if !strings.Contains(got.Detail, c.contains) {
				t.Errorf("detail = %q, want it to mention %q", got.Detail, c.contains)
			}
		})
	}
}

// Connecting the simulator must never be mistaken for a working timer.
func TestSimulatedTimerNeverReportsAFullPass(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	if err := a.Timer.Connect(ctx, "", timer.SimulatorKey); err != nil {
		t.Fatalf("connect simulator: %v", err)
	}
	if _, err := a.Timer.RunBench(ctx); err != nil {
		t.Fatalf("run bench: %v", err)
	}

	for _, check := range a.Preflight(ctx) {
		if check.Name != "Timer" {
			continue
		}
		if check.Verdict == Pass {
			t.Error("a simulated timer must not report a clean pass")
		}
		if !strings.Contains(check.Detail, "simulated") {
			t.Errorf("detail = %q, want it to say the timer is simulated", check.Detail)
		}
		return
	}
	t.Fatal("Timer check missing")
}

func TestPreflightFailsOnBadWebsitePath(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	// A directory with no content/ is not a Hugo site.
	bad := t.TempDir()
	if err := a.DB.SetSetting(ctx, "derby_site_path", bad); err != nil {
		t.Fatal(err)
	}
	for _, c := range a.Preflight(ctx) {
		if c.Name == "Website path" {
			if c.Verdict != Fail {
				t.Errorf("verdict = %q, want fail (detail: %s)", c.Verdict, c.Detail)
			}
			return
		}
	}
	t.Fatal("Website path check missing")
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1 << 20, "1.0 MB"},
		{1 << 30, "1.0 GB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
