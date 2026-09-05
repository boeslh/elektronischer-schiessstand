// ============================================================================
// preisanzeige – eigenständiger Anzeige-Prozess für Preisschiessen-Auswertungen
//
// Liest NUR den von server/preisschiessen_wertungen.go periodisch berechneten
// Ergebnis-Cache (ps_wertung_ergebnisse) sowie die Anzeige-Konfiguration
// (ps_anzeige_config) - führt selbst nie eine Berechnung aus. Dadurch ist
// häufiges Polling (mehrere Anzeige-Bildschirme, Reload alle paar Sekunden)
// unbedenklich, siehe Design-Kontext in .claude/plans/hidden-booping-duckling.md.
//
// Ein laufender Prozess bedient ALLE Preisschießen der DB gleichzeitig,
// ausgewählt über den Pfad (/ps/{id}/..., siehe site.go registerSiteRoutes) -
// die Startseite "/" listet sie zur Auswahl auf. Läuft unabhängig vom
// Hauptserver-Prozess (eigenes Go-Modul/Binary) - auf dem Server selbst oder
// jedem anderen Rechner mit Postgres-Zugriff auf dieselbe Datenbank startbar.
//
// Build:  go build -o preisanzeige .
// Start:  ./preisanzeige -config config.json
// ============================================================================
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	PostgresDSN string `json:"postgres_dsn"`
	ListenAddr  string `json:"listen_addr"`
	// WerbungDir: Basisverzeichnis für Werbebilder, siehe werbung.go.
	// Aufbau: <WerbungDir>/<preisschiessen_id>/{main,lists}/*.{jpg,jpeg,png,gif,webp}
	WerbungDir string `json:"werbung_dir"`
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8091"
	}
	if cfg.WerbungDir == "" {
		cfg.WerbungDir = "/opt/ps/bilder"
	}
	return &cfg, nil
}

func main() {
	configPath := flag.String("config", "config.json", "Pfad zur Konfigurationsdatei")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("FATAL Konfiguration (%s): %v", *configPath, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.PostgresDSN)
	if err != nil {
		log.Fatalf("FATAL DB: %v", err)
	}
	defer pool.Close()

	werbungBaseDir = cfg.WerbungDir

	mux := http.NewServeMux()
	registerSiteRoutes(mux, pool)
	srv := &http.Server{Addr: cfg.ListenAddr, Handler: mux}

	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	log.Printf("preisanzeige: DB %s, http://localhost%s", redactDSN(cfg.PostgresDSN), cfg.ListenAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("FATAL HTTP: %v", err)
	}
	log.Printf("Beendet.")
}

// redactDSN blendet ein evtl. enthaltenes Passwort für die Log-Ausgabe aus.
func redactDSN(dsn string) string {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return "***"
	}
	return cfg.ConnConfig.Host
}
