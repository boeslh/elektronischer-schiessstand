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
	// Protocol: "rmiii" (2400 Baud, RTS/DSR) oder "rmiv" (38400 Baud,
	// ENQ/STX/ACK - vom Geraet selbst/Referenzcode auch "RM III Windows"
	// genannt, siehe disag/rmiv.go).
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
		cfg.Protocol = "rmiii"
	}
	return &cfg, nil
}
