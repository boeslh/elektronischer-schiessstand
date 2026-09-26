// ============================================================================
// config_backup.go – Sicherung und Verlauf der kompletten ESP32-Konfiguration
// beim Server (server/esp32_config_history.go).
//
// Die Kalibrier-Sicherung (calibration_backup.go) enthaelt nur OFS0-5 und
// SOUNDSPEED - alle uebrigen Einstellungen (OFFSETX/Y, Standoff, ALGO,
// Piezo, Papiervorschub, ...) standen bisher nur auf der Platine selbst und
// waren nach einem Platinentausch verloren. Deshalb meldet der Stand-PC jede
// empfangene Konfiguration (SHOW-Antwort, egal wodurch ausgeloest) als
// Firmware-SET-Schluessel -> Wert an den Server; der speichert nur, was sich
// gegenueber dem bisherigen Stand des Stands geaendert hat. SHOW wird
// ausgeloest: kurz nach dem Start, alle configBackupInterval, nach jeder
// Aenderung ueber die Admin-GUI und nach einer abgeschlossenen Kalibrierung
// (siehe main.go / admin.go).
//
// Die Admin-GUI zeigt den Verlauf und kann den Zustand zu einem beliebigen
// Eintrag wiederherstellen (gleicher Weg wie die Simulator-Freigabe:
// Unterschiede anzeigen, CAL IMPORT + SET, per SHOW kontrollieren).
// ============================================================================
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"time"
)

const configBackupInterval = 10 * time.Minute

// deviceConfigSettings: komplette Konfiguration als Firmware-SET-Schluessel
// -> Wert (Schluesselumfang = Konfigurationstabelle der Admin-GUI).
func deviceConfigSettings(c *ConfigTelegram) map[string]string {
	out := make(map[string]string, len(adminConfigKeys))
	for _, k := range adminConfigKeys {
		if v, ok := currentSettingValue(c, k.Key); ok {
			out[k.Key] = v
		}
	}
	return out
}

// onDeviceConfig wird fuer JEDES empfangene config-Telegramm aufgerufen
// (DeviceState-Handler, eigene Goroutine). Unveraenderte Konfigurationen
// werden lokal verworfen, damit nicht jede SHOW-Abfrage (Admin-GUI-Status,
// Uebernahmen) eine Serveranfrage ausloest - erst nach erfolgreicher
// Meldung gilt ein Stand als gesichert, bei Serverausfall wird es beim
// naechsten SHOW erneut versucht.
func (ws *WebServer) onDeviceConfig(c ConfigTelegram) {
	if ws.cfg.ServerURL == "" || c.MAC == "" {
		return
	}
	settings := deviceConfigSettings(&c)
	key, _ := json.Marshal(struct {
		MAC string
		S   map[string]string
	}{c.MAC, settings}) // json sortiert Map-Schluessel -> stabiler Vergleich

	ws.configBackupMu.Lock()
	defer ws.configBackupMu.Unlock()
	if bytes.Equal(key, ws.lastConfigBackup) {
		return
	}
	data, _ := json.Marshal(map[string]any{"lane_no": ws.cfg.LaneNo, "mac": c.MAC, "config": settings})
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(ws.cfg.ServerURL+"/api/esp32-config", "application/json", bytes.NewReader(data))
	if err != nil {
		log.Printf("Konfigurationssicherung an Server fehlgeschlagen: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("Konfigurationssicherung vom Server abgelehnt: %s", resp.Status)
		return
	}
	var r struct {
		Recorded bool `json:"recorded"`
	}
	json.NewDecoder(resp.Body).Decode(&r)
	if r.Recorded {
		log.Printf("Konfigurationssicherung: Aenderung fuer %s beim Server gespeichert", c.MAC)
	}
	ws.lastConfigBackup = key
}

// requestConfigSnapshot fordert per SHOW eine aktuelle Konfiguration an -
// die Antwort landet ueber dispatchLine() automatisch in onDeviceConfig.
// Nicht waehrend einer laufenden Kalibrierung (unnoetiger Verkehr zum ESP32
// stoert dort die zeitkritische Erfassung, siehe handleAdminStatusPassive).
func (ws *WebServer) requestConfigSnapshot() {
	if cal := ws.deviceState.Snapshot().Cal; cal != nil && (cal.State == "waiting" || cal.State == "start") {
		return
	}
	ws.cmds.SendCommand("SHOW", 0)
}

// runConfigBackupLoop: erste Sicherung kurz nach dem Start (Geraet hat sich
// bis dahin meist verbunden), danach periodisch - faengt auch Aenderungen
// ab, die direkt an der seriellen Konsole gemacht wurden.
func (ws *WebServer) runConfigBackupLoop(ctx context.Context) {
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			ws.requestConfigSnapshot()
			timer.Reset(configBackupInterval)
		}
	}
}

// ── Verlauf / Wiederherstellen (Admin-GUI) ─────────────────────────────

func (ws *WebServer) serverGet(path string, v any) (int, error) {
	if ws.cfg.ServerURL == "" {
		return http.StatusServiceUnavailable, fmt.Errorf("kein Server konfiguriert")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(ws.cfg.ServerURL + path)
	if err != nil {
		return http.StatusBadGateway, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = "Server antwortete mit " + resp.Status
		}
		return resp.StatusCode, fmt.Errorf("%s", e.Error)
	}
	return http.StatusOK, json.NewDecoder(resp.Body).Decode(v)
}

// handleAdminConfigHistory: GET /admin/api/config-history
func (ws *WebServer) handleAdminConfigHistory(w http.ResponseWriter, r *http.Request) {
	var list []json.RawMessage
	if status, err := ws.serverGet(fmt.Sprintf("/api/esp32-config/history?lane_no=%d&limit=100", ws.cfg.LaneNo), &list); err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"lane_no": ws.cfg.LaneNo, "current_mac": ws.currentOrQueriedMAC(), "entries": list,
	})
}

type configState struct {
	ID        int64             `json:"id"`
	MAC       string            `json:"mac"`
	CreatedAt time.Time         `json:"created_at"`
	Settings  []firmwareSetting `json:"settings"`
}

// fetchConfigState holt den Zustand nach Verlaufseintrag id, sortiert wie
// die Konfigurationstabelle der Admin-GUI.
func (ws *WebServer) fetchConfigState(id int64) (*configState, int, error) {
	var st configState
	if status, err := ws.serverGet(fmt.Sprintf("/api/esp32-config/state?lane_no=%d&until_id=%d", ws.cfg.LaneNo, id), &st); err != nil {
		return nil, status, err
	}
	order := map[string]int{}
	for i, k := range adminConfigKeys {
		order[k.Key] = i
	}
	sort.SliceStable(st.Settings, func(i, j int) bool { return order[st.Settings[i].Key] < order[st.Settings[j].Key] })
	return &st, http.StatusOK, nil
}

// handleAdminConfigRestoreCheck: GET /admin/api/config-restore?id=N -
// Unterschiede zwischen dem Zustand nach Eintrag N und dem Geraet.
func (ws *WebServer) handleAdminConfigRestoreCheck(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil {
		http.Error(w, "id erforderlich", http.StatusBadRequest)
		return
	}
	st, status, err := ws.fetchConfigState(id)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	var cfg ConfigTelegram
	showErr := ws.queryTelegram("SHOW", "config", &cfg)
	var cur *ConfigTelegram
	if showErr == nil {
		cur = &cfg
	}
	items := diffTransfer(cur, st.Settings)
	resp := map[string]any{"state": st, "items": items, "changed": countChanged(items), "device_connected": showErr == nil}
	if showErr != nil {
		resp["device_error"] = showErr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleAdminConfigRestoreApply: POST /admin/api/config-restore/apply {id}
func (ws *WebServer) handleAdminConfigRestoreApply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID <= 0 {
		http.Error(w, "id erforderlich", http.StatusBadRequest)
		return
	}
	st, status, err := ws.fetchConfigState(body.ID)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	res, status, err := ws.applySettings(st.Settings)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	// Die abschliessende SHOW-Antwort in applySettings hat den neuen Zustand
	// bereits ueber onDeviceConfig an den Server gemeldet (neuer Verlaufs-
	// eintrag mit den zurueckgesetzten Werten).
	writeJSON(w, http.StatusOK, res)
}
