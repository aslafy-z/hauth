package kiosk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/coder/websocket"
)

var errSessionNotFound = errors.New("session not found")

// wsControl is the only text frame the client may send: a resize request.
// Everything else on the socket is raw pty bytes in binary frames.
type wsControl struct {
	Type string `json:"type"`
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

func (s *Server) handleSessionWS(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.ctrl.Attach(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, errSessionNotFound)
		return
	}
	// Origin was already validated by the middleware; the library check
	// would only re-reject what we allow (tunneled loopback origins).
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusInternalError, "closed")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	output, unsubscribe := sess.Subscribe()
	defer unsubscribe()

	// Client -> pty.
	go func() {
		defer cancel()
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			switch typ {
			case websocket.MessageBinary:
				if _, err := sess.Write(data); err != nil {
					return
				}
			case websocket.MessageText:
				var ctl wsControl
				if json.Unmarshal(data, &ctl) == nil && ctl.Type == "resize" {
					_ = sess.Resize(ctl.Rows, ctl.Cols)
				}
			}
		}
	}()

	// pty -> client: ring replay first (Subscribe guarantees it), then live.
	for {
		select {
		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "")
			return
		case <-sess.Done():
			// Drain whatever the subscription still buffers, then close.
			for {
				select {
				case chunk, ok := <-output:
					if !ok {
						conn.Close(websocket.StatusNormalClosure, "session ended")
						return
					}
					if conn.Write(ctx, websocket.MessageBinary, chunk) != nil {
						return
					}
				default:
					conn.Close(websocket.StatusNormalClosure, "session ended")
					return
				}
			}
		case chunk, ok := <-output:
			if !ok {
				conn.Close(websocket.StatusNormalClosure, "")
				return
			}
			if err := conn.Write(ctx, websocket.MessageBinary, chunk); err != nil {
				return
			}
		}
	}
}
