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
}

func NewRMIV(portName string, handler EventHandler) *RMIV {
	return &RMIV{portName: portName, handler: handler}
}

// NewRMIVWithRawSink wie NewRMIV, zusaetzlich mit Live-Rohdaten-Mitschnitt
// (siehe serial.go RawSink) - fuer den Entwickler-Debug-Modus.
func NewRMIVWithRawSink(portName string, handler EventHandler, rawSink RawSink) *RMIV {
	return &RMIV{portName: portName, handler: handler, rawSink: rawSink}
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

// Configure fuehrt den ENQ/STX-Handshake aus, sendet den Konfigurationsstring
// und startet danach den Lesevorgang fuer Ergebniszeilen in einer eigenen
// Goroutine.
func (m *RMIV) Configure(configString string) error {
	port, err := m.ensurePort()
	if err != nil {
		return err
	}

	m.handler.OnStatus(StatusEvent{Connected: false, Message: "sende ENQ, warte auf STX..."})
	deadline := time.Now().Add(30 * time.Second)
	gotSTX := false
	for time.Now().Before(deadline) {
		if _, err := port.Write([]byte{rmivENQ}); err != nil {
			return fmt.Errorf("ENQ senden: %w", err)
		}
		ok, err := waitForByte(port, rmivSTX, 100*time.Millisecond)
		if err != nil {
			return fmt.Errorf("auf STX warten: %w", err)
		}
		if ok {
			gotSTX = true
			break
		}
	}
	if !gotSTX {
		return fmt.Errorf("Wertmaschine antwortet nicht (kein STX auf ENQ nach 30s)")
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
		m.handleLine(string(payload))
	}
}

func (m *RMIV) handleLine(line string) {
	switch {
	case strings.HasPrefix(line, "SCH="):
		m.handleShotLine(strings.TrimPrefix(line, "SCH="))
	case strings.HasPrefix(line, "WSC="):
		m.handler.OnStatus(StatusEvent{Connected: true, Message: "Wartet auf Scheibe: " + line})
	case strings.HasPrefix(line, "WSE"): // WSE/WSEA
		m.handler.OnStatus(StatusEvent{Connected: true, Message: "Messreihe beendet"})
	case strings.HasPrefix(line, "STA"): // STA/STAF
		m.handler.OnStatus(StatusEvent{Connected: true, Message: "Neue Scheibe gestartet"})
	default:
		m.handler.OnStatus(StatusEvent{Connected: true, Message: "unbekannte Meldung: " + line})
	}
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

func (m *RMIV) Abort() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.abortCh != nil {
		close(m.abortCh)
		m.abortCh = nil
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
