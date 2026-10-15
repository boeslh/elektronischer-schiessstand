// timing.go – Schiesszeit am Stand (Uhrzeit / Soft / Hard).
//
// Der Server liefert je Session (server/store.go sessionTiming): Zeitmodus
// des Wettkampfs, die Zeiten der Disziplin (Gesamtzeit fuer Probe und
// Wertung oder getrennte Probe- und Wertungszeit), eine Zeitgutschrift und
// den Startzeitpunkt ("Zeit starten" in der Standsteuerung, sonst Beginn
// der Probe).
//
// Umschalten Probe -> Wertung (nur bei getrennten Zeiten, sonst immer
// individuell):
//
//	individuell  der Schuetze schaltet selbst um; die Wertungszeit beginnt
//	             mit Ablauf der Probezeit oder frueher mit dem Umschalten
//	             (die restliche Probezeit verfaellt). Bei Hard werden
//	             Probeschuesse nach Ablauf der Probezeit nicht gewertet.
//	automatisch  wie individuell, nach Ablauf der Probezeit schaltet der
//	             Stand selbst auf Wertung.
//	zentral      Zeitstart und Umschalten nur ueber die Standsteuerung; der
//	             Schuetze kann nicht umschalten. Vor "Zeit starten" und nach
//	             Ablauf der Probezeit bis zur Wertungsfreigabe wird kein
//	             Schuss gewertet; die Wertungszeit beginnt mit der Freigabe.
//
// Hard: nach Ablauf der Wertungszeit wird kein Schuss mehr gewertet (Schuss
// wird als ungueltig protokolliert). Soft: Countdown ohne Wirkung.
package main

import (
	"log"
	"time"
)

const (
	TimeModeUhrzeit = "uhrzeit"
	TimeModeSoft    = "soft"
	TimeModeHard    = "hard"

	SwitchIndividuell = "individuell"
	SwitchZentral     = "zentral"
	SwitchAutomatisch = "automatisch"
)

// serverTiming: Feld "timing" von /api/lanes/{no}/session.
type serverTiming struct {
	Mode          string `json:"mode"`
	Switch        string `json:"switch"`
	Combined      bool   `json:"combined"`
	SightingTimeS int    `json:"sighting_time_s"`
	MatchTimeS    int    `json:"match_time_s"`
	BonusS        int    `json:"bonus_s"`
	StartMs       int64  `json:"start_ms"`
	MatchStartMs  int64  `json:"match_start_ms"`
	ServerNowMs   int64  `json:"server_now_ms"`
}

// TimerInfo: Zustand der Uhr fuer die Anzeige (Zeitpunkte in Stand-PC-Zeit,
// Unix-ms; der Browser gleicht seine Uhr ueber StatusInfo.NowMs ab).
type TimerInfo struct {
	Mode            string `json:"mode"`   // uhrzeit | soft | hard
	Switch          string `json:"switch"` // individuell | zentral | automatisch
	Separate        bool   `json:"separate"`
	Started         bool   `json:"started"`
	ProbeDeadlineMs int64  `json:"probe_deadline_ms,omitempty"` // nur getrennte Zeiten
	DeadlineMs      int64  `json:"deadline_ms,omitempty"`       // 0 = Wertungszeit laeuft noch nicht (zentral)
	ProbeS          int    `json:"probe_s,omitempty"`           // fuer die Anzeige vor dem Start
	TotalS          int    `json:"total_s,omitempty"`           // Gesamt- bzw. Wertungszeit inkl. Gutschrift
}

func nowMs() int64 { return time.Now().UnixMilli() }

// applyTimingLocked: neue Zeitdaten vom Server; true wenn sich fuer die
// Anzeige etwas geaendert hat.
func (m *SessionManager) applyTimingLocked(t *serverTiming) bool {
	var nt serverTiming
	if t != nil {
		nt = *t
		if nt.ServerNowMs > 0 {
			m.clockOffsetMs = nt.ServerNowMs - nowMs()
		}
	}
	nt.ServerNowMs = 0
	old := m.timing
	m.timing = nt
	return old != nt
}

// toLocal: Server-Zeitpunkt -> Stand-PC-Zeit.
func (m *SessionManager) toLocal(serverMs int64) int64 {
	if serverMs <= 0 {
		return 0
	}
	return serverMs - m.clockOffsetMs
}

// markWertungLocked: Wechsel in die Wertung merken (Beginn der Wertungszeit
// bei getrennten Zeiten).
func (m *SessionManager) markWertungLocked() {
	if m.wertungSinceMs == 0 {
		m.wertungSinceMs = nowMs()
	}
}

// switchLocked: wirksamer Umschaltmodus.
func (m *SessionManager) switchLocked() string {
	t := m.timing
	if t.Combined || t.SightingTimeS <= 0 || t.MatchTimeS <= 0 || t.Switch == "" || m.sessionID == "" {
		return SwitchIndividuell
	}
	return t.Switch
}

// timerLocked: Zeitstrahl der Session. Die Zeitpunkte werden auch im
// Zeitmodus "uhrzeit" berechnet (Umschalten automatisch/zentral braucht
// sie), die Anzeige zeigt dann aber nur die Uhrzeit.
func (m *SessionManager) timerLocked() TimerInfo {
	t := m.timing
	ti := TimerInfo{Mode: t.Mode, Switch: m.switchLocked()}
	if ti.Mode == "" {
		ti.Mode = TimeModeUhrzeit
	}
	if t.MatchTimeS <= 0 || m.sessionID == "" {
		ti.Mode = TimeModeUhrzeit
		return ti
	}
	ti.Separate = !t.Combined
	ti.TotalS = t.MatchTimeS + t.BonusS
	if ti.Separate {
		ti.ProbeS = t.SightingTimeS
	}
	start := m.toLocal(t.StartMs)
	if start == 0 {
		return ti
	}
	ti.Started = true
	if !ti.Separate {
		ti.DeadlineMs = start + int64(ti.TotalS)*1000
		return ti
	}
	ti.ProbeDeadlineMs = start + int64(t.SightingTimeS)*1000
	ws := m.wertungSinceMs
	if ws == 0 {
		ws = m.toLocal(t.MatchStartMs)
	}
	if ti.Switch == SwitchZentral {
		// Wertungszeit erst ab der Freigabe ueber die Standsteuerung
		if ws > 0 {
			ti.DeadlineMs = ws + int64(ti.TotalS)*1000
		}
		return ti
	}
	wStart := ti.ProbeDeadlineMs
	if ws > 0 && ws < wStart {
		wStart = ws
	}
	ti.DeadlineMs = wStart + int64(ti.TotalS)*1000
	return ti
}

// probeExpiredSwitchLocked: Umschaltmodus automatisch - Probezeit
// abgelaufen, Stand ist noch in der Probe -> selbst in die Wertung wechseln.
func (m *SessionManager) probeExpiredSwitchLocked() bool {
	ti := m.timerLocked()
	if ti.Switch != SwitchAutomatisch || !ti.Started || m.mode != ModeProbe {
		return false
	}
	if nowMs() < ti.ProbeDeadlineMs {
		return false
	}
	m.mode = ModeWertung
	m.wertungCount = 0
	m.wertungShotsFired = 0
	m.wertungSinceMs = ti.ProbeDeadlineMs
	m.web.ResetHistory()
	st := m.statusLocked()
	st.Notification = "Probezeit abgelaufen – Wertung beginnt"
	m.web.BroadcastStatus(st)
	log.Printf("Probezeit abgelaufen – automatisch auf Wertung umgeschaltet")
	return true
}

// CheckTime: vor jedem Schuss. Liefert eine Begruendung, wenn der Schuss
// wegen Zeit- oder Umschaltregel nicht gewertet werden darf (der Schuetze
// bekommt dazu einen Hinweis).
func (m *SessionManager) CheckTime() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.probeExpiredSwitchLocked()
	ti := m.timerLocked()
	now := nowMs()
	reason, note := "", ""
	switch {
	case ti.Switch == SwitchZentral && !ti.Started:
		reason, note = "Schießzeit noch nicht gestartet", "Schießzeit noch nicht gestartet – Schuss wird nicht gewertet"
	case ti.Switch == SwitchZentral && m.mode == ModeProbe && now >= ti.ProbeDeadlineMs:
		reason, note = "Probezeit abgelaufen", "Probezeit abgelaufen – bitte auf die Wertungsfreigabe warten"
	case ti.Switch == SwitchZentral && m.mode == ModeProbe && m.discipline != nil &&
		m.discipline.TrialShots > 0 && m.probeCount >= m.discipline.TrialShots:
		reason, note = "Probeschüsse ausgeschöpft", "Probeschüsse ausgeschöpft – bitte auf die Wertungsfreigabe warten"
	case ti.Mode == TimeModeHard && ti.Started && ti.Separate && m.mode == ModeProbe && now >= ti.ProbeDeadlineMs:
		reason, note = "Probezeit abgelaufen", "Probezeit abgelaufen – Schuss wird nicht gewertet, bitte auf Wertung umschalten"
	case ti.Mode == TimeModeHard && ti.Started && ti.DeadlineMs > 0 && now > ti.DeadlineMs:
		reason, note = "Schießzeit abgelaufen", "Schießzeit abgelaufen – Schuss wird nicht gewertet"
	}
	if reason != "" {
		st := m.statusLocked()
		st.Notification = note
		m.web.BroadcastStatus(st)
	}
	return reason
}

// ShooterMayToggle: darf der Schuetze selbst zwischen Probe und Wertung
// umschalten? Bei zentralem Umschalten nur die Standsteuerung.
func (m *SessionManager) shooterMayToggleLocked() bool {
	return m.switchLocked() != SwitchZentral
}

// tickTiming: periodisch (Poll-Takt) - Hard-Wechsel in die Wertung auch
// ohne Schuss.
func (m *SessionManager) tickTiming() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.probeExpiredSwitchLocked()
}
