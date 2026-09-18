package web

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/davisc01/derbyandales/internal/store"
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

// A car gets one championship. The check-in table should be told, and should
// not have anything decided for it.
func TestCheckingInACarThatRacedAChampionshipWarns(t *testing.T) {
	s, a := testServerPair(t)
	ctx := context.Background()

	// The club's own 2025 championship, read in the way the app reads it.
	site := t.TempDir()
	dir := filepath.Join(site, "content", "races", "2025", "championship")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join("..", "..", "testdata", "championships", "2025-standings.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "standings.csv"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.DB.SetSetting(ctx, store.KeyDerbySitePath, site); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ImportChampionships(ctx); err != nil {
		t.Fatal(err)
	}

	race, err := a.SeedDemoRace(ctx, 2027)
	if err != nil {
		t.Fatal(err)
	}

	add := func(first, last, car string, number int) map[string]any {
		t.Helper()
		form := url.Values{
			"race_id": {itoa(race.ID)}, "first_name": {first}, "last_name": {last},
			"car_name": {car}, "car_number": {itoa(int64(number))},
		}
		req := httptest.NewRequest("POST", "/api/entry", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		handler(t, s).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("checking in %s: %d %s", car, rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	// Michael Sanders won the 2025 championship with "The Pelvinator".
	out := add("Michael", "Sanders", "The Pelvinator", 501)
	warning, _ := out["warning"].(string)
	if warning == "" {
		t.Fatal("a car that won a previous championship was checked in with no warning")
	}
	for _, want := range []string{"The Pelvinator", "2025", "one championship"} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning does not mention %q: %s", want, warning)
		}
	}

	// It warns; it does not decide. The car is checked in and eligible until a
	// person says otherwise, with a reason.
	entries, _ := a.DB.Entries(ctx, race.ID)
	for _, e := range entries {
		if e.CarNumber == 501 && e.Excluded {
			t.Error("the car was excluded automatically; that is a person's decision")
		}
	}

	// A car nobody has raced before goes in quietly.
	if out := add("Ada", "Fairweather", "Brand New Thing", 502); out["warning"] != nil {
		t.Errorf("a new car was warned about: %v", out["warning"])
	}
}

// A car that has qualified is not allowed to race again before the championship.
// The person at the table is the one who can stop it, so they are told.
func TestCheckingInACarThatAlreadyQualifiedWarns(t *testing.T) {
	s, a, seasonID := seasonServer(t)
	ctx := context.Background()
	h := handler(t, s)

	slots, err := a.DB.Qualifiers(ctx, seasonID)
	if err != nil || len(slots) == 0 {
		t.Fatalf("no qualifiers in the demo season (%v)", err)
	}
	slot := slots[0]
	racer, _ := a.DB.Racer(ctx, slot.RacerID)
	live := liveRace(t, a)

	code, out := postForm(t, h, "/api/entry", url.Values{
		"race_id": {itoa(live.ID)}, "first_name": {racer.FirstName}, "last_name": {racer.LastName},
		"car_name": {strings.ToUpper(slot.CarName)}, "car_number": {"900"},
	})
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, out)
	}
	warning, _ := out["warning"].(string)
	if !strings.Contains(warning, "already qualified") {
		t.Errorf("no already-qualified warning: %q", warning)
	}
	if reason, _ := out["exclusion_reason"].(string); !strings.Contains(reason, "already qualified") {
		t.Errorf("the reason offered for marking it ineligible is %q", reason)
	}

	// A new car from the same racer is fine: they are allowed up to three.
	code, out = postForm(t, h, "/api/entry", url.Values{
		"race_id": {itoa(live.ID)}, "first_name": {racer.FirstName}, "last_name": {racer.LastName},
		"car_name": {"Brand New Build"}, "car_number": {"901"},
	})
	if code != http.StatusOK || out["warning"] != nil {
		t.Errorf("a new car was warned about: %d %v", code, out)
	}
}

// --- editing a checked-in car -------------------------------------------------

// A car is checked in at a noisy table and the details are read off a card, so
// the number, the name and the note are all routinely wrong by a character.
func TestACheckedInCarCanBeCorrected(t *testing.T) {
	h, _ := checkinFixture(t)

	postForm(t, h, "/api/entry", url.Values{
		"first_name": {"Ada"}, "last_name": {"Fairweather"},
		"car_number": {"7"}, "car_name": {"Lightening Bug"},
	})
	entries := getJSON(t, h, "/api/entries")["entries"].([]any)
	id := int64(entries[0].(map[string]any)["id"].(float64))

	code, body := postForm(t, h, "/api/entry/update", url.Values{
		"id": {itoa(id)}, "car_number": {"17"}, "car_name": {"Lightning Bug"},
		"note": {"paint still tacky"},
	})
	if code != http.StatusOK {
		t.Fatalf("edit failed: %d %v", code, body)
	}

	e := getJSON(t, h, "/api/entries")["entries"].([]any)[0].(map[string]any)
	if e["car_number"].(float64) != 17 {
		t.Errorf("car_number = %v, want 17", e["car_number"])
	}
	if e["car_name"] != "Lightning Bug" {
		t.Errorf("car_name = %v", e["car_name"])
	}
	if e["note"] != "paint still tacky" {
		t.Errorf("note = %v", e["note"])
	}
	// The driver was not part of the edit, so it must be untouched.
	if e["driver"] != "Ada Fairweather" {
		t.Errorf("driver = %v, want it unchanged", e["driver"])
	}
}

// Car number 7 twice is the one clash that matters, and the edit path has to
// say so as readably as check-in does.
func TestEditingOntoATakenCarNumberIsRefusedReadably(t *testing.T) {
	h, _ := checkinFixture(t)

	postForm(t, h, "/api/entry", url.Values{
		"first_name": {"Ada"}, "last_name": {"Fairweather"}, "car_number": {"7"},
	})
	postForm(t, h, "/api/entry", url.Values{
		"first_name": {"Greg"}, "last_name": {"Thrift"}, "car_number": {"9"},
	})
	entries := getJSON(t, h, "/api/entries")["entries"].([]any)
	nine := int64(entries[1].(map[string]any)["id"].(float64))

	code, body := postForm(t, h, "/api/entry/update", url.Values{
		"id": {itoa(nine)}, "car_number": {"7"},
	})
	if code == http.StatusOK {
		t.Fatal("two cars were allowed the same number")
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "Car number 7") {
		t.Errorf("error = %q, want it to name the car number", msg)
	}
	if strings.Contains(msg, "UNIQUE") || strings.Contains(msg, "idx_") {
		t.Errorf("error = %q, which is a database message", msg)
	}
}

// A car handed in under the wrong name is a different correction from a
// misspelled one: this moves one car, and leaves that person's other car and
// the other driver's season alone.
func TestACarCanBeMovedToADifferentDriver(t *testing.T) {
	h, _ := checkinFixture(t)

	postForm(t, h, "/api/entry", url.Values{
		"first_name": {"Chris"}, "last_name": {"Bryan"},
		"car_number": {"7"}, "car_name": {"Loose Moose"},
	})
	postForm(t, h, "/api/entry", url.Values{
		"first_name": {"Chris"}, "last_name": {"Bryan"},
		"car_number": {"8"}, "car_name": {"Drive-By"},
	})
	entries := getJSON(t, h, "/api/entries")["entries"].([]any)
	seven := int64(entries[0].(map[string]any)["id"].(float64))

	code, body := postForm(t, h, "/api/entry/update", url.Values{
		"id": {itoa(seven)}, "first_name": {"Greg"}, "last_name": {"Thrift"},
	})
	if code != http.StatusOK {
		t.Fatalf("moving the car failed: %d %v", code, body)
	}

	entries = getJSON(t, h, "/api/entries")["entries"].([]any)
	moved := entries[0].(map[string]any)
	if moved["driver"] != "Greg Thrift" {
		t.Errorf("car 7 driver = %v, want Greg Thrift", moved["driver"])
	}
	if moved["car_name"] != "Loose Moose" {
		t.Errorf("the car itself changed: %v", moved["car_name"])
	}
	// The other car is the point: a rename would have taken it too.
	if other := entries[1].(map[string]any); other["driver"] != "Chris Bryan" {
		t.Errorf("car 8 driver = %v, want Chris Bryan left alone", other["driver"])
	}

	racers := getJSON(t, h, "/api/racers")["racers"].([]any)
	if len(racers) != 2 {
		t.Errorf("got %d racers, want both Chris Bryan and Greg Thrift", len(racers))
	}
}

// An exclusion still has to be answerable weeks later, whichever form it was
// entered on.
func TestEditingIntoAnExclusionStillNeedsAReason(t *testing.T) {
	h, _ := checkinFixture(t)

	postForm(t, h, "/api/entry", url.Values{
		"first_name": {"Ada"}, "last_name": {"Fairweather"}, "car_number": {"7"},
	})
	id := int64(getJSON(t, h, "/api/entries")["entries"].([]any)[0].(map[string]any)["id"].(float64))

	code, body := postForm(t, h, "/api/entry/update", url.Values{
		"id": {itoa(id)}, "excluded": {"true"},
	})
	if code == http.StatusOK {
		t.Fatal("an exclusion with no reason was accepted on the edit form")
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "reason") {
		t.Errorf("error = %q, want it to ask for a reason", msg)
	}

	// And clearing the exclusion clears the reason with it, so a car does not
	// keep a stale explanation attached to it.
	postForm(t, h, "/api/entry/update", url.Values{
		"id": {itoa(id)}, "excluded": {"true"}, "reason": {"wrong scale"},
	})
	postForm(t, h, "/api/entry/update", url.Values{
		"id": {itoa(id)}, "excluded": {"false"},
	})
	e := getJSON(t, h, "/api/entries")["entries"].([]any)[0].(map[string]any)
	if e["excluded"] != false || e["reason"] != "" {
		t.Errorf("excluded = %v, reason = %q after clearing", e["excluded"], e["reason"])
	}
}

// Removing a car stops once the schedule is built; correcting one must not.
// A car number read wrong is discovered when the trays are loaded, which is
// well after check-in has closed.
func TestACarCanStillBeCorrectedAfterTheScheduleIsBuilt(t *testing.T) {
	h, _ := checkinFixture(t)

	for i := 1; i <= 6; i++ {
		postForm(t, h, "/api/entry", url.Values{
			"first_name": {"Driver"}, "last_name": {itoa(int64(i))},
			"car_number": {itoa(int64(i * 3))},
		})
	}
	if code, body := postForm(t, h, "/api/race/schedule", url.Values{}); code != http.StatusOK {
		t.Fatalf("scheduling failed: %d %v", code, body)
	}

	id := int64(getJSON(t, h, "/api/entries")["entries"].([]any)[0].(map[string]any)["id"].(float64))
	code, body := postForm(t, h, "/api/entry/update", url.Values{
		"id": {itoa(id)}, "car_name": {"Loose Moose"},
	})
	if code != http.StatusOK {
		t.Fatalf("correcting a scheduled car was refused: %d %v", code, body)
	}
	if e := getJSON(t, h, "/api/entries")["entries"].([]any)[0].(map[string]any); e["car_name"] != "Loose Moose" {
		t.Errorf("car_name = %v", e["car_name"])
	}
}

// A car number has to be a car number. Sending rubbish used to be ignored,
// which looked like the edit had been saved.
func TestEditingRefusesACarNumberThatIsNotOne(t *testing.T) {
	h, _ := checkinFixture(t)
	postForm(t, h, "/api/entry", url.Values{
		"first_name": {"Ada"}, "last_name": {"Fairweather"}, "car_number": {"7"},
	})
	id := int64(getJSON(t, h, "/api/entries")["entries"].([]any)[0].(map[string]any)["id"].(float64))

	for _, bad := range []string{"", "0", "-3", "seven"} {
		code, body := postForm(t, h, "/api/entry/update", url.Values{
			"id": {itoa(id)}, "car_number": {bad},
		})
		if code == http.StatusOK {
			t.Errorf("car_number %q was accepted", bad)
		}
		if msg, _ := body["error"].(string); !strings.Contains(msg, "number") {
			t.Errorf("car_number %q: error = %q", bad, msg)
		}
	}
}

// Handing a car to a different person once the race is recorded would credit
// the wrong racer with frozen points and leave no sign of it. A misspelled
// name is a rename on the season page; this is not that.
func TestTheDriverIsFixedOnceTheRaceIsRecorded(t *testing.T) {
	s, a, seasonID := seasonServer(t)
	h := handler(t, s)
	ctx := context.Background()

	races, err := a.DB.Races(ctx, seasonID)
	if err != nil {
		t.Fatal(err)
	}
	var frozen int64
	for _, r := range races {
		if done, _ := a.DB.RaceFrozen(ctx, r.ID); done {
			frozen = r.ID
			break
		}
	}
	if frozen == 0 {
		t.Fatal("the demo season has no recorded race to test against")
	}
	entries, err := a.DB.Entries(ctx, frozen)
	if err != nil || len(entries) == 0 {
		t.Fatalf("entries: %v", err)
	}
	e := entries[0]

	code, body := postForm(t, h, "/api/entry/update", url.Values{
		"id": {itoa(e.ID)}, "first_name": {"Somebody"}, "last_name": {"Else"},
	})
	if code == http.StatusOK {
		t.Fatal("the driver of a recorded race was changed")
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, e.FullName()) {
		t.Errorf("error = %q, want it to name who the points belong to", msg)
	}
	if !strings.Contains(msg, "season page") {
		t.Errorf("error = %q, want it to point at the rename that does work", msg)
	}

	// Everything else about the car is still correctable: a car name spelled
	// wrong is still spelled wrong after the race.
	if code, body := postForm(t, h, "/api/entry/update", url.Values{
		"id": {itoa(e.ID)}, "car_name": {"Loose Moose"},
	}); code != http.StatusOK {
		t.Fatalf("correcting the car name on a recorded race was refused: %d %v", code, body)
	}
	after, _ := a.DB.Entry(ctx, e.ID)
	if after.CarName != "Loose Moose" {
		t.Errorf("car_name = %q", after.CarName)
	}
	if after.RacerID != e.RacerID {
		t.Error("the driver moved anyway")
	}
}

// A photo of the wrong car is worse than no photo: the impound official loads
// trays by picture. Removing one has to be possible.
func TestAPhotoCanBeReplacedAndRemoved(t *testing.T) {
	h, _ := checkinFixture(t)

	postForm(t, h, "/api/entry", url.Values{
		"first_name": {"Ada"}, "last_name": {"Fairweather"}, "car_number": {"7"},
	})
	id := int64(getJSON(t, h, "/api/entries")["entries"].([]any)[0].(map[string]any)["id"].(float64))

	req := httptest.NewRequest("POST", "/api/photo", bytes.NewReader(testPNG(t)))
	req.Header.Set("Content-Type", "image/png")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	var photo map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &photo)

	if code, body := postForm(t, h, "/api/entry/update", url.Values{
		"id": {itoa(id)}, "photo_id": {itoa(int64(photo["id"].(float64)))},
	}); code != http.StatusOK {
		t.Fatalf("attaching the photo failed: %d %v", code, body)
	}
	if e := getJSON(t, h, "/api/entries")["entries"].([]any)[0].(map[string]any); e["photo_id"] == nil {
		t.Fatal("the photo was not attached")
	}

	// An empty photo_id is the edit form saying "no photo".
	if code, body := postForm(t, h, "/api/entry/update", url.Values{
		"id": {itoa(id)}, "photo_id": {""},
	}); code != http.StatusOK {
		t.Fatalf("removing the photo failed: %d %v", code, body)
	}
	if e := getJSON(t, h, "/api/entries")["entries"].([]any)[0].(map[string]any); e["photo_id"] != nil {
		t.Errorf("photo_id = %v, want it gone", e["photo_id"])
	}
}

// testPNG is a real image, because SavePhoto decodes what it is given.
func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The roster is the part of the page nobody sees until race night, when it is
// thirty rows long and somebody is looking for one car.
func TestTheRosterOffersEditingAndFiltering(t *testing.T) {
	s, _ := testServer(t)
	h := handler(t, s)

	code, body := postForm(t, h, "/api/season", url.Values{"year": {"2027"}})
	if code != http.StatusOK {
		t.Fatalf("create season: %d %v", code, body)
	}
	code, body = postForm(t, h, "/api/race", url.Values{
		"season_id": {itoa(int64(body["ID"].(float64)))}, "number": {"1"},
	})
	if code != http.StatusOK {
		t.Fatalf("create race: %d %v", code, body)
	}
	postForm(t, h, "/api/entry", url.Values{
		"first_name": {"Ada"}, "last_name": {"Fairweather"},
		"car_number": {"7"}, "car_name": {"Lightning Bug"},
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/checkin", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	page := rec.Body.String()
	for _, want := range []string{
		"roster-search", "roster-filter", "roster-sort",
		"edit-entry", "remove-entry", "edit-form",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the roster has no %s", want)
		}
	}
	// The row carries what the filtering and sorting read.
	for _, want := range []string{`data-number="7"`, `data-car="Lightning Bug"`,
		`data-driver="Ada Fairweather"`, `data-photo="0"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the row is missing %s", want)
		}
	}
}
