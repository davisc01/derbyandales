package web

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// postForm posts a form and returns the status and decoded body.
func postForm(t *testing.T, h http.Handler, path string, form url.Values) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func getJSON(t *testing.T, h http.Handler, path string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v (body %s)", path, err, rec.Body.String())
	}
	return body
}

// checkinFixture returns a server with a season and race open for check-in.
func checkinFixture(t *testing.T) (http.Handler, int64) {
	t.Helper()
	s, _ := testServer(t)
	h := handler(t, s)

	code, body := postForm(t, h, "/api/season", url.Values{"year": {"2027"}})
	if code != http.StatusOK {
		t.Fatalf("create season: %d %v", code, body)
	}
	seasonID := int64(body["ID"].(float64))

	code, body = postForm(t, h, "/api/race", url.Values{
		"season_id": {itoa(seasonID)}, "number": {"1"}, "venue": {"Test Brewing"},
	})
	if code != http.StatusOK {
		t.Fatalf("create race: %d %v", code, body)
	}
	return h, int64(body["ID"].(float64))
}

// itoa keeps the test calls readable.
func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestCheckinAddsAnEntry(t *testing.T) {
	h, _ := checkinFixture(t)

	code, body := postForm(t, h, "/api/entry", url.Values{
		"first_name": {"Ada"}, "last_name": {"Fairweather"},
		"car_number": {"7"}, "car_name": {"Lightning Bug"},
	})
	if code != http.StatusOK {
		t.Fatalf("check-in failed: %d %v", code, body)
	}

	entries := getJSON(t, h, "/api/entries")["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0].(map[string]any)
	if e["driver"] != "Ada Fairweather" {
		t.Errorf("driver = %v", e["driver"])
	}
	if e["car_number"].(float64) != 7 {
		t.Errorf("car number = %v", e["car_number"])
	}
}

// The same person's second car must reuse their racer record, or season points
// would be split between two identities.
func TestSecondCarReusesTheRacer(t *testing.T) {
	h, _ := checkinFixture(t)

	for _, car := range []string{"7", "8"} {
		code, body := postForm(t, h, "/api/entry", url.Values{
			"first_name": {"Ada"}, "last_name": {"Fairweather"}, "car_number": {car},
		})
		if code != http.StatusOK {
			t.Fatalf("car %s: %d %v", car, code, body)
		}
	}

	racers := getJSON(t, h, "/api/racers")["racers"].([]any)
	if len(racers) != 1 {
		t.Errorf("got %d racers for two cars by the same person, want 1", len(racers))
	}
	entries := getJSON(t, h, "/api/entries")["entries"].([]any)
	if len(entries) != 2 {
		t.Errorf("got %d entries, want 2", len(entries))
	}
}

// A clash needs a message someone can act on, not a constraint name.
func TestDuplicateCarNumberIsRefusedReadably(t *testing.T) {
	h, _ := checkinFixture(t)

	postForm(t, h, "/api/entry", url.Values{
		"first_name": {"Ada"}, "last_name": {"F"}, "car_number": {"7"},
	})
	code, body := postForm(t, h, "/api/entry", url.Values{
		"first_name": {"Bertie"}, "last_name": {"C"}, "car_number": {"7"},
	})

	if code == http.StatusOK {
		t.Fatal("a duplicate car number was accepted")
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "7") || !strings.Contains(strings.ToLower(msg), "already") {
		t.Errorf("error = %q; it should name the car and say it is taken", msg)
	}
	if strings.Contains(msg, "UNIQUE") || strings.Contains(msg, "idx_") {
		t.Errorf("error = %q; a database constraint leaked into the UI", msg)
	}
}

func TestEntryNeedsANameAndANumber(t *testing.T) {
	h, _ := checkinFixture(t)

	if code, _ := postForm(t, h, "/api/entry", url.Values{"car_number": {"7"}}); code == http.StatusOK {
		t.Error("an entry with no driver name was accepted")
	}
	if code, _ := postForm(t, h, "/api/entry", url.Values{
		"first_name": {"Ada"}, "last_name": {"F"},
	}); code == http.StatusOK {
		t.Error("an entry with no car number was accepted")
	}
}

// An exclusion without a reason is unanswerable later, when someone asks why
// their car was not in the standings.
func TestExclusionRequiresAReason(t *testing.T) {
	h, _ := checkinFixture(t)

	code, body := postForm(t, h, "/api/entry", url.Values{
		"first_name": {"Justin"}, "last_name": {"Palmer"},
		"car_number": {"901"}, "excluded": {"true"},
	})
	if code == http.StatusOK {
		t.Fatal("an exclusion with no reason was accepted")
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "reason") {
		t.Errorf("error = %q, want it to ask for a reason", msg)
	}

	code, body = postForm(t, h, "/api/entry", url.Values{
		"first_name": {"Justin"}, "last_name": {"Palmer"}, "car_number": {"901"},
		"excluded": {"true"}, "reason": {"raced in a previous championship"},
	})
	if code != http.StatusOK {
		t.Fatalf("with a reason it should be accepted: %d %v", code, body)
	}

	entries := getJSON(t, h, "/api/entries")["entries"].([]any)
	e := entries[0].(map[string]any)
	if e["excluded"] != true {
		t.Error("the entry should be marked excluded")
	}
	if e["reason"] != "raced in a previous championship" {
		t.Errorf("reason = %v", e["reason"])
	}
}

// Removing a car once the schedule exists would leave heats pointing at
// nothing. Withdrawing after that is an exclusion.
func TestCannotRemoveAnEntryAfterTheScheduleIsBuilt(t *testing.T) {
	h, _ := checkinFixture(t)

	for i := 1; i <= 6; i++ {
		postForm(t, h, "/api/entry", url.Values{
			"first_name": {"Driver"}, "last_name": {itoa(int64(i))},
			"car_number": {itoa(int64(i * 3))},
		})
	}
	entries := getJSON(t, h, "/api/entries")["entries"].([]any)
	id := int64(entries[0].(map[string]any)["id"].(float64))

	// Before the schedule: removal works.
	if code, body := postForm(t, h, "/api/entry/delete", url.Values{"id": {itoa(id)}}); code != http.StatusOK {
		t.Fatalf("removing before scheduling should work: %d %v", code, body)
	}

	if code, body := postForm(t, h, "/api/race/schedule", url.Values{}); code != http.StatusOK {
		t.Fatalf("scheduling failed: %d %v", code, body)
	}

	entries = getJSON(t, h, "/api/entries")["entries"].([]any)
	id = int64(entries[0].(map[string]any)["id"].(float64))
	code, body := postForm(t, h, "/api/entry/delete", url.Values{"id": {itoa(id)}})
	if code == http.StatusOK {
		t.Fatal("removing a scheduled car was allowed")
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "excluded") {
		t.Errorf("error = %q; it should point at exclusion as the alternative", msg)
	}
}

// The CONTROL car is ranked but earns nothing, and is not part of the intros.
func TestControlCarIsExcludedFromTheRoster(t *testing.T) {
	h, _ := checkinFixture(t)

	postForm(t, h, "/api/entry", url.Values{
		"first_name": {"Derby"}, "last_name": {"Ales"},
		"car_number": {"1"}, "car_name": {"CONTROL"}, "is_control": {"true"},
	})
	postForm(t, h, "/api/entry", url.Values{
		"first_name": {"Ada"}, "last_name": {"Fairweather"}, "car_number": {"7"},
	})

	entries := getJSON(t, h, "/api/entries")["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	roster := getJSON(t, h, "/api/race/roster")
	if got := roster["cars"].(float64); got != 1 {
		t.Errorf("roster shows %v cars, want 1 — the pace car is not in the intros", got)
	}
}

// Camera access depends on the address the page was served from, and getting
// this wrong is the difference between check-in working and a dead camera.
func TestSecureContextDetection(t *testing.T) {
	cases := []struct {
		host string
		tls  bool
		want bool
	}{
		{host: "localhost:8080", want: true},
		{host: "127.0.0.1:8080", want: true},
		{host: "[::1]:8080", want: true},
		{host: "192.168.1.42:8080", want: false},
		{host: "race-mac.local:8080", want: false},
		{host: "192.168.1.42:8443", tls: true, want: true},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "http://"+c.host+"/checkin", nil)
		req.Host = c.host
		if c.tls {
			req.TLS = &tls.ConnectionState{}
		}
		if got := isSecureContext(req); got != c.want {
			t.Errorf("isSecureContext(%s, tls=%v) = %v, want %v", c.host, c.tls, got, c.want)
		}
	}
}
