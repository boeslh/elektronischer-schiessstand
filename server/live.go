// ============================================================================
// live.go – Live-Verteilung neuer Schuesse an alle Browser
//
// PostgreSQL LISTEN 'shot_fired' (Trigger aus 002_notify.sql) -> SSE.
// Kein Polling: Der Stand-PC schreibt den Schuss in die DB, der Trigger
// feuert pg_notify, der Server verteilt sofort an alle Clients
// (Aufsichts-UI, spaeter Zuschauer-Displays).
// ============================================================================
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

type LiveHub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func NewLiveHub() *LiveHub {
	return &LiveHub{clients: make(map[chan []byte]struct{})}
}

// RunListener haelt eine dedizierte LISTEN-Verbindung (mit Reconnect)
func (h *LiveHub) RunListener(ctx context.Context, dsn string) {
	for {
		if err := h.listenOnce(ctx, dsn); err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				log.Printf("LISTEN: %v – Reconnect in 3s", err)
				time.Sleep(3 * time.Second)
			}
		} else {
			return // ctx beendet
		}
	}
}

func (h *LiveHub) listenOnce(ctx context.Context, dsn string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx, "LISTEN shot_fired"); err != nil {
		return err
	}
	log.Printf("LISTEN shot_fired aktiv")

	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		h.broadcast([]byte(n.Payload))
	}
}

func (h *LiveHub) broadcast(data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- data:
		default:
		}
	}
}

func (h *LiveHub) HandleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE nicht unterstuetzt", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	ch := make(chan []byte, 32)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients, ch)
		h.mu.Unlock()
	}()

	keepAlive := time.NewTicker(20 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepAlive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case data := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
