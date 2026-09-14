package web

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/davisc01/derbyandales/internal/timer"
)

// Timeouts for the interactive checks. Each is generous enough for someone to
// walk to the track and back, and short enough that a wedged check gives up
// rather than hanging the page.
const (
	gateWatchTimeout = 60 * time.Second
	laneCheckTimeout = 60 * time.Second
	testHeatTimeout  = 120 * time.Second
	benchRunTimeout  = 30 * time.Second
)

func (s *Server) timerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /timer/test", s.handleTimerBench)

	mux.HandleFunc("GET /api/timer/status", s.handleTimerStatus)
	mux.HandleFunc("GET /api/timer/ports", s.handleTimerPorts)
	mux.HandleFunc("POST /api/timer/connect", s.handleTimerConnect)
	mux.HandleFunc("POST /api/timer/disconnect", s.handleTimerDisconnect)

	mux.HandleFunc("POST /api/timer/bench", s.handleTimerBenchRun)
	mux.HandleFunc("POST /api/timer/bench/gate", s.handleTimerGate)
	mux.HandleFunc("POST /api/timer/bench/lane", s.handleTimerLane)
	mux.HandleFunc("POST /api/timer/bench/heat", s.handleTimerHeat)
	mux.HandleFunc("POST /api/timer/bench/override", s.handleTimerOverride)

	// Simulator stand-ins for the person at the track, so the bench can be
	// rehearsed with no hardware present.
	mux.HandleFunc("POST /api/timer/sim/gate", s.handleSimGate)
	mux.HandleFunc("POST /api/timer/sim/car", s.handleSimCar)

	mux.HandleFunc("GET /api/timer/trace", s.handleTimerTrace)
	mux.HandleFunc("POST /api/timer/trace/save", s.handleTimerTraceSave)
	mux.HandleFunc("POST /api/timer/send", s.handleTimerSend)
}

func (s *Server) handleTimerBench(w http.ResponseWriter, r *http.Request) {
	ports, err := s.app.Timer.Ports()
	if err != nil {
		s.app.Log.Warn("listing serial ports", "err", err)
	}
	s.render(w, r, "timer.html", pageData{
		Title:  "Timer Test",
		Active: "timer",
		Data: map[string]any{
			"Status":   s.app.Timer.Status(),
			"Ports":    ports,
			"Profiles": timer.Profiles(),
			"SimKey":   timer.SimulatorKey,
			"MaxAge":   timer.MaxBenchAge,
			"Lanes":    s.app.Timer.LaneNumbers(r.Context()),
		},
	})
}

func (s *Server) handleTimerStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.app.Timer.Status())
}

func (s *Server) handleTimerPorts(w http.ResponseWriter, r *http.Request) {
	ports, err := s.app.Timer.Ports()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ports)
}

func (s *Server) handleTimerConnect(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	port := r.Form.Get("port")
	profile := r.Form.Get("profile")
	if profile == "" {
		profile = timer.FastTrack().Key
	}
	if err := s.app.Timer.Connect(r.Context(), port, profile); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.app.Timer.Status())
}

func (s *Server) handleTimerDisconnect(w http.ResponseWriter, r *http.Request) {
	s.app.Timer.Disconnect()
	writeJSON(w, http.StatusOK, s.app.Timer.Status())
}

func (s *Server) handleTimerBenchRun(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), benchRunTimeout)
	defer cancel()

	result, err := s.app.Timer.RunBench(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleTimerGate(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), gateWatchTimeout+10*time.Second)
	defer cancel()

	check, err := s.app.Timer.WatchGate(ctx, gateWatchTimeout)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, check)
}

func (s *Server) handleTimerLane(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	lane, err := strconv.Atoi(r.Form.Get("lane"))
	if err != nil || lane < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which lane?"})
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), laneCheckTimeout+10*time.Second)
	defer cancel()

	check, err := s.app.Timer.CheckLaneMapping(ctx, lane, laneCheckTimeout)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, check)
}

func (s *Server) handleTimerHeat(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), testHeatTimeout+10*time.Second)
	defer cancel()

	check, err := s.app.Timer.RunTestHeat(ctx, testHeatTimeout)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, check)
}

func (s *Server) handleTimerOverride(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	reason := r.Form.Get("reason")
	if err := s.app.Timer.Override(r.Context(), "coordinator", reason); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.app.Timer.Status())
}

func (s *Server) handleTimerTrace(w http.ResponseWriter, r *http.Request) {
	trace := s.app.Timer.Trace()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if trace == nil || trace.Len() == 0 {
		w.Write([]byte("Nothing recorded yet. Connect a timer and run the test.\n"))
		return
	}
	w.Write([]byte(trace.String()))
}

func (s *Server) handleTimerTraceSave(w http.ResponseWriter, r *http.Request) {
	path, err := s.app.Timer.SaveTrace()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

// handleSimGate opens or closes the simulated start gate.
func (s *Server) handleSimGate(w http.ResponseWriter, r *http.Request) {
	sim := s.app.Timer.Simulator()
	if sim == nil {
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "the simulated timer is not connected"})
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if r.Form.Get("closed") == "true" {
		sim.CloseGate()
	} else {
		sim.OpenGate()
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSimCar sends one simulated car down one lane, which is what the lane
// mapping check is waiting for.
func (s *Server) handleSimCar(w http.ResponseWriter, r *http.Request) {
	sim := s.app.Timer.Simulator()
	if sim == nil {
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "the simulated timer is not connected"})
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	lane, err := strconv.Atoi(r.Form.Get("lane"))
	if err != nil || lane < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which lane?"})
		return
	}
	sim.EmitSingleLane(lane, 2.45)
	writeJSON(w, http.StatusOK, map[string]int{"lane": lane})
}

// handleTimerSend writes an arbitrary command, for the diagnostic console.
func (s *Server) handleTimerSend(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	dev := s.app.Timer.Device()
	if dev == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no timer connected"})
		return
	}
	cmd := r.Form.Get("command")
	if cmd == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no command given"})
		return
	}
	if err := dev.Send(cmd); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	_ = s.app.DB.Audit(r.Context(), "coordinator", "timer.send", cmd)
	writeJSON(w, http.StatusOK, map[string]string{"sent": cmd})
}
