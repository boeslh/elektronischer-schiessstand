// ============================================================================
// session.go – Anbindung an den zentralen Server + Probe/Wertung-Steuerung
//
// Der Stand-PC pollt GET {server_url}/api/lanes/{lane_no}/session und
// uebernimmt automatisch:
//   - die aktive session_id (fuer die DB-Schreibung)
//   - die KALIBRIERUNG des Stands (Sensorpositionen, Blechwinkel,
//     Offsets, Schallgeschwindigkeit) -> der TDOA-Solver wird zur
//     Laufzeit ausgetauscht; die lokale config.json dient nur noch
//     als Fallback fuer den Offline-/Trainingsbetrieb
//   - die Disziplin (Name -> DisciplineDef aus Standardtabelle)
//   - den Modus (mode): Server kann direkt "wertung" signalisieren
//
// Modus-Regeln:
//   - Start ist immer "probe", ausser die Disziplin hat TrialShots == 0
//   - Umschalten probe -> wertung: jederzeit moeglich; setzt History zurueck
//   - Umschalten wertung -> probe: nur wenn noch kein Wertungsschuss
//     abgegeben wurde (wertungCount == 0)
//   - Sessionwechsel: immer Neustart mit "probe"
// ============================================================================
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

const (
	ModeProbe   = "probe"
	ModeWertung = "wertung"
)

// serverSession: Antwort von /api/lanes/{no}/session
type serverSession struct {
	SessionID     string     `json:"session_id"`
	Status        string     `json:"status"`
	Discipline    string     `json:"discipline"`
	Mode          string     `json:"mode"` // optionales Signal: "wertung" -> sofort umschalten
	Shooter       string     `json:"shooter"`
	EventName     string     `json:"event_name"`
	EventType     string     `json:"event_type"` // einzel | runde | gruppe
	SensorPosRaw  []sensorXY `json:"sensor_pos"`
	PlateAngleDeg float64    `json:"plate_angle_deg"`
	SoundSpeedMPS float64    `json:"sound_speed_mps"`
	PlateOffsetX  float64    `json:"plate_offset_x"`
	PlateOffsetY  float64    `json:"plate_offset_y"`
}

// Das Datenmodell speichert Sensorpositionen als [{"x":..,"y":..}]
type sensorXY struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// StatusInfo wird an den Browser gesendet wenn sich Modus oder Disziplin aendern.
type StatusInfo struct {
	Mode           string         `json:"mode"`
	Discipline     *DisciplineDef `json:"discipline"`
	DisciplineName string         `json:"discipline_name,omitempty"` // Klarname (Fallback wenn kein lok. Match)
	Shooter        string         `json:"shooter"`
	EventName      string         `json:"event_name,omitempty"`
	EventType      string         `json:"event_type,omitempty"`   // einzel | runde | gruppe
	WertungCount   int            `json:"wertung_count"`          // abgegebene Wertungsschuesse
	Notification   string         `json:"notification,omitempty"` // einmaliger Hinweis fuer den Browser
	HistoryGen     int            `json:"history_gen"`            // aendert sich bei jedem ResetHistory (Session-/Modus-/Disziplinwechsel)
}

type SessionManager struct {
	cfg         *Config
	web         *WebServer
	disciplines []DisciplineDef // geladene Disziplinliste

	mu           sync.RWMutex
	sessionID    string
	shooter      string
	solver       *TDOASolver
	shotNo       int // PC-seitiger Zaehler, 1-basiert je Session

	mode             string         // ModeProbe | ModeWertung
	discipline       *DisciplineDef // aktuelle Disziplin; nil = keine
	rawDiscipline    string         // Disziplinname vom Server (auch wenn kein lokaler Match)
	probeCount       int            // Probeschuesse in dieser Disziplinen-Phase
	wertungCount     int            // Wertungsschuesse in dieser Session
	eventName        string         // Veranstaltungsname (leer = Training/kein Event)
	eventType        string         // einzel | runde | gruppe
}

func NewSessionManager(cfg *Config, web *WebServer, disciplines []DisciplineDef) *SessionManager {
	return &SessionManager{
		cfg:         cfg,
		web:         web,
		disciplines: disciplines,
		solver:      NewTDOASolver(cfg),
		mode:        ModeProbe,
	}
}

// ReloadDisciplines ersetzt die Disziplinliste zur Laufzeit.
// Die aktuelle Disziplin bleibt erhalten wenn sie noch in der neuen Liste vorhanden ist.
func (m *SessionManager) ReloadDisciplines(defs []DisciplineDef) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.disciplines = defs
	// Aktuelle Disziplin neu suchen; sonst nil
	if m.discipline != nil {
		name := m.discipline.Name
		m.discipline = nil
		for i := range defs {
			if defs[i].Name == name {
				m.discipline = &m.disciplines[i]
				break
			}
		}
	}
	m.web.BroadcastStatus(m.statusLocked())
}

// ListDisciplines gibt die geladene Disziplinliste zurück.
func (m *SessionManager) ListDisciplines() []DisciplineDef {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]DisciplineDef, len(m.disciplines))
	copy(out, m.disciplines)
	return out
}

// LookupDiscipline sucht eine Disziplin nach Name in der geladenen Liste.
func (m *SessionManager) LookupDiscipline(name string) *DisciplineDef {
	for i := range m.disciplines {
		if m.disciplines[i].Name == name {
			return &m.disciplines[i]
		}
	}
	return nil
}

// setDisciplineLocked: Disziplin und Modus setzen. Muss unter m.mu aufgerufen werden.
func (m *SessionManager) setDisciplineLocked(def *DisciplineDef) {
	m.discipline = def
	m.mode = ModeProbe
	if def != nil && def.TrialShots == 0 {
		m.mode = ModeWertung
	}
	m.probeCount  = 0
	m.wertungCount = 0
	m.web.ResetHistory()
	m.web.BroadcastStatus(m.statusLocked())
}

// SetDiscipline schaltet die Disziplin zur Laufzeit um (Schützen-Aktion über Menü).
// Setzt Modus auf Probe und leert den Verlauf.
func (m *SessionManager) SetDiscipline(def *DisciplineDef) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if def != nil {
		m.rawDiscipline = def.Name
	} else {
		m.rawDiscipline = ""
	}
	m.setDisciplineLocked(def)
	if def != nil {
		log.Printf("Disziplin: %s (manuell gesetzt)", def.Name)
	}
}

// Solver liefert den aktuell gueltigen TDOA-Solver (lokal oder vom Server)
func (m *SessionManager) Solver() *TDOASolver {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.solver
}

// CurrentMode liefert den aktuellen Modus ("probe" | "wertung").
func (m *SessionManager) CurrentMode() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mode
}

// CurrentDiscipline liefert die aktuelle Disziplin (nil wenn keine).
func (m *SessionManager) CurrentDiscipline() *DisciplineDef {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.discipline
}

// SetMode schaltet den Modus um. Gibt false zurueck wenn die Umschaltung
// nicht erlaubt ist (wertung -> probe nach bereits abgegebenen Wertungsschuessen).
func (m *SessionManager) SetMode(newMode string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if newMode == m.mode {
		return true
	}
	if newMode == ModeProbe && m.wertungCount > 0 {
		return false // kein Zurueck nach erstem Wertungsschuss
	}
	m.mode = newMode
	if newMode == ModeWertung {
		m.wertungCount = 0
	}
	if newMode == ModeProbe {
		m.probeCount = 0
	}
	m.web.ResetHistory()
	m.web.BroadcastStatus(m.statusLocked())
	log.Printf("Modus: %s", newMode)
	return true
}

// NextShot liefert (sessionID, shot_no, mode, overcount) fuer ein neues
// Telegramm. counts=false (reject-Telegramme, pos_valid=0) zaehlt NICHT als
// Probe-/Wertungsschuss und loest keinen Auto-Switch/Disziplinabschluss aus,
// bekommt aber trotzdem eine fortlaufende shot_no (Meyton-Prinzip: nichts
// wird geloescht/uebersprungen, auch technisch verworfene Telegramme landen
// in der DB). sessionID=="" bedeutet: nicht in die DB schreiben.
func (m *SessionManager) NextShot(counts bool) (string, int, string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	mode := m.mode
	overcount := false

	if counts {
		switch mode {
		case ModeProbe:
			m.probeCount++
			disc := m.discipline
			if disc != nil && disc.TrialShots > 0 && m.probeCount >= disc.TrialShots {
				// Letzter Probeschuss erreicht: automatisch auf Wertung umschalten
				m.mode = ModeWertung
				m.wertungCount = 0
				m.web.ResetHistory()
				st := m.statusLocked()
				st.Notification = "Probeserie beendet – Wertung beginnt"
				m.web.BroadcastStatus(st)
				log.Printf("Probe beendet (%d Schuesse) – automatisch auf Wertung umgeschaltet",
					m.probeCount)
			}

		case ModeWertung:
			disc := m.discipline
			if disc != nil && disc.ScoringShots > 0 && m.wertungCount >= disc.ScoringShots {
				// Wertung bereits abgeschlossen – Ueberanzahl-Schuss
				overcount = true
			} else {
				m.wertungCount++
				if disc != nil && disc.ScoringShots > 0 && m.wertungCount >= disc.ScoringShots {
					// Letzter Wertungsschuss: Disziplin beendet
					st := m.statusLocked()
					st.Notification = "Disziplin beendet"
					m.web.BroadcastStatus(st)
					log.Printf("Disziplin beendet (%d Wertungsschuesse)", m.wertungCount)
				}
			}
		}
	}

	if m.sessionID == "" {
		return "", 0, mode, overcount
	}
	m.shotNo++
	return m.sessionID, m.shotNo, mode, overcount
}

// Status liefert eine Kopie des aktuellen Status fuer den Browser.
func (m *SessionManager) Status() StatusInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.statusLocked()
}

// statusLocked: muss unter m.mu gehalten werden.
func (m *SessionManager) statusLocked() StatusInfo {
	discName := m.rawDiscipline
	if m.discipline != nil {
		discName = m.discipline.Name
	}
	return StatusInfo{
		Mode:           m.mode,
		Discipline:     m.discipline,
		DisciplineName: discName,
		Shooter:        m.shooter,
		EventName:      m.eventName,
		EventType:      m.eventType,
		WertungCount:   m.wertungCount,
		HistoryGen:     m.web.HistoryGen(),
	}
}

// SaveState speichert den aktuellen Session-Zustand auf Disk.
// Darf von beliebigen Goroutinen ohne gehaltenen Lock aufgerufen werden.
func (m *SessionManager) SaveState() {
	m.mu.RLock()
	state := RecoverState{
		SessionID:     m.sessionID,
		Shooter:       m.shooter,
		RawDiscipline: m.rawDiscipline,
		Mode:          m.mode,
		ShotNo:        m.shotNo,
		ProbeCount:    m.probeCount,
		WertungCount:  m.wertungCount,
	}
	m.mu.RUnlock()
	if err := SaveRecoverState(m.cfg.ShotLogDir, m.cfg.LaneNo, state); err != nil {
		log.Printf("State speichern: %v", err)
	}
}

// tryRestore versucht den Zustand aus der lokalen State-Datei wiederherzustellen,
// sofern die übergebene sessionID mit der gespeicherten übereinstimmt.
// Muss unter m.mu aufgerufen werden.
func (m *SessionManager) tryRestoreLocked(sessionID string) {
	saved, err := LoadRecoverState(m.cfg.ShotLogDir, m.cfg.LaneNo)
	if err != nil {
		log.Printf("State lesen: %v", err)
		return
	}
	if saved == nil || saved.SessionID != sessionID {
		return
	}
	shots := LoadShotsForSession(m.cfg.ShotLogDir, m.cfg.LaneNo, sessionID)
	if len(shots) == 0 {
		return
	}
	m.shotNo       = saved.ShotNo
	m.probeCount   = saved.ProbeCount
	m.wertungCount = saved.WertungCount
	if saved.Mode == ModeWertung {
		m.mode = ModeWertung
	}
	m.web.RestoreHistory(shots) // greift intern auf ws.mu – muss OHNE m.mu aufgerufen werden
	log.Printf("Session: WIEDERHERGESTELLT %s | Probe %d | Wertung %d | %d Schüsse",
		sessionID, m.probeCount, m.wertungCount, len(shots))
}

// Run pollt den Server. Blockiert nicht – als Goroutine starten.
func (m *SessionManager) Run(ctx context.Context) {
	if m.cfg.ServerURL == "" {
		log.Printf("Session: kein server_url konfiguriert – lokaler Betrieb "+
			"(statische session_id %q)", m.cfg.SessionID)
		m.mu.Lock()
		m.sessionID = m.cfg.SessionID
		// Lokale Wiederherstellung ohne Server
		if m.sessionID != "" {
			saved, _ := LoadRecoverState(m.cfg.ShotLogDir, m.cfg.LaneNo)
			if saved != nil && saved.SessionID == m.sessionID {
				shots := LoadShotsForSession(m.cfg.ShotLogDir, m.cfg.LaneNo, m.sessionID)
				if len(shots) > 0 {
					m.shotNo       = saved.ShotNo
					m.probeCount   = saved.ProbeCount
					m.wertungCount = saved.WertungCount
					if saved.Mode == ModeWertung {
						m.mode = ModeWertung
					}
					m.mu.Unlock()
					m.web.RestoreHistory(shots)
					log.Printf("Session (lokal): WIEDERHERGESTELLT | %d Schüsse", len(shots))
					return
				}
			}
		}
		m.mu.Unlock()
		return
	}

	url := fmt.Sprintf("%s/api/lanes/%d/session",
		m.cfg.ServerURL, m.cfg.LaneNo)
	client := &http.Client{Timeout: 3 * time.Second}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	m.poll(client, url)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.poll(client, url)
		}
	}
}

func (m *SessionManager) poll(client *http.Client, url string) {
	resp, err := client.Get(url)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}

	var s *serverSession
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		log.Printf("Session: Antwort unlesbar: %v", err)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	newID := ""
	if s != nil {
		newID = s.SessionID
	}

	// --- Modus-Signal vom Server (unabhaengig vom Sessionwechsel) ---
	if s != nil && s.Mode == ModeWertung && m.mode == ModeProbe {
		m.mode = ModeWertung
		m.probeCount  = 0
		m.wertungCount = 0
		m.web.ResetHistory()
		m.web.BroadcastStatus(m.statusLocked())
		log.Printf("Modus: wertung (Server-Signal)")
	}

	if newID == m.sessionID {
		return
	}

	// --- Sessionwechsel ---
	if newID == "" {
		log.Printf("Session: Stand freigegeben (war %s)", m.sessionID)
		m.sessionID    = ""
		m.shooter      = ""
		m.rawDiscipline = ""
		m.shotNo       = 0
		m.probeCount   = 0
		m.wertungCount = 0
		m.mode         = ModeProbe
		m.discipline   = nil
		m.eventName    = ""
		m.eventType    = ""
		m.solver = NewTDOASolver(m.cfg)
		m.web.ResetHistory()
		m.web.BroadcastStatus(m.statusLocked())
		ClearRecoverState(m.cfg.ShotLogDir, m.cfg.LaneNo)
		return
	}

	// Kalibrierung vom Server
	sensors := make([]SensorPos, len(s.SensorPosRaw))
	for i, p := range s.SensorPosRaw {
		sensors[i] = SensorPos{X: p.X, Y: p.Y}
	}
	if len(sensors) >= 3 {
		m.solver = NewTDOASolverParams(sensors, s.SoundSpeedMPS,
			s.PlateAngleDeg, s.PlateOffsetX, s.PlateOffsetY)
	} else {
		log.Printf("Session: Server-Kalibrierung unvollstaendig – "+
			"behalte lokale (Sensoren: %d)", len(sensors))
	}

	// Disziplin + Modus (via setDisciplineLocked, haelt bereits m.mu)
	m.rawDiscipline = s.Discipline // Server-Name merken, auch wenn kein lokaler Match
	m.setDisciplineLocked(m.LookupDiscipline(s.Discipline))
	if m.discipline != nil {
		log.Printf("Session: Disziplin %s (Probe %d / Wertung %d)",
			m.discipline.Name, m.discipline.TrialShots, m.discipline.ScoringShots)
	}
	// Server kann direkt Wertung erzwingen
	if s.Mode == ModeWertung {
		m.mode = ModeWertung
	}

	m.sessionID = newID
	m.shooter   = s.Shooter
	m.eventName = s.EventName
	m.eventType = s.EventType
	m.shotNo    = 0
	// wertungCount und History wurden bereits von setDisciplineLocked zurueckgesetzt.
	// Jetzt prüfen ob ein gespeicherter Zustand für diese Session vorliegt.
	m.tryRestoreLocked(newID)
	m.web.BroadcastStatus(m.statusLocked())
	log.Printf("Session: NEU %s | %s | %s | v=%.0f m/s | Modus: %s",
		newID, s.Shooter, s.Discipline, s.SoundSpeedMPS, m.mode)
}
