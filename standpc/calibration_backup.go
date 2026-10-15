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
// liest sie fuer den Restore-Button der Admin-GUI wieder zurueck; nach einem
// Platinentausch hilft fetchCalibrationBackupByLane() (juengste Sicherung
// des Stands, nach Rueckfrage in der Admin-GUI).
// ============================================================================
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"
)

// uploadCalibrationBackup sendet eine abgeschlossene Kalibrierung best-effort
// an den Server - Fehler werden nur geloggt, blockieren nichts (der ESP32
// bleibt in jedem Fall kalibriert, das Backup ist eine reine Absicherung).
func (ws *WebServer) uploadCalibrationBackup(cal CalTelegram) {
	if ws.cfg.ServerURL == "" || cal.MAC == "" {
		return
	}
	// Seit Firmware Rev 4.14/4.15 zusaetzlich: genaue Schallgeschwindigkeit
	// (sound_mps ist gerundet), WALKK und der Erfassungsmodus, in dem die
	// Offsets bestimmt wurden (ISR-Offsets passen nicht zu MCPWM).
	type payload struct {
		MAC         string   `json:"mac"`
		OffsetsNs   [6]int   `json:"offsets_ns"`
		SoundMps    int      `json:"sound_mps"`
		SoundSetMps *float64 `json:"sound_set_mps,omitempty"`
		WalkK       *float64 `json:"walk_k,omitempty"`
		Capture     string   `json:"capture,omitempty"`
		LaneNo      int      `json:"lane_no"`
	}
	capture := cal.capture
	if capture == "" && ws.deviceState != nil {
		capture = ws.deviceState.ActiveCapture()
	}
	data, err := json.Marshal(payload{cal.MAC, cal.OffsetsNs, cal.SoundMps, cal.SoundSetMps, cal.WalkK, capture, ws.cfg.LaneNo})
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

// esp32Calibration: Antwortformat von GET /api/esp32-calibrations/{mac} bzw.
// ?lane_no=N (server/esp32calibrations.go) - Grundlage fuer "CAL IMPORT" im
// Restore.
type esp32Calibration struct {
	MAC         string     `json:"mac"`
	OffsetsNs   [6]int     `json:"offsets_ns"`
	SoundMps    int        `json:"sound_mps"`
	SoundSetMps *float64   `json:"sound_set_mps"` // nil bei Sicherungen vor Firmware 4.15
	WalkK       *float64   `json:"walk_k"`
	Capture     *string    `json:"capture"` // Erfassungsmodus der Offsets, nil = unbekannt (vor 4.13)
	LaneNo      *int       `json:"lane_no"`
	UpdatedAt   *time.Time `json:"updated_at"`
}

// soundSet: genaue Schallgeschwindigkeit der Sicherung.
func (c esp32Calibration) soundSet() float64 {
	if c.SoundSetMps != nil {
		return *c.SoundSetMps
	}
	return float64(c.SoundMps)
}

// errNoBackup: fuer diese Geraete-ID bzw. diesen Stand ist keine Sicherung
// hinterlegt (Server 404) - im Gegensatz zu Verbindungsfehlern.
var errNoBackup = errors.New("keine Sicherung hinterlegt")

func getCalibrationBackup(url string) (*esp32Calibration, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errNoBackup
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

// fetchCalibrationBackup holt die Sicherung genau dieser Platine (mac).
func fetchCalibrationBackup(serverURL, mac string) (*esp32Calibration, error) {
	if serverURL == "" {
		return nil, fmt.Errorf("kein Server konfiguriert")
	}
	return getCalibrationBackup(serverURL + "/api/esp32-calibrations/" + url.PathEscape(mac))
}

// fetchCalibrationBackupByLane holt die juengste Sicherung des Stands, egal
// von welcher Platine - Fallback nach einem Platinentausch (die neue
// Platine hat eine neue mac und noch keine eigene Sicherung).
func fetchCalibrationBackupByLane(serverURL string, laneNo int) (*esp32Calibration, error) {
	if serverURL == "" {
		return nil, fmt.Errorf("kein Server konfiguriert")
	}
	return getCalibrationBackup(fmt.Sprintf("%s/api/esp32-calibrations?lane_no=%d", serverURL, laneNo))
}
