// ============================================================================
// wertmaschine – eigenstaendiger Dienst fuer die Disag-Wertmaschine
// (RM III legacy oder "RM III Windows"/RMIV-Protokoll, siehe disag/).
//
// Laeuft auf dem Rechner mit dem COM-Port (das kann der Server-Rechner selbst
// sein oder ein separater Buero-PC) und spricht ausschliesslich per
// signiertem HTTP mit dem zentralen Server - kein eigener Datenbankzugriff.
// Siehe Konzept .claude/plans/wise-scribbling-abelson.md.
//
// Build:  go build -o wertmaschine .
// Start:  ./wertmaschine -config config.json
// ============================================================================
package main

import (
	"flag"
	"log"
)

func main() {
	configPath := flag.String("config", "config.json", "Pfad zur Konfigurationsdatei")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("FATAL Konfiguration (%s): %v", *configPath, err)
	}
	if cfg.ComPort == "" {
		log.Fatalf("FATAL: com_port nicht konfiguriert")
	}
	if cfg.ServerURL == "" {
		log.Fatalf("FATAL: server_url nicht konfiguriert")
	}

	client, err := NewServerClient(cfg.ServerURL, cfg.ServerPSKHex)
	if err != nil {
		log.Fatalf("FATAL: %v", err)
	}

	web := NewWebServer()
	session := NewSession(cfg, client)
	session.web = web
	web.session = session
	go session.pollDevMode()

	log.Printf("wertmaschine: Port %s, Protokoll %s, Server %s", cfg.ComPort, cfg.Protocol, cfg.ServerURL)
	if err := web.Run(cfg.HTTPListen); err != nil {
		log.Fatalf("FATAL HTTP: %v", err)
	}
}
