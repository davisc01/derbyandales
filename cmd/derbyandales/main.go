// Command derbyandales runs the club's race server.
//
// It starts a local web server and everything else happens in a browser: the
// coordinator on this Mac, check-in tablets, the voting tablet, and TV displays
// all connect to it over the LAN.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
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
		showVer   = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("derbyandales", version)
		return
	}

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	if err := run(log, *dataDir, *httpPort, *httpsPort, *noTLS, *noOpen, *demo); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, dataDir string, httpPort, httpsPort int, noTLS, noOpen, demo bool) error {
	// Interrupts are caught so the database gets a clean close and connected
	// displays are told the server is going away.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	paths, err := app.DefaultPaths(dataDir)
	if err != nil {
		return err
	}

	a, err := app.Open(ctx, paths, log)
	if err != nil {
		return err
	}
	defer a.Close()

	if demo && !a.HasDemoData(ctx) {
		if _, err := a.SeedDemoRace(ctx, time.Now().Year()); err != nil {
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
			return fmt.Errorf("http listener: %w", err)
		}
		return nil
	case <-time.After(150 * time.Millisecond):
	}

	fmt.Fprint(os.Stderr, "\n"+a.Banner()+"\n")

	if !noOpen {
		openBrowser(a.URLs().Local, log)
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
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
