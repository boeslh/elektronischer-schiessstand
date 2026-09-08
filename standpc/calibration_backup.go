// ============================================================================
// calibration_backup.go – Kalibrierungs-Backup am Server (Phase B, siehe Plan
// "ESP32-Firmware Rev 4.9.0: Gegenstück in StandPC + Server").
//
// Der ESP32 selbst persistiert seine Kalibrierung nur lokal (NVS) - geht das
// Geraet defekt oder wird getauscht, ist der zuletzt gemessene Stand weg.
// uploadCalibrationBackup() sichert deshalb jede erfolgreich abgeschlossene
// Kalibrierung (cal/"done", siehe transport.go dispatchLine) beim zentralen
// Server, adressiert per mac (stabile Geraete-ID, ueberlebt einen
// Geraetetausch NICHT, wohl aber Lane-Umsteckungen). fetchCalibrationBackup()
// liest sie fuer den Restore-Button der Admin-GUI wieder zurueck.
// ============================================================================
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// uploadCalibrationBackup sendet eine abgeschlossene Kalibrierung best-effort
// an den Server - Fehler werden nur geloggt, blockieren nichts (der ESP32
// bleibt in jedem Fall kalibriert, das Backup ist eine reine Absicherung).
func (ws *WebServer) uploadCalibrationBackup(cal CalTelegram) {
	if ws.cfg.ServerURL == "" || cal.MAC == "" {
		return
	}
	type payload struct {
		MAC       string `json:"mac"`
		OffsetsNs [6]int `json:"offsets_ns"`
		SoundMps  int    `json:"sound_mps"`
		LaneNo    int    `json:"lane_no"`
	}
	data, err := json.Marshal(payload{cal.MAC, cal.OffsetsNs, cal.SoundMps, ws.cfg.LaneNo})
	if err != nil {
		return
	}
	url := ws.cfg.ServerURL + "/api/esp32-calibrations"
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		log.Printf("Kalibrierungs-Backup: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Kalibrierungs-Backup an Server fehlgeschlagen: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("Kalibrierungs-Backup vom Server abgelehnt: %s", resp.Status)
		return
	}
	log.Printf("Kalibrierungs-Backup fuer %s beim Server gesichert", cal.MAC)
}

// esp32Calibration: Antwortformat von GET /api/esp32-calibrations/{mac}
// (server/esp32calibrations.go) - Grundlage fuer "CAL IMPORT" im Restore.
type esp32Calibration struct {
	MAC       string `json:"mac"`
	OffsetsNs [6]int `json:"offsets_ns"`
	SoundMps  int    `json:"sound_mps"`
}

// fetchCalibrationBackup holt das zuletzt gesicherte Kalibrierungs-Backup
// fuer die angegebene MAC vom Server (fuer den "Vom Server wiederherstellen"
// -Button der Admin-GUI, siehe admin.go handleAdminCalRestore).
func fetchCalibrationBackup(serverURL, mac string) (*esp32Calibration, error) {
	if serverURL == "" {
		return nil, fmt.Errorf("kein Server konfiguriert")
	}
	if mac == "" {
		return nil, fmt.Errorf("Geraete-ID (mac) noch unbekannt - Status abrufen und erneut versuchen")
	}
	url := serverURL + "/api/esp32-calibrations/" + mac
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("kein Backup fuer %s beim Server hinterlegt", mac)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Server antwortete mit %s", resp.Status)
	}
	var cal esp32Calibration
	if err := json.NewDecoder(resp.Body).Decode(&cal); err != nil {
		return nil, err
	}
	return &cal, nil
}
