package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/davisc01/derbyandales/internal/app"
	"github.com/davisc01/derbyandales/internal/bus"
)

func testServer(t *testing.T) (*Server, *app.App) {
	t.Helper()
	paths, err := app.DefaultPaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(context.Background(), paths, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })

	s, err := New(a)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, a
}

// handler exposes the routed mux for direct testing, without binding a port.
func handler(t *testing.T, s *Server) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	s.routes(mux)
	return mux
}

// Templates are parsed at startup, so a typo in one would otherwise first show
// up as a 500 in front of a room full of people.
func TestAllTemplatesParse(t *testing.T) {
	pages, err := parsePages()
	if err != nil {
		t.Fatalf("parsePages: %v", err)
	}
	for _, want := range []string{"home.html", "settings.html"} {
		if _, ok := pages[want]; !ok {
			t.Errorf("page %q was not parsed", want)
		}
	}
}

// Every page must actually render with real data, not merely parse.
func TestPagesRender(t *testing.T) {
	s := mustServer(t)
	h := handler(t, s)

	for _, path := range []string{"/", "/settings", "/timer/test", "/displays", "/season", "/championship", "/publish", "/run"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, "Derby and Ales") {
				t.Error("layout did not render")
			}
			// A template that failed mid-render would leave a truncated page.
			if !strings.Contains(body, "</html>") {
				t.Error("page is truncated")
			}
		})
	}
}

func TestHomeShowsConnectionURLs(t *testing.T) {
	s := mustServer(t)
	rec := httptest.NewRecorder()
	handler(t, s).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	body := rec.Body.String()
	// The whole point of the card is telling someone where the camera works.
	if !strings.Contains(body, "http://localhost:") {
		t.Error("home page does not show the local URL")
	}
	if !strings.Contains(body, "camera works") {
		t.Error("home page does not explain where the camera works")
	}
}

func TestPreflightAPIReturnsJSON(t *testing.T) {
	s := mustServer(t)
	rec := httptest.NewRecorder()
	handler(t, s).ServeHTTP(rec, httptest.NewRequest("GET", "/api/preflight", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var checks []app.Check
	if err := json.Unmarshal(rec.Body.Bytes(), &checks); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(checks) == 0 {
		t.Fatal("no checks returned")
	}
	for _, c := range checks {
		if c.Name == "" || c.Verdict == "" {
			t.Errorf("incomplete check: %+v", c)
		}
	}
}

func TestSettingsSaveOnlyAcceptsKnownKeys(t *testing.T) {
	s, a := testServerPair(t)
	h := handler(t, s)

	form := strings.NewReader("derby_site_path=/tmp/site&evil_key=owned")
	req := httptest.NewRequest("POST", "/settings", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	ctx := context.Background()
	if got, _ := a.DB.Setting(ctx, "derby_site_path", ""); got != "/tmp/site" {
		t.Errorf("known key not saved, got %q", got)
	}
	if got, _ := a.DB.Setting(ctx, "evil_key", ""); got != "" {
		t.Errorf("unknown key was written: %q", got)
	}
}

func TestHealthz(t *testing.T) {
	s := mustServer(t)
	rec := httptest.NewRecorder()
	handler(t, s).ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true {
		t.Errorf("ok = %v", body["ok"])
	}
}

// The SSE stream is how every display stays current. It must send headers and
// flush immediately, not buffer until the first event.
func TestEventStreamFlushesAndDelivers(t *testing.T) {
	s, a := testServerPair(t)
	srv := httptest.NewServer(handler(t, s))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/events?topics=race", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q", ct)
	}

	buf := make([]byte, 256)
	n, err := resp.Body.Read(buf)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "retry:") {
		t.Errorf("expected a retry hint first, got %q", buf[:n])
	}

	// Wait for the subscription to land, then publish.
	deadline := time.Now().Add(2 * time.Second)
	for a.Bus.Subscribers() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	a.Bus.Publish(bus.TopicRace, "heat.armed", map[string]int{"heat": 3})

	n, err = resp.Body.Read(buf)
	if err != nil {
		t.Fatalf("read event: %v", err)
	}
	got := string(buf[:n])
	if !strings.Contains(got, "event: race") || !strings.Contains(got, "heat.armed") {
		t.Errorf("unexpected frame: %q", got)
	}
}

// A subscriber asking only for one topic must not be woken by others.
func TestEventStreamRespectsTopicFilter(t *testing.T) {
	s, a := testServerPair(t)
	srv := httptest.NewServer(handler(t, s))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/events?topics=vote", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 256)
	resp.Body.Read(buf) // the retry hint

	deadline := time.Now().Add(2 * time.Second)
	for a.Bus.Subscribers() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	a.Bus.Publish(bus.TopicTimer, "gate.open", nil) // filtered out
	a.Bus.Publish(bus.TopicVote, "tally", nil)      // should arrive

	n, err := resp.Body.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(buf[:n])
	if strings.Contains(got, "gate.open") {
		t.Error("a filtered topic leaked into the stream")
	}
	if !strings.Contains(got, "tally") {
		t.Errorf("expected the vote event, got %q", got)
	}
}

func TestParseTopics(t *testing.T) {
	if got := parseTopics(""); got != nil {
		t.Errorf("empty should mean all topics, got %v", got)
	}
	if got := parseTopics("  "); got != nil {
		t.Errorf("whitespace should mean all topics, got %v", got)
	}
	got := parseTopics("race, timer ,")
	want := []bus.Topic{bus.TopicRace, bus.TopicTimer}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestStaticAssetsAreServed(t *testing.T) {
	s := mustServer(t)
	h := handler(t, s)
	for _, path := range []string{"/static/app.css", "/static/app.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s status = %d", path, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("%s is empty", path)
		}
	}
}

// --- helpers -----------------------------------------------------------------

func mustServer(t *testing.T) *Server {
	t.Helper()
	s, _ := testServer(t)
	return s
}

func testServerPair(t *testing.T) (*Server, *app.App) {
	t.Helper()
	return testServer(t)
}
