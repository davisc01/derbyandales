package web

import (
	"net/http"
	"strconv"

	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/publish"
)

// The publish screen: show exactly what would be written, then write it.
//
// The app never runs git. What it produces is a working tree somebody looks at
// and commits, so the diff here is not a courtesy — it is the only review step
// between a race result and the club's website.

func (s *Server) publishRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /publish", s.handlePublishPage)
	mux.HandleFunc("POST /api/publish", s.handlePublish)
}

func (s *Server) handlePublishPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := map[string]any{}

	seasonID := s.seasonIDParam(r)
	if seasonID == 0 {
		s.render(w, r, "publish.html", pageData{Title: "Publish", Active: "publish", Data: data})
		return
	}
	sn, err := s.app.DB.Season(ctx, seasonID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data["Season"] = sn

	if path, err := s.app.Publish.SitePath(ctx); err != nil {
		data["SiteError"] = err.Error()
	} else {
		data["SitePath"] = path
	}

	races, err := s.app.DB.Races(ctx, seasonID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data["Races"] = races

	// What to preview: a race if one was asked for, otherwise the season.
	what := r.URL.Query().Get("what")
	raceID, _ := strconv.ParseInt(r.URL.Query().Get("race_id"), 10, 64)
	if what == "" && raceID != 0 {
		what = "race"
	}
	data["What"] = what
	data["RaceID"] = raceID

	if data["SiteError"] == nil && what != "" {
		var plan publish.Plan
		var planErr error
		if what == "season" {
			plan, planErr = s.app.Publish.PlanSeason(ctx, seasonID)
		} else {
			plan, planErr = s.app.Publish.PlanRace(ctx, raceID)
		}
		if planErr != nil {
			data["PlanError"] = planErr.Error()
		} else {
			data["Plan"] = plan
		}
	}

	s.render(w, r, "publish.html", pageData{Title: "Publish", Active: "publish", Data: data})
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx := r.Context()
	seasonID := s.seasonIDForm(r)

	var plan publish.Plan
	var err error
	switch r.FormValue("what") {
	case "season":
		plan, err = s.app.Publish.PlanSeason(ctx, seasonID)
	case "race":
		raceID, parseErr := strconv.ParseInt(r.FormValue("race_id"), 10, 64)
		if parseErr != nil || raceID == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which race?"})
			return
		}
		plan, err = s.app.Publish.PlanRace(ctx, raceID)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "publish what?"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	written, err := s.app.Publish.Apply(ctx, plan, "coordinator")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"written": written,
		"count":   len(written),
	})
}

// publishable reports whether a race has anything worth publishing yet, so the
// picker does not offer races that have not been run.
func publishable(r model.Race) bool {
	return r.Status == model.StatusRacing || r.Status == model.StatusVoting ||
		r.Status == model.StatusComplete
}
