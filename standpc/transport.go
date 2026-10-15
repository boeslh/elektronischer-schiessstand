// ============================================================================
// transport.go – Gemeinsamer Telegramm-Dispatch fuer Serial UND TCP
//
// Beide Transporte liefern identische JSON-Zeilen; die Auswertung ist
// deshalb zentral. serial.go und tcp.go rufen nur noch dispatchLine() auf.
// ============================================================================
package main

import (
	"bytes"
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
	truncated := false
	if bytes.HasPrefix(line, []byte(`{"type":"config"`)) && !json.Valid(line) {
		// Firmware 4.16.0 schneidet das config-Telegramm nach ~900 Zeichen ab
		// (Puffergrenze) - die vollstaendigen Felder retten, siehe salvageJSON.
		if fixed := salvageJSON(line); fixed != nil {
			log.Printf("WARNUNG: config-Telegramm von der Firmware abgeschnitten (%d Zeichen) - vollstaendige Felder uebernommen", len(line))
			line, truncated = fixed, true
		}
	}
	if err := json.Unmarshal(line, &hdr); err != nil {
		// vollstaendig protokollieren (bis 4000 Zeichen) - eine abgeschnittene
		// Zeile laesst sich sonst nicht als Firmware-Fehler nachweisen
		log.Printf("Telegramm unparsbar (%d Zeichen, %v): %.4000s", len(line), err, line)
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
		if state != nil {
			raw.Ctx = state.ShotContext()
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
	case "raw":
		// SET RAWLOG=1 (Firmware Rev 4.16.0): folgt direkt auf den shot mit
		// derselben seq - pairRawTelegrams haengt es dort an.
		var h struct {
			Seq int `json:"seq"`
		}
		if err := json.Unmarshal(line, &h); err != nil {
			log.Printf("raw-Telegramm unparsbar: %.80s", line)
			return
		}
		select {
		case out <- RawShot{Type: "raw", Seq: h.Seq, RawEdges: append(json.RawMessage(nil), line...)}:
		default:
			log.Printf("WARNUNG: Pipeline voll, raw-Telegramm #%d verworfen!", h.Seq)
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
			cfg.Truncated = truncated
			state.SetConfig(cfg)
			// Eine abgeschnittene Konfiguration nicht sichern: fehlende Felder
			// stuenden sonst mit 0 im Verlauf und koennten so wiederhergestellt
			// werden.
			if state.onConfig != nil && !truncated {
				go state.onConfig(cfg)
			}
		}
		log.Printf("ESP32: %s", line)
	case "zones":
		var z ZonesTelegram
		if err := json.Unmarshal(line, &z); err == nil && state != nil {
			state.SetZones(z)
		}
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
	case "ready", "pong", "ok", "cand", "net":
		log.Printf("ESP32: %s", line)
	case "error":
		log.Printf("ESP32 FEHLER: %s", line)
	default:
		log.Printf("ESP32 unbekannt: %.80s", line)
	}
}

// salvageJSON macht aus einem abgeschnittenen JSON-Objekt ein gueltiges,
// indem es das unvollstaendige letzte Feld verwirft und das Objekt schliesst.
// nil, wenn das nicht gelingt.
func salvageJSON(line []byte) []byte {
	end := len(line)
	for tries := 0; tries < 8; tries++ {
		i := bytes.LastIndex(line[:end], []byte(`,"`))
		if i <= 0 {
			return nil
		}
		cand := append(append([]byte(nil), line[:i]...), '}')
		if json.Valid(cand) {
			return cand
		}
		end = i
	}
	return nil
}
