package main

import (
	"log"
	"strings"
	"sync"
	"time"
)

// Automatischer Leervorschub ("ACTION TARGETCHANGE") bei jeder neuen Scheibe
// und beim Umschalten von Probe auf Wertung: zwischen zwei Scheiben bzw.
// zwischen Probe und Wertung bleibt so auf dem Papierband ein Feld frei.
//
// Mehrfaches Umschalten ohne Schuss dazwischen loest nur EINEN Vorschub aus
// (fed bleibt gesetzt, bis wieder ein Schuss eintrifft). Der erste Schuss nach
// einem ausgefuehrten Vorschub bekommt paper_feeds_before=1 - daran erkennt
// die Bandkontrolle (server/strip_check.go) das zusaetzlich freie Feld.

const (
	targetChangeRetry   = 300 * time.Millisecond
	targetChangeTimeout = 20 * time.Second
)

type targetChanger struct {
	mu      sync.Mutex
	fed     bool // Vorschub seit dem letzten Schuss bereits ausgeloest/angefordert
	running bool // Sende-Goroutine laeuft (Retry bei "paper feed busy")
	pending int  // ausgefuehrte Vorschuebe, die der naechste Schuss mitbekommt
}

// RequestTargetChange fordert einen Leervorschub an (nicht blockierend, darf
// unter SessionManager.mu aufgerufen werden).
func (ws *WebServer) RequestTargetChange(reason string) {
	if ws == nil || ws.cmds == nil {
		return
	}
	tc := &ws.targetChange
	tc.mu.Lock()
	if tc.fed || tc.running {
		tc.mu.Unlock()
		log.Printf("Scheibenwechsel-Vorschub (%s): bereits erfolgt, kein Schuss seitdem – übersprungen", reason)
		return
	}
	tc.fed = true
	tc.running = true
	tc.mu.Unlock()
	go ws.runTargetChange(reason)
}

func (ws *WebServer) runTargetChange(reason string) {
	tc := &ws.targetChange
	defer func() {
		tc.mu.Lock()
		tc.running = false
		tc.mu.Unlock()
	}()
	if ws.deviceState != nil {
		if c := ws.deviceState.Snapshot().Config; c != nil && (c.PaperAuto == 0 || c.PaperFeedMm <= 0) {
			log.Printf("Scheibenwechsel-Vorschub (%s): Papiervorschub am Gerät aus – nicht gesendet", reason)
			return
		}
	}
	deadline := time.Now().Add(targetChangeTimeout)
	for {
		err := ws.sendChecked("ACTION TARGETCHANGE")
		if err == nil {
			tc.mu.Lock()
			tc.pending = 1
			tc.mu.Unlock()
			log.Printf("Scheibenwechsel-Vorschub ausgelöst (%s)", reason)
			return
		}
		// "paper feed busy": Vorschub des letzten Schusses laeuft noch
		// (z.B. automatischer Wechsel in die Wertung nach dem letzten
		// Probeschuss) - kurz warten und erneut versuchen. Ebenso, solange
		// das Geraet (noch) nicht verbunden ist.
		if time.Now().After(deadline) {
			log.Printf("Scheibenwechsel-Vorschub (%s) fehlgeschlagen: %v", reason, err)
			tc.mu.Lock()
			tc.fed = false // spaeterer Wechsel darf es erneut versuchen
			tc.mu.Unlock()
			return
		}
		if !strings.Contains(err.Error(), "busy") {
			time.Sleep(time.Second)
		} else {
			time.Sleep(targetChangeRetry)
		}
	}
}

// ConsumeTargetChange: fuer jeden eintreffenden Schuss aufrufen. Liefert die
// Zahl der seit dem vorigen Schuss ausgefuehrten Leervorschuebe (0/1) und
// gibt die Entprellung wieder frei.
func (ws *WebServer) ConsumeTargetChange() int {
	if ws == nil {
		return 0
	}
	tc := &ws.targetChange
	tc.mu.Lock()
	defer tc.mu.Unlock()
	n := tc.pending
	tc.pending = 0
	if !tc.running {
		tc.fed = false
	}
	return n
}
