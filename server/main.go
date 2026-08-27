// ============================================================================
// Schiessstand-Server – zentrale "Workstation" (Meyton-Vorbild)
//
// Aufgaben:
//   - Standverwaltung: Staende anlegen/belegen/freigeben
//   - Sitzungssteuerung: Schuetze + Disziplin einem Stand zuweisen,
//     Session starten/beenden -> die Stand-PCs holen sich ihre aktive
//     Session per GET /api/lanes/{no}/session ab
//   - Live-Uebersicht aller Staende (SSE, gespeist aus pg_notify)
//   - Ergebnis-Abfragen ueber die Views des Datenmodells
//
// Bewusst NICHT hier: Treffererfassung/TDOA (macht der Stand-PC) und
// Schussprotokoll (liegt lokal am Stand). Der Server ist reine Verwaltung
// und Anzeige – faellt er aus, schiessen die Staende autark weiter.
//
// Build:  go build -o server .
// Start:  ./server -dsn "postgres://user:pass@host/db" -listen :8090
// ============================================================================
package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"
)

func main() {
	dsn := flag.String("dsn",
		"postgres://schiessstand:test@127.0.0.1/schiessstand",
		"PostgreSQL DSN")
	listen := flag.String("listen", ":8090", "HTTP Listen-Adresse")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := NewStore(ctx, *dsn)
	if err != nil {
		log.Fatalf("FATAL DB: %v", err)
	}
	defer store.Close()

	live := NewLiveHub()
	go live.RunListener(ctx, *dsn) // pg LISTEN shot_fired -> SSE

	srv := NewAPIServer(store, live, *listen)
	log.Printf("Server: http://localhost%s", *listen)
	if err := srv.Run(ctx); err != nil {
		log.Fatalf("FATAL HTTP: %v", err)
	}
	log.Printf("Beendet.")
}
