package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// eventsHandler upgrades to a WebSocket and starts streaming events
// from the agent's event bus. The connection is kept open until the
// client closes it or the context is cancelled.
func eventsHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.EventBus == nil {
			writeError(w, http.StatusServiceUnavailable, "no_events", "event bus not configured")
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			d.Log.Warn("ws upgrade failed", slog.String("error", err.Error()))
			return
		}
		defer conn.Close()

		// Set a generous initial deadline; each write resets it.
		conn.SetReadLimit(256)
		_ = conn.SetReadDeadline(time.Now().Add(24 * time.Hour))

		id, ch := d.EventBus.Subscribe(64)
		defer d.EventBus.Unsubscribe(id)

		d.Log.Info("ws client connected", slog.String("sub_id", id))

		// Drain routine: read pings from client so we can detect
		// disconnection.
		go func() {
			for {
				if _, _, err := conn.NextReader(); err != nil {
					return
				}
			}
		}()

		// Write routine: stream events.
		for evt := range ch {
			_ = conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
			b, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
				d.Log.Info("ws write failed", slog.String("sub_id", id), slog.String("error", err.Error()))
				return
			}
		}
	}
}
