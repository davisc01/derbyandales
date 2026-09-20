package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// getPage fetches a rendered page, failing the test if it did not render.
func getPage(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, body: %s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// The demo buttons on the settings page. The page is where somebody goes to
// start a rehearsal, so it has to say what is there and what deleting it takes.

func TestTheSettingsPageOffersADemoRace(t *testing.T) {
	s := mustServer(t)
	h := handler(t, s)

	body := getPage(t, h, "/settings")
	if !strings.Contains(body, "Create a demo race") {
		t.Error("no button to create a demo race")
	}
	if !strings.Contains(body, "No demo data") {
		t.Error("an empty database should say there is no demo data")
	}

	status, out := postForm(t, h, "/api/demo/race", url.Values{})
	if status != 200 {
		t.Fatalf("status = %d, body %v", status, out)
	}
	if out["race"] != "Race 1" {
		t.Errorf("race = %v, want Race 1", out["race"])
	}
	if cars, _ := out["cars"].(float64); cars < 2 {
		t.Errorf("cars = %v, want a field", out["cars"])
	}

	body = getPage(t, h, "/settings")
	if !strings.Contains(body, "demo data)") {
		t.Error("the page does not name the demo season it now has")
	}
	if strings.Contains(body, "No demo data") {
		t.Error("the page still says there is no demo data")
	}
}

// The delete button is offered only when there is something to delete, or it is
// a button that does nothing on most nights — which is a button people press.
func TestTheDeleteButtonIsDisabledWithoutDemoData(t *testing.T) {
	s := mustServer(t)
	h := handler(t, s)

	body := getPage(t, h, "/settings")
	i := strings.Index(body, `id="demo-clear"`)
	if i < 0 {
		t.Fatal("no delete button on the settings page")
	}
	if !strings.Contains(body[i:i+120], "disabled") {
		t.Error("the delete button is offered with no demo data to delete")
	}

	if status, out := postForm(t, h, "/api/demo/race", url.Values{}); status != 200 {
		t.Fatalf("create: %d %v", status, out)
	}
	body = getPage(t, h, "/settings")
	i = strings.Index(body, `id="demo-clear"`)
	if strings.Contains(body[i:i+120], "disabled") {
		t.Error("the delete button is still disabled with demo data on hand")
	}
}

func TestDeletingDemoDataReportsWhatWent(t *testing.T) {
	s := mustServer(t)
	h := handler(t, s)

	if status, out := postForm(t, h, "/api/demo/race", url.Values{}); status != 200 {
		t.Fatalf("create: %d %v", status, out)
	}
	status, out := postForm(t, h, "/api/demo/clear", url.Values{})
	if status != 200 {
		t.Fatalf("status = %d, body %v", status, out)
	}
	detail, _ := out["detail"].(string)
	if !strings.Contains(detail, "1 race") {
		t.Errorf("detail = %q, want it to say what was deleted", detail)
	}

	// And the error, when there is nothing left, is a sentence rather than a
	// constraint name.
	status, out = postForm(t, h, "/api/demo/clear", url.Values{})
	if status == 200 {
		t.Fatal("clearing twice should say there is nothing to clear")
	}
	if msg, _ := out["error"].(string); msg != "there is no demo data to clear" {
		t.Errorf("error = %q", msg)
	}
}
