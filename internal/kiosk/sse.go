package kiosk

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// sseSnapshot is the first frame sent on /events so clients render without
// a separate status fetch.
type sseSnapshot struct {
	Type     string        `json:"type"`
	Statuses []HarnessView `json:"statuses"`
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(v any) bool {
		data, err := json.Marshal(v)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if !send(sseSnapshot{Type: "snapshot", Statuses: s.ctrl.Statuses(r.Context())}) {
		return
	}

	events, cancel := s.ctrl.Subscribe()
	defer cancel()

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case ev, ok := <-events:
			if !ok {
				return
			}
			if !send(ev) {
				return
			}
		}
	}
}
