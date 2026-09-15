// Command derbyandales runs the club's race server.
//
// It starts a local web server and everything else happens in a browser: the
// coordinator on this Mac, check-in tablets, the voting tablet, and TV displays
// all connect to it over the LAN.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/davisc01/derbyandales/internal/app"
	"github.com/davisc01/derbyandales/internal/bus"
	"github.com/davisc01/derbyandales/internal/web"
)

// version is stamped at build time by packaging/make-app.sh.
var version = "dev"

func main() {
	var (
		dataDir   = flag.String("data", "", "data directory (default: ~/Library/Application Support/DerbyAndAles)")
		httpPort  = flag.Int("http", 0, "HTTP port (overrides the stored setting)")
		httpsPort = flag.Int("https", 0, "HTTPS port (overrides the stored setting)")
		noTLS     = flag.Bool("no-tls", false, "skip the HTTPS listener (remote camera capture will not work)")
		noOpen    = flag.Bool("no-open", false, "do not open a browser on startup")
		debug     = flag.Bool("debug", false, "verbose logging")
		demo      = flag.Bool("demo", false, "create a demo season and race if none exists, for learning the software without real data")
		demoChamp = flag.Bool("demo-championship", false, "create a demo season that has finished, with its bracket championship at check-in")
		showVer   = flag.Bool("version", false, "print version and exit")
	)
	// Older macOS versions pass a process serial number to a launched app. It
	// is not a flag this program knows, and must not stop it starting.
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-psn_") {
			args = append(args[:i], args[i+1:]...)
			i--
		}
	}
	flag.CommandLine.Parse(args)

	if *showVer {
		fmt.Println("derbyandales", version)
		return
	}

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	opts := &slog.HandlerOptions{Level: level}
	log := slog.New(slog.NewTextHandler(os.Stderr, opts))

	runApp(func() error {
		return run(log, opts, *dataDir, *httpPort, *httpsPort, *noTLS, *noOpen, *demo, *demoChamp)
	}, func(err error) {
		log.Error("fatal", "err", err)
	})
}

func run(log *slog.Logger, opts *slog.HandlerOptions, dataDir string, httpPort, httpsPort int, noTLS, noOpen, demo, demoChamp bool) error {
	// Interrupts are caught so the database gets a clean close and connected
	// displays are told the server is going away.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	paths, err := app.DefaultPaths(dataDir)
	if err != nil {
		return err
	}

	// Double-clicking the app while it is already running is the normal way
	// somebody gets back to it — there is no window to switch to. Opening the
	// browser on the copy already running is what they meant; starting a second
	// one would fight it for the ports and the database.
	probe := httpPort
	if probe == 0 {
		probe = app.DefaultHTTPPort
	}
	if alreadyRunning(probe) {
		log.Info("already running; opening it", "port", probe)
		if !noOpen {
			openBrowser(fmt.Sprintf("http://localhost:%d", probe), log)
		}
		return nil
	}

	if f, err := app.OpenLogFile(paths, app.DefaultLogKeep); err != nil {
		log.Warn("could not open a log file; logging to the terminal only", "err", err)
	} else {
		defer f.Close()
		log = slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, f), opts))
		log.Info("starting", "version", version, "log", f.Name())
	}

	a, err := app.Open(ctx, paths, log)
	if err != nil {
		return err
	}
	defer a.Close()

	if (demo || demoChamp) && !a.HasDemoData(ctx) {
		seed := a.SeedDemoSeason
		if demoChamp {
			seed = a.SeedDemoChampionship
		}
		if _, err := seed(ctx, time.Now().Year()); err != nil {
			log.Warn("could not create demo data", "err", err)
		} else {
			// The startup load already ran against an empty database.
			a.Race.LoadMostRecentRace(ctx)
		}
	}

	if httpPort > 0 {
		a.HTTPPort = httpPort
	}
	if httpsPort > 0 {
		a.HTTPSPort = httpsPort
	}

	srv, err := web.New(a)
	if err != nil {
		return err
	}
	// The browser is the only UI, so it needs a way to stop the app. This runs
	// the same shutdown path as Ctrl-C.
	srv.OnQuit(stop)
	// Quitting from the Dock or the menu bar takes the same path as Ctrl-C and
	// the Status page's Quit.
	dockHooks.quit = stop

	if !noTLS {
		cert, err := app.EnsureCert(paths)
		if err != nil {
			// HTTPS is a convenience for remote camera capture, not a
			// requirement — a certificate problem must not stop a race night.
			log.Warn("https unavailable; remote check-in devices will have no camera", "err", err)
		} else if err := srv.StartTLS(cert); err != nil {
			log.Warn("https listener failed to start", "err", err)
		}
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	// Give the listener a moment to bind so a failure surfaces before we print
	// URLs the operator cannot actually reach.
	select {
	case err := <-errCh:
		if err != nil {
			// A port saved in Settings is only known once the database is
			// open, so a copy already running on it is caught here instead.
			if alreadyRunning(a.HTTPPort) {
				log.Info("already running; opening it", "port", a.HTTPPort)
				if !noOpen {
					openBrowser(a.URLs().Local, log)
				}
				return nil
			}
			return fmt.Errorf("http listener: %w", err)
		}
		return nil
	case <-time.After(150 * time.Millisecond):
	}

	fmt.Fprint(os.Stderr, "\n"+a.Banner()+"\n")

	dockHooks.coordinator = func() { openBrowser(a.URLs().Local, log) }
	dockHooks.devices = func() {
		base := a.URLs().Local
		if lan := a.URLs().LAN(); len(lan) > 0 {
			base = lan[0]
		}
		openBrowser(base+"/devices", log)
	}

	if !noOpen {
		openBrowser(a.URLs().Local, log)
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	}

	// A final snapshot on every way out — the Dock, ⌘Q, the Status page, Ctrl-C —
	// so the night's work is never the thing that gets lost.
	if _, err := a.Backup(context.Background(), app.BackupManual); err != nil {
		log.Warn("snapshot on quit failed", "err", err)
	}

	// Tell displays the server is going away, so they show "server stopped"
	// rather than silently freezing on a stale heat.
	a.Bus.Publish(bus.TopicSystem, "shutdown", nil)
	time.Sleep(100 * time.Millisecond)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("shutdown", "err", err)
	}
	return nil
}

// openBrowser opens the coordinator UI. Failure is not an error: the URL is
// printed either way.
func openBrowser(url string, log *slog.Logger) {
	if runtime.GOOS != "darwin" {
		return
	}
	if err := exec.Command("open", url).Start(); err != nil {
		log.Debug("could not open browser", "err", err)
	}
}

// alreadyRunning reports whether Derby and Ales is already answering on the
// port. It asks the health endpoint rather than just trying the port, so some
// other program on 8080 is not mistaken for it.
func alreadyRunning(port int) bool {
	client := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/healthz", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var body struct {
		App string `json:"app"`
	}
	return json.NewDecoder(resp.Body).Decode(&body) == nil && body.App == "derbyandales"
}
