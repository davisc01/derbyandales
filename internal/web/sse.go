package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/davisc01/derbyandales/internal/bus"
)

// heartbeatInterval keeps intermediaries and sleeping tablets from dropping an
// idle SSE connection. It is a comment frame, so clients ignore it.
const heartbeatInterval = 20 * time.Second

// handleEvents streams bus events to one browser.
//
// A display opens this once and stays connected for the night. Reconnection is
// handled by the browser's own EventSource retry, so an unplugged HDMI stick or
// a wifi blip recovers on its own.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	topics := parseTopics(r.URL.Query().Get("topics"))

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	// Without this, any proxy that buffers would hold results back until the
	// connection closed — i.e. forever.
	h.Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)

	// Tell the client how long to wait before reconnecting, then flush so the
	// EventSource `open` event fires immediately rather than on first data.
	fmt.Fprint(w, "retry: 2000\n\n")
	if err := rc.Flush(); err != nil {
		return
	}

	events, cancel := s.app.Bus.Subscribe(topics...)
	defer cancel()

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return

		case ev, ok := <-events:
			if !ok {
				return
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				s.app.Log.Error("marshal event", "topic", ev.Topic, "kind", ev.Kind, "err", err)
				continue
			}
			// Name the SSE event after the topic so a page can listen for just
			// the streams it cares about.
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Topic, payload)
			if err := rc.Flush(); err != nil {
				return
			}

		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}

// parseTopics turns "race,timer" into topics. An empty value subscribes to
// everything.
func parseTopics(s string) []bus.Topic {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []bus.Topic
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, bus.Topic(part))
		}
	}
	return out
}

// writeJSON is the single JSON response path.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already sent, so all we can do is stop.
		return
	}
}
