// ============================================================================
// serial.go – Liest JSON-Zeilen vom ESP32 (USB-Serial) und dekodiert sie.
//
// Robustheit:
//   - Automatischer Reconnect, wenn das USB-Geraet getrennt/neu gesteckt wird
//   - Unbekannte Telegrammtypen (status, pong, reject...) werden geloggt,
//     aber nicht als Schuss verarbeitet
//   - Zeilenpuffer-Limit gegen Muellzeilen
// ============================================================================
package main

import (
	"bufio"
	"context"
	"log"
	"time"

	"go.bug.st/serial"
)

func runSerialReader(ctx context.Context, cfg *Config, out chan<- RawShot) {
	mode := &serial.Mode{BaudRate: cfg.BaudRate}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		port, err := serial.Open(cfg.SerialPort, mode)
		if err != nil {
			log.Printf("Serial: %s nicht verfuegbar (%v) – neuer Versuch in 3s",
				cfg.SerialPort, err)
			if !sleepCtx(ctx, 3*time.Second) {
				return
			}
			continue
		}
		log.Printf("Serial: verbunden mit %s @ %d Baud",
			cfg.SerialPort, cfg.BaudRate)

		// Lesetimeout, damit ctx-Abbruch regelmaessig geprueft werden kann
		_ = port.SetReadTimeout(500 * time.Millisecond)

		readLoop(ctx, port, out)
		port.Close()

		select {
		case <-ctx.Done():
			return
		default:
			log.Printf("Serial: Verbindung verloren – Reconnect...")
		}
	}
}

func readLoop(ctx context.Context, port serial.Port, out chan<- RawShot) {
	scanner := bufio.NewScanner(port)
	scanner.Buffer(make([]byte, 4096), 4096)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		dispatchLine(scanner.Bytes(), out)
	}
	// scanner.Err() == nil bei Timeout; Schleife endet bei echtem Fehler/EOF
	if err := scanner.Err(); err != nil {
		log.Printf("Serial Lesefehler: %v", err)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func nowRFC3339() string {
	return time.Now().Format(time.RFC3339Nano)
}
