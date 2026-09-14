package web

import (
	"net/http"
	"strconv"

	"github.com/davisc01/derbyandales/internal/app"
	"github.com/davisc01/derbyandales/internal/bracket"
	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/season"
	"github.com/davisc01/derbyandales/internal/store"
)

// The championship screen: seed the field, build the bracket, run it.
//
// The seeding review is the part that matters. Everything else on this page is
// buttons, but a wrongly-seeded car ends up in the wrong half of the bracket
// and nobody finds out until the semi-final.

func (s *Server) bracketRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /championship", s.handleChampionshipPage)

	mux.HandleFunc("GET /api/bracket/state", s.handleBracketState)
	mux.HandleFunc("GET /api/bracket/plan", s.handlePlan)
	mux.HandleFunc("POST /api/bracket/generate", s.handleGenerateBracket)
	mux.HandleFunc("POST /api/bracket/arm", s.handleArmMatchup)
	mux.HandleFunc("POST /api/bracket/result", s.handleMatchupResult)
	mux.HandleFunc("POST /api/bracket/declare", s.handleDeclareMatchup)
	mux.HandleFunc("POST /api/bracket/swap", s.handleSwapLanes)
}

// championshipRace finds the season's championship, creating nothing.
func (s *Server) championshipRace(r *http.Request, seasonID int64) (model.Race, bool) {
	races, err := s.app.DB.Races(r.Context(), seasonID)
	if err != nil {
		return model.Race{}, false
	}
	for _, race := range races {
		if race.Kind == model.RaceChampionship {
			return race, true
		}
	}
	return model.Race{}, false
}

func (s *Server) handleChampionshipPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := map[string]any{}

	seasonID := s.seasonIDParam(r)
	if seasonID == 0 {
		s.render(w, r, "championship.html", pageData{
			Title: "Championship", Active: "championship", Data: data,
		})
		return
	}

	// Not named "season": the package of that name is used just below.
	sn, err := s.app.DB.Season(ctx, seasonID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data["Season"] = sn
	data["Plan"] = bracket.PlanFor(sn.RaceCount, sn.AutoQualPlaces, sn.WildcardSpots)
	data["Suggestions"] = bracket.SuggestWildcards(sn.RaceCount, sn.AutoQualPlaces, 24)

	race, ok := s.championshipRace(r, seasonID)
	if !ok {
		s.render(w, r, "championship.html", pageData{
			Title: "Championship", Active: "championship", Data: data,
		})
		return
	}
	data["Race"] = race

	state, err := s.app.Bracket.State(ctx, race.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data["State"] = state
	data["Rounds"] = roundsOf(state)

	// Before the bracket exists, the page is a seeding review. Afterwards it is
	// the bracket, and the proposals would only be confusing.
	if len(state.Matchups) == 0 {
		proposals, err := s.app.Bracket.Seed(ctx, seasonID, race.ID)
		if err != nil {
			data["SeedError"] = err.Error()
		} else {
			data["Proposals"] = proposals
			matched, loose := 0, 0
			for _, p := range proposals {
				if !p.Matched() {
					continue
				}
				matched++
				// A wildcard racer has no expected car, so there is nothing to
				// check against. Only a qualifier who brought something other
				// than the car that qualified is worth a second look.
				if p.Origin == season.OriginQualifier && !p.Exact {
					loose++
				}
			}
			data["Matched"] = matched
			data["Loose"] = loose
			data["Missing"] = len(proposals) - matched
		}
	}

	s.render(w, r, "championship.html", pageData{
		Title: "Championship", Active: "championship", Data: data,
	})
}

// roundView groups the bracket by round for rendering, with the name the club
// would use out loud.
type roundView struct {
	Round    int
	Name     string
	Matchups []store.MatchupView
}

func roundsOf(state app.State) []roundView {
	if len(state.Matchups) == 0 {
		return nil
	}
	last := 0
	for _, m := range state.Matchups {
		if m.Round > last {
			last = m.Round
		}
	}
	byRound := map[int][]store.MatchupView{}
	for _, m := range state.Matchups {
		byRound[m.Round] = append(byRound[m.Round], m)
	}
	out := make([]roundView, 0, last)
	for r := 1; r <= last; r++ {
		out = append(out, roundView{Round: r, Name: roundName(r, last), Matchups: byRound[r]})
	}
	return out
}

// roundName is what people call the round, counting back from the final.
func roundName(round, rounds int) string {
	switch rounds - round {
	case 0:
		return "Final"
	case 1:
		return "Semi-finals"
	case 2:
		return "Quarter-finals"
	}
	return "Round " + strconv.Itoa(round)
}

func (s *Server) handleBracketState(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	seasonID := s.seasonIDParam(r)
	race, ok := s.championshipRace(r, seasonID)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"seeded": false})
		return
	}
	state, err := s.app.Bracket.State(ctx, race.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, bracketJSON(state))
}

// bracketJSON is what the display scene and the live page read.
func bracketJSON(state app.State) map[string]any {
	rounds := roundsOf(state)
	out := make([]map[string]any, 0, len(rounds))
	for _, rd := range rounds {
		ms := make([]map[string]any, 0, len(rd.Matchups))
		for _, m := range rd.Matchups {
			ms = append(ms, map[string]any{
				"id":          m.ID,
				"position":    m.Position,
				"top":         slotJSON(m.Top, m.TopSeed),
				"bottom":      slotJSON(m.Bottom, m.BottomSeed),
				"winner_id":   nullable(m.WinnerEntryID),
				"heat":        m.HeatNumber,
				"walkover":    m.Walkover(),
				"ready":       m.Ready(),
				"upset":       m.Upset(),
				"decided":     m.WinnerEntryID != nil,
				"round_index": rd.Round,
			})
		}
		out = append(out, map[string]any{"round": rd.Round, "name": rd.Name, "matchups": ms})
	}

	data := map[string]any{
		"seeded":    state.Seeded,
		"race":      state.RaceName,
		"entrants":  state.Entrants,
		"capacity":  state.Capacity,
		"byes":      state.Byes,
		"rounds":    out,
		"remaining": state.Remaining,
	}
	if state.Champion != nil {
		data["champion"] = map[string]any{
			"car":    state.Champion.CarName,
			"driver": state.Champion.FullName(),
			"number": state.Champion.CarNumber,
		}
	}
	return data
}

func slotJSON(e *store.EntryView, seed int) map[string]any {
	if e == nil {
		return nil
	}
	return map[string]any{
		"entry_id": e.ID,
		"seed":     seed,
		"car":      e.CarName,
		"number":   e.CarNumber,
		"driver":   e.FullName(),
	}
}

func nullable(id *int64) any {
	if id == nil {
		return nil
	}
	return *id
}

// handlePlan answers "what would the championship look like if we did this?"
func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	atoi := func(key string, def int) int {
		if v, err := strconv.Atoi(q.Get(key)); err == nil {
			return v
		}
		return def
	}
	races := atoi("races", 6)
	places := atoi("places", 3)
	wildcards := atoi("wildcards", 6)

	p := bracket.PlanFor(races, places, wildcards)
	suggestions := bracket.SuggestWildcards(races, places, 24)
	alt := make([]map[string]any, 0, len(suggestions))
	for _, sg := range suggestions {
		alt = append(alt, map[string]any{
			"wildcards": sg.Wildcards, "entrants": sg.Entrants,
			"capacity": sg.Capacity, "byes": sg.Byes,
			"first_round": sg.FirstRound, "rounds": sg.Rounds,
			"bye_free": sg.ByeFree(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entrants": p.Entrants, "capacity": p.Capacity, "byes": p.Byes,
		"first_round": p.FirstRound, "rounds": p.Rounds,
		"bye_free": p.ByeFree(), "warnings": p.Warnings,
		"suggestions": alt,
	})
}

func (s *Server) handleGenerateBracket(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx := r.Context()
	seasonID := s.seasonIDForm(r)
	race, ok := s.championshipRace(r, seasonID)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "There is no championship race for this season yet.",
		})
		return
	}

	proposals, err := s.app.Bracket.Seed(ctx, seasonID, race.ID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// The page may have overridden which car fills a place. Anything not named
	// keeps whatever was proposed.
	for i := range proposals {
		key := "entry_" + strconv.Itoa(proposals[i].Seed)
		if v := r.FormValue(key); v != "" {
			id, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error": "That is not a car.",
				})
				return
			}
			proposals[i].EntryID = id
		}
	}

	state, err := s.app.Bracket.Generate(ctx, race.ID, proposals, "coordinator")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entrants": state.Entrants, "capacity": state.Capacity,
		"byes": state.Byes, "rounds": state.Rounds,
	})
}

func (s *Server) handleArmMatchup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx := r.Context()
	seasonID := s.seasonIDForm(r)
	race, ok := s.championshipRace(r, seasonID)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no championship"})
		return
	}

	var err error
	if v := r.FormValue("matchup_id"); v != "" {
		id, parseErr := strconv.ParseInt(v, 10, 64)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which matchup?"})
			return
		}
		err = s.app.Bracket.ArmMatchup(ctx, race.ID, id)
	} else {
		err = s.app.Bracket.ArmNext(ctx, race.ID)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMatchupResult(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx := r.Context()
	seasonID := s.seasonIDForm(r)
	race, ok := s.championshipRace(r, seasonID)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no championship"})
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("matchup_id"), 10, 64)
	if err := s.app.Bracket.RecordResult(ctx, race.ID, id); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDeclareMatchup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx := r.Context()
	seasonID := s.seasonIDForm(r)
	race, ok := s.championshipRace(r, seasonID)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no championship"})
		return
	}
	matchupID, _ := strconv.ParseInt(r.FormValue("matchup_id"), 10, 64)
	winnerID, _ := strconv.ParseInt(r.FormValue("winner_id"), 10, 64)
	reason := r.FormValue("reason")
	if reason == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Say why this was decided by hand — it goes on the record.",
		})
		return
	}
	if err := s.app.Bracket.Declare(ctx, race.ID, matchupID, winnerID, "coordinator", reason); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSwapLanes(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("matchup_id"), 10, 64)
	if err := s.app.Bracket.SwapLanes(r.Context(), id, "coordinator"); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
