// ============================================================================
// rmiv.go – Disag "RM III Windows"-Protokoll (im VB6-Referenzcode/ini als
// "RMIV" bezeichnet - siehe Einlesen.frm Kommentar "Maschine muss auf RMIII
// Windows S eingestellt sein": es handelt sich um denselben physischen RM
// III, in einem schnelleren, modernen Protokollmodus, nicht um eine andere
// Geraetegeneration).
//
// 38400 Baud, 8N1, kein Flow-Control. Steuerbytes: ENQ=0x05, STX=0x02,
// ACK=0x06, NAK=0x15, CR=0x0D. Rahmen: String + XOR-Pruefsumme + CR
// (Pruefsumme = XOR aller Zeichen im String, Ergebnis <32 -> +32).
//
// Handshake (PC-initiiert): PC sendet ENQ, wartet <=0.1s auf STX (Retry bis
// 30s gesamt), sendet dann Konfigstring+Pruefsumme+CR, wartet <=0.3s auf
// ACK/NAK (Retry <=3x). Danach streamt das Geraet Ergebniszeilen
// (SCH=[Schußnr];[Ring];[Teiler];[Winkel];[Flag]), jede mit Pruefsumme+CR
// abgeschlossen - der PC bestaetigt jede empfangene, pruefsummenkorrekte
// Zeile mit ACK (bei falscher Pruefsumme: NAK, echte Verbesserung gegenueber
// dem VB6-Referenzcode, der Pruefsummen dort ungeprueft akzeptiert).
// ============================================================================
package disag

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

const (
	rmivBaud        = 38400
	rmivENQ    byte = 0x05
	rmivACK    byte = 0x06
	rmivSTX    byte = 0x02
	rmivNAK    byte = 0x15
	rmivCR     byte = 0x0D
	rmivReadTO      = 100 * time.Millisecond
)

type RMIV struct {
	portName string
	handler  EventHandler
	rawSink  RawSink // siehe serial.go - nil = kein Live-Rohdaten-Mitschnitt

	mu      sync.Mutex
	port    serial.Port
	abortCh chan struct{}

	// pendingEditLastCard > 0, waehrend eine Editier-Anfrage der RM
	// (WSC=-N) noch unbeantwortet ist - siehe handleWSCLine/
	// attemptPendingEdit. editTrigger weckt readLoop auf, um eine faellige
	// EDI-Antwort zu senden, sobald der Bediener seine Korrektur(en)
	// abgeschlossen hat (siehe TryResolvePendingEdit) - als Kanal statt
	// direktem Aufruf, damit ausschliesslich readLoop selbst den Port
	// liest/schreibt (kein nebenlaeufiger zweiter Leser/Schreiber, siehe
	// dortiger Kommentar).
	pendingEditLastCard int
	editTrigger         chan struct{}
}

func NewRMIV(portName string, handler EventHandler) *RMIV {
	return &RMIV{portName: portName, handler: handler, editTrigger: make(chan struct{}, 1)}
}

// NewRMIVWithRawSink wie NewRMIV, zusaetzlich mit Live-Rohdaten-Mitschnitt
// (siehe serial.go RawSink) - fuer den Entwickler-Debug-Modus.
func NewRMIVWithRawSink(portName string, handler EventHandler, rawSink RawSink) *RMIV {
	return &RMIV{portName: portName, handler: handler, rawSink: rawSink, editTrigger: make(chan struct{}, 1)}
}

func rmivChecksum(s []byte) byte {
	var c byte
	for _, b := range s {
		c ^= b
	}
	if c < 32 {
		c += 32
	}
	return c
}

// ensurePort - siehe rmiii.go ensurePort-Kommentar: mit Timeout abgesichert,
// da serial.Open() auf einen Geraeteknoten mit getrenntem USB-Adapter
// unbegrenzt haengen kann.
func (m *RMIV) ensurePort() (serial.Port, error) {
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
		port, err := openPort(m.portName, rmivBaud, rmivReadTO, m.rawSink)
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

// readByte liest ein einzelnes Byte mit dem Port-Lese-Timeout (100ms) -
// gibt (0, false) bei Timeout zurueck (kein Fehler).
func readByte(port serial.Port) (byte, bool, error) {
	buf := make([]byte, 1)
	n, err := port.Read(buf)
	if err != nil {
		return 0, false, err
	}
	if n == 0 {
		return 0, false, nil
	}
	return buf[0], true, nil
}

// waitForByte wartet bis zu timeout auf ein bestimmtes Steuerbyte.
func waitForByte(port serial.Port, want byte, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		b, ok, err := readByte(port)
		if err != nil {
			return false, err
		}
		if ok && b == want {
			return true, nil
		}
	}
	return false, nil
}

// enqStxHandshake fuehrt das "PC moechte senden"-Praeludium aus (ENQ senden,
// bis zu 30s mit 100ms-Retry auf STX warten) - laut allgemeinem
// Rahmenprotokoll (siehe Dateikopf) noetig, bevor der PC IRGENDEINEN String
// an die RM sendet, nicht nur beim initialen Konfigurationsstring. Wird auch
// von sendEdit() verwendet - eine Editier-Antwort (EDI=...), die ohne dieses
// Praeludium gesendet wurde, blieb mit echter Hardware unbestaetigt (kein
// ACK/NAK nach 300ms trotz korrekter Pruefsumme).
func (m *RMIV) enqStxHandshake(port serial.Port) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := port.Write([]byte{rmivENQ}); err != nil {
			return fmt.Errorf("ENQ senden: %w", err)
		}
		ok, err := waitForByte(port, rmivSTX, 100*time.Millisecond)
		if err != nil {
			return fmt.Errorf("auf STX warten: %w", err)
		}
		if ok {
			return nil
		}
	}
	return fmt.Errorf("Wertmaschine antwortet nicht (kein STX auf ENQ nach 30s)")
}

// Configure fuehrt den ENQ/STX-Handshake aus, sendet den Konfigurationsstring
// und startet danach den Lesevorgang fuer Ergebniszeilen in einer eigenen
// Goroutine.
func (m *RMIV) Configure(configString string) error {
	port, err := m.ensurePort()
	if err != nil {
		return err
	}

	m.handler.OnStatus(StatusEvent{Connected: false, Message: "sende ENQ, warte auf STX..."})
	if err := m.enqStxHandshake(port); err != nil {
		return err
	}

	frame := append([]byte(configString), rmivChecksum([]byte(configString)), rmivCR)
	acked := false
	for attempt := 0; attempt < 3 && !acked; attempt++ {
		m.handler.OnStatus(StatusEvent{Connected: false, Message: "sende Konfiguration..."})
		if _, err := port.Write(frame); err != nil {
			return fmt.Errorf("Konfiguration senden: %w", err)
		}
		deadline := time.Now().Add(300 * time.Millisecond)
		for time.Now().Before(deadline) {
			b, ok, err := readByte(port)
			if err != nil {
				return fmt.Errorf("auf ACK/NAK warten: %w", err)
			}
			if ok && b == rmivACK {
				acked = true
				break
			}
			if ok && b == rmivNAK {
				break // retry
			}
		}
	}
	if !acked {
		return fmt.Errorf("Wertmaschine hat die Konfiguration nicht bestaetigt (kein ACK)")
	}

	m.handler.OnStatus(StatusEvent{Connected: true, Message: "verbunden, warte auf Schuesse"})
	m.mu.Lock()
	m.abortCh = make(chan struct{})
	abortCh := m.abortCh
	m.mu.Unlock()
	go m.readLoop(port, abortCh)
	return nil
}

// readLoop liest Ergebniszeilen (String+Pruefsumme+CR), verifiziert die
// Pruefsumme (ACK bei Erfolg, NAK bei Mismatch - Verbesserung gegenueber dem
// VB6-Referenzcode) und wertet erkannte Befehls-Praefixe aus. Befehls-Tokens
// werden als Praefix gematcht (nicht exakt), da in der Praxis abweichende
// Varianten beobachtet wurden (siehe Konzept Abschnitt 1: "WSEA" statt
// dokumentiert "WSE", undokumentiertes "STAF").
func (m *RMIV) readLoop(port serial.Port, abortCh <-chan struct{}) {
	var lineBuf []byte
	for {
		select {
		case <-abortCh:
			return
		case <-m.editTrigger:
			m.attemptPendingEdit(port)
			continue
		default:
		}
		b, ok, err := readByte(port)
		if err != nil || !ok {
			continue
		}
		if b != rmivCR {
			lineBuf = append(lineBuf, b)
			continue
		}
		if len(lineBuf) < 2 {
			lineBuf = lineBuf[:0]
			continue
		}
		payload := lineBuf[:len(lineBuf)-1]
		checksum := lineBuf[len(lineBuf)-1]
		lineBuf = lineBuf[:0]

		if rmivChecksum(payload) != checksum {
			port.Write([]byte{rmivNAK})
			m.handler.OnStatus(StatusEvent{Connected: true, Message: "Pruefsummenfehler empfangen, NAK gesendet"})
			continue
		}
		port.Write([]byte{rmivACK})
		m.handleLine(port, string(payload))
	}
}

func (m *RMIV) handleLine(port serial.Port, line string) {
	switch {
	case strings.HasPrefix(line, "SCH="):
		m.handleShotLine(strings.TrimPrefix(line, "SCH="))
	case strings.HasPrefix(line, "WSC="):
		m.handleWSCLine(port, strings.TrimPrefix(line, "WSC="))
	case strings.HasPrefix(line, "WSE"): // WSE/WSEA
		m.handler.OnStatus(StatusEvent{Connected: true, Message: "Messreihe beendet"})
	case strings.HasPrefix(line, "STA"): // STA/STAF
		m.handler.OnStatus(StatusEvent{Connected: true, Message: "Neue Scheibe gestartet"})
	default:
		m.handler.OnStatus(StatusEvent{Connected: true, Message: "unbekannte Meldung: " + line})
	}
}

// handleWSCLine reagiert auf "WSC=<n>" (Anzahl erwarteter Schuesse fuer die
// naechste Scheibe). Ein NEGATIVES Vorzeichen bedeutet laut Dokumentation
// (VB-Abend/schnittstellenbeschreibung.pdf Seite 9), dass die RM einen
// unklaren/markierten Schuss der zuletzt gemessenen Karte editiert haben
// moechte (EDI=.../WID/ABR-Austausch), bevor sie die naechste Karte annimmt -
// ohne Antwort bleibt das Geraet in diesem Zustand haengen (mit echter
// Hardware bestaetigt: WSC=-10 blockierte die naechste Karte vollstaendig;
// ein automatisches "ABR" als Antwort hat sich dabei als wirkungslos
// erwiesen).
//
// Analog zur bewussten Entscheidung bei RM III (siehe rmiii.go, "5-Schuss-
// Karte"), den geraeteseitigen Editier-DIALOG nicht per Software
// nachzubilden, korrigiert der Bediener weiterhin lokal in der
// Bedienoberflaeche (session.go CorrectShot) - ABER anders als bei RM III
// wird das Ergebnis hier per EDI=Gesamtschusszahl;LetzteKartenschusszahl
// gefolgt von je einer S=[Nr];[Ring];[Teiler];[Flag]-Zeile (Flag U/V) an die
// RM zurueckgemeldet, wie es das RM-IV-Protokoll fuer diesen Fall verlangt.
//
// WICHTIG: die Korrektur passiert typischerweise ERST NACHDEM der Bediener
// den in Echtzeit angezeigten fehlerhaften Schuss bemerkt und behoben hat -
// also zeitlich NACH dieser WSC=-N-Meldung. Die EDI-Antwort darf deshalb
// nicht sofort (mit noch unkorrigierten Werten) gesendet werden, sondern
// erst, wenn EventHandler.AllShotsResolved() true liefert - siehe
// attemptPendingEdit, das hier direkt (falls schon alles aufgeloest ist) UND
// erneut ueber TryResolvePendingEdit (von session.go nach jeder Korrektur
// aufgerufen) versucht wird.
//
// NOCH NICHT AN ECHTER HARDWARE VERIFIZIERT. Fuer schnelles Ausprobieren
// einer Alternative (z.B. "WID") ohne Dienst-Neustart siehe SendRaw
// (web.go handleSendRaw).
func (m *RMIV) handleWSCLine(port serial.Port, rest string) {
	rest = strings.TrimSpace(rest)
	n, err := strconv.Atoi(rest)
	if err == nil && n < 0 {
		m.mu.Lock()
		m.pendingEditLastCard = -n
		m.mu.Unlock()
		m.handler.OnStatus(StatusEvent{Connected: true,
			Message: fmt.Sprintf("Editier-Anfrage der Wertmaschine (WSC=%d) - bitte fehlerhafte(n) Schuss/Schuesse oben korrigieren, wird danach automatisch fortgesetzt.", n)})
		m.attemptPendingEdit(port)
		return
	}
	m.handler.OnStatus(StatusEvent{Connected: true, Message: "Wartet auf Scheibe: WSC=" + rest})
}

// attemptPendingEdit sendet die faellige EDI-Antwort, sofern eine
// Editier-Anfrage offen ist UND der Bediener alle betroffenen Schuesse
// bereits korrigiert hat - sonst No-Op (wird beim naechsten
// TryResolvePendingEdit-Aufruf erneut versucht). Darf NUR aus der
// readLoop-Goroutine heraus aufgerufen werden (direkt aus handleWSCLine oder
// ueber editTrigger/readLoop's select) - liest/schreibt den Port ohne
// weitere Synchronisierung, das ist nur sicher, solange kein zweiter Leser
// gleichzeitig aktiv ist.
func (m *RMIV) attemptPendingEdit(port serial.Port) {
	m.mu.Lock()
	lastCard := m.pendingEditLastCard
	m.mu.Unlock()
	if lastCard == 0 {
		return // keine Anfrage offen
	}
	if !m.handler.AllShotsResolved() {
		return // Bediener noch nicht fertig - warten auf naechsten Trigger
	}
	shots := m.handler.PendingShotsForEdit()
	total := len(shots)
	m.handler.OnStatus(StatusEvent{Connected: true, Message: fmt.Sprintf("Sende EDI=%d;%d...", total, lastCard)})
	err := m.sendEdit(port, total, lastCard, shots)
	m.mu.Lock()
	m.pendingEditLastCard = 0
	m.mu.Unlock()
	if err != nil {
		m.handler.OnStatus(StatusEvent{Connected: true, Message: "EDI senden fehlgeschlagen: " + err.Error()})
		return
	}
	m.handler.OnStatus(StatusEvent{Connected: true, Message: "EDI-Korrektur gesendet - warte auf naechste Scheibe"})
}

// TryResolvePendingEdit weckt readLoop auf, um eine ggf. offene
// Editier-Anfrage zu bearbeiten - von session.go nach jeder Bediener-
// Korrektur aufgerufen (siehe CorrectShot). Nicht-blockierend: readLoop
// entscheidet selbst (attemptPendingEdit), ob tatsaechlich schon alles
// aufgeloest ist.
func (m *RMIV) TryResolvePendingEdit() {
	select {
	case m.editTrigger <- struct{}{}:
	default: // bereits ein Trigger anhaengig - reicht, wird sowieso geprueft
	}
}

// sendEdit sendet den EDI-Header gefolgt von je einer S=-Zeile pro Schuss -
// Flag "V" fuer einen vom Bediener korrigierten Schuss (ReadShot.Corrected),
// sonst "U". Jede einzelne Zeile (Header UND jede S=-Zeile) bekommt ihr
// eigenes ENQ/STX-Praeludium, siehe sendLineWithHandshake - mit echter
// Hardware bestaetigt: der EDI-Header wurde nur MIT vorherigem ENQ/STX
// bestaetigt, die direkt folgende S=-Zeile OHNE eigenes ENQ/STX blieb
// dagegen unbestaetigt (Timeout). "PC moechte senden" gilt demnach je Zeile,
// nicht nur einmal pro zusammenhaengender Sendefolge.
func (m *RMIV) sendEdit(port serial.Port, total, lastCard int, shots []EditShot) error {
	if err := m.sendLineWithHandshake(port, fmt.Sprintf("EDI=%d;%d", total, lastCard)); err != nil {
		return fmt.Errorf("EDI-Kopfzeile: %w", err)
	}
	for i, sh := range shots {
		flag := "U"
		if sh.Changed {
			flag = "V"
		}
		line := fmt.Sprintf("S=%d;%s;%s;%s", i+1, formatEditRing(sh.Ring, sh.Decimal), formatEditTeiler(sh.Teiler), flag)
		if err := m.sendLineWithHandshake(port, line); err != nil {
			return fmt.Errorf("S-Zeile %d: %w", i+1, err)
		}
	}
	return nil
}

// sendLineWithHandshake fuehrt eine vollstaendige "PC moechte senden"-
// Transaktion aus (ENQ, auf STX warten, Zeile senden, auf ACK/NAK warten) -
// siehe enqStxHandshake/sendFramedAwaitAck.
func (m *RMIV) sendLineWithHandshake(port serial.Port, cmd string) error {
	if err := m.enqStxHandshake(port); err != nil {
		return fmt.Errorf("ENQ/STX: %w", err)
	}
	return m.sendFramedAwaitAck(port, cmd)
}

func formatEditRing(ring *int, decimal *float64) string {
	if decimal != nil {
		return strconv.FormatFloat(*decimal, 'f', -1, 64)
	}
	if ring != nil {
		return strconv.Itoa(*ring)
	}
	return "0"
}

func formatEditTeiler(teiler *float64) string {
	if teiler != nil {
		return strconv.FormatFloat(*teiler, 'f', 2, 64)
	}
	return "0.00"
}

// writeFramed sendet einen Befehl im ueblichen RM-IV-Rahmen (String+
// Pruefsumme+CR, siehe rmivChecksum) OHNE auf ACK/NAK zu warten - fuer
// Befehle, die (anders als der initiale Konfigurationsstring in Configure)
// waehrend einer laufenden Messreihe gesendet werden.
func (m *RMIV) writeFramed(port serial.Port, cmd string) error {
	frame := append([]byte(cmd), rmivChecksum([]byte(cmd)), rmivCR)
	_, err := port.Write(frame)
	return err
}

// sendFramedAwaitAck wie writeFramed, wartet danach aber bis zu 300ms auf
// ACK/NAK (wie beim initialen Konfigurationsstring in Configure) - fuer den
// mehrzeiligen EDI-Austausch, wo laut allgemeinem Rahmenprotokoll jede
// einzelne Zeile bestaetigt werden muss. Nur sicher aus der readLoop-
// Goroutine heraus aufrufbar (siehe attemptPendingEdit-Kommentar).
func (m *RMIV) sendFramedAwaitAck(port serial.Port, cmd string) error {
	if err := m.writeFramed(port, cmd); err != nil {
		return err
	}
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		b, ok, err := readByte(port)
		if err != nil {
			return err
		}
		if ok && b == rmivACK {
			return nil
		}
		if ok && b == rmivNAK {
			return fmt.Errorf("NAK fuer %q empfangen", cmd)
		}
	}
	return fmt.Errorf("kein ACK/NAK fuer %q nach 300ms", cmd)
}

// SendRaw schickt einen beliebigen Befehl im RM-IV-Rahmen an die aktuell
// offene Verbindung - fuer manuelle Diagnose (z.B. "WID" ausprobieren) ohne
// Dienst-Neustart, siehe web.go handleSendRaw. Nur nutzbar, waehrend
// Configure() bereits eine Verbindung aufgebaut hat. Wartet bewusst NICHT
// auf ACK/NAK (laeuft nicht in der readLoop-Goroutine, ein Read hier wuerde
// mit deren Lesevorgang um dieselben Bytes konkurrieren) - die Reaktion der
// RM zeigt sich stattdessen ueber das normale Debug-Log (RX).
func (m *RMIV) SendRaw(cmd string) error {
	m.mu.Lock()
	port := m.port
	m.mu.Unlock()
	if port == nil {
		return fmt.Errorf("keine offene Verbindung")
	}
	return m.writeFramed(port, cmd)
}

// handleShotLine parst "[Schußnr];[Ring];[Teiler];[Winkel];[Flag]" (der
// SCH=-Praefix ist bereits abgeschnitten).
func (m *RMIV) handleShotLine(rest string) {
	fields := strings.Split(rest, ";")
	if len(fields) < 5 {
		m.handler.OnStatus(StatusEvent{Connected: true, Message: "unvollstaendige Schusszeile: " + rest})
		return
	}
	shotNo, _ := strconv.Atoi(strings.TrimSpace(fields[0]))
	ev := ShotEvent{ShotNo: shotNo, Flag: "ok"}

	ringStr := strings.TrimSpace(fields[1])
	if ringStr == "0.0" || ringStr == "-,-" || ringStr == "" {
		ev.Flag = "invalid"
	} else if v, err := strconv.ParseFloat(ringStr, 64); err == nil {
		r := int(v)
		ev.Ring = &r
		ev.Decimal = &v
	}
	if v, err := strconv.ParseFloat(strings.TrimSpace(fields[2]), 64); err == nil {
		ev.Teiler = &v
	}
	if v, err := strconv.ParseFloat(strings.TrimSpace(fields[3]), 64); err == nil {
		ev.Winkel = &v
	}
	switch strings.TrimSpace(fields[4]) {
	case "K":
		ev.Flag = "review"
	case "U":
		ev.Flag = "invalid"
	}
	m.handler.OnShot(ev)
}

// Abort sendet best-effort "ABR" an die RM (siehe Machine-Interface-
// Kommentar) und beendet danach den lokalen Lesevorgang. Das Senden ist rein
// best-effort (Fehler werden ignoriert, kein Blockieren) - Abort() muss auch
// funktionieren, wenn die Verbindung bereits gestoert ist; der wichtigere
// Teil ist in jedem Fall das lokale Schliessen des Lesevorgangs.
func (m *RMIV) Abort() {
	m.mu.Lock()
	port := m.port
	if m.abortCh != nil {
		close(m.abortCh)
		m.abortCh = nil
	}
	m.mu.Unlock()
	if port != nil {
		_ = m.writeFramed(port, "ABR")
	}
}

// Recover beendet den laufenden Lesevorgang - der ENQ/STX-Handshake in
// Configure() baut die Verbindung beim naechsten Aufruf von Grund auf neu
// auf. Anders als bei RM III (siehe rmiii.go) ist dieser Pfad noch nicht an
// echter RM-IV-Hardware verifiziert.
func (m *RMIV) Recover() error {
	m.Abort()
	return nil
}

func (m *RMIV) Close() error {
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
