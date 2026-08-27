// ============================================================================
// recover.go – Zustandswiederherstellung nach StandPC-Neustart
//
// Nach jedem Schuss und nach Modus-/Disziplinwechseln wird eine kompakte
// State-Datei geschrieben (lane01_state.json). Beim Start prüft der
// SessionManager:
//   1. Gibt es eine State-Datei? Welche session_id war zuletzt aktiv?
//   2. Stimmt die Server-Zuweisung überein? → History aus JSONL laden.
//   3. Kein Server / Offline → direkt aus State-Datei wiederherstellen.
//
// Die Shot-History wird aus den lokalen JSONL-Logs rekonstruiert (heutig
// + gestern, falls der Neustart über Mitternacht geht).
// ============================================================================
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// RecoverState hält alle Felder, die nach einem Neustart benötigt werden.
type RecoverState struct {
	SessionID     string    `json:"session_id"`
	Shooter       string    `json:"shooter"`
	RawDiscipline string    `json:"raw_discipline"`
	Mode          string    `json:"mode"`
	ShotNo        int       `json:"shot_no"`
	ProbeCount    int       `json:"probe_count"`
	WertungCount  int       `json:"wertung_count"`
	SavedAt       time.Time `json:"saved_at"`
}

func recoverStatePath(dir string, laneNo int) string {
	return filepath.Join(dir, fmt.Sprintf("lane%02d_state.json", laneNo))
}

// SaveRecoverState schreibt atomisch via tmp-Datei.
func SaveRecoverState(dir string, laneNo int, s RecoverState) error {
	if dir == "" || s.SessionID == "" {
		return nil
	}
	s.SavedAt = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := recoverStatePath(dir, laneNo) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, recoverStatePath(dir, laneNo))
}

// LoadRecoverState liest die State-Datei; gibt nil zurück wenn keine vorhanden.
func LoadRecoverState(dir string, laneNo int) (*RecoverState, error) {
	if dir == "" {
		return nil, nil
	}
	data, err := os.ReadFile(recoverStatePath(dir, laneNo))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var s RecoverState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("State-Datei unlesbar: %w", err)
	}
	return &s, nil
}

// ClearRecoverState löscht die State-Datei (Session sauber beendet).
func ClearRecoverState(dir string, laneNo int) {
	if dir == "" {
		return
	}
	os.Remove(recoverStatePath(dir, laneNo))
}

// LocalSession fasst Metadaten einer Session aus dem JSONL-Log zusammen.
type LocalSession struct {
	SessionID    string    `json:"session_id"`
	WertungCount int       `json:"wertung_count"`
	ProbeCount   int       `json:"probe_count"`
	FirstAt      time.Time `json:"first_at"`
	LastAt       time.Time `json:"last_at"`
}

// ListLocalSessions scannt alle JSONL-Dateien der Lane und gibt eine
// nach Datum absteigende Liste aller darin gefundenen Sessions zurück.
func ListLocalSessions(dir string, laneNo int) ([]LocalSession, error) {
	if dir == "" {
		return nil, nil
	}
	pattern := filepath.Join(dir, fmt.Sprintf("lane%02d_*.jsonl", laneNo))
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}

	byID := map[string]*LocalSession{}
	for _, fname := range files {
		f, err := os.Open(fname)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var shot Shot
			if err := json.Unmarshal(sc.Bytes(), &shot); err != nil || shot.SessionID == "" {
				continue
			}
			ls, ok := byID[shot.SessionID]
			if !ok {
				ls = &LocalSession{SessionID: shot.SessionID}
				byID[shot.SessionID] = ls
			}
			t, _ := time.Parse(time.RFC3339, shot.FiredAt)
			if ls.FirstAt.IsZero() || t.Before(ls.FirstAt) {
				ls.FirstAt = t
			}
			if t.After(ls.LastAt) {
				ls.LastAt = t
			}
			if !shot.Rejected {
				if shot.Mode == "wertung" {
					ls.WertungCount++
				} else if shot.Mode == "probe" {
					ls.ProbeCount++
				}
			}
		}
		f.Close()
	}

	out := make([]LocalSession, 0, len(byID))
	for _, ls := range byID {
		out = append(out, *ls)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastAt.After(out[j].LastAt)
	})
	return out, nil
}

// ResyncShotsFromLog liest das HEUTIGE Schussprotokoll der Lane und liefert
// alle Schuesse mit aktiver Session als DB-Eintraege (fuer DBWriter.NewDBWriter
// - "Nachschreiben" nach einem Neustart waehrend eines DB-Ausfalls).
//
// shot_no wird dabei aus der Position des Schusses INNERHALB seiner Session
// im Log rekonstruiert (1-basiert, pro session_id gezaehlt). Das ist exakt
// die Zahl, die SessionManager.NextShot() beim urspruenglichen Einschuss
// live vergeben hat: JEDES Telegramm mit aktiver Session (auch reject/
// pos_valid=0) verbraucht dort einen shot_no UND wird lueckenlos vom
// ShotLog protokolliert - Log-Reihenfolge und shot_no-Vergabe sind also
// deckungsgleich. Schuesse ohne Session (sessionID=="") werden ignoriert,
// genau wie im Live-Betrieb (dort landen sie auch nicht in der DB).
func ResyncShotsFromLog(dir string, laneNo int) []dbEntry {
	if dir == "" {
		return nil
	}
	today := time.Now().Format("2006-01-02")
	name := filepath.Join(dir, fmt.Sprintf("lane%02d_%s.jsonl", laneNo, today))
	f, err := os.Open(name)
	if err != nil {
		return nil
	}
	defer f.Close()

	shotNoBySession := map[string]int{}
	var entries []dbEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 1<<20) // Zeilen mit air_ns koennen lang werden
	for scanner.Scan() {
		var shot Shot
		if err := json.Unmarshal(scanner.Bytes(), &shot); err != nil || shot.SessionID == "" {
			continue
		}
		shotNoBySession[shot.SessionID]++
		s := shot // eigene Kopie je Zeile
		entries = append(entries, dbEntry{&s, shot.SessionID, shotNoBySession[shot.SessionID]})
	}
	return entries
}

// LoadShotsForSession lädt alle Schüsse der angegebenen Session aus den
// JSONL-Logs. Es werden das heutige und das gestrige Log durchsucht,
// damit ein Neustart über Mitternacht funktioniert.
func LoadShotsForSession(dir string, laneNo int, sessionID string) []*Shot {
	if dir == "" || sessionID == "" {
		return nil
	}
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	var shots []*Shot
	for _, date := range []string{yesterday, today} {
		name := filepath.Join(dir, fmt.Sprintf("lane%02d_%s.jsonl", laneNo, date))
		f, err := os.Open(name)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			var shot Shot
			if err := json.Unmarshal(scanner.Bytes(), &shot); err != nil {
				continue
			}
			if shot.SessionID == sessionID {
				shots = append(shots, &shot)
			}
		}
		f.Close()
	}
	return shots
}
