// ============================================================================
// rmiii.go – Disag RM III, Modus "2. Komplette Fernsteuerung" (offizielle
// Bedienungsanleitung RMIIIBA_1/2.pdf, Kapitel "Computeranschluss"):
//
// 2400 Baud, 8N1, keine Paritaet, 1 Stopbit. Handshake (siehe Anleitung
// woertlich): "Die RM3 wartet mit dem Senden von Daten, bis 'CTS' oder 'DSR'
// gesetzt wurde. Wenn die RM3 bereit ist, Daten zu empfangen, wird 'RTS' und
// 'DTR' von der RM3 gesetzt." - d.h. wir (PC) setzen RTS/DTR, damit die RM3
// (an ihren CTS/DSR-Eingaengen, ueber das Nullmodem-Kabel gekreuzt) sendebereit
// wird; die RM3 setzt ihrerseits RTS/DTR, was wir an unseren CTS/DSR-Eingaengen
// sehen und als "RM3 empfangsbereit" werten.
//
// Ablauf: "V"(CR) via Handshake -> Anzeige wechselt zu "FERN" -> 9-stelliger
// Einstellungsstring(CR) via Handshake -> RM3 wartet auf Scheiben.
//
// Ergebniszeile pro Schuss (KEIN "START;"-Praefix, anders als zunaechst
// angenommen - das stammte aus dem unverwandten RM1-Kompatibilitaetsmodus):
//
//	<Schußnummer>;<Ringwert>;<Teilerwert>;<X-Abw. 1/100 Ring>;<Y-Abw. 1/100 Ring>;<M|N>(CR)
//	Beispiel: 2;8.0;749.1;2.82;-2.75;N(CR)
//
// M = manuell zu kontrollieren (Flag "review"), N = keine Markierung.
// Ringwert "?.?" = nicht auswertbar (Flag "invalid"). Teilerwert "-" = keine
// Teilermessung aktiv. X/Y sind in 1/100 Ring angegeben (NICHT mm) - da wir
// daraus nur den Winkel brauchen (atan2 ist einheitenunabhaengig) und den
// Teilerwert direkt aus dem eigenen Feld der Zeile haben (in der etablierten
// 1/100mm-Konvention, wie bei RM IV - siehe server/manual_result.go
// centerDistanceHundredthMM, mit echter Hardware/Nutzer-Rueckmeldung
// bestaetigt: der Rohwert entspricht direkt dem aufgedruckten Teiler, KEINE
// zusaetzliche Umrechnung noetig), wird ausschliesslich Teiler+Winkel an den
// Server gemeldet - keine eigene dX/dY-Einheit noetig.
//
// Sonderfall Fuenfschuss-Karte (siehe Anleitung): erkennt die RM3 bei einer
// Fuenfschusswertung einen unklaren/markierten Schuss, wirft sie die Karte
// vorne aus und WARTET auf einen 4-Zeilen-Austausch (Gesamtschusszahl;
// Summenwert;Scheibenaufdruck;"Edit"), bevor sie weiterarbeitet - die Karte
// muss dafuer von Hand erneut eingelegt werden. Dieser Austausch wird hier
// bewusst NICHT automatisiert (siehe Konzept: lokale Korrektur durch den
// Bediener statt geraeteseitigem Editier-Dialog) - der Bediener muss die
// Karte nach einer solchen Meldung von Hand erneut einlegen und die
// betroffene Zeile danach in der Bedienoberflaeche pruefen/korrigieren.
//
// Kein ENQ/STX/Pruefsummen-Rahmen (anders als RM IV) - daher hier auch kein
// Aequivalent zu ABR. "EXIT"(CR) schaltet laut Anleitung auf Handbetrieb
// zurueck; Abort() sendet dies best-effort mit, verlaesst sich aber primaer
// auf das lokale Beenden des Lesevorgangs (im VB6-Referenzcode ebenfalls
// nicht anders vorgesehen).
// ============================================================================
package disag

import (
	"bufio"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

const rmiiiBaud = 2400
const rmiiiHandshakeTimeout = 10 * time.Second
const rmiiiReadTimeout = 500 * time.Millisecond
const ensurePortTimeout = 5 * time.Second
const sendViaHandshakeMaxWait = rmiiiHandshakeTimeout + 5*time.Second

type RMIII struct {
	portName string
	handler  EventHandler
	rawSink  RawSink // siehe serial.go - nil = kein Live-Rohdaten-Mitschnitt

	mu      sync.Mutex
	port    serial.Port
	abortCh chan struct{}
}

func NewRMIII(portName string, handler EventHandler) *RMIII {
	return &RMIII{portName: portName, handler: handler}
}

// NewRMIIIWithRawSink wie NewRMIII, zusaetzlich mit Live-Rohdaten-Mitschnitt
// (siehe serial.go RawSink) - fuer den Entwickler-Debug-Modus in der
// Bedienoberflaeche (wertmaschine/session.go).
func NewRMIIIWithRawSink(portName string, handler EventHandler, rawSink RawSink) *RMIII {
	return &RMIII{portName: portName, handler: handler, rawSink: rawSink}
}

// ensurePort oeffnet den Port (falls noch nicht offen) - DTR wird erst in
// sendViaHandshake gesetzt (gemeinsam mit RTS, siehe dortiger Kommentar).
//
// WICHTIG (mit echter Hardware bestaetigt): serial.Open() auf einen
// Geraeteknoten, dessen USB-Adapter zwischenzeitlich getrennt wurde, kann am
// zugrundeliegenden Treiber/Kernel unbegrenzt haengen bleiben, OHNE dass der
// Aufruf selbst jemals mit einem Fehler zurueckkehrt - ohne eigene
// Zeitbegrenzung blockiert das den kompletten wertmaschine-Dienst (auch
// /status antwortet dann nicht mehr). Deshalb hier mit einem harten Timeout
// per Goroutine+Channel abgesichert: die haengende openPort()-Goroutine
// bleibt im Zweifel zurueck (verkraftbar, seltener Fall), der Aufrufer gibt
// aber spaetestens nach ensurePortTimeout einen Fehler zurueck.
func (m *RMIII) ensurePort() (serial.Port, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.port != nil {
		return m.port, nil
	}
	type result struct {
		port serial.Port
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		port, err := openPort(m.portName, rmiiiBaud, rmiiiReadTimeout, m.rawSink)
		ch <- result{port, err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		m.port = res.port
		return res.port, nil
	case <-time.After(ensurePortTimeout):
		return nil, fmt.Errorf("Port %s laesst sich nicht oeffnen (Timeout nach %s - USB-Adapter angeschlossen?)", m.portName, ensurePortTimeout)
	}
}

// sendViaHandshake setzt RTS+DTR (damit die RM3 an ihren CTS/DSR-Eingaengen
// sendebereit wird), wartet bis die RM3 ihrerseits RTS/DTR setzt (bei uns an
// CTS ODER DSR sichtbar - je nach Kabel/Adapter ist ggf. nur eine der beiden
// Leitungen tatsaechlich durchverbunden, siehe Anleitung: "nicht alle
// Nullmodemkabel arbeiten mit den BIOS-Routinen"), und sendet dann
// payload+CR. Entspricht der VB6-Funktion ZuRM(in_str), die fuer sowohl den
// "V"-Fernsteuermodus-Befehl als auch den Konfigurationsstring identisch
// verwendet wird.
//
// WICHTIG (mit echter Hardware bestaetigt): wird der USB-Seriell-Adapter
// zwischenzeitlich getrennt, koennen SetRTS/SetDTR/GetModemStatusBits/Write
// am zugrundeliegenden Treiber unbegrenzt haengen bleiben - der interne
// rmiiiHandshakeTimeout (Deadline-Check ZWISCHEN den Aufrufen) greift dann
// NICHT, da schon ein einzelner Aufruf nie zurueckkehrt. Deshalb laeuft die
// komplette Funktion in einer eigenen Goroutine mit einem harten aeusseren
// Timeout (wie ensurePort) - blockiert sie, bekommt der Aufrufer trotzdem
// spaetestens nach sendViaHandshakeMaxWait einen Fehler zurueck, damit z.B.
// /status im wertmaschine-Dienst nicht mit blockiert (das war vorher der
// Fall, da ein haengender Request implizit auch andere Anfragen zum
// Erliegen brachte).
func (m *RMIII) sendViaHandshake(payload string) error {
	port, err := m.ensurePort()
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		done <- m.doHandshake(port, payload)
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(sendViaHandshakeMaxWait):
		return fmt.Errorf("Wertmaschine antwortet nicht (Timeout nach %s - Port/Kabel/USB-Adapter pruefen)", sendViaHandshakeMaxWait)
	}
}

func (m *RMIII) doHandshake(port serial.Port, payload string) error {
	m.handler.OnStatus(StatusEvent{Connected: false, Message: "warte auf Wertmaschine (RTS/DTR-Handshake)..."})
	if err := port.SetRTS(true); err != nil {
		return fmt.Errorf("RTS setzen: %w", err)
	}
	if err := port.SetDTR(true); err != nil {
		return fmt.Errorf("DTR setzen: %w", err)
	}
	deadline := time.Now().Add(rmiiiHandshakeTimeout)
	for {
		bits, err := port.GetModemStatusBits()
		if err != nil {
			return fmt.Errorf("Modem-Status lesen: %w", err)
		}
		if bits.CTS || bits.DSR {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("Wertmaschine antwortet nicht (kein CTS/DSR nach %s - "+
				"pruefen Sie, ob das Kabel/USB-Seriell-Adapter RTS/CTS/DTR/DSR tatsaechlich "+
				"durchverbindet und nicht nur TX/RX/GND)", rmiiiHandshakeTimeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := port.Write([]byte(payload + "\r")); err != nil {
		return fmt.Errorf("Senden (%q): %w", payload, err)
	}
	return nil
}

// EnterRemoteMode versetzt die RM III in den Fernsteuermodus ("V"(CR), siehe
// RMIIIBA_2.pdf "RM3 auf Fernbedienung umschalten") - nur einmal nach dem
// Einschalten noetig (Display muss zu diesem Zeitpunkt bereits "NEU" zeigen,
// d.h. der ca. 1-minuetige Selbsttest nach dem Einschalten muss abgeschlossen
// sein). Die RM3 antwortet mit einem fuer den Anwender bedeutungslosen String
// (laut Anleitung) - dieser wird hier nicht ausgewertet, nur "FERN" im
// Display zeigt den Erfolg an (nicht per Software pruefbar).
func (m *RMIII) EnterRemoteMode() error {
	if err := m.sendViaHandshake("V"); err != nil {
		return err
	}
	m.handler.OnStatus(StatusEvent{Connected: true, Message: "Fernsteuermodus-Befehl gesendet - Display sollte auf 'FERN' wechseln"})
	return nil
}

// EnterWinMode versetzt die RM III in den "RMIII-Win"-Modus (Anzeige zeigt
// "FEr" statt "FErn" wie im normalen Fernsteuermodus) - Software-Alternative
// zur Tastenkombination "SERIE"+"TEILER"+"SCHUSS"+"NEUSTART" am Geraet,
// siehe VB-Abend/schnittstellenbeschreibung.pdf. Wie EnterRemoteMode nur
// einmal je Einschaltvorgang noetig (Display muss "NEU" zeigen). Nach dem
// Senden braucht das Geraet laut Dokumentation ca. 20-30s, bis es im neuen
// Modus (38400 Baud, ENQ/STX-Rahmenprotokoll, siehe disag/rmiv.go) reagiert
// - das Warten uebernimmt der Aufrufer (wertmaschine/session.go
// EnsureWinMode), nicht diese Funktion; hier wird nur der Umschaltbefehl
// gesendet.
//
// NOCH NICHT AN ECHTER HARDWARE VERIFIZIERT (anders als EXIT/V bei
// Recover()/EnterRemoteMode()): die Dokumentation nennt "W" nur als
// Alternative zur Tastenkombination, ohne den RTS/DTR-Handshake explizit zu
// erwaehnen (anders als beim "V"-Befehl in der separaten RMIIIBA_2.pdf).
// Hier wird testweise derselbe Handshake-Weg wie bei "V" verwendet
// (sendViaHandshake) - naheliegend, da beide Befehle vermutlich denselben
// Firmware-Empfangsmechanismus im Boot-/Handbetrieb-Zustand nutzen. Sollte
// das am echten Geraet nicht funktionieren (Handshake-Timeout trotz
// frisch eingeschalteter RM3), naechster Versuch: SendCommand("W") ganz
// ohne RTS/DTR-Handshake (z.B. ueber cmd_test_manual -send W).
func (m *RMIII) EnterWinMode() error {
	if err := m.sendViaHandshake("W"); err != nil {
		return err
	}
	m.handler.OnStatus(StatusEvent{Connected: true, Message: "RMIII-Win-Umschaltbefehl gesendet - Display sollte auf 'FEr' wechseln"})
	return nil
}

// SendCommand schickt einen beliebigen Befehl (RTS/DTR-Handshake + payload +
// CR) - fuer manuelle Diagnose ueber cmd_test_manual (z.B. "-send E" fuer
// "Einstellung ausgeben", siehe RMIIIBA_1.pdf). KEIN Aequivalent zu einem der
// dedizierten Sub/Function-Wrapper (EnterRemoteMode/Configure/Recover) - der
// Aufrufer traegt selbst die Verantwortung fuer sinnvolle Befehle.
func (m *RMIII) SendCommand(cmd string) error {
	return m.sendViaHandshake(cmd)
}

// Configure kann mehrfach auf demselben Objekt aufgerufen werden (z.B. um
// nach einer beendeten Serie/ENDE eine neue Serie zu starten, ohne die
// Verbindung komplett neu aufzubauen - laut Anleitung ausdruecklich erlaubt:
// "Wird beim Warten auf eine neue Scheibe ein neuer Einstellungsstring
// gesendet, beginnt die RM3 mit dieser Einstellung bei einer neuen Serie.").
// Eine noch laufende readLoop-Goroutine aus einem vorherigen Configure()-
// Aufruf wird dafuer zuerst beendet - sonst wuerden zwei Goroutinen
// gleichzeitig vom selben Port lesen (Race Condition, moegliche Ursache
// fuer beobachtete Zeichensalat-Zeilen).
func (m *RMIII) Configure(configString string) error {
	port, err := m.ensurePort()
	if err != nil {
		return err
	}

	m.mu.Lock()
	if m.abortCh != nil {
		close(m.abortCh)
		m.abortCh = nil
	}
	m.mu.Unlock()

	if err := m.sendViaHandshake(configString); err != nil {
		return err
	}
	m.handler.OnStatus(StatusEvent{Connected: true, Message: "verbunden, warte auf Scheiben"})

	m.mu.Lock()
	m.abortCh = make(chan struct{})
	abortCh := m.abortCh
	m.mu.Unlock()
	go m.readLoop(port, abortCh)
	return nil
}

func (m *RMIII) readLoop(port serial.Port, abortCh <-chan struct{}) {
	reader := bufio.NewReader(port)
	var lineBuf strings.Builder
	buf := make([]byte, 256)
	for {
		select {
		case <-abortCh:
			return
		default:
		}
		n, err := reader.Read(buf)
		if err != nil {
			// Read-Timeout (kein Fehler, siehe openPort SetReadTimeout) oder
			// Port geschlossen - bei geschlossenem Port einfach beenden.
			if n == 0 {
				continue
			}
		}
		for i := 0; i < n; i++ {
			b := buf[i]
			if b == '\r' || b == '\n' {
				if lineBuf.Len() > 0 {
					m.handleLine(lineBuf.String())
					lineBuf.Reset()
				}
				continue
			}
			lineBuf.WriteByte(b)
		}
	}
}

func (m *RMIII) handleLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	switch line {
	case "SCHEIBE":
		m.handler.OnStatus(StatusEvent{Connected: true, Message: "Scheibe/Karte zu Ende - naechste einlegen"})
		return
	case "ENDE":
		m.handler.OnStatus(StatusEvent{Connected: true, Message: "Messreihe beendet"})
		return
	}

	fields := strings.Split(line, ";")
	if len(fields) < 6 {
		m.handler.OnStatus(StatusEvent{Connected: true, Message: "unbekannte Zeile: " + line})
		return
	}

	shotNo, _ := strconv.Atoi(strings.TrimSpace(fields[0]))
	flag := "ok"
	switch strings.TrimSpace(fields[5]) {
	case "M":
		flag = "review"
	case "N":
		flag = "ok"
	}

	ev := ShotEvent{ShotNo: shotNo, Flag: flag}
	ringStr := strings.TrimSpace(fields[1])
	if ringStr == "?.?" || ringStr == "" {
		ev.Flag = "invalid"
	} else if v, err := strconv.ParseFloat(strings.Replace(ringStr, ",", ".", 1), 64); err == nil {
		r := int(v)
		ev.Ring = &r
		ev.Decimal = &v
	}

	teilerStr := strings.TrimSpace(fields[2])
	var teiler float64
	haveTeiler := false
	if teilerStr != "-" && teilerStr != "" {
		if v, err := strconv.ParseFloat(strings.Replace(teilerStr, ",", ".", 1), 64); err == nil {
			teiler = v
			haveTeiler = true
			ev.Teiler = &v
		}
	}

	if haveTeiler {
		dx, errX := strconv.ParseFloat(strings.Replace(strings.TrimSpace(fields[3]), ",", ".", 1), 64)
		dy, errY := strconv.ParseFloat(strings.Replace(strings.TrimSpace(fields[4]), ",", ".", 1), 64)
		if errX == nil && errY == nil && (dx != 0 || dy != 0) {
			winkel := math.Atan2(dx, dy) * 180 / math.Pi
			if winkel < 0 {
				winkel += 360
			}
			ev.Winkel = &winkel
		} else {
			// dX=dY=0 (Treffer im Zentrum): Winkel unbestimmt, Teiler=0 macht
			// die Positionsberechnung beim Server ohnehin trivial (0,0).
			zero := 0.0
			ev.Winkel = &zero
		}
		_ = teiler
	}

	m.handler.OnShot(ev)
}

// Recover setzt die RM3 per "EXIT"(CR) auf Handbetrieb zurueck und schaltet
// sie danach wieder in den Fernsteuermodus ("V") - ein softwareseitiger Reset
// ohne Aus-/Einschalten des Geraets. Mit echter Hardware bestaetigt als
// zuverlaessiger Weg aus einer blockierten Kartenkontrolle heraus (siehe
// SubmitCardControl-Kommentar: der dort dokumentierte 4-Zeilen-Austausch hat
// sich in der Praxis als nicht zuverlaessig erwiesen). Der Aufrufer muss nach
// Recover() erneut Configure() mit dem zuletzt verwendeten Einstellungsstring
// aufrufen, um weiterzulesen.
//
// WICHTIG (Nutzerhinweis, Betrieb am echten Geraet): beim Umschalten in den
// Fernsteuermodus (V) liest die RM3 ihr Programm neu von einer eingelegten
// Diskette - haeufiges Umschalten nutzt das Laufwerk/die Diskette mechanisch
// ab. Recover() ist daher als seltener, gezielter Rueckfall gedacht (z.B.
// ueber einen Bedienoberflaechen-Button mit Bestaetigung), NICHT fuer
// routinemaessigen/automatischen Einsatz.
func (m *RMIII) Recover() error {
	m.mu.Lock()
	port := m.port
	if m.abortCh != nil {
		close(m.abortCh)
		m.abortCh = nil
	}
	m.mu.Unlock()
	if port == nil {
		return fmt.Errorf("kein offener Port")
	}
	if _, err := port.Write([]byte("EXIT\r")); err != nil {
		return fmt.Errorf("EXIT senden: %w", err)
	}
	m.handler.OnStatus(StatusEvent{Connected: false, Message: "Reset gesendet (EXIT) - Display sollte auf 'NEU' wechseln"})
	// Mit echter Hardware bestaetigt: 500ms zwischen EXIT und V reichen nicht
	// (das Geraet ignoriert das zu frueh gesendete "V" dann offenbar) - ein
	// paar Sekunden Pause ist nach dem Wechsel zu "NEU" noetig.
	time.Sleep(3 * time.Second)
	return m.EnterRemoteMode()
}

// SubmitCardControl beantwortet den 4-Zeilen-Kontrollaustausch, den die RM3
// bei einer unklaren Fuenfschuss-Karte auswirft (siehe RMIIIBA_2.pdf) - laut
// Anleitung UND dem Referenz-VB6-Code (Einlesen.frm Fehler_beheben/ZuRM)
// nimmt das Geraet danach die naechste Karte wieder an. Byte-genau mit
// ZuRM(rm_str) abgeglichen (RTS/DTR-Handshake, ein String mit eingebetteten
// CR + abschliessendem CR). MIT ECHTER HARDWARE GETESTET: dieser Austausch
// hat die Blockade in der Praxis NICHT zuverlaessig aufgehoben (auch mit den
// laut VB6-Code korrekten Werten, 10er-Dekadengrenze statt Kartengroesse) -
// die Maschine verhaelt sich hier nachweislich nicht wie dokumentiert. Bleibt
// dokumentiert/verfuegbar (evtl. hilfreich bei anderen Geraete-Firmwareständen),
// ist aber NICHT der empfohlene Weg aus einer Blockade - dafuer Recover()
// verwenden (EXIT+V, mit Hardware bestaetigt zuverlaessig).
func (m *RMIII) SubmitCardControl(totalShots int, sumValue float64, aufdruck string) error {
	m.mu.Lock()
	port := m.port
	m.mu.Unlock()
	if port == nil {
		return fmt.Errorf("kein offener Port")
	}
	lines := []string{
		strconv.Itoa(totalShots),
		strconv.FormatFloat(sumValue, 'f', -1, 64),
		aufdruck,
		"Edit",
	}
	for _, l := range lines {
		if _, err := port.Write([]byte(l + "\r")); err != nil {
			return fmt.Errorf("Kontrollantwort (%q): %w", l, err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	return nil
}

// Abort beendet nur den lokalen Lesevorgang - sendet KEIN "EXIT" an das
// Geraet (frueher der Fall, siehe Konzept-Historie: das hatte den
// unerwuenschten Nebeneffekt, dass jedes Schliessen der Verbindung - auch
// beim normalen Beenden/Neustarten des wertmaschine-Dienstes - die RM3
// stillschweigend auf Handbetrieb zurueckgesetzt hat, mit echter Hardware
// als Fehlverhalten bestaetigt). Ein gezielter Geraete-Reset erfolgt nur noch
// explizit ueber Recover().
func (m *RMIII) Abort() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.abortCh != nil {
		close(m.abortCh)
		m.abortCh = nil
	}
}

func (m *RMIII) Close() error {
	m.Abort()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.port != nil {
		err := m.port.Close()
		m.port = nil
		return err
	}
	return nil
}
