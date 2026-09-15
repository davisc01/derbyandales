// Package web serves the browser UI and the JSON API. Everything the
// coordinator, check-in tablets, voting tablet and TV displays use is here.
package web

import (
	"bytes"
	"context"
	"crypto/tls"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/davisc01/derbyandales/internal/app"
	"github.com/davisc01/derbyandales/internal/scoring"
	"github.com/davisc01/derbyandales/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Server owns the HTTP and HTTPS listeners.
type Server struct {
	app *app.App
	// pages holds one template set per page. Every page defines a block named
	// "content", so they cannot share a namespace — each is parsed together
	// with the layout instead.
	pages map[string]*template.Template

	http  *http.Server
	https *http.Server

	// quit stops the process. Set by the caller so the browser has a way to
	// shut the app down without a Terminal.
	quit func()
}

// OnQuit registers the function called when the browser requests a shutdown.
func (s *Server) OnQuit(fn func()) { s.quit = fn }

// New builds the server and parses templates.
func New(a *app.App) (*Server, error) {
	pages, err := parsePages()
	if err != nil {
		return nil, err
	}
	s := &Server{app: a, pages: pages}

	mux := http.NewServeMux()
	s.routes(mux)
	handler := s.withLogging(mux)

	s.http = &http.Server{
		Addr:              fmt.Sprintf(":%d", a.HTTPPort),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: SSE connections are long-lived by design.
	}
	return s, nil
}

// routes registers every endpoint.
func (s *Server) routes(mux *http.ServeMux) {
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // embedded, so this is a build error not a runtime one
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))

	mux.HandleFunc("GET /{$}", s.handleHome)
	mux.HandleFunc("GET /settings", s.handleSettings)
	mux.HandleFunc("POST /settings", s.handleSaveSettings)
	mux.HandleFunc("POST /api/history/import", s.handleImportChampionships)

	mux.HandleFunc("GET /events", s.handleEvents)

	mux.HandleFunc("GET /api/preflight", s.handlePreflightAPI)
	mux.HandleFunc("POST /api/backup", s.handleBackupAPI)
	mux.HandleFunc("POST /api/backup/restore", s.handleRestoreAPI)
	mux.HandleFunc("POST /api/backup/restore/cancel", s.handleCancelRestoreAPI)
	mux.HandleFunc("POST /api/quit", s.handleQuitAPI)
	mux.HandleFunc("GET /healthz", s.handleHealthz)

	s.timerRoutes(mux)
	s.displayRoutes(mux)
	s.raceRoutes(mux)
	s.checkinRoutes(mux)
	s.voteRoutes(mux)
	s.seasonRoutes(mux)
	s.bracketRoutes(mux)
	s.publishRoutes(mux)
	s.runRoutes(mux)
	s.devicesRoutes(mux)
}

// StartTLS brings up the HTTPS listener, which exists so remote check-in
// devices get a secure context and therefore camera access.
func (s *Server) StartTLS(cert tls.Certificate) error {
	s.https = &http.Server{
		Addr:              fmt.Sprintf(":%d", s.app.HTTPSPort),
		Handler:           s.http.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
	}
	go func() {
		if err := s.https.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.app.Log.Error("https listener stopped", "err", err)
		}
	}()
	return nil
}

// ListenAndServe runs the HTTP listener until the server is shut down.
func (s *Server) ListenAndServe() error {
	err := s.http.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown stops both listeners gracefully.
func (s *Server) Shutdown(ctx context.Context) error {
	var firstErr error
	if s.https != nil {
		if err := s.https.Shutdown(ctx); err != nil {
			firstErr = err
		}
	}
	if err := s.http.Shutdown(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// withLogging records each request. Static assets and the SSE stream are
// skipped so the log stays readable during a race.
func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/events" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.app.Log.Debug("request",
			"method", r.Method, "path", r.URL.Path,
			"status", rec.status, "dur", time.Since(start).Round(time.Millisecond))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer, which the
// SSE handler needs in order to flush.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// --- page handlers -----------------------------------------------------------

type pageData struct {
	Title  string
	Active string
	// NoNav leaves the coordinator's navigation off, for a page handed to a
	// device at a table rather than to the person running the night.
	NoNav bool
	App   *app.App
	Data  any
}

// bareLayoutPages use the chrome-free layout. A TV showing the race has no
// business displaying a navigation bar.
var bareLayoutPages = map[string]bool{
	"display.html": true,
	"vote.html":    true,
}

// parsePages pairs each page template with a layout, giving every page its own
// namespace for the "content" block.
func parsePages() (map[string]*template.Template, error) {
	layouts := map[bool][]byte{}
	for bare, file := range map[bool]string{false: "layout.html", true: "layout-bare.html"} {
		body, err := templateFS.ReadFile("templates/" + file)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
		layouts[bare] = body
	}

	names, err := fs.Glob(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	pages := make(map[string]*template.Template, len(names))
	for _, path := range names {
		name := strings.TrimPrefix(path, "templates/")
		if strings.HasPrefix(name, "layout") {
			continue
		}
		body, err := templateFS.ReadFile(path)
		if err != nil {
			return nil, err
		}
		t, err := template.New(name).Funcs(funcMap()).Parse(string(layouts[bareLayoutPages[name]]))
		if err != nil {
			return nil, fmt.Errorf("parse layout for %s: %w", name, err)
		}
		if _, err := t.Parse(string(body)); err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		if t.Lookup("content") == nil {
			return nil, fmt.Errorf("page %s does not define a \"content\" block", name)
		}
		pages[name] = t
	}
	return pages, nil
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, page string, d pageData) {
	t, ok := s.pages[page]
	if !ok {
		s.app.Log.Error("unknown page template", "page", page)
		http.Error(w, "unknown page: "+page, http.StatusInternalServerError)
		return
	}
	d.App = s.app
	// Render to a buffer first: a template error halfway through would
	// otherwise leave a half-written page with a 200 status.
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", d); err != nil {
		s.app.Log.Error("render", "page", page, "err", err)
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	seasons, err := s.app.DB.Seasons(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, r, "home.html", pageData{
		Title:  "Status",
		Active: "home",
		Data: map[string]any{
			"Checks":  s.app.Preflight(ctx),
			"URLs":    s.app.URLs(),
			"Backups": app.ListBackups(s.app.Paths.Backups),
			"Restore": app.RestorePending(s.app.Paths),
			"Seasons": seasons,
			"DBPath":  s.app.Paths.DB,
		},
	})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.app.DB.AllSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	historyYears, _ := s.app.DB.ChampionshipYears(r.Context())
	historyCars, _ := s.app.DB.PastChampionshipCars(r.Context())

	s.render(w, r, "settings.html", pageData{
		Title:  "Settings",
		Active: "settings",
		Data: map[string]any{
			"Settings":     settings,
			"HistoryYears": historyYears,
			"HistoryCars":  historyCars,
			"HTTPPort":     s.app.HTTPPort,
			"HTTPSPort":    s.app.HTTPSPort,
			"Paths":        s.app.Paths,
		},
	})
}

func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	// Only keys we know about are writable, so a stray form field cannot
	// invent configuration.
	allowed := []string{
		store.KeyDerbySitePath,
		store.KeyAutoAdvanceSecs,
		store.KeyBackupKeep,
		store.KeyHTTPPort,
		store.KeyHTTPSPort,
	}
	for _, key := range allowed {
		if !r.Form.Has(key) {
			continue
		}
		val := strings.TrimSpace(r.Form.Get(key))
		if err := s.app.DB.SetSetting(ctx, key, val); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	_ = s.app.DB.Audit(ctx, "coordinator", "settings.save", "")
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

// --- API handlers ------------------------------------------------------------

func (s *Server) handlePreflightAPI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.app.Preflight(r.Context()))
}

func (s *Server) handleBackupAPI(w http.ResponseWriter, r *http.Request) {
	b, err := s.app.Backup(r.Context(), app.BackupManual)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path": b.Path, "size": b.Size, "taken": b.Taken,
	})
}

// handleRestoreAPI stages a backup to replace the database and stops the app.
// The swap happens on the next launch; see app.StageRestore for why.
func (s *Server) handleRestoreAPI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Going back to an earlier state in the middle of a heat would throw away
	// whatever the timer is about to record.
	if s.app.Race.State(ctx).Running {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "racing is running — stop it before going back to a backup",
		})
		return
	}
	name := r.FormValue("name")
	keep, _ := s.app.DB.SettingInt(ctx, store.KeyBackupKeep, app.DefaultBackupKeep)
	if err := app.StageRestore(ctx, s.app.DB, s.app.Paths, name, keep); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	_ = s.app.DB.Audit(ctx, "coordinator", "backup.restore", "staged "+name+"; the app stops and restores on next launch")
	writeJSON(w, http.StatusOK, map[string]bool{"stopping": true})
	s.stopSoon("restore staged")
}

func (s *Server) handleCancelRestoreAPI(w http.ResponseWriter, r *http.Request) {
	if err := app.CancelRestore(s.app.Paths); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	_ = s.app.DB.Audit(r.Context(), "coordinator", "backup.restore.cancel", "")
	writeJSON(w, http.StatusOK, map[string]bool{"cancelled": true})
}

// stopSoon signals shutdown once the response has had a moment to flush.
func (s *Server) stopSoon(why string) {
	go func() {
		time.Sleep(150 * time.Millisecond)
		s.app.Log.Info("stopping", "why", why)
		if s.quit != nil {
			s.quit()
		}
	}()
}

// handleQuitAPI stops the server.
//
// Launched from the .app there is no Dock icon and no Terminal, so without this
// the only way to stop the app would be Activity Monitor. Quitting takes a
// final snapshot first, so the night's work is never the thing that gets lost.
func (s *Server) handleQuitAPI(w http.ResponseWriter, r *http.Request) {
	if _, err := s.app.Backup(r.Context(), app.BackupManual); err != nil {
		s.app.Log.Warn("snapshot before quit failed", "err", err)
	}
	_ = s.app.DB.Audit(r.Context(), "coordinator", "app.quit", "")
	writeJSON(w, http.StatusOK, map[string]bool{"stopping": true})

	s.stopSoon("quit requested from the browser")
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"subscribers": s.app.Bus.Subscribers(),
		"drops":       s.app.Bus.Drops(),
	})
}

// --- template helpers --------------------------------------------------------

func funcMap() template.FuncMap {
	return template.FuncMap{
		"bytes": func(n int64) string {
			const unit = 1024
			if n < unit {
				return strconv.FormatInt(n, 10) + " B"
			}
			div, exp := int64(unit), 0
			for m := n / unit; m >= unit; m /= unit {
				div *= unit
				exp++
			}
			return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
		},
		"ago": func(t time.Time) string {
			d := time.Since(t)
			switch {
			case d < time.Minute:
				return "just now"
			case d < time.Hour:
				return fmt.Sprintf("%dm ago", int(d.Minutes()))
			case d < 24*time.Hour:
				return fmt.Sprintf("%dh ago", int(d.Hours()))
			default:
				return fmt.Sprintf("%dd ago", int(d.Hours()/24))
			}
		},
		"datetime": func(t time.Time) string { return t.Format("Mon 2 Jan 15:04") },
		"online":   displayOnline,
		"time": func(t *float64) string {
			if t == nil {
				return ""
			}
			return scoring.FormatTime(*t)
		},
		"average": scoring.FormatAverage,
		"seconds": scoring.FormatTime,
		// Odds are rounded to something sayable: "1 in 256", not "1 in 256.0".
		"odds": func(n float64) string { return strconv.FormatFloat(n, 'f', 0, 64) },
		"signed": func(n int) string {
			if n > 0 {
				return "+" + strconv.Itoa(n)
			}
			return strconv.Itoa(n)
		},
		"publishable": publishable,
		// A diff mark is '+', '-' or ' ', none of which is a usable class name.
		"diffclass": func(mark byte) string {
			switch mark {
			case '+':
				return "dplus"
			case '-':
				return "dminus"
			}
			return "dspace"
		},
		"setting": func(m map[string]string, key, def string) string {
			if v, ok := m[key]; ok && v != "" {
				return v
			}
			return def
		},
	}
}

// handleImportChampionships reads the club's published archive back in, so the
// previous-championship rule can be checked at the table rather than recalled.
func (s *Server) handleImportChampionships(w http.ResponseWriter, r *http.Request) {
	years, err := s.app.ImportChampionships(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	out := make([]map[string]any, 0, len(years))
	cars := 0
	for _, y := range years {
		row := map[string]any{"year": y.Year, "cars": y.Cars}
		if y.Problem != "" {
			row["problem"] = y.Problem
		}
		cars += y.Cars
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"years": out, "cars": cars})
}
