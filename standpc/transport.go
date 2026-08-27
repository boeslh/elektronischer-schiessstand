// ============================================================================
// transport.go – Gemeinsamer Telegramm-Dispatch fuer Serial UND TCP
//
// Beide Transporte liefern identische JSON-Zeilen; die Auswertung ist
// deshalb zentral. serial.go und tcp.go rufen nur noch dispatchLine() auf.
// ============================================================================
package main

import (
	"encoding/json"
	"log"
)

// dispatchLine dekodiert eine Telegrammzeile und reicht Schuesse in die
// Pipeline. Nicht-Schuss-Telegramme werden geloggt.
func dispatchLine(line []byte, out chan<- RawShot) {
	if len(line) == 0 || line[0] != '{' {
		return // Bootmeldungen, Kommentare ("# ..."), Leerzeilen
	}

	var raw RawShot
	if err := json.Unmarshal(line, &raw); err != nil {
		log.Printf("Telegramm unparsbar: %.80s", line)
		return
	}

	switch raw.Type {
	case "shot", "reject":
		// Beide Telegrammtypen bekommen seit Firmware Rev 4.6.1 eine
		// fortlaufende "seq" und durchlaufen dieselbe Pipeline (Log/DB) -
		// reject wird dort als Rejected markiert und landet nicht in der
		// Anzeige/Wertung, bleibt aber fuer Analysen/Simulationen erhalten.
		select {
		case out <- raw:
		default:
			log.Printf("WARNUNG: Pipeline voll, Telegramm #%d verworfen!", raw.Seq)
		}
	case "ready", "status", "pong", "ok", "cal", "cand":
		log.Printf("ESP32: %s", line)
	case "error":
		log.Printf("ESP32 FEHLER: %s", line)
	default:
		log.Printf("ESP32 unbekannt: %.80s", line)
	}
}
