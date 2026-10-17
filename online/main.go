// ============================================================================
// schiessstand-online – Online-Anzeige im Internet fuer mehrere Vereine.
//
// Zeigt Preisschiessen (mit Historie) und Rundenwettkaempfe (Liste und
// Scheiben, live) an. Die Daten schickt der Schiessstand-Server eines
// Vereins selbst (server/online_sync.go): Die Verbindung geht immer vom
// Schiessstand ins Internet, per HTTPS mit einem API-Schluessel des Vereins.
// Dieser Dienst baut nie eine Verbindung zu einem Schiessstand auf und
// loescht nie selbststaendig Daten - geloescht wird nur von Hand in der
// Verwaltung (/admin).
//
// Uebertragen werden nur fertige Anzeigedaten (siehe publish.go): Namen,
// Verein und Ergebnisse der Teilnehmer bzw. Starter - keine Stammdaten.
//
// Start:   ./online -config /etc/schiessstand/online.json
// Admin:   ./online -config ... -create-admin <name>   (Passwort per stdin)
// ============================================================================
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/acme/autocert"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Config struct {
	// Datenbank des Online-Dienstes (eigene Datenbank, NICHT die des Schiessstands)
	DSN string `json:"postgres_dsn"`
	// Listen: Adresse fuer HTTPS (bzw. HTTP hinter einem Reverse-Proxy)
	Listen string `json:"listen_addr"`
	// TLS mit eigenem Zertifikat (PEM-Dateien) ...
	TLSCert string `json:"tls_cert"`
	TLSKey  string `json:"tls_key"`
	// ... oder automatisch per Let's Encrypt fuer diese Domains (dann
	// zusaetzlich Port 80 fuer die Pruefung, siehe AcmeHTTPListen).
	AcmeDomains    []string `json:"acme_domains"`
	AcmeCacheDir   string   `json:"acme_cache_dir"`
	AcmeEmail      string   `json:"acme_email"`
	AcmeHTTPListen string   `json:"acme_http_listen"`
	// BehindProxy: TLS endet an einem vorgeschalteten Reverse-Proxy (nginx,
	// Caddy); der Dienst selbst lauscht dann nur lokal per HTTP. Ohne TLS
	// und ohne diese Angabe startet der Dienst nicht.
	BehindProxy bool `json:"behind_proxy"`
	// Titel der Startseite
	SiteTitle string `json:"site_title"`
	// BasePath: Unterverzeichnis, unter dem die Anzeige erreichbar ist (z. B.
	// "/befreeshoot" hinter einem Apache, der mehrere Webseiten bedient);
	// leer = direkt unter "/".
	BasePath string `json:"base_path"`
}

func loadConfig(path string) (Config, error) {
	c := Config{Listen: ":8443", SiteTitle: "Schießstand online", AcmeCacheDir: "/var/lib/schiessstand-online/acme",
		AcmeHTTPListen: ":80"}
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if c.DSN == "" {
		return c, fmt.Errorf("postgres_dsn fehlt")
	}
	// Zertifikatspfade duerfen Umgebungsvariablen enthalten - z. B.
	// ${CREDENTIALS_DIRECTORY}/cert.pem, wenn systemd die Let's-Encrypt-
	// Dateien per LoadCredential= uebergibt (siehe schiessstand-online.service).
	c.TLSCert, c.TLSKey = os.ExpandEnv(c.TLSCert), os.ExpandEnv(c.TLSKey)
	c.BasePath = strings.TrimRight(strings.TrimSpace(c.BasePath), "/")
	if c.BasePath != "" && !strings.HasPrefix(c.BasePath, "/") {
		c.BasePath = "/" + c.BasePath
	}
	return c, nil
}

// version: beim Bauen gesetzt (build-release.sh, -ldflags "-X main.version=...")
var version = "entwicklung"

func main() {
	cfgPath := flag.String("config", "online.json", "Konfigurationsdatei")
	showVersion := flag.Bool("version", false, "Version anzeigen")
	createAdmin := flag.String("create-admin", "", "Plattform-Administrator anlegen bzw. Passwort setzen (Passwort per stdin)")
	migrateOnly := flag.Bool("migrate-only", false, "nur Datenbank-Migrationen ausfuehren")
	flag.Parse()
	if *showVersion {
		fmt.Println("schiessstand-online", version)
		return
	}

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("Konfiguration: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DSN)
	if err != nil {
		log.Fatalf("Datenbank: %v", err)
	}
	defer pool.Close()
	if err := migrate(ctx, pool); err != nil {
		log.Fatalf("Migrationen: %v", err)
	}
	if *migrateOnly {
		return
	}
	if *createAdmin != "" {
		fmt.Fprint(os.Stderr, "Passwort: ")
		pw, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		pw = strings.TrimRight(pw, "\r\n")
		if err := setPlatformAdmin(ctx, pool, *createAdmin, pw); err != nil {
			log.Fatalf("Administrator: %v", err)
		}
		fmt.Fprintf(os.Stderr, "\nAdministrator %q gespeichert.\n", *createAdmin)
		return
	}

	log.Printf("schiessstand-online %s", version)
	app := &App{cfg: cfg, pool: pool, throttle: newLoginThrottle()}
	go app.cleanupLoop(ctx)
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           app.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()

	switch {
	case len(cfg.AcmeDomains) > 0:
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(cfg.AcmeDomains...),
			Cache:      autocert.DirCache(cfg.AcmeCacheDir),
			Email:      cfg.AcmeEmail,
		}
		srv.TLSConfig = &tls.Config{GetCertificate: m.GetCertificate, MinVersion: tls.VersionTLS12,
			NextProtos: []string{"h2", "http/1.1", "acme-tls/1"}}
		go func() {
			// Port 80: Let's-Encrypt-Pruefung, sonst Umleitung auf HTTPS
			log.Printf("ACME/Umleitung auf %s", cfg.AcmeHTTPListen)
			if err := http.ListenAndServe(cfg.AcmeHTTPListen, m.HTTPHandler(nil)); err != nil {
				log.Printf("ACME-HTTP: %v", err)
			}
		}()
		log.Printf("HTTPS (Let's Encrypt %s) auf %s", strings.Join(cfg.AcmeDomains, ", "), cfg.Listen)
		err = srv.ListenAndServeTLS("", "")
	case cfg.TLSCert != "" && cfg.TLSKey != "":
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		log.Printf("HTTPS (Zertifikat %s) auf %s", cfg.TLSCert, cfg.Listen)
		err = srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
	case cfg.BehindProxy:
		log.Printf("HTTP hinter Reverse-Proxy auf %s", cfg.Listen)
		err = srv.ListenAndServe()
	default:
		log.Fatalf("Kein TLS konfiguriert: tls_cert/tls_key oder acme_domains angeben " +
			"(oder behind_proxy=true, wenn ein Reverse-Proxy HTTPS bereitstellt)")
	}
	if err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP: %v", err)
	}
}

// migrate: eingebettete SQL-Dateien in Namensreihenfolge, je Datei einmal.
func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		var done bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, n).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		sqlText, err := migrationsFS.ReadFile("migrations/" + n)
		if err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, string(sqlText)); err != nil {
			return fmt.Errorf("%s: %w", n, err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES ($1)`, n); err != nil {
			return err
		}
		log.Printf("Migration angewandt: %s", n)
	}
	return nil
}
