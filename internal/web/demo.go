package web

import (
	"fmt"
	"net/http"
)

// The demo data, from the settings page.
//
// Demo data exists so a race night can be rehearsed — and so a change like a
// new screen can be seen working — without twenty real cars and a timer. It
// used to take a command-line flag, and that flag only seeds when there is no
// demo data yet, so the way to start over was to go and delete the database.
// These two buttons are that, without the terminal.

func (s *Server) demoRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/demo/race", s.handleCreateDemoRace)
	mux.HandleFunc("POST /api/demo/clear", s.handleClearDemoData)
}

// handleCreateDemoRace makes a demo race with the invented field checked in.
func (s *Server) handleCreateDemoRace(w http.ResponseWriter, r *http.Request) {
	race, err := s.app.AddDemoRace(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	entries, _ := s.app.DB.Entries(r.Context(), race.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"race":    race.Name,
		"race_id": race.ID,
		"cars":    len(entries),
	})
}

// handleClearDemoData deletes the demo season and everything raced in it.
func (s *Server) handleClearDemoData(w http.ResponseWriter, r *http.Request) {
	removed, err := s.app.ClearDemoData(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"season": removed.Season,
		"races":  removed.Races,
		"detail": fmt.Sprintf("%s and %d race%s deleted",
			removed.Season, removed.Races, plural(removed.Races)),
	})
}
