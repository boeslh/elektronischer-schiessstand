// ============================================================================
// config.go – Konfiguration des wertmaschine-Dienstes.
// ============================================================================
package main

import (
	"encoding/json"
	"os"
)

type Config struct {
	// ServerURL: zentraler Server, siehe server_client.go.
	ServerURL string `json:"server_url"`
	// ServerPSKHex: Pre-Shared-Key (Hex) fuer die /api/wertmaschine/*-Endpunkte,
	// gleiches Schema wie ESP32<->StandPC (server/wertmaschine_auth.go).
	ServerPSKHex string `json:"server_psk_hex"`
	// ComPort: nativer COM-Port ODER USB-Seriell-Geraetepfad (z.B.
	// "/dev/ttyUSB0" oder "COM3") - go.bug.st/serial behandelt beides gleich.
	ComPort string `json:"com_port"`
	// Protocol (Standard: "rmiii-win"): "rmiii" (2400 Baud, RTS/DSR,
	// normaler Fernsteuermodus) oder "rmiv" (38400 Baud, ENQ/STX/ACK - vom
	// Geraet selbst/Referenzcode auch "RM III Windows" genannt, siehe
	// disag/rmiv.go; setzt voraus, dass die RM III BEREITS manuell in
	// diesen Modus geschaltet ist, z.B. per Tastenkombination) oder
	// "rmiii-win" (dasselbe 38400-Baud-Protokoll wie "rmiv", aber der
	// Dienst kann die RM III per Software dorthin schalten - 2400 Baud,
	// Befehl "W", siehe disag/rmiii.go EnterWinMode). Der Wechsel in den
	// Fernsteuermodus (bei rmiii: "V"; bei rmiii-win: "W"+Umschalten) laeuft
	// ausschliesslich ueber den "Fern"-Button in der Bedienoberflaeche
	// (session.go EnterFernMode) - kein automatischer Trigger beim
	// Dienststart (Nutzer-Feedback: bringt nichts, wenn die Maschine schon
	// in FEr feststeckt, und der Bediener sieht am Display der Wertmaschine
	// besser als die Software, ob/wann das noetig ist). Je Wertungs-PC ist
	// genau eine Wertmaschine vorgesehen, daher gilt dieser Wert fuer den
	// ganzen Dienst (schiessstand-wertmaschine.service), mehrere PCs
	// koennen unterschiedliche Werte haben.
	Protocol string `json:"protocol"`
	// HTTPListen: lokale Bedienoberflaeche (Browser).
	HTTPListen string `json:"http_listen"`
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
	if cfg.HTTPListen == "" {
		cfg.HTTPListen = ":8092"
	}
	if cfg.Protocol == "" {
		cfg.Protocol = "rmiii-win"
	}
	return &cfg, nil
}
