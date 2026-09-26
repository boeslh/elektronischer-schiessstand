// ============================================================================
// esp32calibrations.go – Kalibrierungs-Backup je ESP32 (Firmware Rev 4.9.0,
// siehe migrations/048_esp32_calibrations.sql). Der Stand-PC laedt hier jede
// erfolgreich abgeschlossene Kalibrierung hoch (siehe
// standpc/calibration_backup.go uploadCalibrationBackup) und liest sie fuer
// den "Vom Server wiederherstellen"-Button seiner Admin-GUI wieder zurueck.
//
// Unauthentifiziert wie die anderen Stand-PC-seitigen Endpunkte
// (/api/settings/standpc-dev-mode u.ae.) - Stand-PCs melden sich nicht an.
// ============================================================================
package main

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

type ESP32Calibration struct {
	MAC       string     `json:"mac"`
	OffsetsNs [6]int     `json:"offsets_ns"`
	SoundMps  int        `json:"sound_mps"`
	LaneNo    *int       `json:"lane_no,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"` // nur beim Lesen gesetzt
}

func (s *Store) UpsertESP32Calibration(ctx context.Context, c ESP32Calibration) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO esp32_calibrations (mac, offsets_ns, sound_mps, lane_no, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (mac) DO UPDATE SET
		  offsets_ns=EXCLUDED.offsets_ns, sound_mps=EXCLUDED.sound_mps,
		  lane_no=EXCLUDED.lane_no, updated_at=now()`,
		c.MAC, c.OffsetsNs[:], c.SoundMps, c.LaneNo)
	return err
}

func (s *Store) GetESP32Calibration(ctx context.Context, mac string) (*ESP32Calibration, error) {
	var c ESP32Calibration
	var offsets []int
	c.MAC = mac
	err := s.pool.QueryRow(ctx, `
		SELECT offsets_ns, sound_mps, lane_no, updated_at FROM esp32_calibrations WHERE mac=$1`, mac,
	).Scan(&offsets, &c.SoundMps, &c.LaneNo, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	copy(c.OffsetsNs[:], offsets)
	return &c, nil
}

// GetESP32CalibrationByLane liefert die juengste Sicherung eines Stands -
// unabhaengig von der Geraete-ID. Gebraucht nach einem Platinentausch: die
// neue Platine hat eine neue MAC und damit noch keine eigene Sicherung,
// die Sicherung ihrer Vorgaengerin am selben Stand ist aber der beste
// verfuegbare Ausgangspunkt (siehe standpc/admin.go handleAdminCalRestore).
func (s *Store) GetESP32CalibrationByLane(ctx context.Context, laneNo int) (*ESP32Calibration, error) {
	var c ESP32Calibration
	var offsets []int
	err := s.pool.QueryRow(ctx, `
		SELECT mac, offsets_ns, sound_mps, lane_no, updated_at FROM esp32_calibrations
		WHERE lane_no=$1 ORDER BY updated_at DESC LIMIT 1`, laneNo,
	).Scan(&c.MAC, &offsets, &c.SoundMps, &c.LaneNo, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	copy(c.OffsetsNs[:], offsets)
	return &c, nil
}

func (a *APIServer) postESP32Calibration(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[ESP32Calibration](r)
	if err != nil {
		return nil, err
	}
	if body.MAC == "" {
		return nil, errBadRequest("mac darf nicht leer sein")
	}
	if err := a.store.UpsertESP32Calibration(r.Context(), body); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

func (a *APIServer) getESP32Calibration(w http.ResponseWriter, r *http.Request) (any, error) {
	mac := r.PathValue("mac")
	cal, err := a.store.GetESP32Calibration(r.Context(), mac)
	if err != nil {
		return nil, &httpError{code: http.StatusNotFound, msg: "kein Backup fuer " + mac + " hinterlegt"}
	}
	return cal, nil
}

// getESP32CalibrationByLane: GET /api/esp32-calibrations?lane_no=N -
// juengste Sicherung des Stands (egal von welcher Platine), fuer den
// Wiederherstellen-Fallback nach einem Platinentausch.
func (a *APIServer) getESP32CalibrationByLane(w http.ResponseWriter, r *http.Request) (any, error) {
	laneNo, err := strconv.Atoi(r.URL.Query().Get("lane_no"))
	if err != nil || laneNo < 1 {
		return nil, errBadRequest("lane_no erforderlich")
	}
	cal, err := a.store.GetESP32CalibrationByLane(r.Context(), laneNo)
	if err != nil {
		return nil, &httpError{code: http.StatusNotFound, msg: "keine Sicherung fuer Stand " + strconv.Itoa(laneNo) + " hinterlegt"}
	}
	return cal, nil
}
