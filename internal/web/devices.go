package web

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// The devices page: the one address every other device bookmarks.
//
// A race night uses several devices besides the Mac — a tablet at the check-in
// table, a screen at the impound table, the voting tablet, perhaps the main TV
// — and each used to need its own address typed in, one of them on a different
// port. Now each is given this page once, and a button opens what it is for.

func (s *Server) devicesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /devices", s.handleDevices)
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	checkin, secure := s.checkinURL(r)
	race := ""
	if id := s.app.Race.CurrentRaceID(); id != 0 {
		if rc, err := s.app.DB.Race(r.Context(), id); err == nil {
			race = rc.Name
		}
	}
	s.render(w, r, "devices.html", pageData{
		Title:  "Devices",
		Active: "devices",
		NoNav:  true,
		Data: map[string]any{
			"CheckinURL":    checkin,
			"CheckinSecure": secure,
			"Race":          race,
		},
	})
}

// checkinURL is where the check-in button sends this device, and whether the
// camera will work there.
//
// Browsers only allow the camera on a secure address. The Mac itself counts as
// one on localhost, and so does anything already on HTTPS — but a tablet that
// opened this page over plain HTTP has to be sent across to the HTTPS port, or
// it gets a check-in page with no camera and nobody knows why.
func (s *Server) checkinURL(r *http.Request) (string, bool) {
	if r.TLS != nil {
		return "/checkin", true
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		host = h
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return "/checkin", true
	}
	if s.https == nil {
		return "/checkin", false
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("https://%s:%d/checkin", host, s.app.HTTPSPort), true
}
