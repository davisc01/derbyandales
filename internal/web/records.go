package web

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/records"
	"github.com/davisc01/derbyandales/internal/scoring"
	"github.com/davisc01/derbyandales/internal/store"
)

func (s *Server) recordRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /records", s.handleRecords)
	mux.HandleFunc("GET /api/records", s.handleRecordsAPI)
	mux.HandleFunc("GET /api/race/wrapup", s.handleWrapUp)
}

// handleRecords is the record book: the records, how each fell, the
// leaderboards, careers and everybody's personal best.
func (s *Server) handleRecords(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	book, err := s.app.Records(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	races, runs, _ := s.app.DB.ArchiveRaces(ctx)
	ft, denom := s.clubScale(ctx)

	// Most recent first: the latest record is the one people ask about.
	runHistory := reversed(book.RunHistory)
	avgHistory := reversedResults(book.AverageHistory)

	career := book.Career
	for i, c := range career {
		if c.Wins == 0 && c.Podiums == 0 && c.Cups == 0 {
			career = career[:i]
			break
		}
	}

	s.render(w, r, "records.html", pageData{
		Title:  "Records",
		Active: "records",
		Data: map[string]any{
			"Book":          book,
			"RunHistory":    runHistory,
			"AvgHistory":    avgHistory,
			"Career":        career,
			"ArchiveRaces":  races,
			"ArchiveRuns":   runs,
			"TrackLengthFt": ft,
			"ScaleDenom":    denom,
		},
	})
}

// handleRecordsAPI is the records scene: the headline records, the lanes, and
// the leading careers — what fits on a TV across a room.
func (s *Server) handleRecordsAPI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	book, err := s.app.Records(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	ft, denom := s.clubScale(ctx)
	runJSON := func(run records.Run) map[string]any {
		return map[string]any{
			"time":       scoring.FormatTime(run.Time),
			"mph":        scoring.FormatMPH(scoring.ScaleMPH(ft, denom, run.Time)),
			"driver":     run.Driver,
			"car_name":   run.CarName,
			"car_number": run.CarNumber,
			"race":       run.Race.Label(),
			"lane":       run.Lane,
		}
	}

	out := map[string]any{"demo": book.Demo}
	if book.FastestRun != nil {
		out["fastest_run"] = runJSON(*book.FastestRun)
	}
	if a := book.FastestAverage; a != nil {
		out["fastest_average"] = map[string]any{
			"time":       scoring.FormatAverage(a.Average),
			"mph":        scoring.FormatMPH(scoring.ScaleMPH(ft, denom, a.Average)),
			"driver":     a.Driver,
			"car_name":   a.CarName,
			"car_number": a.CarNumber,
			"race":       a.Race.Label(),
		}
	}
	lanes := make([]map[string]any, 0, len(book.Lanes))
	for _, l := range book.Lanes {
		lanes = append(lanes, runJSON(l))
	}
	out["lanes"] = lanes

	career := make([]map[string]any, 0, 5)
	for _, c := range book.Career {
		if len(career) == 5 || c.Wins == 0 {
			break
		}
		career = append(career, map[string]any{
			"driver": c.Driver, "wins": c.Wins, "podiums": c.Podiums, "cups": c.Cups,
		})
	}
	out["career"] = career
	writeJSON(w, http.StatusOK, out)
}

// noticeJSON describes a broken record for a display: what it was, and what it
// beat, because "new track record" means more with the old one beside it.
func noticeJSON(n records.Notice, lane int) map[string]any {
	out := map[string]any{"kind": string(n.Kind)}
	switch n.Kind {
	case records.TrackRecord:
		out["label"] = "New track record"
	case records.LaneRecord:
		out["label"] = fmt.Sprintf("Lane %d record", lane)
	case records.PersonalBest:
		out["label"] = "Personal best"
	case records.AverageRecord:
		out["label"] = "New record average"
	}
	switch n.Kind {
	case records.PersonalBest:
		// It is the same person's; their name would be noise.
		out["previous"] = fmt.Sprintf("was %s, %s", scoring.FormatTime(n.Previous.Time), n.Previous.Race.Label())
	case records.AverageRecord:
		p := n.PreviousAverage
		out["previous"] = fmt.Sprintf("was %s, %s, %s",
			scoring.FormatAverage(p.Average), p.Driver, p.Race.Label())
	default:
		p := n.Previous
		out["previous"] = fmt.Sprintf("was %s, %s, %s",
			scoring.FormatTime(p.Time), p.Driver, p.Race.Label())
	}
	return out
}

// clubScale is the track the scale speeds are quoted for: the newest season's.
//
// The track has not changed in any way that matters, so records are compared
// on time and one figure converts all of them. The archive's own MPH columns
// cannot be used: the timer was set to 30.7 ft until 2022 and about 26.5 ft
// for two years after, so the same run is three different speeds there.
func (s *Server) clubScale(ctx context.Context) (float64, int) {
	seasons, err := s.app.DB.Seasons(ctx)
	if err != nil || len(seasons) == 0 || seasons[0].TrackLengthFt <= 0 || seasons[0].ScaleDenom <= 0 {
		return 28, 25
	}
	return seasons[0].TrackLengthFt, seasons[0].ScaleDenom
}

func reversed(in []records.Run) []records.Run {
	out := make([]records.Run, len(in))
	for i, r := range in {
		out[len(in)-1-i] = r
	}
	return out
}

func reversedResults(in []records.Result) []records.Result {
	out := make([]records.Result, len(in))
	for i, r := range in {
		out[len(in)-1-i] = r
	}
	return out
}

// handleWrapUp is the night in review, for the end of the evening: what the
// track did, the highlights, and what records fell.
func (s *Server) handleWrapUp(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	raceID := s.raceIDParam(r)
	if raceID == 0 {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	race, err := s.app.DB.Race(ctx, raceID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such race"})
		return
	}
	runs, err := s.app.DB.NightRuns(ctx, raceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	entries, _ := s.app.DB.Entries(ctx, raceID)
	byID := make(map[int64]store.EntryView, len(entries))
	for _, e := range entries {
		byID[e.ID] = e
	}
	car := func(id int64) map[string]any {
		e := byID[id]
		return map[string]any{"car_number": e.CarNumber, "car_name": e.CarName, "driver": e.FullName()}
	}
	season := s.seasonFor(r, raceID)
	mph := func(t float64) string {
		return scoring.FormatMPH(scoring.ScaleMPH(season.TrackLengthFt, season.ScaleDenom, t))
	}

	night := scoring.Night(runs)
	out := map[string]any{
		"race":  race.Name,
		"heats": night.Heats,
		"runs":  night.Runs,
		"dnfs":  night.DNFs,
	}

	lanes := make([]map[string]any, 0, len(night.Lanes))
	for _, l := range night.Lanes {
		row := map[string]any{"lane": l.Lane, "runs": l.Runs, "wins": l.Wins, "dnfs": l.DNFs,
			"expected": strconv.FormatFloat(l.Expected, 'f', 1, 64), "unusual": l.Unusual}
		if l.OneIn > 0 {
			row["one_in"] = strconv.FormatFloat(l.OneIn, 'f', 0, 64)
		}
		if l.Average > 0 {
			row["average"] = scoring.FormatAverage(l.Average)
		}
		lanes = append(lanes, row)
	}
	out["lanes"] = lanes

	fastest := make([]map[string]any, 0, len(night.FastestHeats))
	for _, h := range night.FastestHeats {
		row := car(h.EntryID)
		row["heat"], row["lane"] = h.Heat, h.Lane
		row["time"], row["mph"] = scoring.FormatTime(h.Time), mph(h.Time)
		fastest = append(fastest, row)
	}
	out["fastest_heats"] = fastest

	if c := night.Closest; c != nil {
		out["closest"] = map[string]any{
			"heat": c.Heat, "gap": scoring.FormatTime(c.Gap),
			"winner": car(c.Winner), "runner_up": car(c.RunnerUp),
		}
	}
	if sp := night.Steadiest; sp != nil {
		row := car(sp.EntryID)
		row["gap"], row["best"], row["worst"] =
			scoring.FormatTime(sp.Gap), scoring.FormatTime(sp.Best), scoring.FormatTime(sp.Worst)
		out["steadiest"] = row
	}

	// The winning margin is between the two best cars that count. The pace car
	// is ranked in the standings, and would otherwise be somebody's rival.
	if !race.Bracket() {
		if standings, err := s.app.DB.Standings(ctx, raceID); err == nil {
			var top []store.Standing
			for _, st := range standings {
				if !st.Entry.IsControl && st.Heats > 0 {
					top = append(top, st)
				}
				if len(top) == 2 {
					break
				}
			}
			if len(top) == 2 {
				out["margin"] = map[string]any{
					"gap":    scoring.FormatAverage(top[1].Average - top[0].Average),
					"winner": top[0].Entry.FullName(), "runner_up": top[1].Entry.FullName(),
				}
			}
		}
	}

	// What fell tonight. Track and lane records and record averages are each
	// named; personal bests are counted, because a good night has a dozen.
	broken, averages, err := s.app.NightRecords(ctx, raceID)
	if err != nil {
		s.app.Log.Warn("checking the night for records failed", "race", raceID, "err", err)
	}
	var named []map[string]any
	var pbs []string
	// A racer with two cars is one person with one personal best.
	seen := map[string]bool{}
	heats, _ := s.app.DB.Heats(ctx, raceID)
	who := map[[2]int]int64{}
	for _, h := range heats {
		for _, l := range h.Lanes {
			if l.EntryID != nil {
				who[[2]int{h.Number, l.Lane}] = *l.EntryID
			}
		}
	}
	for _, b := range broken {
		id := who[[2]int{b.Heat, b.Lane}]
		if b.Kind == records.PersonalBest {
			if name := byID[id].FullName(); !seen[name] {
				seen[name] = true
				pbs = append(pbs, name)
			}
			continue
		}
		row := noticeJSON(b.Notice, b.Lane)
		row["who"] = car(id)
		row["time"] = scoring.FormatTime(timeOf(heats, b.Heat, b.Lane))
		row["heat"] = b.Heat
		named = append(named, row)
	}
	for _, e := range entries {
		if n, ok := averages[e.CarNumber]; ok && !e.IsControl && !e.Excluded {
			row := noticeJSON(n, 0)
			row["who"] = car(e.ID)
			named = append(named, row)
		}
	}
	out["records"] = named
	out["personal_bests"] = pbs
	out["timer"] = s.timerHealth(ctx, raceID)
	if top := s.topRaces(ctx, raceID); top != nil {
		out["top_races"] = top
	}
	if c := s.controlReview(ctx, raceID); c != nil {
		out["control"] = c
	}
	writeJSON(w, http.StatusOK, out)
}

// timerHealth is the night's trouble, heat by heat: what was re-run, typed in,
// misread or triggered with nothing on the track, and any heat still flagged
// as a fault. A night with none of it says so, which is worth knowing too.
func (s *Server) timerHealth(ctx context.Context, raceID int64) map[string]any {
	events, err := s.app.DB.HeatEvents(ctx, raceID)
	if err != nil {
		s.app.Log.Warn("reading the heat events failed", "race", raceID, "err", err)
	}
	type tally struct {
		heats []int
		seen  map[int]bool
		lanes map[int]int
	}
	kinds := map[store.HeatEventKind]*tally{}
	for _, e := range events {
		t := kinds[e.Kind]
		if t == nil {
			t = &tally{seen: map[int]bool{}, lanes: map[int]int{}}
			kinds[e.Kind] = t
		}
		if !t.seen[e.Heat] {
			t.seen[e.Heat] = true
			t.heats = append(t.heats, e.Heat)
		}
		for _, l := range e.Lanes {
			t.lanes[l]++
		}
	}
	item := func(k store.HeatEventKind) map[string]any {
		t := kinds[k]
		if t == nil {
			return map[string]any{"count": 0}
		}
		out := map[string]any{"count": len(t.heats), "heats": t.heats}
		if len(t.lanes) > 0 {
			out["lanes"] = t.lanes
		}
		return out
	}

	out := map[string]any{
		"reruns":         item(store.HeatReRun),
		"bad_reads":      item(store.HeatBadRead),
		"typed":          item(store.HeatManual),
		"false_triggers": item(store.HeatFalseTrigger),
	}
	// A heat still flagged as every car running its slowest or fastest is a
	// fault nobody has re-run.
	var flagged []int
	if found, err := s.app.DB.HeatAnomalies(ctx, raceID); err == nil {
		for _, a := range found {
			if a.Clear {
				flagged = append(flagged, a.Heat)
			}
		}
	}
	out["flagged"] = flagged
	return out
}

// controlNights is how many earlier nights tonight's CONTROL car is compared
// with: about a season. Further back the car itself was different — the
// archive has it three quarters of a second slower in 2021 — and "usual" would
// mean nothing.
const controlNights = 6

// controlReview is the CONTROL car tonight against its recent nights. The same
// car on the same track should run the same times, so it is the one measure of
// the track rather than of the racers: a CONTROL car a tenth slower than usual
// is a track or a timer that needs looking at.
func (s *Server) controlReview(ctx context.Context, raceID int64) map[string]any {
	r, err := s.app.DB.Race(ctx, raceID)
	if err != nil {
		return nil
	}
	season, err := s.app.DB.Season(ctx, r.SeasonID)
	if err != nil {
		return nil
	}
	race := records.Race{Year: season.Year, Championship: r.Kind == model.RaceChampionship, Number: r.Number}
	nights, err := s.app.DB.ControlNights(ctx, s.app.DemoLoaded(ctx))
	if err != nil {
		s.app.Log.Warn("reading the CONTROL car's nights failed", "err", err)
		return nil
	}
	var tonight *store.ControlNight
	var before []store.ControlNight
	for i, n := range nights {
		switch {
		case n.Race == race:
			tonight = &nights[i]
		case n.Race.Before(race):
			before = append(before, n)
		}
	}
	if tonight == nil || tonight.Average <= 0 {
		return nil
	}
	if len(before) > controlNights {
		before = before[len(before)-controlNights:]
	}

	out := map[string]any{
		"car_name": tonight.CarName,
		"average":  scoring.FormatAverage(tonight.Average),
		"dnfs":     tonight.DNFs,
	}
	if tonight.Best > 0 {
		out["best"] = scoring.FormatTime(tonight.Best)
		out["worst"] = scoring.FormatTime(tonight.Worst)
		out["spread"] = scoring.FormatTime(tonight.Worst - tonight.Best)
	}
	recent := make([]map[string]any, 0, len(before))
	avgs := make([]float64, 0, len(before))
	for _, n := range before {
		recent = append(recent, map[string]any{"race": n.Race.Label(), "average": scoring.FormatAverage(n.Average)})
		avgs = append(avgs, n.Average)
	}
	out["recent"] = recent
	if len(avgs) > 0 {
		// The median, so one night with a wheel coming loose does not become
		// the usual.
		sort.Float64s(avgs)
		usual := avgs[len(avgs)/2]
		if len(avgs)%2 == 0 {
			usual = (avgs[len(avgs)/2-1] + avgs[len(avgs)/2]) / 2
		}
		diff := tonight.Average - usual
		out["usual"] = scoring.FormatAverage(usual)
		out["nights"] = len(avgs)
		switch {
		case diff > 0.0005:
			out["versus"] = scoring.FormatAverage(diff) + "s slower than usual"
		case diff < -0.0005:
			out["versus"] = scoring.FormatAverage(-diff) + "s faster than usual"
		default:
			out["versus"] = "the same as usual"
		}
	}
	return out
}

func timeOf(heats []store.HeatView, heat, lane int) float64 {
	for _, h := range heats {
		if h.Number != heat {
			continue
		}
		for _, l := range h.Lanes {
			if l.Lane == lane && l.FinishTime != nil {
				return *l.FinishTime
			}
		}
	}
	return 0
}

// topRaces is the club's fastest race nights and where tonight landed among
// them. Only a finished night is ranked: half a night's average is not the
// night's.
func (s *Server) topRaces(ctx context.Context, raceID int64) map[string]any {
	d, err := s.app.DB.RecordData(ctx, s.app.DemoLoaded(ctx))
	if err != nil {
		s.app.Log.Warn("ranking the race nights failed", "err", err)
		return nil
	}
	speeds := records.RaceSpeeds(d.Runs)
	if len(speeds) == 0 {
		return nil
	}
	tonight, known := d.Races[raceID]
	progress, _ := s.app.DB.Progress(ctx, raceID)
	finished := known && progress.Total > 0 && progress.Completed >= progress.Total

	row := func(r records.RaceSpeed) map[string]any {
		return map[string]any{
			"rank":    r.Rank,
			"race":    r.Race.Label(),
			// Always three places: in a ranking, 2.46 beside 2.461 reads as
			// the faster of the two.
			"average": strconv.FormatFloat(r.Average, 'f', 3, 64),
			"tonight": finished && r.Race == tonight,
		}
	}
	top := make([]map[string]any, 0, 5)
	for _, r := range speeds {
		if len(top) == 5 {
			break
		}
		top = append(top, row(r))
	}
	out := map[string]any{"top": top, "of": len(speeds)}
	switch {
	case !known:
	case tonight.Championship:
		out["this_race"] = map[string]any{"championship": true}
	case finished:
		for _, r := range speeds {
			if r.Race == tonight {
				out["this_race"] = row(r)
			}
		}
	}
	return out
}
