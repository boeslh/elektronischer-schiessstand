// ============================================================================
// session.go – Ablaufsteuerung einer Wertmaschinen-Erfassung: Disziplin vom
// Server aufloesen, Konfigurationsstring holen, RM starten, Schuesse sammeln
// (mit Bediener-Korrekturmoeglichkeit bei unsicheren Werten), Ergebnis zum
// Server uebertragen. Siehe Konzept .claude/plans/wise-scribbling-abelson.md
// Abschnitt 4/5.
// ============================================================================
package main

import (
	"fmt"
	"net/url"
	"sync"
	"time"

	"wertmaschine/disag"
)

// ReadShot: ein empfangener Schuss inkl. Original-Flag der Wertmaschine -
// wird bei Bedarf vom Bediener durch manuelles Ueberschreiben von Ring/
// Decimal korrigiert (siehe Corrected), das Original bleibt zur
// Nachvollziehbarkeit erhalten.
type ReadShot struct {
	ManualShotInput
	RawFlag   string `json:"raw_flag"`
	Corrected bool   `json:"corrected"`
}

type Session struct {
	mu sync.Mutex

	cfg    *Config
	client *ServerClient
	web    *WebServer

	machine disag.Machine

	mode             string // "preisschiessen" | "rundenwettkampf"
	preisschiessenID string
	physicalSerial   string
	starterID        string
	disciplineID     string

	connected bool
	statusMsg string
	shots     []ReadShot

	lastConfigString string // fuer Recover() - siehe dort

	devMode bool // globaler Entwicklermodus vom Server (siehe pollDevMode) - schaltet den Rohdaten-Debug-Mitschnitt frei
}

func NewSession(cfg *Config, client *ServerClient) *Session {
	return &Session{cfg: cfg, client: client}
}

// StatusSnapshot fuer die Bedienoberflaeche (GET /status, initial SSE-Event).
type StatusSnapshot struct {
	Mode             string     `json:"mode"`
	PreisschiessenID string     `json:"preisschiessen_id,omitempty"`
	PhysicalSerial   string     `json:"physical_serial,omitempty"`
	StarterID        string     `json:"starter_id,omitempty"`
	DisciplineID     string     `json:"discipline_id,omitempty"`
	Connected        bool       `json:"connected"`
	StatusMsg        string     `json:"status_msg"`
	Shots            []ReadShot `json:"shots"`
	Protocol         string     `json:"protocol"`
	ComPort          string     `json:"com_port"`
	DevMode          bool       `json:"dev_mode"`
}

func (s *Session) Status() StatusSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	shots := make([]ReadShot, len(s.shots))
	copy(shots, s.shots)
	return StatusSnapshot{
		Mode: s.mode, PreisschiessenID: s.preisschiessenID, PhysicalSerial: s.physicalSerial,
		StarterID: s.starterID, DisciplineID: s.disciplineID,
		Connected: s.connected, StatusMsg: s.statusMsg, Shots: shots,
		Protocol: s.cfg.Protocol, ComPort: s.cfg.ComPort, DevMode: s.devMode,
	}
}

// pollDevMode fragt den globalen Entwicklermodus-Schalter (siehe
// server/settings.go GetStandpcDevMode - trotz des Namens ein
// anlagenweiter, nicht standpc-spezifischer Schalter) periodisch beim
// Server ab und schaltet damit den Rohdaten-Debug-Mitschnitt frei/aus.
// Analog zu standpc/session.go pollDevMode.
func (s *Session) pollDevMode() {
	for {
		enabled, err := s.client.GetDevMode()
		if err == nil {
			s.mu.Lock()
			changed := s.devMode != enabled
			s.devMode = enabled
			s.mu.Unlock()
			if changed {
				s.web.broadcastState()
			}
		}
		time.Sleep(5 * time.Second)
	}
}

// newMachine erzeugt die Protokoll-Implementierung passend zu cfg.Protocol -
// im Entwicklermodus (s.devMode) mit Live-Rohdaten-Mitschnitt auf die
// Bedienoberflaeche (siehe web.go broadcastRaw).
//
// WICHTIG: wird ausschliesslich aus startMachine() aufgerufen, das s.mu
// bereits haelt - hier KEIN eigenes s.mu.Lock() (nicht wiedereintrittsfaehiger
// Mutex, sonst Deadlock bei jedem Sitzungsstart - so live reproduziert).
func (s *Session) newMachine() disag.Machine {
	dev := s.devMode
	var sink disag.RawSink
	if dev {
		sink = s.web.broadcastRaw
	}
	if s.wireProtocol() == "rmiv" {
		if sink != nil {
			return disag.NewRMIVWithRawSink(s.cfg.ComPort, s, sink)
		}
		return disag.NewRMIV(s.cfg.ComPort, s)
	}
	if sink != nil {
		return disag.NewRMIIIWithRawSink(s.cfg.ComPort, s, sink)
	}
	return disag.NewRMIII(s.cfg.ComPort, s)
}

// wireProtocol liefert das tatsaechliche Draht-Protokoll: "rmiii-win"
// verwendet nach der Umschaltung (siehe EnsureWinMode) exakt dasselbe
// 38400-Baud-ENQ-Protokoll wie "rmiv" (disag/rmiv.go) - nur der Weg dorthin
// unterscheidet sich (automatische Software-Umschaltung statt eines bereits
// manuell umgeschalteten Geraets). newMachine() und FetchConfig() (fuer den
// vom Server passend zu erzeugenden Konfigurationsstring) brauchen
// ausschliesslich dieses Draht-Protokoll, nicht den konfigurierten Wert an
// sich.
func (s *Session) wireProtocol() string {
	if s.cfg.Protocol == "rmiii-win" {
		return "rmiv"
	}
	return s.cfg.Protocol
}

// EnsureWinMode schaltet eine physische RM III per Software in den
// "RMIII-Win"-Modus (siehe disag/rmiii.go EnterWinMode) - No-Op, wenn
// cfg.Protocol nicht "rmiii-win" ist. Wird sowohl beim Dienststart
// (main.go, einmalig) als auch ueber den Button "RMIII-Win Modus
// umschalten" (web.go handleSwitchWinMode, manuell z.B. nach einem
// Stromausfall/Neustart der Wertmaschine) aufgerufen. Blockiert fuer die
// Dauer der Umschaltung (Handshake + ca. 25s Wartezeit) - waehrenddessen
// laufende Status-Updates (s.OnStatus) werden unabhaengig vom Aufrufer
// sofort per SSE an die Bedienoberflaeche gesendet.
func (s *Session) EnsureWinMode() error {
	if s.cfg.Protocol != "rmiii-win" {
		return nil
	}
	s.OnStatus(disag.StatusEvent{Connected: false, Message: "Schalte RM III in RMIII-Win-Modus (W)..."})
	switcher := disag.NewRMIII(s.cfg.ComPort, s)
	defer switcher.Close()
	if err := switcher.EnterWinMode(); err != nil {
		s.OnStatus(disag.StatusEvent{Connected: false, Message: "Umschalten auf RMIII-Win fehlgeschlagen: " + err.Error()})
		return err
	}
	s.OnStatus(disag.StatusEvent{Connected: false, Message: "Umschaltbefehl gesendet - warte 25s, bis das Geraet im RMIII-Win-Modus (38400 Baud) bereit ist..."})
	time.Sleep(25 * time.Second)
	s.OnStatus(disag.StatusEvent{Connected: false, Message: "RMIII-Win-Modus sollte jetzt aktiv sein (Display zeigt 'FEr')"})
	return nil
}

// EnterFernMode versetzt die Wertmaschine in den passenden Fernsteuermodus -
// je nach konfiguriertem Protokoll entweder "V" (rmiii, normale
// Fernsteuerung, siehe disag.RMIII.EnterRemoteMode) oder "W" + Umschalten
// auf 38400 Baud (rmiii-win, siehe EnsureWinMode). Nutzer-Feedback: ein
// automatischer Trigger beim Dienststart bringt nichts, wenn die Maschine
// schon in FEr feststeckt (das Geraet reagiert dann auf ein erneutes "W"
// nicht) - der Bediener sieht am Display der Wertmaschine besser als die
// Software, ob/wann ein Umschalten noetig ist, daher ausschliesslich
// manuell ueber den "Fern"-Button ausgeloest (kein Aufruf mehr in main.go).
func (s *Session) EnterFernMode() error {
	switch s.cfg.Protocol {
	case "rmiii-win":
		return s.EnsureWinMode()
	case "rmiii":
		switcher := disag.NewRMIII(s.cfg.ComPort, s)
		defer switcher.Close()
		return switcher.EnterRemoteMode()
	default: // "rmiv": setzt voraus, dass die RM bereits manuell umgeschaltet ist
		return fmt.Errorf("fuer Protokoll %q nicht erforderlich", s.cfg.Protocol)
	}
}

// StartPreisschiessen loest die Scheibe ueber ihre physische Seriennummer auf
// und startet die Wertmaschine mit der zugehoerigen Disziplin-Konfiguration.
func (s *Session) StartPreisschiessen(preisschiessenID, physicalSerial string) error {
	params := url.Values{"preisschiessen_id": {preisschiessenID}, "physical_serial_no": {physicalSerial}}
	cfgString, disciplineID, err := s.client.FetchConfig(s.wireProtocol(), params)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.mode = "preisschiessen"
	s.preisschiessenID = preisschiessenID
	s.physicalSerial = physicalSerial
	s.starterID = ""
	s.disciplineID = disciplineID
	s.shots = nil
	s.mu.Unlock()
	s.web.broadcastState()
	return s.startMachine(cfgString)
}

// StartRundenwettkampf loest die Disziplin ueber den Starter auf und startet
// die Wertmaschine.
func (s *Session) StartRundenwettkampf(starterID string) error {
	params := url.Values{"starter_id": {starterID}}
	cfgString, disciplineID, err := s.client.FetchConfig(s.wireProtocol(), params)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.mode = "rundenwettkampf"
	s.starterID = starterID
	s.preisschiessenID = ""
	s.physicalSerial = ""
	s.disciplineID = disciplineID
	s.shots = nil
	s.mu.Unlock()
	s.web.broadcastState()
	return s.startMachine(cfgString)
}

func (s *Session) startMachine(cfgString string) error {
	s.mu.Lock()
	if s.machine != nil {
		s.machine.Close()
	}
	s.machine = s.newMachine()
	machine := s.machine
	s.lastConfigString = cfgString
	s.mu.Unlock()
	return machine.Configure(cfgString)
}

// Recover setzt eine blockierte Wertmaschine zurueck (siehe disag.Machine
// Recover-Kommentar, bei RM III mit echter Hardware bestaetigt: EXIT+V) und
// konfiguriert sie danach sofort erneut mit dem zuletzt verwendeten
// Einstellungsstring, damit die laufende Erfassung ohne Datenverlust
// weitergeht - bereits empfangene Schuesse (s.shots) bleiben erhalten, nur
// die Geraeteverbindung wird neu aufgebaut.
func (s *Session) Recover() error {
	s.mu.Lock()
	machine := s.machine
	cfgString := s.lastConfigString
	s.mu.Unlock()
	if machine == nil {
		return fmt.Errorf("keine aktive Erfassung")
	}
	if err := machine.Recover(); err != nil {
		return err
	}
	return machine.Configure(cfgString)
}

// SendRaw schickt einen beliebigen Rohbefehl an die aktuell verbundene
// Wertmaschine - fuer manuelle Diagnose ohne Dienst-Neustart (z.B. um bei
// RMIV/RMIII-Win schnell "WID" statt des automatischen "ABR" auszuprobieren,
// siehe disag/rmiv.go handleWSCLine, oder bei RM III "E" fuer "Einstellung
// ausgeben"). Nutzt strukturelles Interface-Matching statt eine gemeinsame
// Methode im disag.Machine-Interface zu erzwingen, da RMIII (SendCommand,
// mit RTS/DTR-Handshake) und RMIV (SendRaw, im laufenden Rahmenprotokoll)
// grundverschiedene Sendewege haben.
func (s *Session) SendRaw(cmd string) error {
	s.mu.Lock()
	m := s.machine
	s.mu.Unlock()
	if m == nil {
		return fmt.Errorf("keine aktive Verbindung")
	}
	switch mm := m.(type) {
	case interface{ SendRaw(string) error }:
		return mm.SendRaw(cmd)
	case interface{ SendCommand(string) error }:
		return mm.SendCommand(cmd)
	default:
		return fmt.Errorf("Rohbefehle werden fuer dieses Protokoll nicht unterstuetzt")
	}
}

// OnShot implementiert disag.EventHandler - wird aus der Lese-Goroutine des
// jeweiligen Machine-Treibers aufgerufen.
func (s *Session) OnShot(ev disag.ShotEvent) {
	s.mu.Lock()
	rs := ReadShot{
		ManualShotInput: ManualShotInput{
			Ring: ev.Ring, Decimal: ev.Decimal, Teiler: ev.Teiler, Winkel: ev.Winkel,
			Count: 1,
		},
		RawFlag: ev.Flag,
	}
	s.shots = append(s.shots, rs)
	s.mu.Unlock()
	s.web.broadcastState()
}

func (s *Session) OnStatus(ev disag.StatusEvent) {
	s.mu.Lock()
	s.connected = ev.Connected
	s.statusMsg = ev.Message
	s.mu.Unlock()
	s.web.broadcastState()
}

// CorrectShot ueberschreibt Ring/Decimal/Teiler eines vom Geraet als
// unsicher/ungueltig gemeldeten Schusses durch den vom Bediener eingegebenen
// Wert - lokale Korrektur statt des interaktiven geraeteseitigen Editier-
// Dialogs (WSC=-N/EDI/S=.../WID/ABR), wie im bewaehrten VB6-Referenzcode.
// Stoesst danach TryResolvePendingEdit an (nur bei RM IV relevant - siehe
// disag/rmiv.go): falls die RM gerade auf eine EDI-Antwort wartet und jetzt
// alle betroffenen Schuesse korrigiert sind, wird sie automatisch gesendet.
func (s *Session) CorrectShot(index int, ring int, decimal float64) error {
	s.mu.Lock()
	if index < 0 || index >= len(s.shots) {
		s.mu.Unlock()
		return fmt.Errorf("ungueltiger Index")
	}
	s.shots[index].Ring = &ring
	s.shots[index].Decimal = &decimal
	s.shots[index].Corrected = true
	m := s.machine
	s.mu.Unlock()
	if mm, ok := m.(interface{ TryResolvePendingEdit() }); ok {
		mm.TryResolvePendingEdit()
	}
	return nil
}

// AllResolved: true, wenn kein Schuss mehr eine ungeklaerte Bediener-Korrektur
// braucht (Abschliessen-Button wird davon abhaengig freigeschaltet).
func (s *Session) AllResolved() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sh := range s.shots {
		if (sh.RawFlag == "review" || sh.RawFlag == "invalid") && !sh.Corrected {
			return false
		}
	}
	return true
}

// AllShotsResolved/PendingShotsForEdit implementieren disag.EventHandler -
// nur vom RM-IV-Treiber fuer die EDI-Antwort auf eine Editier-Anfrage
// genutzt (siehe disag/rmiv.go attemptPendingEdit). AllShotsResolved ist
// bewusst ein duenner Wrapper um das bereits bestehende AllResolved()
// (oeffentliche API fuer die Bedienoberflaeche), damit der Treiber denselben
// Zustand ohne eigene Kopie der Aufloesungs-Logik abfragen kann.
func (s *Session) AllShotsResolved() bool {
	return s.AllResolved()
}

func (s *Session) PendingShotsForEdit() []disag.EditShot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]disag.EditShot, len(s.shots))
	for i, sh := range s.shots {
		out[i] = disag.EditShot{Ring: sh.Ring, Decimal: sh.Decimal, Teiler: sh.Teiler, Changed: sh.Corrected}
	}
	return out
}

// Submit ueberträgt die gesammelten Einzelschuesse (Granularitaet "shot") an
// den Server und setzt die Session danach zurueck.
func (s *Session) Submit(confirmOverwrite bool) (string, error) {
	s.mu.Lock()
	mode, psID, serial, starterID := s.mode, s.preisschiessenID, s.physicalSerial, s.starterID
	shots := make([]ManualShotInput, len(s.shots))
	for i, sh := range s.shots {
		shots[i] = sh.ManualShotInput
	}
	s.mu.Unlock()

	if len(shots) == 0 {
		return "", fmt.Errorf("keine Schuesse erfasst")
	}
	var sessionID string
	var err error
	switch mode {
	case "preisschiessen":
		sessionID, err = s.client.SubmitPreisschiessenScheibe(psID, serial, "shot", shots, confirmOverwrite)
	case "rundenwettkampf":
		sessionID, err = s.client.SubmitRundenwettkampf(starterID, "shot", shots, confirmOverwrite)
	default:
		return "", fmt.Errorf("keine aktive Erfassung")
	}
	if err != nil {
		return "", err
	}
	s.reset()
	return sessionID, nil
}

func (s *Session) Abort() {
	s.mu.Lock()
	m := s.machine
	s.mu.Unlock()
	if m != nil {
		m.Abort()
	}
	s.reset()
}

// reset raeumt den Sitzungszustand auf. WICHTIG: broadcastState() ruft
// s.Status() auf, das seinerseits s.mu sperrt - der Lock hier darf also
// bereits freigegeben sein, BEVOR broadcastState() aufgerufen wird (kein
// defer Unlock ueber die gesamte Funktion, sonst Deadlock).
func (s *Session) reset() {
	s.mu.Lock()
	if s.machine != nil {
		s.machine.Close()
		s.machine = nil
	}
	s.mode = ""
	s.preisschiessenID = ""
	s.physicalSerial = ""
	s.starterID = ""
	s.disciplineID = ""
	s.shots = nil
	s.connected = false
	s.statusMsg = ""
	s.mu.Unlock()
	s.web.broadcastState()
}
