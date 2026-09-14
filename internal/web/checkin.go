package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/davisc01/derbyandales/internal/app"
	"github.com/davisc01/derbyandales/internal/bus"
	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/store"
)

func (s *Server) checkinRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /checkin", s.handleCheckinPage)

	mux.HandleFunc("POST /api/season", s.handleCreateSeason)
	mux.HandleFunc("POST /api/race", s.handleCreateRace)

	mux.HandleFunc("GET /api/entries", s.handleListEntries)
	mux.HandleFunc("POST /api/entry", s.handleCreateEntry)
	mux.HandleFunc("POST /api/entry/update", s.handleUpdateEntry)
	mux.HandleFunc("POST /api/entry/delete", s.handleDeleteEntry)
	mux.HandleFunc("GET /api/racers", s.handleListRacers)

	mux.HandleFunc("POST /api/photo", s.handleUploadPhoto)
	mux.HandleFunc("GET /photo/{id}", s.handleServePhoto)
}

func (s *Server) handleCheckinPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	raceID := s.raceIDParam(r)

	var (
		race    model.Race
		season  model.Season
		entries []store.EntryView
		racers  []model.Racer
	)
	if raceID != 0 {
		race, _ = s.app.DB.Race(ctx, raceID)
		season, _ = s.app.DB.Season(ctx, race.SeasonID)
		entries, _ = s.app.DB.Entries(ctx, raceID)
		racers, _ = s.app.DB.Racers(ctx, race.SeasonID)
	}
	races, _ := s.allRaces(r)
	seasons, _ := s.app.DB.Seasons(ctx)

	// Car numbers already taken, so the form can say so before submitting.
	taken := make([]int, 0, len(entries))
	for _, e := range entries {
		taken = append(taken, e.CarNumber)
	}

	s.render(w, r, "checkin.html", pageData{
		Title:  "Check-in",
		Active: "checkin",
		Data: map[string]any{
			"Race":       race,
			"Season":     season,
			"Entries":    entries,
			"Racers":     racers,
			"Races":      races,
			"Seasons":    seasons,
			"TakenCars":  taken,
			"NextYear":   time.Now().Year() + 1,
			"SecureHost": isSecureContext(r),
		},
	})
}

// isSecureContext reports whether this page was served somewhere a browser will
// grant camera access.
//
// This is the difference between check-in working and a mysteriously dead
// camera: localhost counts as secure, a plain-HTTP LAN address does not.
func isSecureContext(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host := r.Host
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host == "localhost" || host == "127.0.0.1" || host == "[::1]" || host == "::1"
}

func (s *Server) handleCreateSeason(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	year, err := strconv.Atoi(r.Form.Get("year"))
	if err != nil || year < 2000 || year > 2200 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which year?"})
		return
	}

	season := store.DefaultSeason(year)
	if name := strings.TrimSpace(r.Form.Get("name")); name != "" {
		season.Name = name
	}
	if v := r.Form.Get("lane_count"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			season.LaneCount = n
		}
	}
	if v := r.Form.Get("track_length_ft"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			season.TrackLengthFt = f
		}
	}
	if v := r.Form.Get("race_count"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			season.RaceCount = n
		}
	}

	created, err := s.app.DB.CreateSeason(r.Context(), season)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	_ = s.app.DB.Audit(r.Context(), "coordinator", "season.create", created.Name)
	writeJSON(w, http.StatusOK, created)
}

func (s *Server) handleCreateRace(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	seasonID, err := strconv.ParseInt(r.Form.Get("season_id"), 10, 64)
	if err != nil || seasonID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which season?"})
		return
	}
	number, _ := strconv.Atoi(r.Form.Get("number"))
	if number <= 0 {
		number = 1
	}

	kind := model.RacePoints
	if r.Form.Get("kind") == string(model.RaceChampionship) {
		kind = model.RaceChampionship
	}

	name := strings.TrimSpace(r.Form.Get("name"))
	if name == "" {
		if kind == model.RaceChampionship {
			name = "Championship"
		} else {
			name = "Race " + strconv.Itoa(number)
		}
	}

	date := time.Now()
	if v := r.Form.Get("date"); v != "" {
		if parsed, err := time.Parse("2006-01-02", v); err == nil {
			date = parsed
		}
	}

	race, err := s.app.DB.CreateRace(r.Context(), model.Race{
		SeasonID: seasonID,
		Number:   number,
		Name:     name,
		Date:     date,
		Venue:    strings.TrimSpace(r.Form.Get("venue")),
		Kind:     kind,
		Status:   model.StatusCheckin,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// A new race becomes the one the screens follow, so the roster can go up
	// without anyone selecting it.
	_ = s.app.Race.SetRace(r.Context(), race.ID)
	_ = s.app.DB.Audit(r.Context(), "coordinator", "race.create", race.Name)
	if _, err := s.app.Backup(r.Context(), app.BackupRaceOpen); err != nil {
		s.app.Log.Warn("snapshot on race open failed", "err", err)
	}
	writeJSON(w, http.StatusOK, race)
}

// --- entries -----------------------------------------------------------------

func (s *Server) handleListEntries(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, http.StatusOK, map[string]any{"entries": entriesJSON(entries)})
}

func entriesJSON(entries []store.EntryView) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		row := map[string]any{
			"id":         e.ID,
			"car_number": e.CarNumber,
			"car_name":   e.CarName,
			"first_name": e.FirstName,
			"last_name":  e.LastName,
			"driver":     e.FullName(),
			"is_control": e.IsControl,
			"excluded":   e.Excluded,
			"reason":     e.ExclusionReason,
			"note":       e.Note,
		}
		if e.PhotoID != nil {
			row["photo_id"] = *e.PhotoID
		}
		out = append(out, row)
	}
	return out
}

func (s *Server) handleCreateEntry(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx := r.Context()

	raceID := s.raceIDForm(r)
	if raceID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no race is open for check-in"})
		return
	}
	race, err := s.app.DB.Race(ctx, raceID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown race"})
		return
	}

	first := strings.TrimSpace(r.Form.Get("first_name"))
	last := strings.TrimSpace(r.Form.Get("last_name"))
	if first == "" && last == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the driver needs a name"})
		return
	}

	carNumber, err := strconv.Atoi(r.Form.Get("car_number"))
	if err != nil || carNumber <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the car needs a number"})
		return
	}

	racer, err := s.app.DB.FindOrCreateRacer(ctx, race.SeasonID, first, last)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	now := time.Now()
	entry := model.Entry{
		RaceID:          raceID,
		RacerID:         racer.ID,
		CarNumber:       carNumber,
		CarName:         strings.TrimSpace(r.Form.Get("car_name")),
		IsControl:       r.Form.Get("is_control") == "true",
		Excluded:        r.Form.Get("excluded") == "true",
		ExclusionReason: strings.TrimSpace(r.Form.Get("reason")),
		Note:            strings.TrimSpace(r.Form.Get("note")),
		CheckedInAt:     &now,
	}
	if v := r.Form.Get("photo_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			entry.PhotoID = &id
		}
	}
	// An exclusion without a reason is unanswerable later, when someone asks
	// why their car was not in the standings.
	if entry.Excluded && entry.ExclusionReason == "" {
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "an excluded car needs a reason"})
		return
	}

	created, err := s.app.DB.CreateEntry(ctx, entry)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": checkinError(err, carNumber)})
		return
	}

	s.app.Bus.Publish(bus.TopicRace, "checkin", map[string]any{
		"race_id": raceID, "car_number": carNumber, "driver": racer.FullName(),
	})

	out := map[string]any{"id": created.ID}
	// A car gets one championship. Checking that has always been somebody
	// remembering, so say it here rather than let it be found out afterwards —
	// but only say it. Excluding a car is a decision, and it needs a reason.
	if seen, err := s.app.DB.RacedBefore(ctx, racer.LastName, entry.CarName); err == nil && len(seen) > 0 {
		out["raced_before"] = pastCarsJSON(seen)
		out["warning"] = racedBeforeMessage(entry.CarName, seen)
	}
	writeJSON(w, http.StatusOK, out)
}

// racedBeforeMessage is what the person at the table reads.
func racedBeforeMessage(carName string, seen []store.PastCar) string {
	first := seen[0]
	if first.SameDriver {
		return fmt.Sprintf("%s raced the %d championship — same car name, same driver. "+
			"A car gets one championship, so this may not be eligible.",
			carName, first.Year)
	}
	return fmt.Sprintf("A car called %s raced the %d championship, driven by %s. "+
		"Probably a different car, but worth a look.",
		carName, first.Year, first.Driver())
}

func pastCarsJSON(seen []store.PastCar) []map[string]any {
	out := make([]map[string]any, 0, len(seen))
	for _, c := range seen {
		out = append(out, map[string]any{
			"year": c.Year, "car_name": c.CarName, "driver": c.Driver(),
			"car_number": c.CarNumber, "place": c.Place, "same_driver": c.SameDriver,
		})
	}
	return out
}

// checkinError turns a constraint violation into something a person can act on.
func checkinError(err error, carNumber int) string {
	if strings.Contains(err.Error(), "idx_entry_car_number") ||
		strings.Contains(err.Error(), "UNIQUE") {
		return "Car number " + strconv.Itoa(carNumber) + " is already checked in for this race."
	}
	return err.Error()
}

func (s *Server) handleUpdateEntry(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx := r.Context()

	id, err := strconv.ParseInt(r.Form.Get("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which entry?"})
		return
	}
	existing, err := s.app.DB.Entry(ctx, id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown entry"})
		return
	}

	entry := existing.Entry
	if v := r.Form.Get("car_number"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			entry.CarNumber = n
		}
	}
	if r.Form.Has("car_name") {
		entry.CarName = strings.TrimSpace(r.Form.Get("car_name"))
	}
	if r.Form.Has("note") {
		entry.Note = strings.TrimSpace(r.Form.Get("note"))
	}
	if r.Form.Has("is_control") {
		entry.IsControl = r.Form.Get("is_control") == "true"
	}
	if r.Form.Has("excluded") {
		entry.Excluded = r.Form.Get("excluded") == "true"
		entry.ExclusionReason = strings.TrimSpace(r.Form.Get("reason"))
		if entry.Excluded && entry.ExclusionReason == "" {
			writeJSON(w, http.StatusBadRequest,
				map[string]string{"error": "an excluded car needs a reason"})
			return
		}
		if !entry.Excluded {
			entry.ExclusionReason = ""
		}
	}
	if v := r.Form.Get("photo_id"); v != "" {
		if pid, err := strconv.ParseInt(v, 10, 64); err == nil && pid > 0 {
			entry.PhotoID = &pid
		}
	}

	if err := s.app.DB.UpdateEntry(ctx, entry); err != nil {
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": checkinError(err, entry.CarNumber)})
		return
	}
	if entry.Excluded != existing.Excluded {
		_ = s.app.DB.Audit(ctx, "coordinator", "entry.exclude",
			"car "+strconv.Itoa(entry.CarNumber)+": "+entry.ExclusionReason)
	}
	s.app.Bus.Publish(bus.TopicRace, "checkin", map[string]any{"entry_id": id})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteEntry(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx := r.Context()

	id, err := strconv.ParseInt(r.Form.Get("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which entry?"})
		return
	}
	entry, err := s.app.DB.Entry(ctx, id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown entry"})
		return
	}

	// Removing a car that is already in the schedule would leave heats pointing
	// at nothing. Withdrawing after check-in closes is an exclusion, not a
	// deletion.
	if p, err := s.app.DB.Progress(ctx, entry.RaceID); err == nil && p.Total > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "the schedule is already built. Mark the car excluded instead of removing it.",
		})
		return
	}

	if err := s.app.DB.DeleteEntry(ctx, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.app.Bus.Publish(bus.TopicRace, "checkin", map[string]any{"removed": id})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleListRacers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	raceID := s.raceIDParam(r)
	if raceID == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"racers": []any{}})
		return
	}
	race, err := s.app.DB.Race(ctx, raceID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"racers": []any{}})
		return
	}
	racers, err := s.app.DB.Racers(ctx, race.SeasonID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	rows := make([]map[string]any, 0, len(racers))
	for _, rr := range racers {
		rows = append(rows, map[string]any{
			"id": rr.ID, "first_name": rr.FirstName, "last_name": rr.LastName,
			"name": rr.FullName(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"racers": rows})
}

// --- photos ------------------------------------------------------------------

func (s *Server) handleUploadPhoto(w http.ResponseWriter, r *http.Request) {
	// The camera posts a JPEG blob directly; a file picker posts multipart.
	var body = r.Body
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(maxUploadMemory); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		file, _, err := r.FormFile("photo")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no photo was sent"})
			return
		}
		defer file.Close()
		body = file
	}

	photo, err := s.app.SavePhoto(r.Context(), body)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, app.ErrUnsupportedImage) {
			status = http.StatusUnsupportedMediaType
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	// Attach straight away when the caller says which car it belongs to.
	if v := r.URL.Query().Get("entry_id"); v != "" {
		if entryID, err := strconv.ParseInt(v, 10, 64); err == nil && entryID > 0 {
			if err := s.app.DB.SetEntryPhoto(r.Context(), entryID, photo.ID); err != nil {
				s.app.Log.Warn("attaching photo", "entry", entryID, "err", err)
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id": photo.ID, "width": photo.Width, "height": photo.Height,
	})
}

// maxUploadMemory is how much of a multipart upload is buffered in memory
// before spilling to disk.
const maxUploadMemory = 8 << 20

func (s *Server) handleServePhoto(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}

	size := app.PhotoCard
	switch r.URL.Query().Get("size") {
	case "thumb":
		size = app.PhotoThumb
	case "full":
		size = app.PhotoFull
	}

	path, err := s.app.PhotoFile(r.Context(), id, size)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Photos are content-addressed, so a given id always yields the same bytes
	// and can be cached hard. This matters on a slideshow cycling 30 cars.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, path)
}
