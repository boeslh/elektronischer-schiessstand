// ============================================================================
// devicelink.go – Kommando-Versand an den ESP32 (Gegenstück zum reinen
// Empfangspfad in tcp.go/serial.go/transport.go).
//
// DeviceLink kapselt die jeweils AKTUELL aktive Verbindung (TCP oder
// Serial - beide sind io.Writer) threadsicher, damit ein Admin-Handler
// Befehle senden kann, ohne selbst zu wissen, welcher Transport gerade
// verbunden ist. CommandManager baut darauf die Korrelations-ID-Logik
// (Firmware Rev 4.9.0, siehe protokoll-referenz.md Abschnitt 2) auf: ein
// Befehl bekommt ein Suffix " #<id>", passende Antwortzeilen tragen
// "corr":<id> und werden hier gesammelt statt nur geloggt zu werden.
//
// Authentifizierung (TCP-only, siehe verifyAndStripLine): die TCP-Strecke
// zum ESP32 ist das einzige NETZWERK-erreichbare Glied dieser Verbindung
// (Serial braucht physischen Kabelzugriff, der bereits die Absicherung
// ist - und die Firmware hat einen dokumentierten manuellen
// Diagnose-Workflow ueber ein Serial-Terminal, der bei Signaturpflicht
// kaputt ginge). Ist ein PSK konfiguriert, wird deshalb NUR beim
// TCP-Schreiben signiert (isTCP-Flag bei SetActive) bzw. beim TCP-Lesen
// geprueft (tcp.go). Ohne PSK bleibt das Verhalten unveraendert
// (Rueckwaertskompatibel fuer einen risikofreien Rollout, siehe Plan).
// ============================================================================
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

var ErrDeviceNotConnected = errors.New("kein Geraet verbunden")

// DeviceLink haelt die aktuell aktive Schreibverbindung zum ESP32 (TCP-Conn
// oder Serial-Port). SetActive/Clear werden von tcp.go/serial.go bei
// Verbindungsauf-/-abbau aufgerufen; Clear loescht nur, wenn w noch der
// aktuell aktive Writer ist (verhindert ein Race, falls eine neue
// Verbindung eine alte beim Schliessen ueberholt).
type DeviceLink struct {
	psk []byte // anlagenweiter Pre-Shared Key, leer = Signieren aus (siehe Dateikopf)

	mu     sync.Mutex
	writer io.Writer
	isTCP  bool
}

// NewDeviceLink: psk darf leer sein (Feature dann inaktiv, siehe Dateikopf).
func NewDeviceLink(psk []byte) *DeviceLink { return &DeviceLink{psk: psk} }

func (d *DeviceLink) SetActive(w io.Writer, isTCP bool) {
	d.mu.Lock()
	d.writer = w
	d.isTCP = isTCP
	d.mu.Unlock()
}

func (d *DeviceLink) Clear(w io.Writer) {
	d.mu.Lock()
	if d.writer == w {
		d.writer = nil
	}
	d.mu.Unlock()
}

// Send schreibt eine Befehlszeile (ohne "\n" - wird hier angehaengt) an die
// aktuell aktive Verbindung - signiert, wenn es sich um TCP handelt und ein
// PSK konfiguriert ist (siehe Dateikopf).
func (d *DeviceLink) Send(line string) error {
	d.mu.Lock()
	w := d.writer
	isTCP := d.isTCP
	d.mu.Unlock()
	if w == nil {
		return ErrDeviceNotConnected
	}
	if isTCP && len(d.psk) > 0 {
		line = signLine(line, d.psk)
	}
	_, err := io.WriteString(w, line+"\n")
	return err
}

// authTagHexLen: 8 Byte (64 Bit) HMAC-SHA256-Tag, hex-kodiert - siehe Plan
// "Wire-Format" fuer die Begruendung der verkuerzten Taglaenge.
const authTagHexLen = 16

// signLine stellt der Zeile "<16-hex-tag> " voran (Tag ueber die
// Original-Bytes von line, siehe Wire-Format im Plan).
func signLine(line string, psk []byte) string {
	return authTagHex(psk, []byte(line)) + " " + line
}

// verifyAndStripLine prueft eine eingehende Zeile gegen den erwarteten
// HMAC-Tag und liefert bei Erfolg die reine Nutzlast (ohne Tag-Praefix)
// zurueck. Konstant-zeit-Vergleich via hmac.Equal (Timing-Seitenkanal).
func verifyAndStripLine(line []byte, psk []byte) ([]byte, bool) {
	sp := bytes.IndexByte(line, ' ')
	if sp != authTagHexLen {
		return nil, false
	}
	tagHex, payload := line[:sp], line[sp+1:]
	want := authTagHex(psk, payload)
	if !hmac.Equal([]byte(want), tagHex) {
		return nil, false
	}
	return payload, true
}

func authTagHex(psk, payload []byte) string {
	mac := hmac.New(sha256.New, psk)
	mac.Write(payload)
	sum := mac.Sum(nil)
	return hex.EncodeToString(sum[:authTagHexLen/2])
}

// telegramHeader: minimaler Peek auf jede eingehende Zeile, um "type" und
// (seit Rev 4.9.0) "corr" zu bestimmen, bevor in den eigentlichen,
// typspezifischen Struct dekodiert wird (siehe transport.go dispatchLine).
type telegramHeader struct {
	Type string `json:"type"`
	Corr *int   `json:"corr"`
}

// CommandManager verwaltet die Korrelations-IDs fuer per SendCommand
// gesendete Befehle und liefert die dazugehoerigen Antwortzeilen zurueck.
type CommandManager struct {
	link *DeviceLink

	mu      sync.Mutex
	nextID  int
	pending map[int]chan []byte
}

func NewCommandManager(link *DeviceLink) *CommandManager {
	return &CommandManager{link: link, pending: map[int]chan []byte{}}
}

// Deliver wird von dispatchLine() fuer JEDE eingehende Zeile mit gesetztem
// "corr" aufgerufen - liefert die Zeile an den wartenden SendCommand-Aufruf
// aus, falls einer mit dieser ID existiert (sonst wird die Zeile ignoriert,
// z.B. bei verspaeteten/verwaisten Antworten nach Timeout).
func (c *CommandManager) Deliver(corr int, line []byte) {
	c.mu.Lock()
	ch, ok := c.pending[corr]
	c.mu.Unlock()
	if !ok {
		return
	}
	cp := append([]byte(nil), line...)
	select {
	case ch <- cp:
	default:
		// Kanal voll (sehr viele Zeilen fuer einen Befehl) - collectResponses
		// liest kontinuierlich mit,
		// das sollte praktisch nie blockieren; im Zweifel Zeile verwerfen
		// statt den ESP32-Lesepfad aufzuhalten.
	}
}

const defaultCommandTimeout = 2 * time.Second
const commandQuietPeriod = 250 * time.Millisecond

// SendCommand sendet cmd mit angehaengter Korrelations-ID an den ESP32 und
// sammelt alle Antwortzeilen mit passendem "corr", bis entweder timeout
// erreicht ist oder commandQuietPeriod lang keine neue Zeile mehr ankam
// (robust fuer Ein- wie Mehrzeilen-Antworten, ohne Sonderfaelle je Befehl).
func (c *CommandManager) SendCommand(cmd string, timeout time.Duration) ([]json.RawMessage, error) {
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan []byte, 32)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.link.Send(fmt.Sprintf("%s #%d", cmd, id)); err != nil {
		return nil, err
	}

	var out []json.RawMessage
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	quiet := time.NewTimer(timeout) // erst nach der ersten Zeile auf die kurze Ruhephase umstellen
	defer quiet.Stop()
	gotAny := false
	for {
		select {
		case line := <-ch:
			out = append(out, json.RawMessage(line))
			gotAny = true
			if !quiet.Stop() {
				select {
				case <-quiet.C:
				default:
				}
			}
			quiet.Reset(commandQuietPeriod)
		case <-quiet.C:
			if gotAny {
				return out, nil
			}
			// noch keine Zeile: weiter auf deadline warten
		case <-deadline.C:
			if gotAny {
				return out, nil
			}
			return nil, fmt.Errorf("keine Antwort innerhalb von %s", timeout)
		}
	}
}
