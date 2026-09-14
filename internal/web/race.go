package web

import (
	"net/http"
	"strconv"

	"github.com/davisc01/derbyandales/internal/schedule"
	"github.com/davisc01/derbyandales/internal/store"
)

func (s *Server) raceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /race", s.handleRacePage)

	mux.HandleFunc("POST /api/race/load", s.handleLoadRace)
	mux.HandleFunc("POST /api/race/schedule", s.handleGenerateSchedule)
	mux.HandleFunc("POST /api/race/start", s.handleStartRace)
	mux.HandleFunc("POST /api/race/stop", s.handleStopRace)
	mux.HandleFunc("POST /api/race/next", s.handleNextHeat)
	mux.HandleFunc("POST /api/race/rerun", s.handleReRunHeat)
	mux.HandleFunc("POST /api/race/auto", s.handleAutoAdvance)
	mux.HandleFunc("POST /api/race/runoff", s.handleRunOff)
}

func (s *Server) handleRacePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	races, _ := s.allRaces(r)
	state := s.app.Race.State(ctx)

	var heats []store.HeatView
	var anomalies []store.HeatAnomalyView
	var ties []store.TieView
	if state.RaceID != 0 {
		heats, _ = s.app.DB.Heats(ctx, state.RaceID)
		// A tie for a trophy is settled on the track, so it belongs on the race
		// screen rather than buried in the awards page.
		ties, _ = s.app.DB.UnsettledTies(ctx, state.RaceID)
		// Only once every heat has been run. Before that, a car's "slowest run
		// of the night" is the slowest of however few it has had so far, and
		// the check would point at the early heats every time.
		if allHeatsRun(heats) {
			anomalies, _ = s.app.DB.HeatAnomalies(ctx, state.RaceID)
		}
	}

	s.render(w, r, "race.html", pageData{
		Title:  "Race",
		Active: "race",
		Data: map[string]any{
			"State":     state,
			"Races":     races,
			"Heats":     heats,
			"Anomalies": anomalies,
			"Ties":      ties,
			"Timer":     s.app.Timer.Status(),
		},
	})
}

func (s *Server) handleGenerateSchedule(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	raceID := s.raceIDForm(r)
	if raceID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which race?"})
		return
	}

	sched, err := s.app.Race.GenerateSchedule(r.Context(), raceID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// The schedule's quality is worth saying out loud. For the club's field
	// sizes it is always perfect; for a small turnout it may not be, and the
	// coordinator should hear that from the software rather than from a racer.
	resp := map[string]any{
		"heats":        len(sched.Heats),
		"cars":         sched.Cars,
		"runs_per_car": sched.RunsPerCar(),
		"perfect":      sched.Perfect(),
		"back_to_back": schedule.BackToBack(sched),
	}
	if !sched.Perfect() {
		resp["note"] = "With this many cars, some pairs will race each other more than once."
	}
	if schedule.BackToBack(sched) > 0 {
		resp["warning"] = "With this many cars, some racers will run in back-to-back heats. " +
			"That is unavoidable at this field size, not a scheduling fault."
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleStartRace(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	raceID := s.raceIDForm(r)
	if raceID == 0 {
		raceID = s.app.Race.CurrentRaceID()
	}
	if err := s.app.Race.Start(r.Context(), raceID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.app.Race.State(r.Context()))
}

func (s *Server) handleStopRace(w http.ResponseWriter, r *http.Request) {
	s.app.Race.Stop()
	writeJSON(w, http.StatusOK, s.app.Race.State(r.Context()))
}

func (s *Server) handleNextHeat(w http.ResponseWriter, r *http.Request) {
	if err := s.app.Race.ArmNext(r.Context()); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.app.Race.State(r.Context()))
}

func (s *Server) handleReRunHeat(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	heatID, err := strconv.ParseInt(r.Form.Get("heat_id"), 10, 64)
	if err != nil || heatID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which heat?"})
		return
	}
	if err := s.app.Race.ReRun(r.Context(), heatID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.app.Race.State(r.Context()))
}

func (s *Server) handleAutoAdvance(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	s.app.Race.SetAutoAdvance(r.Form.Get("on") == "true")
	writeJSON(w, http.StatusOK, s.app.Race.State(r.Context()))
}

// raceIDForm reads a race id from a form, falling back to the loaded race.
func (s *Server) raceIDForm(r *http.Request) int64 {
	if v := r.Form.Get("race_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			return id
		}
	}
	return s.app.Race.CurrentRaceID()
}

// allHeatsRun reports whether the schedule is finished.
func allHeatsRun(heats []store.HeatView) bool {
	if len(heats) == 0 {
		return false
	}
	for _, h := range heats {
		if !h.Complete() {
			return false
		}
	}
	return true
}

// handleRunOff arms the heat that settles a tie for a trophy.
func (s *Server) handleRunOff(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	raceID := s.raceIDForm(r)
	place, err := strconv.Atoi(r.Form.Get("place"))
	if err != nil || place <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a tie for which place?"})
		return
	}
	if err := s.app.Race.ArmRunOff(r.Context(), raceID, place); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.app.Race.State(r.Context()))
}
