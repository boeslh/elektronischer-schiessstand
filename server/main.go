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
	"encoding/hex"
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
	migrationsDir := flag.String("migrations-dir", "migrations",
		"Verzeichnis der Migrationsdateien (fuer automatischen Nachzug nach Full Restore, siehe migrations.go)")
	workerOnly := flag.Bool("worker-only", false,
		"Nur Preisschiessen-Auswertung im Hintergrund berechnen, kein HTTP-Server/UI/Standverwaltung. "+
			"Zum horizontalen Skalieren der Auswertungslast: beliebig viele Instanzen mit derselben -dsn "+
			"auf verschiedenen Rechnern starten, siehe preisschiessen_wertungen.go.")
	migrateOnly := flag.Bool("migrate-only", false,
		"Nur fehlende Migrationen anwenden (siehe migrations.go ApplyPendingMigrations) und beenden - "+
			"kein HTTP-Server. Fuer install-service.sh/install.sh, damit Erstinstallation UND Update "+
			"denselben versionsverfolgten Mechanismus nutzen wie der Full-Restore-Nachzug in backup.go.")
	wertmaschinePSKHex := flag.String("wertmaschine-psk-hex", "",
		"Pre-Shared-Key (Hex) fuer die /api/wertmaschine/*-Endpunkte, gleiches HMAC-Schema wie "+
			"ESP32<->StandPC (standpc/devicelink.go). Leer = Feature aus (Endpunkte lehnen jede Anfrage ab).")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := NewStore(ctx, *dsn)
	if err != nil {
		log.Fatalf("FATAL DB: %v", err)
	}
	defer store.Close()

	if *migrateOnly {
		applied, err := ApplyPendingMigrations(ctx, store.pool, *migrationsDir)
		if err != nil {
			log.Fatalf("FATAL Migrationen: %v", err)
		}
		if len(applied) == 0 {
			log.Printf("Datenbank bereits auf aktuellem Stand, keine Migration noetig.")
		} else {
			log.Printf("%d Migration(en) angewandt: %v", len(applied), applied)
		}
		return
	}

	if *workerOnly {
		log.Printf("Auswertung-Worker (keine UI/HTTP): %s", redactDSN(*dsn))
		RunAuswertungScheduler(ctx, store.pool)
		log.Printf("Beendet.")
		return
	}

	live := NewLiveHub()
	go live.RunListener(ctx, *dsn) // pg LISTEN shot_fired -> SSE
	go RunAuswertungScheduler(ctx, store.pool)

	wertmaschinePSK, err := hex.DecodeString(*wertmaschinePSKHex)
	if err != nil {
		log.Fatalf("FATAL: -wertmaschine-psk-hex ungueltig (muss Hex sein): %v", err)
	}
	if len(wertmaschinePSK) == 0 {
		log.Printf("WARNUNG: wertmaschine_psk_hex nicht konfiguriert - /api/wertmaschine/* lehnt jede Anfrage ab")
	}

	srv := NewAPIServer(store, live, *listen, *dsn, *backupDir, *migrationsDir, wertmaschinePSK)
	log.Printf("Server: http://localhost%s", *listen)
	if err := srv.Run(ctx); err != nil {
		log.Fatalf("FATAL HTTP: %v", err)
	}
	log.Printf("Beendet.")
}
