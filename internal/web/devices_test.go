package web

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Every device other than the Mac bookmarks one page. It has to offer the four
// jobs, stay out of the coordinator's navigation, and send check-in somewhere
// the camera actually works.
func TestTheDevicesPageOffersEachJob(t *testing.T) {
	s, _ := testServer(t)
	s.https = &http.Server{} // as if the HTTPS listener were running
	h := handler(t, s)

	page := func(host string, secure bool) string {
		t.Helper()
		req := httptest.NewRequest("GET", "/devices", nil)
		req.Host = host
		if secure {
			req.TLS = &tls.ConnectionState{}
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}

	body := page("192.168.1.42:8080", false)
	for _, want := range []string{
		`id="device-checkin"`, `href="/display?scene=impound"`, `href="/vote"`, `href="/display"`,
		`target="_blank"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the devices page is missing %s", want)
		}
	}
	if strings.Contains(body, `href="/settings"`) {
		t.Error("the devices page shows the coordinator's navigation")
	}

	// A tablet on plain HTTP is sent across to HTTPS for the camera.
	if !strings.Contains(body, "https://192.168.1.42:") || !strings.Contains(body, "/checkin") {
		t.Error("check-in from a plain-HTTP tablet does not go to the HTTPS address")
	}
	// On the Mac, or already on HTTPS, it stays where it is.
	for _, c := range []struct {
		host   string
		secure bool
	}{{"localhost:8080", false}, {"192.168.1.42:8443", true}} {
		if b := page(c.host, c.secure); !strings.Contains(b, `href="/checkin"`) {
			t.Errorf("check-in from %s (secure %v) was not left on the same address", c.host, c.secure)
		}
	}
}
