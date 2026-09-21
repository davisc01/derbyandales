package web

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/davisc01/derbyandales/internal/bus"
	"github.com/davisc01/derbyandales/internal/model"
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
	mux.HandleFunc("GET /api/race/awards", s.handleRaceAwards)
	mux.HandleFunc("GET /api/race/impound", s.handleImpound)
	mux.HandleFunc("GET /api/race/bracket", s.handleRaceBracket)
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
	// Remember that this race showed this scene. The run of show has two steps
	// — the reveal and the final standings — that change no result and would
	// otherwise never complete.
	if err := s.app.DB.RecordSceneShown(r.Context(), s.app.Race.CurrentRaceID(), scene); err != nil {
		s.app.Log.Warn("recording the scene failed", "scene", scene, "err", err)
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

	// A broken record is announced with the result. Only asked once there are
	// times, because a staged heat has broken nothing and this reads every run
	// on file.
	if timed(state.Heat.Lanes) {
		notices, err := s.app.HeatRecords(ctx, *state.Heat)
		if err != nil {
			s.app.Log.Warn("checking the heat for records failed", "heat", state.Heat.ID, "err", err)
		}
		for _, lane := range lanes {
			l := lane["lane"].(int)
			if n, ok := notices[l]; ok {
				lane["record"] = noticeJSON(n, l)
			}
		}
	}

	out["lanes"] = lanes
	out["complete"] = state.Heat.Complete()
	return out
}

func timed(lanes []store.LaneView) bool {
	for _, l := range lanes {
		if l.FinishTime != nil {
			return true
		}
	}
	return false
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

	// Alphabetical by surname, the way a programme lists people, so anybody
	// looking for their own name on a TV across the room can find it. Entries
	// come back in car-number order, which is the loading order for the track
	// and no use at all for reading. A racer with two cars is sorted by number
	// between them.
	sorted := make([]store.EntryView, len(entries))
	copy(sorted, entries)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if x, y := strings.ToLower(a.LastName), strings.ToLower(b.LastName); x != y {
			return x < y
		}
		if x, y := strings.ToLower(a.FirstName), strings.ToLower(b.FirstName); x != y {
			return x < y
		}
		return a.CarNumber < b.CarNumber
	})
	entries = sorted

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

	// A record average belongs to the reveal, which is where the room hears it.
	// There is none until the race is over and its result recorded, so a car
	// cannot be announced as a record-breaker from half a night's runs.
	notices, err := s.app.AverageRecords(r.Context(), raceID)
	if err != nil {
		s.app.Log.Warn("checking the averages for records failed", "race", raceID, "err", err)
	}

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
		if n, ok := notices[st.Entry.CarNumber]; ok && !st.Entry.IsControl && !st.Entry.Excluded {
			row["record"] = noticeJSON(n, 0)
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"race":         race.Name,
		"standings":    rows,
		"trophies":     store.TrophyPlaces(race),
		"trophy_names": store.TrophyNames(race),
		"championship": race.Kind == model.RaceChampionship,
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

// handleRaceAwards is what the awards scene reads.
//
// The two voted trophies come first and carry a photo: they are given for how a
// car looks, so the car has to be on the screen. The speed trophies are handed
// out during the results reveal instead, as each of the top three comes up, so
// they are returned but marked.
func (s *Server) handleRaceAwards(w http.ResponseWriter, r *http.Request) {
	raceID := s.raceIDParam(r)
	if raceID == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"awards": []any{}})
		return
	}
	awards, err := s.app.DB.RaceAwards(r.Context(), raceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	race, _ := s.app.DB.Race(r.Context(), raceID)

	rows := make([]map[string]any, 0, len(awards))
	for _, a := range awards {
		if a.EntryID == nil {
			continue // not decided yet
		}
		row := map[string]any{
			"name":       a.Name,
			"voted":      a.Source == model.AwardVote,
			"car_number": a.Entry.CarNumber,
			"car_name":   a.Entry.CarName,
			"driver":     a.Entry.FullName(),
		}
		// The theme trophy is the one award that means nothing without the
		// theme it was judged against, so the night's theme rides along with
		// it and the screen can say "Best Theme — Movie Night".
		if race.Theme != "" && a.Name == store.AwardNameForVote[store.VoteTheme] {
			row["theme"] = race.Theme
		}
		if a.Entry.PhotoID != nil {
			row["photo_id"] = *a.Entry.PhotoID
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"race": race.Name, "awards": rows})
}

// handleImpound is what the loading-tray screen reads.
//
// The official at the impound table is not watching the racing; they are
// putting the next four cars into a tray while the current four are on the
// track. So this shows two heats, and nothing but numbers, lanes and pictures —
// a name is no use when you are looking for a car on a shelf.
func (s *Server) handleImpound(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	raceID := s.raceIDParam(r)
	out := map[string]any{"current": nil, "upcoming": nil}
	if raceID == 0 {
		writeJSON(w, http.StatusOK, out)
		return
	}

	heats, err := s.app.DB.Heats(ctx, raceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	race, _ := s.app.DB.Race(ctx, raceID)
	out["race"] = race.Name

	// The heat on the track is the armed one; failing that, the first one with
	// no times against it.
	current := -1
	if state := s.app.Race.State(ctx); state.Heat != nil {
		for i, h := range heats {
			if h.ID == state.Heat.ID {
				current = i
			}
		}
	}
	if current < 0 {
		for i, h := range heats {
			if !h.Complete() {
				current = i
				break
			}
		}
	}
	if current < 0 {
		writeJSON(w, http.StatusOK, out)
		return
	}

	out["current"] = impoundHeat(heats[current])

	// A bracket builds each heat when it is armed, so the next one does not
	// exist yet. The next matchup ready to race is what goes in the tray.
	if race.Bracket() {
		if next, ok := s.nextMatchupAfter(ctx, race, heats[current]); ok {
			out["upcoming"] = next
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	// The next heat that still has to be loaded. A heat already run is not
	// something anybody needs to fetch cars for.
	upcoming := -1
	for i := current + 1; i < len(heats); i++ {
		if !heats[i].Complete() {
			upcoming = i
			break
		}
	}

	if upcoming >= 0 {
		out["upcoming"] = impoundHeat(heats[upcoming])
	}
	out["total"] = len(heats)
	writeJSON(w, http.StatusOK, out)
}

func impoundHeat(h store.HeatView) map[string]any {
	lanes := make([]map[string]any, 0, len(h.Lanes))
	for _, l := range h.Lanes {
		row := map[string]any{"lane": l.Lane}
		if l.EntryID != nil {
			row["car_number"] = l.CarNumber
			row["car_name"] = l.CarName
			row["driver"] = l.DriverName()
			if l.PhotoID != nil {
				row["photo_id"] = *l.PhotoID
			}
		}
		lanes = append(lanes, row)
	}
	return map[string]any{
		"heat":     h.Number,
		"lanes":    lanes,
		"complete": h.Complete(),
	}
}

// nextMatchupAfter is the matchup that will be raced after the one on the
// track, shown the way its heat will be laid out.
func (s *Server) nextMatchupAfter(ctx context.Context, race model.Race, current store.HeatView) (map[string]any, bool) {
	matchups, err := s.app.DB.Matchups(ctx, race.ID)
	if err != nil {
		return nil, false
	}
	sn, err := s.app.DB.Season(ctx, race.SeasonID)
	if err != nil {
		return nil, false
	}
	for _, m := range matchups {
		if !m.Ready() || (current.BracketMatchupID != nil && *current.BracketMatchupID == m.ID) {
			continue
		}
		lanes := make([]map[string]any, 0, sn.LaneCount)
		for lane := 1; lane <= sn.LaneCount; lane++ {
			row := map[string]any{"lane": lane}
			var e *store.EntryView
			switch lane {
			case sn.BracketLaneA:
				e = m.Top
			case sn.BracketLaneB:
				e = m.Bottom
			}
			if e != nil {
				row["car_number"] = e.CarNumber
				row["car_name"] = e.CarName
				row["driver"] = e.FullName()
				if e.PhotoID != nil {
					row["photo_id"] = *e.PhotoID
				}
			}
			lanes = append(lanes, row)
		}
		// Not yet numbered: the heat is numbered when it is built.
		return map[string]any{"heat": current.Number + 1, "lanes": lanes, "complete": false}, true
	}
	return nil, false
}

// handleRaceBracket is what the bracket scene reads: the loaded race's bracket,
// with the matchup on the track marked.
func (s *Server) handleRaceBracket(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	raceID := s.raceIDParam(r)
	race, err := s.app.DB.Race(ctx, raceID)
	if raceID == 0 || err != nil || !race.Bracket() {
		writeJSON(w, http.StatusOK, map[string]any{"seeded": false, "race": race.Name})
		return
	}
	state, err := s.app.Bracket.State(ctx, raceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	data := bracketJSON(state)
	if h := s.app.Race.State(ctx).Heat; h != nil && h.BracketMatchupID != nil {
		data["on_track"] = *h.BracketMatchupID
	}
	writeJSON(w, http.StatusOK, data)
}
