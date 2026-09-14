package web

import (
	"net/http"
	"strconv"

	"github.com/davisc01/derbyandales/internal/bus"
	"github.com/davisc01/derbyandales/internal/store"
)

func (s *Server) voteRoutes(mux *http.ServeMux) {
	// The ballot, for the tablet on the table.
	mux.HandleFunc("GET /vote", s.handleBallot)
	mux.HandleFunc("GET /api/vote/ballot", s.handleBallotData)
	mux.HandleFunc("POST /api/vote/cast", s.handleCastVote)

	// The coordinator's side.
	mux.HandleFunc("GET /voting", s.handleVotingAdmin)
	mux.HandleFunc("GET /api/vote/tally", s.handleTally)
	mux.HandleFunc("POST /api/vote/undo", s.handleUndoVote)
	mux.HandleFunc("POST /api/vote/reset", s.handleResetVotes)
	mux.HandleFunc("POST /api/vote/declare", s.handleDeclareWinner)
	mux.HandleFunc("POST /api/vote/clear", s.handleClearWinner)
	mux.HandleFunc("POST /api/vote/enable", s.handleEnableCategory)

	// The intermission.
	mux.HandleFunc("POST /api/race/resume", s.handleResumeRacing)
	mux.HandleFunc("POST /api/vote/reopen", s.handleReopenVoting)
	mux.HandleFunc("POST /api/vote/close", s.handleCloseVoting)
}

// handleBallot serves the voting tablet. It uses the chrome-free layout: this
// is a kiosk, not a page someone navigates around.
func (s *Server) handleBallot(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "vote.html", pageData{
		Title:  "Vote",
		Active: "vote",
		Data:   map[string]any{},
	})
}

// handleBallotData is what the tablet polls: whether voting is open, the
// questions, and the cars.
func (s *Server) handleBallotData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	raceID := s.raceIDParam(r)

	out := map[string]any{
		"open":       s.app.Race.VotingOpen(),
		"categories": []any{},
		"cars":       []any{},
	}
	if raceID == 0 {
		writeJSON(w, http.StatusOK, out)
		return
	}

	race, _ := s.app.DB.Race(ctx, raceID)
	out["race"] = race.Name

	categories, err := s.app.DB.VoteCategories(ctx, raceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	cats := make([]map[string]any, 0, len(categories))
	for _, c := range categories {
		if !c.Enabled {
			continue
		}
		cats = append(cats, map[string]any{
			"id": c.ID, "key": c.Key, "label": c.Label,
		})
	}
	out["categories"] = cats

	entries, err := s.app.DB.BallotEntries(ctx, raceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	cars := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		car := map[string]any{
			"id": e.ID, "car_number": e.CarNumber, "car_name": e.CarName,
			"driver": e.FullName(),
		}
		if e.PhotoID != nil {
			car["photo_id"] = *e.PhotoID
		}
		cars = append(cars, car)
	}
	out["cars"] = cars

	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCastVote(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// The tablet sits on the table all evening. A tap outside the intermission
	// must be refused clearly rather than vanishing.
	if !s.app.Race.VotingOpen() {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "Voting is closed.",
		})
		return
	}

	categoryID, err := strconv.ParseInt(r.Form.Get("category_id"), 10, 64)
	if err != nil || categoryID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which question?"})
		return
	}
	entryID, err := strconv.ParseInt(r.Form.Get("entry_id"), 10, 64)
	if err != nil || entryID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which car?"})
		return
	}

	if _, err := s.app.DB.CastVote(r.Context(), categoryID, entryID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	count, _ := s.app.DB.VoteCount(r.Context(), categoryID)
	s.app.Bus.Publish(bus.TopicVote, "cast", map[string]any{
		"category_id": categoryID, "count": count,
	})
	writeJSON(w, http.StatusOK, map[string]any{"count": count})
}

// --- coordinator -------------------------------------------------------------

func (s *Server) handleVotingAdmin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	raceID := s.raceIDParam(r)

	type categoryView struct {
		ID      int64
		Key     string
		Label   string
		Enabled bool
		Votes   int
		Tally   []store.TallyRow
		Winner  *store.EntryView
		Tied    bool
	}

	var views []categoryView
	if raceID != 0 {
		_ = s.app.DB.EnsureVoteCategories(ctx, raceID)
		categories, _ := s.app.DB.VoteCategories(ctx, raceID)
		for _, c := range categories {
			v := categoryView{ID: c.ID, Key: c.Key, Label: c.Label, Enabled: c.Enabled}
			v.Tally, _ = s.app.DB.Tally(ctx, c.ID)
			v.Votes, _ = s.app.DB.VoteCount(ctx, c.ID)

			leaders := 0
			for _, row := range v.Tally {
				if row.Leading {
					leaders++
				}
			}
			v.Tied = leaders > 1

			if c.WinnerEntryID != nil {
				if entry, err := s.app.DB.Entry(ctx, *c.WinnerEntryID); err == nil {
					v.Winner = &entry
				}
			}
			views = append(views, v)
		}
	}

	race, _ := s.app.DB.Race(ctx, raceID)
	s.render(w, r, "voting.html", pageData{
		Title:  "Voting",
		Active: "voting",
		Data: map[string]any{
			"Race":         race,
			"Categories":   views,
			"Intermission": s.app.Race.Intermission(ctx),
			"URLs":         s.app.URLs(),
		},
	})
}

func (s *Server) handleTally(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	raceID := s.raceIDParam(r)
	if raceID == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"categories": []any{}})
		return
	}

	categories, err := s.app.DB.VoteCategories(ctx, raceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	out := make([]map[string]any, 0, len(categories))
	for _, c := range categories {
		tally, _ := s.app.DB.Tally(ctx, c.ID)
		rows := make([]map[string]any, 0, len(tally))
		leaders := 0
		for _, t := range tally {
			if t.Leading {
				leaders++
			}
			rows = append(rows, map[string]any{
				"entry_id": t.Entry.ID, "car_number": t.Entry.CarNumber,
				"car_name": t.Entry.CarName, "driver": t.Entry.FullName(),
				"votes": t.Votes, "leading": t.Leading,
			})
		}
		count, _ := s.app.DB.VoteCount(ctx, c.ID)

		entry := map[string]any{
			"id": c.ID, "key": c.Key, "label": c.Label,
			"enabled": c.Enabled, "votes": count, "tally": rows,
			"tied": leaders > 1,
		}
		if c.WinnerEntryID != nil {
			entry["winner_entry_id"] = *c.WinnerEntryID
		}
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"categories":   out,
		"voting_open":  s.app.Race.VotingOpen(),
		"intermission": s.app.Race.Intermission(ctx),
	})
}

func (s *Server) handleUndoVote(w http.ResponseWriter, r *http.Request) {
	categoryID, ok := s.categoryParam(w, r)
	if !ok {
		return
	}
	vote, err := s.app.DB.UndoLastVote(r.Context(), categoryID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Say which car, so the person at the table can confirm it was the misclick
	// they meant rather than someone else's vote.
	undone := ""
	if entry, err := s.app.DB.Entry(r.Context(), vote.EntryID); err == nil {
		undone = "#" + strconv.Itoa(entry.CarNumber) + " " + entry.CarName
	}
	_ = s.app.DB.Audit(r.Context(), "crew", "vote.undo", undone)

	count, _ := s.app.DB.VoteCount(r.Context(), categoryID)
	s.app.Bus.Publish(bus.TopicVote, "undo", map[string]any{
		"category_id": categoryID, "count": count,
	})
	writeJSON(w, http.StatusOK, map[string]any{"undone": undone, "count": count})
}

func (s *Server) handleResetVotes(w http.ResponseWriter, r *http.Request) {
	categoryID, ok := s.categoryParam(w, r)
	if !ok {
		return
	}
	if err := s.app.DB.ResetVoteCategory(r.Context(), categoryID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	_ = s.app.DB.Audit(r.Context(), "coordinator", "vote.reset", "")
	s.app.Bus.Publish(bus.TopicVote, "reset", map[string]any{"category_id": categoryID})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDeclareWinner(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	categoryID, err := strconv.ParseInt(r.Form.Get("category_id"), 10, 64)
	if err != nil || categoryID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which question?"})
		return
	}
	entryID, err := strconv.ParseInt(r.Form.Get("entry_id"), 10, 64)
	if err != nil || entryID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which car?"})
		return
	}

	if err := s.app.DB.DeclareVoteWinner(r.Context(), categoryID, entryID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	entry, _ := s.app.DB.Entry(r.Context(), entryID)
	_ = s.app.DB.Audit(r.Context(), "coordinator", "vote.declare",
		"#"+strconv.Itoa(entry.CarNumber)+" "+entry.CarName)
	s.app.Bus.Publish(bus.TopicVote, "declared", map[string]any{
		"category_id": categoryID, "entry_id": entryID,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleClearWinner(w http.ResponseWriter, r *http.Request) {
	categoryID, ok := s.categoryParam(w, r)
	if !ok {
		return
	}
	if err := s.app.DB.ClearVoteWinner(r.Context(), categoryID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.app.Bus.Publish(bus.TopicVote, "cleared", map[string]any{"category_id": categoryID})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleEnableCategory(w http.ResponseWriter, r *http.Request) {
	categoryID, ok := s.categoryParam(w, r)
	if !ok {
		return
	}
	enabled := r.Form.Get("enabled") == "true"
	if err := s.app.DB.SetVoteCategoryEnabled(r.Context(), categoryID, enabled); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.app.Bus.Publish(bus.TopicVote, "categories", nil)
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": enabled})
}

func (s *Server) categoryParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return 0, false
	}
	id, err := strconv.ParseInt(r.Form.Get("category_id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which question?"})
		return 0, false
	}
	return id, true
}

// --- intermission ------------------------------------------------------------

func (s *Server) handleResumeRacing(w http.ResponseWriter, r *http.Request) {
	if err := s.app.Race.ResumeRacing(r.Context()); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.app.Race.State(r.Context()))
}

func (s *Server) handleReopenVoting(w http.ResponseWriter, r *http.Request) {
	if err := s.app.Race.ReopenVoting(r.Context()); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.app.Race.Intermission(r.Context()))
}

func (s *Server) handleCloseVoting(w http.ResponseWriter, r *http.Request) {
	s.app.Race.EndIntermissionWithoutRacing(r.Context())
	writeJSON(w, http.StatusOK, s.app.Race.Intermission(r.Context()))
}
