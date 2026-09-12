// ============================================================================
// serial.go – gemeinsames Oeffnen des COM-Ports (nativ oder USB-Seriell,
// go.bug.st/serial abstrahiert das identisch) fuer RM III und RM IV.
//
// Rohdaten-Mitschnitt gibt es auf zwei Wegen, die beide unabhaengig
// voneinander aktiv sein koennen:
//   - Datei (nur fuer die Analyse, siehe cmd_test_manual/main.go): ist die
//     Umgebungsvariable WERTMASCHINE_RAW_LOG gesetzt (Dateipfad), wird jedes
//     gelesene/geschriebene Byte zusaetzlich dorthin protokolliert.
//   - RawSink-Callback (Produktivbetrieb, siehe session.go/web.go): wird
//     openPort ein Sink uebergeben, bekommt er ebenfalls jedes Byte live
//     gemeldet - fuer den Entwickler-Debug-Modus in der Bedienoberflaeche
//     (nur aktiv, wenn der globale Entwicklermodus am Server eingeschaltet
//     ist, siehe wertmaschine/main.go/session.go).
// ============================================================================
package disag

import (
	"fmt"
	"os"
	"sync"
	"time"

	"go.bug.st/serial"
)

// RawSink bekommt jedes gelesene/geschriebene Byte auf der seriellen
// Schnittstelle live gemeldet (dir: "TX"|"RX").
type RawSink func(dir string, data []byte)

// openPort oeffnet den Port mit den gegebenen Einstellungen und einem
// Lese-Timeout (fuer wiederholtes, unblockierendes Pruefen auf Abort()
// zwischen Lesevorgaengen - siehe rmiii.go/rmiv.go readLoop). rawSink darf
// nil sein (kein Live-Mitschnitt).
func openPort(portName string, baud int, readTimeout time.Duration, rawSink RawSink) (serial.Port, error) {
	port, err := serial.Open(portName, &serial.Mode{
		BaudRate: baud,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	})
	if err != nil {
		return nil, fmt.Errorf("Port %s (Baud %d) oeffnen: %w", portName, baud, err)
	}
	if err := port.SetReadTimeout(readTimeout); err != nil {
		port.Close()
		return nil, fmt.Errorf("Lese-Timeout setzen: %w", err)
	}

	var logFile *os.File
	if logPath := os.Getenv("WERTMASCHINE_RAW_LOG"); logPath != "" {
		lf, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			port.Close()
			return nil, fmt.Errorf("Rohdaten-Log %s oeffnen: %w", logPath, err)
		}
		fmt.Fprintf(lf, "--- %s: Port %s (Baud %d) geoeffnet ---\n", time.Now().Format("2006-01-02 15:04:05.000"), portName, baud)
		logFile = lf
	}

	if logFile != nil || rawSink != nil {
		return &loggingPort{Port: port, logFile: logFile, rawSink: rawSink}, nil
	}
	return port, nil
}

// loggingPort spiegelt jedes Read/Write zusaetzlich (roh, ungeparst) in eine
// Logdatei und/oder einen RawSink-Callback - alle anderen Port-Methoden
// (SetRTS/SetDTR/GetModemStatusBits/...) werden unveraendert an das
// eingebettete serial.Port durchgereicht.
type loggingPort struct {
	serial.Port
	logFile *os.File
	rawSink RawSink
	mu      sync.Mutex
}

func (p *loggingPort) Read(b []byte) (int, error) {
	n, err := p.Port.Read(b)
	if n > 0 {
		p.logRaw("RX", b[:n])
	}
	return n, err
}

func (p *loggingPort) Write(b []byte) (int, error) {
	n, err := p.Port.Write(b)
	p.logRaw("TX", b)
	return n, err
}

func (p *loggingPort) Close() error {
	p.mu.Lock()
	if p.logFile != nil {
		fmt.Fprintf(p.logFile, "--- %s: Port geschlossen ---\n", time.Now().Format("2006-01-02 15:04:05.000"))
		p.logFile.Close()
	}
	p.mu.Unlock()
	return p.Port.Close()
}

func (p *loggingPort) logRaw(dir string, data []byte) {
	p.mu.Lock()
	lf := p.logFile
	p.mu.Unlock()
	if lf != nil {
		p.mu.Lock()
		fmt.Fprintf(lf, "%s %s %2d Byte  ascii=%q  hex=% x\n",
			time.Now().Format("15:04:05.000"), dir, len(data), data, data)
		p.mu.Unlock()
	}
	if p.rawSink != nil {
		p.rawSink(dir, data)
	}
}

// ListPorts liefert die auf diesem Rechner verfuegbaren seriellen Ports
// (native COM-Ports und USB-Seriell-Adapter erscheinen hier gleichermassen) -
// fuer die Port-Auswahl in der Bedienoberflaeche (web/index.html).
func ListPorts() ([]string, error) {
	return serial.GetPortsList()
}
