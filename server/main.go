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

	"github.com/jackc/pgx/v5/pgxpool"
)

// redactDSN blendet ein evtl. enthaltenes Passwort fuer die Log-Ausgabe aus.
func redactDSN(dsn string) string {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return "DSN: ***"
	}
	return "Host " + cfg.ConnConfig.Host
}

func main() {
	dsn := flag.String("dsn",
		"postgres://schiessstand:test@127.0.0.1/schiessstand",
		"PostgreSQL DSN")
	listen := flag.String("listen", ":8090", "HTTP Listen-Adresse")
	backupDir := flag.String("backup-dir", "../db-backups",
		"Verzeichnis fuer DB-Backups (Import/Export-Kachel, admin-only)")
	workerOnly := flag.Bool("worker-only", false,
		"Nur Preisschiessen-Auswertung im Hintergrund berechnen, kein HTTP-Server/UI/Standverwaltung. "+
			"Zum horizontalen Skalieren der Auswertungslast: beliebig viele Instanzen mit derselben -dsn "+
			"auf verschiedenen Rechnern starten, siehe preisschiessen_wertungen.go.")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := NewStore(ctx, *dsn)
	if err != nil {
		log.Fatalf("FATAL DB: %v", err)
	}
	defer store.Close()

	if *workerOnly {
		log.Printf("Auswertung-Worker (keine UI/HTTP): %s", redactDSN(*dsn))
		RunAuswertungScheduler(ctx, store.pool)
		log.Printf("Beendet.")
		return
	}

	live := NewLiveHub()
	go live.RunListener(ctx, *dsn) // pg LISTEN shot_fired -> SSE
	go RunAuswertungScheduler(ctx, store.pool)

	srv := NewAPIServer(store, live, *listen, *dsn, *backupDir)
	log.Printf("Server: http://localhost%s", *listen)
	if err := srv.Run(ctx); err != nil {
		log.Fatalf("FATAL HTTP: %v", err)
	}
	log.Printf("Beendet.")
}
