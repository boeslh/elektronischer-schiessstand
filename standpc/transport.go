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
// Pipeline. Nicht-Schuss-Telegramme werden geparst (status/config/
// confignet/cal, siehe devicestate.go) bzw. geloggt. cmds/state duerfen nil
// sein (z.B. in Tests) - dann entfaellt Korrelations-Zustellung bzw.
// Zustandscache, der Schuss-Pfad ist davon unberuehrt.
func dispatchLine(line []byte, out chan<- RawShot, cmds *CommandManager, state *DeviceState) {
	if len(line) == 0 || line[0] != '{' {
		return // Bootmeldungen, Kommentare ("# ..."), Leerzeilen
	}

	var hdr telegramHeader
	if err := json.Unmarshal(line, &hdr); err != nil {
		log.Printf("Telegramm unparsbar: %.80s", line)
		return
	}

	// Korrelierte Antwort (Firmware Rev 4.9.0, siehe devicelink.go) - laeuft
	// PARALLEL zur normalen Verarbeitung unten, ändert nichts am Dispatch.
	if hdr.Corr != nil && cmds != nil {
		cmds.Deliver(*hdr.Corr, line)
	}

	switch hdr.Type {
	case "shot", "reject":
		var raw RawShot
		if err := json.Unmarshal(line, &raw); err != nil {
			log.Printf("Telegramm unparsbar: %.80s", line)
			return
		}
		// Beide Telegrammtypen bekommen seit Firmware Rev 4.6.1 eine
		// fortlaufende "seq" und durchlaufen dieselbe Pipeline (Log/DB) -
		// reject wird dort als Rejected markiert und landet nicht in der
		// Anzeige/Wertung, bleibt aber fuer Analysen/Simulationen erhalten.
		select {
		case out <- raw:
		default:
			log.Printf("WARNUNG: Pipeline voll, Telegramm #%d verworfen!", raw.Seq)
		}
	case "status":
		var st StatusTelegram
		if err := json.Unmarshal(line, &st); err == nil && state != nil {
			state.SetStatus(st)
		}
		log.Printf("ESP32: %s", line)
	case "config":
		var cfg ConfigTelegram
		if err := json.Unmarshal(line, &cfg); err == nil && state != nil {
			state.SetConfig(cfg)
		}
		log.Printf("ESP32: %s", line)
	case "confignet":
		var cn ConfignetTelegram
		if err := json.Unmarshal(line, &cn); err == nil && state != nil {
			state.SetConfignet(cn)
		}
		log.Printf("ESP32: %s", line)
	case "cal":
		var cal CalTelegram
		if err := json.Unmarshal(line, &cal); err == nil {
			if state != nil {
				state.SetCal(cal)
			}
			if cal.State == "done" && state != nil && state.onCalDone != nil {
				go state.onCalDone(cal)
			}
		}
		log.Printf("ESP32: %s", line)
	case "ready", "pong", "ok", "cand", "net", "pin":
		log.Printf("ESP32: %s", line)
	case "error":
		log.Printf("ESP32 FEHLER: %s", line)
	default:
		log.Printf("ESP32 unbekannt: %.80s", line)
	}
}
