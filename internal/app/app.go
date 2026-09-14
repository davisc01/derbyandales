package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/davisc01/derbyandales/internal/bus"
	"github.com/davisc01/derbyandales/internal/store"
)

// Default ports. HTTP serves displays, voting and the coordinator UI; HTTPS
// exists so remote check-in devices get a secure context and therefore a camera.
const (
	DefaultHTTPPort   = 8080
	DefaultHTTPSPort  = 8443
	DefaultBackupKeep = 20
)

// App is the wired-up application: storage, event bus, and resolved settings.
// The web layer and the timer both hang off this.
type App struct {
	Paths Paths
	DB    *store.DB
	Bus   *bus.Bus
	Log   *slog.Logger

	// Timer owns the connection to the finish-line timer. One owner, so the
	// race controller and the test bench can never hold the port at once.
	Timer *TimerController

	// Race runs the heats.
	Race *RaceController

	HTTPPort  int
	HTTPSPort int
}

// Open prepares directories, opens the database, applies migrations, and takes
// a startup snapshot.
func Open(ctx context.Context, paths Paths, log *slog.Logger) (*App, error) {
	if err := paths.EnsureDirs(); err != nil {
		return nil, err
	}

	db, err := store.Open(ctx, paths.DB)
	if err != nil {
		return nil, err
	}

	a := &App{
		Paths: paths,
		DB:    db,
		Bus:   bus.New(),
		Log:   log,
	}

	if a.HTTPPort, err = db.SettingInt(ctx, store.KeyHTTPPort, DefaultHTTPPort); err != nil {
		db.Close()
		return nil, err
	}
	if a.HTTPSPort, err = db.SettingInt(ctx, store.KeyHTTPSPort, DefaultHTTPSPort); err != nil {
		db.Close()
		return nil, err
	}

	a.Timer = NewTimerController(a)
	a.Race = NewRaceController(a)
	// Point the displays at something without being asked, so a screen plugged
	// in at the venue shows the right race straight away.
	a.Race.LoadMostRecentRace(ctx)

	// A startup snapshot means that however badly a race night goes, there is
	// always a copy of the state the night began in.
	keep, _ := db.SettingInt(ctx, store.KeyBackupKeep, DefaultBackupKeep)
	if b, err := Snapshot(ctx, db, paths, BackupStartup, keep); err != nil {
		log.Warn("startup snapshot failed", "err", err)
	} else {
		log.Info("startup snapshot", "path", b.Path, "size", humanBytes(b.Size))
	}

	return a, nil
}

// Close releases the timer and the database.
func (a *App) Close() error {
	if a.Race != nil {
		a.Race.Stop()
	}
	if a.Timer != nil {
		a.Timer.Disconnect()
	}
	if a.DB != nil {
		return a.DB.Close()
	}
	return nil
}

// Preflight runs the server-side checks.
func (a *App) Preflight(ctx context.Context) []Check {
	return RunPreflight(ctx, a.DB, a.Paths, a.Bus.Subscribers(), a.Timer)
}

// URLs reports how to reach this server.
func (a *App) URLs() URLs { return BuildURLs(a.HTTPPort, a.HTTPSPort) }

// Backup takes a snapshot and announces it on the bus.
func (a *App) Backup(ctx context.Context, reason BackupReason) (Backup, error) {
	keep, _ := a.DB.SettingInt(ctx, store.KeyBackupKeep, DefaultBackupKeep)
	b, err := Snapshot(ctx, a.DB, a.Paths, reason, keep)
	if err != nil {
		return b, err
	}
	a.Bus.Publish(bus.TopicSystem, "backup", map[string]any{
		"path":   b.Path,
		"reason": string(b.Reason),
		"size":   b.Size,
		"taken":  b.Taken,
	})
	a.Log.Info("snapshot", "reason", reason, "path", b.Path)
	return b, nil
}

// Banner is the startup text printed to the log and shown on the home page.
func (a *App) Banner() string {
	u := a.URLs()
	s := fmt.Sprintf("%s is running\n\n  Coordinator:  %s   (camera works here)\n", AppName, u.Local)
	for _, addr := range u.Secure {
		s += fmt.Sprintf("  Check-in:     %s   (camera works after trusting the certificate)\n", addr)
	}
	for _, addr := range u.Insecure {
		s += fmt.Sprintf("  Displays:     %s   (voting and screens; no camera)\n", addr)
	}
	return s
}
