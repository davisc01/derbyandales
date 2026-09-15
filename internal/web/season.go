package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/davisc01/derbyandales/internal/app"
)

// The season screen: wildcard points, the auto-qualifier list, and the two
// corrections a coordinator makes to them.
//
// All of this used to be a second application with its own database, kept in
// step by exporting a CSV from one and uploading it to the other. There is
// nothing to keep in step now, so the only thing this page has to do is show
// the numbers and let somebody fix them.

func (s *Server) seasonRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /season", s.handleSeasonPage)

	mux.HandleFunc("POST /api/season/recompute", s.handleRecompute)
	mux.HandleFunc("POST /api/season/settings", s.handleSeasonSettings)
	mux.HandleFunc("POST /api/racer/rename", s.handleRenameRacer)
	mux.HandleFunc("POST /api/racer/merge", s.handleMergeRacers)
	mux.HandleFunc("POST /api/season/adjust", s.handleAdjust)
	mux.HandleFunc("POST /api/season/adjust/remove", s.handleRemoveAdjustment)

}

func (s *Server) handleSeasonPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	seasonID := s.seasonIDParam(r)
	if seasonID == 0 {
		s.render(w, r, "season.html", pageData{
			Title:  "Season",
			Active: "season",
			Data:   map[string]any{},
		})
		return
	}

	overview, err := s.app.Season.Season(ctx, seasonID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	seasons, _ := s.app.DB.Seasons(ctx)
	// Not DB.Racers: that would offer the pace car's driver as somebody whose
	// points could be adjusted.
	racers, _ := s.app.DB.CompetingRacers(ctx, seasonID)

	s.render(w, r, "season.html", pageData{
		Title:  "Season",
		Active: "season",
		Data: map[string]any{
			"Overview": overview,
			"Seasons":  seasons,
			"Racers":   racers,
		},
	})
}

// seasonIDParam resolves which season the page is about: the one asked for,
// otherwise the one the application is working on.
func (s *Server) seasonIDParam(r *http.Request) int64 {
	if v := r.URL.Query().Get("season_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			return id
		}
	}
	id, err := s.app.Season.CurrentSeasonID(r.Context())
	if err != nil {
		return 0
	}
	return id
}

// seasonIDForm resolves the season from a posted form, falling back the same way.
func (s *Server) seasonIDForm(r *http.Request) int64 {
	if v := r.FormValue("season_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			return id
		}
	}
	id, _ := s.app.Season.CurrentSeasonID(r.Context())
	return id
}

func (s *Server) handleRecompute(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	seasonID := s.seasonIDForm(r)
	if seasonID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which season?"})
		return
	}

	n, err := s.app.Season.Recompute(r.Context(), seasonID, "coordinator")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"races": n})
}

func (s *Server) handleAdjust(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	seasonID := s.seasonIDForm(r)
	racerID, _ := strconv.ParseInt(r.FormValue("racer_id"), 10, 64)
	points, err := strconv.Atoi(r.FormValue("points"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Points must be a whole number, positive or negative.",
		})
		return
	}
	if racerID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Choose a racer."})
		return
	}

	if err := s.app.Season.Adjust(r.Context(), seasonID, racerID, points,
		r.FormValue("reason"), "coordinator"); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRemoveAdjustment(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	seasonID := s.seasonIDForm(r)
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err := s.app.Season.RemoveAdjustment(r.Context(), seasonID, id, "coordinator"); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSeasonSettings saves the season's shape. It reports what it did not do
// as plainly as what it did: finished races keep their points until somebody
// recomputes, and a bracket already built is not rebuilt.
func (s *Server) handleSeasonSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	seasonID := s.seasonIDForm(r)
	if seasonID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which season?"})
		return
	}
	num := func(key string) int {
		n, _ := strconv.Atoi(strings.TrimSpace(r.FormValue(key)))
		return n
	}
	track, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("track_length_ft")), 64)
	change, err := s.app.Season.UpdateSettings(r.Context(), seasonID, app.SeasonSettings{
		Name:                 r.FormValue("name"),
		TrackLengthFt:        track,
		RaceCount:            num("race_count"),
		AutoQualPlaces:       num("auto_qual_places"),
		WildcardSpots:        num("wildcard_spots"),
		MaxChampionshipEntry: num("max_championship_entry"),
		PointsCountControl:   r.FormValue("points_count_control") == "on" || r.FormValue("points_count_control") == "true",
		BracketLaneA:         num("bracket_lane_a"),
		BracketLaneB:         num("bracket_lane_b"),
	}, "coordinator")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"changed":         change.Changed,
		"needs_recompute": change.NeedsRecompute,
		"bracket_built":   change.BracketBuilt,
	})
}

func (s *Server) handleRenameRacer(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("racer_id"), 10, 64)
	if id == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which racer?"})
		return
	}
	if err := s.app.Season.RenameRacer(r.Context(), id, r.FormValue("first"), r.FormValue("last"), "coordinator"); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"renamed": true})
}

func (s *Server) handleMergeRacers(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	keep, _ := strconv.ParseInt(r.FormValue("keep_id"), 10, 64)
	drop, _ := strconv.ParseInt(r.FormValue("drop_id"), 10, 64)
	if keep == 0 || drop == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "choose both racers"})
		return
	}
	shared, err := s.app.Season.MergeRacers(r.Context(), keep, drop, "coordinator")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shared_races": shared})
}
