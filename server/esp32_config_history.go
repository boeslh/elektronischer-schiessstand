// ============================================================================
// esp32_config_history.go – Verlauf der ESP32-Gerätekonfiguration je Stand
// (migrations/070_esp32_config_history.sql).
//
// Der Stand-PC meldet die komplette Konfiguration als Firmware-SET-
// Schluessel -> Wert (POST /api/esp32-config, siehe standpc/config_backup.go).
// Gespeichert wird nur, was sich gegenueber dem bisherigen Zustand des
// Stands geaendert hat - bzw. eine (ggf. leere) Aenderungszeile, wenn eine
// andere Platine meldet (Platinentausch). Der Zustand zu einem Zeitpunkt
// ergibt sich durch Aufsummieren aller Eintraege bis dorthin (stateUntil).
//
// Unauthentifiziert wie die uebrigen Stand-PC-Endpunkte
// (/api/esp32-calibrations u.ae.) - Stand-PCs melden sich nicht an.
// ============================================================================
package main

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ConfigHistoryEntry struct {
	ID        int64             `json:"id"`
	LaneNo    int               `json:"lane_no"`
	MAC       string            `json:"mac"`
	Changes   map[string]string `json:"changes"`
	Previous  map[string]string `json:"previous"`
	CreatedAt time.Time         `json:"created_at"`
	// Kind: grobe Einordnung fuer die Anzeige - "erstmalig", "platinentausch",
	// "kalibrierung" (Mikrofon-Offsets geaendert) oder "einstellungen".
	Kind string `json:"kind"`
}

func classifyConfigChange(first, macChanged bool, changes map[string]string) string {
	switch {
	case first:
		return "erstmalig"
	case macChanged:
		return "platinentausch"
	}
	// Nur die Mikrofon-Offsets: CAL START aendert ausschliesslich diese
	// (SOUNDSPEED ist laut Firmware "rein manuell") - eine reine
	// Schallgeschwindigkeits-/Offset-Korrektur zaehlt als Einstellung.
	for k := range changes {
		// WALKK aendert CAL START mit, wenn SET WALKCAL=1 (Firmware 4.14.0)
		// Zonen-Saetze (ZCALA/B/C, Firmware 4.17.0) sind Kalibrierwerte
		if strings.HasPrefix(k, "OFS") || k == "WALKK" || strings.HasPrefix(k, "ZCAL") {
			return "kalibrierung"
		}
	}
	return "einstellungen"
}

// configHistoryIgnored: Schluessel, die nicht in den Verlauf gehoeren, weil
// der Stand-PC sie selbst je Scheibe setzt - ZHOLE (Lochradius der
// Vorsatzscheibe, standpc/zone_hole.go) wechselt bei jedem Umschalten
// zwischen LG und LP und waere sonst jedes Mal eine "Aenderung"; eine
// Wiederherstellung darf ihn ebenfalls nicht setzen.
var configHistoryIgnored = map[string]bool{"ZHOLE": true}

func withoutIgnored(m map[string]string) map[string]string {
	for k := range m {
		if configHistoryIgnored[k] {
			delete(m, k)
		}
	}
	return m
}

// diffConfig: Schluessel aus next, deren Wert von prev abweicht (oder neu ist).
func diffConfig(prev, next map[string]string) (changes, previous map[string]string) {
	changes, previous = map[string]string{}, map[string]string{}
	for k, v := range next {
		if old, ok := prev[k]; !ok || old != v {
			changes[k] = v
			if ok {
				previous[k] = old
			}
		}
	}
	return
}

// RecordESP32Config speichert eine gemeldete Konfiguration, falls sie vom
// bisherigen Zustand des Stands abweicht. Pro Stand serialisiert
// (Advisory-Lock), damit zwei gleichzeitige Meldungen nicht beide gegen
// denselben alten Zustand vergleichen.
func (s *Store) RecordESP32Config(ctx context.Context, laneNo int, mac string, cfg map[string]string) (*ConfigHistoryEntry, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('esp32_config_history'), $1)`, laneNo); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT mac, changes FROM esp32_config_history WHERE lane_no=$1 ORDER BY id`, laneNo)
	if err != nil {
		return nil, err
	}
	state := map[string]string{}
	lastMAC := ""
	n := 0
	for rows.Next() {
		var m string
		var ch map[string]string
		if err := rows.Scan(&m, &ch); err != nil {
			rows.Close()
			return nil, err
		}
		for k, v := range ch {
			state[k] = v
		}
		lastMAC = m
		n++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	changes, previous := diffConfig(withoutIgnored(state), withoutIgnored(cfg))
	macChanged := n > 0 && lastMAC != mac
	if len(changes) == 0 && !macChanged {
		return nil, nil
	}
	e := &ConfigHistoryEntry{LaneNo: laneNo, MAC: mac, Changes: changes, Previous: previous,
		Kind: classifyConfigChange(n == 0, macChanged, changes)}
	if err := tx.QueryRow(ctx, `
		INSERT INTO esp32_config_history (lane_no, mac, changes, previous)
		VALUES ($1,$2,$3,$4) RETURNING id, created_at`, laneNo, mac, changes, previous,
	).Scan(&e.ID, &e.CreatedAt); err != nil {
		return nil, err
	}
	return e, tx.Commit(ctx)
}

// ListESP32ConfigHistory: Eintraege eines Stands, neueste zuerst.
func (s *Store) ListESP32ConfigHistory(ctx context.Context, laneNo, limit int) ([]ConfigHistoryEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, mac, changes, previous, created_at FROM esp32_config_history
		WHERE lane_no=$1 ORDER BY id`, laneNo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var all []ConfigHistoryEntry
	lastMAC := ""
	for rows.Next() {
		e := ConfigHistoryEntry{LaneNo: laneNo}
		if err := rows.Scan(&e.ID, &e.MAC, &e.Changes, &e.Previous, &e.CreatedAt); err != nil {
			return nil, err
		}
		withoutIgnored(e.Changes)
		withoutIgnored(e.Previous)
		e.Kind = classifyConfigChange(len(all) == 0, len(all) > 0 && lastMAC != e.MAC, e.Changes)
		lastMAC = e.MAC
		all = append(all, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// neueste zuerst, begrenzt
	out := make([]ConfigHistoryEntry, 0, len(all))
	for i := len(all) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		out = append(out, all[i])
	}
	return out, nil
}

// ESP32ConfigStateUntil: kompletter Konfigurationszustand des Stands
// unmittelbar nach Eintrag untilID (inklusive), sortiert nach Schluessel.
func (s *Store) ESP32ConfigStateUntil(ctx context.Context, laneNo int, untilID int64) (map[string]any, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, mac, changes, created_at FROM esp32_config_history
		WHERE lane_no=$1 AND id <= $2 ORDER BY id`, laneNo, untilID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	state := map[string]string{}
	var mac string
	var at time.Time
	var lastID int64
	for rows.Next() {
		var ch map[string]string
		if err := rows.Scan(&lastID, &mac, &ch, &at); err != nil {
			return nil, err
		}
		for k, v := range ch {
			state[k] = v
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	withoutIgnored(state) // ZHOLE nicht wiederherstellen
	if lastID != untilID {
		return nil, &httpError{code: 404, msg: "Verlaufseintrag nicht gefunden"}
	}
	keys := make([]string, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	settings := make([]FirmwareSetting, 0, len(keys))
	for _, k := range keys {
		settings = append(settings, FirmwareSetting{Key: k, Value: state[k]})
	}
	return map[string]any{"lane_no": laneNo, "id": untilID, "mac": mac, "created_at": at, "settings": settings}, nil
}

func laneNoQuery(r *http.Request) (int, error) {
	n, err := strconv.Atoi(r.URL.Query().Get("lane_no"))
	if err != nil || n < 1 {
		return 0, errBadRequest("lane_no erforderlich")
	}
	return n, nil
}

func (a *APIServer) postESP32Config(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		LaneNo int               `json:"lane_no"`
		MAC    string            `json:"mac"`
		Config map[string]string `json:"config"`
	}](r)
	if err != nil || body.LaneNo < 1 || body.MAC == "" || len(body.Config) == 0 {
		return nil, errBadRequest("lane_no, mac und config erforderlich")
	}
	e, err := a.store.RecordESP32Config(r.Context(), body.LaneNo, body.MAC, body.Config)
	if err != nil {
		return nil, err
	}
	return map[string]any{"recorded": e != nil, "entry": e}, nil
}

func (a *APIServer) getESP32ConfigHistory(w http.ResponseWriter, r *http.Request) (any, error) {
	laneNo, err := laneNoQuery(r)
	if err != nil {
		return nil, err
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := a.store.ListESP32ConfigHistory(r.Context(), laneNo, limit)
	if list == nil {
		list = []ConfigHistoryEntry{}
	}
	return list, err
}

func (a *APIServer) getESP32ConfigState(w http.ResponseWriter, r *http.Request) (any, error) {
	laneNo, err := laneNoQuery(r)
	if err != nil {
		return nil, err
	}
	untilID, err := strconv.ParseInt(r.URL.Query().Get("until_id"), 10, 64)
	if err != nil {
		return nil, errBadRequest("until_id erforderlich")
	}
	return a.store.ESP32ConfigStateUntil(r.Context(), laneNo, untilID)
}

// ConfigLaneSummary: eine Zeile der Server-Uebersicht (web/geraetekonfiguration.html).
type ConfigLaneSummary struct {
	LaneNo          int                `json:"lane_no"`
	CurrentMAC      string             `json:"current_mac"`
	Entries         int                `json:"entries"`
	FirstAt         time.Time          `json:"first_at"`
	Last            ConfigHistoryEntry `json:"last"`
	LastCalibration *time.Time         `json:"last_calibration"`
	BoardSwaps      int                `json:"board_swaps"`
}

// ESP32ConfigOverview: Zusammenfassung je Stand mit gesicherter Konfiguration.
func (s *Store) ESP32ConfigOverview(ctx context.Context) ([]ConfigLaneSummary, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT lane_no FROM esp32_config_history ORDER BY lane_no`)
	if err != nil {
		return nil, err
	}
	var lanes []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		lanes = append(lanes, n)
	}
	rows.Close()
	out := []ConfigLaneSummary{}
	for _, lane := range lanes {
		list, err := s.ListESP32ConfigHistory(ctx, lane, 0) // neueste zuerst
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			continue
		}
		sum := ConfigLaneSummary{LaneNo: lane, CurrentMAC: list[0].MAC, Entries: len(list),
			FirstAt: list[len(list)-1].CreatedAt, Last: list[0]}
		for i := range list {
			switch list[i].Kind {
			case "platinentausch":
				sum.BoardSwaps++
			case "kalibrierung":
				if sum.LastCalibration == nil {
					sum.LastCalibration = &list[i].CreatedAt
				}
			}
		}
		out = append(out, sum)
	}
	return out, nil
}

func (a *APIServer) getESP32ConfigOverview(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ESP32ConfigOverview(r.Context())
}
