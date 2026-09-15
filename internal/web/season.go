package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/davisc01/derbyandales/internal/app"
	"github.com/davisc01/derbyandales/internal/season"
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
	mux.HandleFunc("POST /api/season/adjust", s.handleAdjust)
	mux.HandleFunc("POST /api/season/adjust/remove", s.handleRemoveAdjustment)

	mux.HandleFunc("GET /api/season/substitutes", s.handleSubstituteCandidates)
	mux.HandleFunc("POST /api/season/substitute", s.handleSubstitute)
	mux.HandleFunc("POST /api/season/substitute/undo", s.handleUndoSubstitution)
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

// handleSubstituteCandidates lists who could take a given slot. The slot names
// the race, so the caller does not have to know or send it.
func (s *Server) handleSubstituteCandidates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	seasonID := s.seasonIDParam(r)
	entryID, _ := strconv.ParseInt(r.URL.Query().Get("entry_id"), 10, 64)

	slots, err := s.app.DB.Qualifiers(ctx, seasonID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var slot *season.Slot
	for i := range slots {
		if slots[i].EntryID == entryID {
			slot = &slots[i]
		}
	}
	if slot == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "That car does not hold a qualifying slot.",
		})
		return
	}
	// The same refusal the substitution itself makes. Offering a list of
	// candidates for a slot that cannot be given away would be an invitation to
	// a dead end.
	if !slot.OverLimit {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": slot.Driver + " is not over the entry limit, so this slot does not need substituting.",
		})
		return
	}

	candidates, err := s.app.DB.SubstituteCandidates(ctx, seasonID, slot.RaceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// How many slots each candidate already holds.
	//
	// Nothing stops a coordinator handing the slot to somebody who is already
	// at the limit, and nothing should — on a thin night they may be the only
	// person left. But it would immediately create the problem this screen
	// exists to solve, so say so rather than let it be discovered later.
	held := map[int64]int{}
	limit := 0
	if rules, err := s.app.DB.Rules(ctx, seasonID); err == nil {
		limit = rules.MaxEntries
	}
	for _, sl := range slots {
		held[sl.RacerID]++
	}

	out := make([]map[string]any, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, map[string]any{
			"entry_id": c.EntryID,
			"driver":   c.Driver,
			"car_name": c.CarName,
			"place":    c.Place,
			"average":  c.Average,
			"slots":    held[c.RacerID],
			"at_cap":   limit > 0 && held[c.RacerID] >= limit,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"race":       slot.RaceNumber,
		"driver":     slot.Driver,
		"car_name":   slot.CarName,
		"candidates": out,
	})
}

func (s *Server) handleSubstitute(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	seasonID := s.seasonIDForm(r)
	original, _ := strconv.ParseInt(r.FormValue("original_entry_id"), 10, 64)
	substitute, _ := strconv.ParseInt(r.FormValue("substitute_entry_id"), 10, 64)

	if err := s.app.Season.Substitute(r.Context(), seasonID, original, substitute, "coordinator"); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleUndoSubstitution(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	seasonID := s.seasonIDForm(r)
	original, _ := strconv.ParseInt(r.FormValue("original_entry_id"), 10, 64)

	if err := s.app.Season.UndoSubstitution(r.Context(), seasonID, original, "coordinator"); err != nil {
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
