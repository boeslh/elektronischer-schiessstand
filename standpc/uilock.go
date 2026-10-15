// uilock.go – Bedienung an der Schuetzenanzeige gesperrt (Standsteuerung,
// server/migrations/086 lanes.ui_locked).
//
// Gesperrt sind alle Bedien-Aktionen des Schuetzen (Umschalten Probe/
// Wertung, Stand freigeben, Disziplin waehlen, Ergebnis-Ausgabe,
// Preisschiessen-Menue); Ansehen und Zoom bleiben moeglich. Die Sperre wird
// hier serverseitig durchgesetzt, die Anzeige blendet die Knoepfe nur aus.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// pollUILock: Sperrzustand des Stands vom Server (im Poll-Takt).
func (m *SessionManager) pollUILock(client *http.Client) {
	if m.cfg.ServerURL == "" {
		return
	}
	resp, err := client.Get(fmt.Sprintf("%s/api/lanes/%d/ui-lock", m.cfg.ServerURL, m.cfg.LaneNo))
	if err != nil {
		return // offline: letzten Zustand behalten
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var body struct {
		Locked bool `json:"locked"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil {
		return
	}
	m.mu.Lock()
	changed := m.uiLocked != body.Locked
	m.uiLocked = body.Locked
	st := m.statusLocked()
	m.mu.Unlock()
	if changed {
		m.web.BroadcastStatus(st)
	}
}

// UILocked: Bedienung gesperrt?
func (m *SessionManager) UILocked() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.uiLocked
}

// lockable: Handler nur ausfuehren, wenn die Bedienung nicht gesperrt ist.
func (ws *WebServer) lockable(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ws.sessions != nil && ws.sessions.UILocked() {
			http.Error(w, "Bedienung gesperrt (Standsteuerung)", http.StatusLocked)
			return
		}
		h(w, r)
	}
}
