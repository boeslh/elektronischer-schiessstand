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

	// Disziplinparameter, vom Server autoritativ mitgeliefert (siehe
	// server/store.go ActiveSessionForLane) - ersetzt den frueheren
	// Namens-Lookup gegen die lokale disciplines.json, der bei Abweichungen
	// zwischen Server und Stand-PC (fehlender/veralteter lokaler Eintrag)
	// dazu fuehrte, dass Probe/Wertung-Zaehlung und Scheiben-Abschluss nie
	// griffen.
	TrialShots     int  `json:"trial_shots"`
	ScoringShots   int  `json:"scoring_shots"`
	ShotsPerSeries int  `json:"shots_per_series"`
	DecimalScoring bool `json:"decimal_scoring"`
	TargetNo       int  `json:"target_no"`

	// Authoritativer Schusszaehler-Stand dieser Session, server-seitig aus
	// den tatsaechlich gespeicherten Schuessen ermittelt (server/store.go
	// ActiveSessionForLane) - wird bei JEDER neuen Zuweisung dieser Session
	// uebernommen (Neustart am selben Stand-PC, aber auch Standwechsel auf
	// einen ANDEREN Stand-PC, der lokal keine Vorgeschichte fuer diese
	// Session hat). Verhindert doppelte/falsche Seriennummern und falsches
	// Probe/Wertung-Zaehlen nach einem Standwechsel.
	LastShotNo        int `json:"last_shot_no"`
	ProbeCount        int `json:"probe_count"`
	WertungShotsFired int `json:"wertung_shots_fired"`
}

// Das Datenmodell speichert Sensorpositionen als [{"x":..,"y":..}]
type sensorXY struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// PSLaneInfoLocal: Antwort von /api/lanes/{no}/preisschiessen - Spiegel von
// PSLaneInfo im zentralen Server (server/preisschiessen.go). nil/absent
// bedeutet: an diesem Stand laeuft aktuell kein Preisschiessen.
type PSLaneInfoLocal struct {
	PreisschiessenName string         `json:"preisschiessen_name"`
	TeilnehmerID       string         `json:"teilnehmer_id"`
	TeilnehmerNr       int            `json:"teilnehmer_nr"`
	ShooterName        string         `json:"shooter_name"`
	Guthaben           float64        `json:"guthaben"`
	Pending            bool           `json:"pending"`
	CurrentScheibeName string         `json:"current_scheibe_name,omitempty"`
	CurrentTargetColor string         `json:"current_target_color,omitempty"`
	ScheibenTypen      []PSScheibeTyp `json:"scheiben_typen"`
	Angebot            PSAngebotLocal `json:"angebot"`
}

type PSScheibeTyp struct {
	ScheibeID string `json:"scheibe_id"`
	Name      string `json:"name"`
	Gekauft   int    `json:"gekauft"`
	Beendet   int    `json:"beendet"`
}

type PSAngebotLocal struct {
	Scheiben []PSAngebotItem `json:"scheiben"`
	Sets     []PSAngebotItem `json:"sets"`
}

// PSAngebotItem deckt sowohl PSScheibe als auch PSSet ab - vom Browser
// werden nur id/name/price genutzt.
type PSAngebotItem struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Price float64 `json:"price"`
}

// StatusInfo wird an den Browser gesendet wenn sich Modus oder Disziplin aendern.
type StatusInfo struct {
	LaneNo         int            `json:"lane_no"`
	Mode           string         `json:"mode"`
	Discipline     *DisciplineDef `json:"discipline"`
	DisciplineName string         `json:"discipline_name,omitempty"` // Klarname (Fallback wenn kein lok. Match)
	Shooter        string         `json:"shooter"`
	EventName      string         `json:"event_name,omitempty"`
	EventType      string         `json:"event_type,omitempty"`   // einzel | runde | gruppe
	WertungCount   int            `json:"wertung_count"`          // abgegebene Wertungsschuesse
	Notification   string         `json:"notification,omitempty"` // einmaliger Hinweis fuer den Browser
	HistoryGen     int            `json:"history_gen"`            // aendert sich bei jedem ResetHistory (Session-/Modus-/Disziplinwechsel)
	DevMode        bool           `json:"dev_mode"`               // Entwicklermodus (Schuesse per Mausklick), global vom Server
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

	mode              string         // ModeProbe | ModeWertung
	discipline        *DisciplineDef // aktuelle Disziplin; nil = keine
	rawDiscipline     string         // Disziplinname vom Server (auch wenn kein lokaler Match)
	probeCount        int            // Probeschuesse in dieser Disziplinen-Phase
	wertungCount      int            // Wertungsschuesse in dieser Session (gedeckelt auf ScoringShots)
	wertungShotsFired int            // ALLE Wertungsschuesse inkl. Ueberzahl (fuer Serien-Nummerierung)
	eventName         string         // Veranstaltungsname (leer = Training/kein Event)
	eventType         string         // einzel | runde | gruppe

	psInfo  *PSLaneInfoLocal // Preisschiessen-Zustand des Stands; nil = kein Preisschiessen
	devMode bool             // Entwicklermodus (Schuesse per Mausklick), global vom Server gepollt

	// fuer PollNow() (sofortiges Nachziehen nach lokaler Aktion, ohne auf
	// den naechsten 3s-Tick zu warten) - in Run() gesetzt.
	pollClient *http.Client
	sessionURL string
	psURL      string
	devModeURL string
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
	m.wertungShotsFired = 0
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

// SetMode schaltet den Modus um. ok=false wenn die Umschaltung nicht erlaubt
// ist (wertung -> probe nach bereits abgegebenen Wertungsschuessen, oder die
// Disziplin hat mit 0 Probeschuessen ueberhaupt keine Probephase); reason
// beschreibt in diesem Fall den Grund fuer die Fehlermeldung im Browser.
func (m *SessionManager) SetMode(newMode string) (ok bool, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if newMode == m.mode {
		return true, ""
	}
	if newMode == ModeProbe {
		if m.discipline != nil && m.discipline.TrialShots == 0 {
			return false, "Disziplin hat keine Probephase (0 Probeschuesse konfiguriert)"
		}
		if m.wertungCount > 0 {
			return false, "Wertungsschuesse bereits abgegeben"
		}
	}
	m.mode = newMode
	if newMode == ModeWertung {
		m.wertungCount = 0
		m.wertungShotsFired = 0
	}
	if newMode == ModeProbe {
		m.probeCount = 0
	}
	m.web.ResetHistory()
	m.web.BroadcastStatus(m.statusLocked())
	log.Printf("Modus: %s", newMode)
	return true, ""
}

// NextShot liefert (sessionID, shot_no, mode, overcount) fuer ein neues
// Telegramm. counts=false (reject-Telegramme, pos_valid=0) zaehlt NICHT als
// Probe-/Wertungsschuss und loest keinen Auto-Switch/Disziplinabschluss aus,
// bekommt aber trotzdem eine fortlaufende shot_no (Meyton-Prinzip: nichts
// wird geloescht/uebersprungen, auch technisch verworfene Telegramme landen
// in der DB). sessionID=="" bedeutet: nicht in die DB schreiben.
// seriesIndex berechnet die 1-basierte Seriennummer fuer den n-ten Schuss
// (n=1,2,3,...) einer Zaehlung (Probe- oder Wertungsschuesse getrennt) bei
// gegebener Serienlaenge. Serien MUESSEN je Session eindeutig und
// deterministisch sein - deshalb wird hier fest aus der laufenden Zaehlung
// berechnet statt irgendwo anders (mehrfach, inkonsistent) hergeleitet.
func seriesIndex(shotIndex, shotsPerSeries int) int {
	if shotsPerSeries <= 0 {
		shotsPerSeries = 10
	}
	return (shotIndex-1)/shotsPerSeries + 1
}

func (m *SessionManager) NextShot(counts bool) (sessionID string, shotNo int, mode string, overcount bool, seriesNo *int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	mode = m.mode

	shotsPerSeries := 10
	if m.discipline != nil && m.discipline.ShotsPerSeries > 0 {
		shotsPerSeries = m.discipline.ShotsPerSeries
	}

	if counts {
		switch mode {
		case ModeProbe:
			m.probeCount++
			n := seriesIndex(m.probeCount, shotsPerSeries)
			seriesNo = &n
			disc := m.discipline
			if disc != nil && disc.TrialShots > 0 && m.probeCount >= disc.TrialShots {
				// Letzter Probeschuss erreicht: automatisch auf Wertung umschalten
				m.mode = ModeWertung
				m.wertungCount = 0
				m.wertungShotsFired = 0
				m.web.ResetHistory()
				st := m.statusLocked()
				st.Notification = "Probeserie beendet – Wertung beginnt"
				m.web.BroadcastStatus(st)
				log.Printf("Probe beendet (%d Schuesse) – automatisch auf Wertung umgeschaltet",
					m.probeCount)
			}

		case ModeWertung:
			// wertungShotsFired zaehlt ALLE Wertungsschuesse inkl. Ueberzahl
			// (fuer eine luecken- und mehrdeutigkeitsfreie Serien-Nummerierung),
			// wertungCount bleibt wie bisher auf ScoringShots gedeckelt.
			m.wertungShotsFired++
			n := seriesIndex(m.wertungShotsFired, shotsPerSeries)
			seriesNo = &n
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
		return "", 0, mode, overcount, seriesNo
	}
	m.shotNo++
	return m.sessionID, m.shotNo, mode, overcount, seriesNo
}

// PreisschiessenAwaitingChoice: true wenn dieser Stand gerade einem
// Preisschiessen-Teilnehmer zugewiesen ist, der noch keine Scheibe zum
// Schiessen gewaehlt hat (Auswahlmenue offen) - entweder die allererste
// Zuweisung, oder weil die zuletzt geschossene Scheibe bereits die
// erforderliche Schusszahl erreicht hat. In BEIDEN Faellen kann ein jetzt
// eintreffender Schuss keiner Scheibe zugeordnet werden: im zweiten Fall
// zeigt m.sessionID noch auf die bereits abgeschlossene alte Session (die
// zugrundeliegende DB-Session bleibt technisch "aktiv", bis der Schuetze die
// naechste Scheibe waehlt) - ein sessionID=="" -Check allein wuerde diesen
// Fall verpassen. Bei anderen Wettkaempfen (keine Scheibenauswahl, immer
// eine fest zugewiesene Session) kommt dieser Zustand nicht vor.
func (m *SessionManager) PreisschiessenAwaitingChoice() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.psInfo != nil && m.psInfo.Pending
}

// WarnPreisschiessenNoScheibe informiert den Stand-PC-Bildschirm per
// Notification, dass gerade ein Schuss verworfen wurde, weil im
// Preisschiessen-Auswahlmenue noch keine Scheibe gewaehlt ist (siehe
// PreisschiessenAwaitingChoice, aufgerufen aus main.go).
func (m *SessionManager) WarnPreisschiessenNoScheibe() {
	m.mu.RLock()
	st := m.statusLocked()
	m.mu.RUnlock()
	st.Notification = "Schuss ungültig – bitte zuerst im Menü eine Scheibe wählen!"
	m.web.BroadcastStatus(st)
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
		LaneNo:         m.cfg.LaneNo,
		Mode:           m.mode,
		Discipline:     m.discipline,
		DisciplineName: discName,
		Shooter:        m.shooter,
		EventName:      m.eventName,
		EventType:      m.eventType,
		WertungCount:   m.wertungCount,
		HistoryGen:     m.web.HistoryGen(),
		DevMode:        m.devMode,
	}
}

// SaveState speichert den aktuellen Session-Zustand auf Disk.
// Darf von beliebigen Goroutinen ohne gehaltenen Lock aufgerufen werden.
func (m *SessionManager) SaveState() {
	m.mu.RLock()
	state := RecoverState{
		SessionID:         m.sessionID,
		Shooter:           m.shooter,
		RawDiscipline:     m.rawDiscipline,
		Mode:              m.mode,
		ShotNo:            m.shotNo,
		ProbeCount:        m.probeCount,
		WertungCount:      m.wertungCount,
		WertungShotsFired: m.wertungShotsFired,
	}
	m.mu.RUnlock()
	if err := SaveRecoverState(m.cfg.ShotLogDir, m.cfg.LaneNo, state); err != nil {
		log.Printf("State speichern: %v", err)
	}
}

// tryRestore versucht den Zustand aus der lokalen State-Datei wiederherzustellen,
// sofern die übergebene sessionID mit der gespeicherten übereinstimmt.
// Muss unter m.mu aufgerufen werden. Liefert true, wenn die sichtbare
// Trefferliste dabei wiederhergestellt wurde (siehe restoreHistoryFromServer
// als Fallback in poll(), wenn dieser Stand-PC-Prozess selbst keine lokale
// Vorgeschichte fuer die Session hat).
func (m *SessionManager) tryRestoreLocked(sessionID string) bool {
	saved, err := LoadRecoverState(m.cfg.ShotLogDir, m.cfg.LaneNo)
	if err != nil {
		log.Printf("State lesen: %v", err)
		return false
	}
	if saved == nil || saved.SessionID != sessionID {
		return false
	}
	shots := LoadShotsForSession(m.cfg.ShotLogDir, m.cfg.LaneNo, sessionID)
	if len(shots) == 0 {
		return false
	}
	m.shotNo = saved.ShotNo
	m.probeCount = saved.ProbeCount
	m.wertungCount = saved.WertungCount
	m.wertungShotsFired = saved.WertungShotsFired
	if saved.Mode == ModeWertung {
		m.mode = ModeWertung
	}
	m.web.RestoreHistory(shots) // greift intern auf ws.mu – muss OHNE m.mu aufgerufen werden
	log.Printf("Session: WIEDERHERGESTELLT %s | Probe %d | Wertung %d | %d Schüsse",
		sessionID, m.probeCount, m.wertungCount, len(shots))
	return true
}

// serverShotRow bildet das JSON-Format von GET /api/sessions/{id}/shots ab
// (server/store.go Store.SessionShots) - nur die fuer die Anzeige relevanten
// Felder.
type serverShotRow struct {
	ShotNo         int     `json:"shot_no"`
	Kind           string  `json:"kind"`
	Status         string  `json:"status"`
	XMM            float64 `json:"x_mm"`
	YMM            float64 `json:"y_mm"`
	Ring           int     `json:"ring"`
	Decimal        float64 `json:"decimal"`
	InnerTen       bool    `json:"inner_ten"`
	CenterDistance float64 `json:"center_distance"`
	FiredAt        string  `json:"fired_at"`
}

// fetchServerShots laedt die bereits gespeicherten Schuesse einer Session
// vom zentralen Server - Fallback fuer die sichtbare Trefferliste, wenn
// dieser Stand-PC-Prozess selbst keine lokale Vorgeschichte dafuer hat (z.B.
// nach "Stand freigeben" + erneuter Wahl derselben Scheibe: die Session wird
// serverseitig reaktiviert/fortgesetzt, siehe Store.AssignTeilnehmerLane,
// aber die lokale Recover-Datei wurde beim Freigeben bewusst geloescht).
// Macht bewusst KEINEN Gebrauch von m.mu - wird von poll() VOR dem Lock
// aufgerufen, damit ein waehrend des Requests eintreffender echter Schuss
// (NextShot haelt ebenfalls m.mu) nicht blockiert wird.
func (m *SessionManager) fetchServerShots(client *http.Client, sessionID string) []*Shot {
	resp, err := client.Get(m.cfg.ServerURL + "/api/sessions/" + sessionID + "/shots")
	if err != nil {
		log.Printf("Trefferliste vom Server laden: %v", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var rows []serverShotRow
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		log.Printf("Trefferliste vom Server: Antwort unlesbar: %v", err)
		return nil
	}
	shots := make([]*Shot, 0, len(rows))
	for _, row := range rows {
		if row.Status == "annulled" {
			continue
		}
		mode := ModeProbe
		if row.Kind == "match" {
			mode = ModeWertung
		}
		shots = append(shots, &Shot{
			SessionID:      sessionID,
			FiredAt:        row.FiredAt,
			XMM:            row.XMM,
			YMM:            row.YMM,
			Ring:           row.Ring,
			Decimal:        row.Decimal,
			InnerTen:       row.InnerTen,
			CenterDistance: row.CenterDistance,
			Mode:           mode,
			Rejected:       row.Status == "rejected",
		})
	}
	return shots
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
					m.shotNo = saved.ShotNo
					m.probeCount = saved.ProbeCount
					m.wertungCount = saved.WertungCount
					m.wertungShotsFired = saved.WertungShotsFired
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
	psURL := fmt.Sprintf("%s/api/lanes/%d/preisschiessen",
		m.cfg.ServerURL, m.cfg.LaneNo)
	devModeURL := m.cfg.ServerURL + "/api/settings/standpc-dev-mode"
	client := &http.Client{Timeout: 3 * time.Second}
	m.pollClient, m.sessionURL, m.psURL, m.devModeURL = client, url, psURL, devModeURL
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	m.poll(client, url)
	m.pollPreisschiessen(client, psURL)
	m.pollDevMode(client, devModeURL)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.poll(client, url)
			m.pollPreisschiessen(client, psURL)
			m.pollDevMode(client, devModeURL)
		}
	}
}

// PollNow zieht Session-, Preisschiessen- und Entwicklermodus-Zustand sofort
// nach, statt auf den naechsten 3s-Tick zu warten - fuer schnelles Feedback
// nach einer lokalen Aktion (Scheibe gewaehlt, Scheibe/Set gebucht).
func (m *SessionManager) PollNow() {
	if m.pollClient == nil {
		return
	}
	m.poll(m.pollClient, m.sessionURL)
	m.pollPreisschiessen(m.pollClient, m.psURL)
	m.pollDevMode(m.pollClient, m.devModeURL)
}

// pollDevMode holt den globalen Entwicklermodus-Schalter (Schuesse per
// Mausklick, ueber die Server-Einstellungen gesetzt) und broadcastet den
// Status erneut, wenn er sich geaendert hat.
func (m *SessionManager) pollDevMode(client *http.Client, url string) {
	resp, err := client.Get(url)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		log.Printf("Entwicklermodus: Antwort unlesbar: %v", err)
		return
	}
	m.mu.Lock()
	changed := m.devMode != body.Enabled
	m.devMode = body.Enabled
	st := m.statusLocked()
	m.mu.Unlock()
	if changed {
		m.web.BroadcastStatus(st)
	}
}

// DevMode liefert den aktuell gepollten Entwicklermodus-Zustand.
func (m *SessionManager) DevMode() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.devMode
}

// pollPreisschiessen holt den Preisschiessen-Zustand des Stands und
// broadcastet ihn an den Browser (auch wenn er sich nicht geaendert hat -
// das Payload ist klein, ein Diff lohnt sich nicht).
func (m *SessionManager) pollPreisschiessen(client *http.Client, url string) {
	resp, err := client.Get(url)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var info *PSLaneInfoLocal
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		log.Printf("Preisschiessen: Antwort unlesbar: %v", err)
		return
	}
	m.mu.Lock()
	m.psInfo = info
	m.mu.Unlock()
	m.web.BroadcastPreisschiessen(info)
}

// PSInfo liefert den zuletzt gepollten Preisschiessen-Zustand (nil = keins).
func (m *SessionManager) PSInfo() *PSLaneInfoLocal {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.psInfo
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

	newID := ""
	if s != nil {
		newID = s.SessionID
	}

	// Bei einem Sessionwechsel auf eine Session mit bereits vorhandenen
	// Schuessen (last_shot_no>0, z.B. eine per "Stand freigeben" beendete
	// und jetzt wiederaufgenommene Scheibe) muss die sichtbare Trefferliste
	// ggf. vom Server nachgeladen werden - siehe fetchServerShots/Fallback
	// unten. Der Request laeuft bewusst HIER, VOR dem Haupt-Lock weiter unten
	// (Netzwerk-I/O darf m.mu nicht blockieren, siehe NextShot).
	var serverShots []*Shot
	m.mu.Lock()
	needsHistoryFetch := newID != "" && newID != m.sessionID && s.LastShotNo > 0
	m.mu.Unlock()
	if needsHistoryFetch {
		serverShots = m.fetchServerShots(client, newID)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// --- Modus-Signal vom Server (unabhaengig vom Sessionwechsel) ---
	if s != nil && s.Mode == ModeWertung && m.mode == ModeProbe {
		m.mode = ModeWertung
		m.probeCount  = 0
		m.wertungCount = 0
		m.wertungShotsFired = 0
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
		m.wertungShotsFired = 0
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

	// Disziplin + Modus (via setDisciplineLocked, haelt bereits m.mu).
	// Die Parameter kommen direkt vom Server (autoritativ) statt per Namen
	// aus der lokalen disciplines.json nachgeschlagen zu werden - so greifen
	// Probe/Wertung-Zaehlung und automatischer Scheibenabschluss zuverlaessig,
	// auch wenn der Disziplinname lokal nicht (mehr) bekannt ist.
	m.rawDiscipline = s.Discipline
	m.setDisciplineLocked(&DisciplineDef{
		Name:           s.Discipline,
		TargetNo:       s.TargetNo,
		TrialShots:     s.TrialShots,
		ScoringShots:   s.ScoringShots,
		ShotsPerSeries: s.ShotsPerSeries,
		DecimalScoring: s.DecimalScoring,
	})
	if m.discipline != nil {
		log.Printf("Session: Disziplin %s (Probe %d / Wertung %d)",
			m.discipline.Name, m.discipline.TrialShots, m.discipline.ScoringShots)
	}

	m.sessionID = newID
	m.shooter   = s.Shooter
	m.eventName = s.EventName
	m.eventType = s.EventType

	// Zaehler-Baseline IMMER vom Server uebernehmen (autoritativ aus den
	// tatsaechlich gespeicherten Schuessen) - deckt sowohl einen Neustart am
	// selben Stand-PC als auch einen Standwechsel auf einen ANDEREN Stand-PC
	// ab, der lokal keine Vorgeschichte fuer diese Session hat. Ohne das
	// wuerden Zaehlung und Serien-Nummerierung nach einem Standwechsel bei 0
	// neu beginnen und mit bereits gespeicherten Schuessen kollidieren.
	m.shotNo = s.LastShotNo
	m.probeCount = s.ProbeCount
	m.wertungShotsFired = s.WertungShotsFired
	m.wertungCount = s.WertungShotsFired
	if m.discipline != nil && m.discipline.ScoringShots > 0 && m.wertungCount > m.discipline.ScoringShots {
		m.wertungCount = m.discipline.ScoringShots
	}
	if s.WertungShotsFired > 0 {
		m.mode = ModeWertung
	}
	// Server kann direkt Wertung erzwingen
	if s.Mode == ModeWertung {
		m.mode = ModeWertung
	}

	// Lokale Zustandsdatei kann die Baseline oben noch verfeinern, wenn
	// DIESER Stand-PC die Session bereits kennt (Neustart am selben Stand) -
	// insbesondere fuer noch nicht beim Server bestaetigte Schuesse aus der
	// DB-Retry-Queue. Stellt bei Uebereinstimmung ausserdem die sichtbare
	// Trefferliste wieder her. Kennt dieser Stand-PC-Prozess die Session
	// lokal nicht (z.B. "Stand freigeben" hat die lokale Recover-Datei
	// geloescht, oder echter Standwechsel), wird die Trefferliste ersatzweise
	// aus der oben (VOR dem Lock) bereits vom Server geladenen Liste
	// wiederhergestellt.
	if !m.tryRestoreLocked(newID) && len(serverShots) > 0 {
		m.web.RestoreHistory(serverShots)
		log.Printf("Session: Trefferliste vom Server wiederhergestellt (%d Schuesse)", len(serverShots))
	}
	m.web.BroadcastStatus(m.statusLocked())
	log.Printf("Session: NEU %s | %s | %s | v=%.0f m/s | Modus: %s",
		newID, s.Shooter, s.Discipline, s.SoundSpeedMPS, m.mode)
}
