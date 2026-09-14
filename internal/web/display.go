package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/davisc01/derbyandales/internal/bus"
	"github.com/davisc01/derbyandales/internal/scoring"
	"github.com/davisc01/derbyandales/internal/store"
)

func (s *Server) displayRoutes(mux *http.ServeMux) {
	// The screen itself. One page; the scene is swapped in place rather than
	// by reloading, so a TV never shows a white flash mid-race.
	mux.HandleFunc("GET /display", s.handleDisplay)

	mux.HandleFunc("POST /api/display/register", s.handleDisplayRegister)
	mux.HandleFunc("POST /api/display/heartbeat", s.handleDisplayHeartbeat)
	mux.HandleFunc("GET /api/display/scene", s.handleDisplayScene)

	// Coordinator side.
	mux.HandleFunc("GET /displays", s.handleDisplays)
	mux.HandleFunc("POST /api/displays/scene", s.handleSetScene)
	mux.HandleFunc("POST /api/displays/rename", s.handleRenameDisplay)
	mux.HandleFunc("POST /api/displays/forget", s.handleForgetDisplay)

	// Scene data.
	mux.HandleFunc("GET /api/race/state", s.handleRaceState)
	_ = jsonParams // reserved for scene parameters
	mux.HandleFunc("GET /api/race/roster", s.handleRoster)
	mux.HandleFunc("GET /api/race/standings", s.handleStandings)
}

// handleDisplay serves the display shell.
func (s *Server) handleDisplay(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "display.html", pageData{
		Title:  "Display",
		Active: "display",
		Data:   map[string]any{},
	})
}

func (s *Server) handleDisplayRegister(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	d, err := s.app.DB.RegisterDisplay(r.Context(), r.Form.Get("token"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.app.Bus.Publish(bus.TopicDisplay, "registered", map[string]any{
		"id": d.ID, "name": d.Name,
	})
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleDisplayHeartbeat(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	d, err := s.app.DB.DisplayByToken(r.Context(), r.Form.Get("token"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown display"})
		return
	}
	_ = s.app.DB.TouchDisplay(r.Context(), d.ID)
	writeJSON(w, http.StatusOK, map[string]any{"scene": d.Page, "params": d.Params})
}

// handleDisplayScene tells one display what it should be showing. Used on
// first load and after a reconnect, so a screen that dropped out comes back to
// the right scene rather than to a blank one.
func (s *Server) handleDisplayScene(w http.ResponseWriter, r *http.Request) {
	d, err := s.app.DB.DisplayByToken(r.Context(), r.URL.Query().Get("token"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown display"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": d.ID, "name": d.Name, "scene": d.Page, "params": d.Params,
	})
}

// --- coordinator -------------------------------------------------------------

func (s *Server) handleDisplays(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	displays, err := s.app.DB.Displays(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	races, _ := s.allRaces(r)

	s.render(w, r, "displays.html", pageData{
		Title:  "Displays",
		Active: "displays",
		Data: map[string]any{
			"Displays": displays,
			"Scenes":   store.Scenes(),
			"Online":   store.DisplayOnlineWindow,
			"Races":    races,
			"RaceID":   s.app.Race.CurrentRaceID(),
			"URLs":     s.app.URLs(),
		},
	})
}

func (s *Server) handleSetScene(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	id, err := strconv.ParseInt(r.Form.Get("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which display?"})
		return
	}
	scene := store.Scene(r.Form.Get("scene"))
	params := r.Form.Get("params")

	if err := s.app.DB.SetDisplayScene(r.Context(), id, scene, params); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// The change reaches the screen over SSE, so there is nothing to poll and
	// no reload: a TV switches scene within a frame or two.
	s.app.Bus.Publish(bus.TopicDisplay, "scene", map[string]any{
		"id": id, "scene": string(scene), "params": params,
	})
	writeJSON(w, http.StatusOK, map[string]string{"scene": string(scene)})
}

func (s *Server) handleRenameDisplay(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	id, _ := strconv.ParseInt(r.Form.Get("id"), 10, 64)
	if err := s.app.DB.RenameDisplay(r.Context(), id, r.Form.Get("name")); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.app.Bus.Publish(bus.TopicDisplay, "renamed", map[string]any{"id": id})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleForgetDisplay(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	id, _ := strconv.ParseInt(r.Form.Get("id"), 10, 64)
	if err := s.app.DB.ForgetDisplay(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleLoadRace points the displays at a race without starting it, so the
// roster can go up before the first heat.
func (s *Server) handleLoadRace(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	id, err := strconv.ParseInt(r.Form.Get("race_id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which race?"})
		return
	}
	if err := s.app.Race.SetRace(r.Context(), id); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"race_id": id})
}

// --- scene data --------------------------------------------------------------

func (s *Server) handleRaceState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.raceStateJSON(r))
}

// raceStateJSON builds what the now-racing scene needs: who is in which lane,
// and the times once they are in.
func (s *Server) raceStateJSON(r *http.Request) map[string]any {
	ctx := r.Context()
	state := s.app.Race.State(ctx)

	out := map[string]any{
		"running":    state.Running,
		"race_name":  state.RaceName,
		"heat_no":    state.HeatNo,
		"heat_total": state.HeatTotal,
		"completed":  state.Completed,
		"timer":      state.Timer,
		"gate":       state.Gate,
		"auto_next":  state.AutoNext,
		"advance_in": state.AdvanceIn,
		// The displays need this: during the intermission a screen should say
		// so and point people at the voting tablet, not sit on a stale heat.
		"intermission": state.Intermission,
	}
	if state.Heat == nil {
		return out
	}

	season := s.seasonFor(r, state.Heat.RaceID)
	lanes := make([]map[string]any, 0, len(state.Heat.Lanes))
	for _, l := range state.Heat.Lanes {
		lane := map[string]any{
			"lane": l.Lane,
			"bye":  l.Bye(),
		}
		if !l.Bye() {
			lane["car_number"] = l.CarNumber
			lane["car_name"] = l.CarName
			lane["driver"] = l.DriverName()
		}
		if l.FinishTime != nil {
			lane["time"] = scoring.FormatTime(*l.FinishTime)
			lane["mph"] = scoring.FormatMPH(
				scoring.ScaleMPH(season.TrackLengthFt, season.ScaleDenom, *l.FinishTime))
			if l.FinishPlace != nil {
				lane["place"] = *l.FinishPlace
			}
		}
		lanes = append(lanes, lane)
	}
	out["lanes"] = lanes
	out["complete"] = state.Heat.Complete()
	return out
}

// seasonFor loads the season a race belongs to, falling back to the club's
// defaults so a display never divides by zero.
func (s *Server) seasonFor(r *http.Request, raceID int64) modelSeason {
	ctx := r.Context()
	fallback := modelSeason{TrackLengthFt: 28, ScaleDenom: 25, LaneCount: 4}

	race, err := s.app.DB.Race(ctx, raceID)
	if err != nil {
		return fallback
	}
	season, err := s.app.DB.Season(ctx, race.SeasonID)
	if err != nil {
		return fallback
	}
	return modelSeason{
		TrackLengthFt: season.TrackLengthFt,
		ScaleDenom:    season.ScaleDenom,
		LaneCount:     season.LaneCount,
	}
}

// modelSeason is the slice of the season the display layer needs.
type modelSeason struct {
	TrackLengthFt float64
	ScaleDenom    int
	LaneCount     int
}

func (s *Server) handleRoster(w http.ResponseWriter, r *http.Request) {
	raceID := s.raceIDParam(r)
	if raceID == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"entries": []any{}})
		return
	}

	entries, err := s.app.DB.Entries(r.Context(), raceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	race, _ := s.app.DB.Race(r.Context(), raceID)

	rows := make([]map[string]any, 0, len(entries))
	drivers := map[string]bool{}
	for _, e := range entries {
		if e.IsControl {
			continue // the pace car is not part of the intros
		}
		drivers[e.FullName()] = true
		rows = append(rows, map[string]any{
			"car_number": e.CarNumber,
			"car_name":   e.CarName,
			"driver":     e.FullName(),
			"photo_id":   e.PhotoID,
			"excluded":   e.Excluded,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"race":    race.Name,
		"entries": rows,
		"racers":  len(drivers),
		"cars":    len(rows),
	})
}

func (s *Server) handleStandings(w http.ResponseWriter, r *http.Request) {
	raceID := s.raceIDParam(r)
	if raceID == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"standings": []any{}})
		return
	}

	standings, err := s.app.DB.Standings(r.Context(), raceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	season := s.seasonFor(r, raceID)
	race, _ := s.app.DB.Race(r.Context(), raceID)

	rows := make([]map[string]any, 0, len(standings))
	for _, st := range standings {
		row := map[string]any{
			"place":      st.Place,
			"tied":       st.Tied,
			"car_number": st.Entry.CarNumber,
			"car_name":   st.Entry.CarName,
			"driver":     st.Entry.FullName(),
			"heats":      st.Heats,
			"is_control": st.Entry.IsControl,
		}
		if st.Heats > 0 {
			row["average"] = scoring.FormatAverage(st.Average)
			row["best"] = scoring.FormatTime(st.Best)
			row["worst"] = scoring.FormatTime(st.Worst)
			row["mph"] = scoring.FormatMPH(
				scoring.ScaleMPH(season.TrackLengthFt, season.ScaleDenom, st.Average))
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"race":      race.Name,
		"standings": rows,
	})
}

// raceIDParam reads an explicit race id, falling back to whichever race is
// loaded — so a display does not need to be told which race it is showing.
func (s *Server) raceIDParam(r *http.Request) int64 {
	if v := r.URL.Query().Get("race_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			return id
		}
	}
	return s.app.Race.CurrentRaceID()
}

// allRaces lists every race across every season, newest season first.
func (s *Server) allRaces(r *http.Request) ([]raceOption, error) {
	ctx := r.Context()
	seasons, err := s.app.DB.Seasons(ctx)
	if err != nil {
		return nil, err
	}
	var out []raceOption
	for _, season := range seasons {
		races, err := s.app.DB.Races(ctx, season.ID)
		if err != nil {
			return nil, err
		}
		for _, race := range races {
			out = append(out, raceOption{
				ID:     race.ID,
				Label:  season.Name + " · " + race.Name,
				Status: string(race.Status),
			})
		}
	}
	return out, nil
}

type raceOption struct {
	ID     int64
	Label  string
	Status string
}

// jsonParams decodes a display's stored parameters, tolerating rubbish.
func jsonParams(raw string) map[string]any {
	out := map[string]any{}
	if raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// displayOnline reports whether a display has been seen recently enough.
func displayOnline(lastSeen time.Time) bool {
	return time.Since(lastSeen) < store.DisplayOnlineWindow
}
