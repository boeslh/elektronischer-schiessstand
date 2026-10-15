// ============================================================================
// maintenance_rawlog.go – Rohdaten (SET RAWLOG=1, Firmware Rev 4.16.0)
// automatisch fuer Wartungs-Sessions einschalten.
//
// Wartungs-Schuesse dienen der Kalibrierung im Simulator, der mit dem
// raw-Telegramm (alle Flanken + Parameter je Schuss) exakt nachrechnen kann.
// Damit das nicht vergessen wird, schaltet der Stand-PC RAWLOG am Geraet ein,
// sobald der Stand mit dem Wartungs-Schuetzen belegt ist, und nach dem Ende
// der Wartungs-Session wieder aus - aber nur, wenn er es selbst eingeschaltet
// hat (ein von Hand dauerhaft eingeschaltetes RAWLOG bleibt unberuehrt).
// Das "selbst eingeschaltet" wird in einer Datei vermerkt, damit es auch
// nach einem Neustart des Stand-PC zurueckgenommen wird. Ist das Geraet
// gerade nicht erreichbar, wird es beim naechsten Durchlauf erneut versucht.
// ============================================================================
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

const maintenanceRawLogInterval = 3 * time.Second

func (ws *WebServer) rawLogAutoFile() string {
	dir := ws.cfg.ShotLogDir
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, fmt.Sprintf("rawlog_auto_lane%d.flag", ws.cfg.LaneNo))
}

// rawLogConfigured: Wert von RAWLOG laut letztem config (ok=false: unbekannt
// bzw. Firmware ohne RAWLOG).
func (ws *WebServer) rawLogConfigured() (on, ok bool) {
	c := ws.deviceState.Snapshot().Config
	if c == nil || c.RawLog == nil {
		return false, false
	}
	return *c.RawLog == 1, true
}

// syncMaintenanceRawLog: ein Durchlauf des Abgleichs (siehe Dateikopf).
func (ws *WebServer) syncMaintenanceRawLog() {
	if ws.sessions == nil || ws.deviceState == nil || ws.cmds == nil {
		return
	}
	want := ws.sessions.IsMaintenance()
	_, statErr := os.Stat(ws.rawLogAutoFile())
	autoOn := statErr == nil

	if !want && !autoOn {
		return
	}
	on, known := ws.rawLogConfigured()
	if !known {
		// config noch unbekannt (oder Firmware ohne RAWLOG): einmal nachfragen
		var cfg ConfigTelegram
		if err := ws.queryTelegram("SHOW", "config", &cfg); err != nil {
			return // Geraet nicht erreichbar - naechster Versuch
		}
		if cfg.RawLog == nil {
			if want {
				log.Printf("Wartung: Firmware kennt RAWLOG nicht (vor 4.16.0) - Rohdaten nicht verfuegbar")
			}
			os.Remove(ws.rawLogAutoFile())
			return
		}
		on = *cfg.RawLog == 1
	}

	switch {
	case want && !on:
		if err := ws.sendChecked("SET RAWLOG=1"); err != nil {
			log.Printf("Wartung: RAWLOG einschalten fehlgeschlagen: %v", err)
			return
		}
		os.WriteFile(ws.rawLogAutoFile(), []byte(time.Now().Format(time.RFC3339)+"\n"), 0o644)
		log.Printf("Wartung: Rohdaten (RAWLOG) automatisch eingeschaltet")
		ws.requestConfigSnapshot()
	case !want && autoOn:
		if on {
			if err := ws.sendChecked("SET RAWLOG=0"); err != nil {
				log.Printf("Wartung: RAWLOG ausschalten fehlgeschlagen: %v", err)
				return
			}
			log.Printf("Wartung beendet: Rohdaten (RAWLOG) wieder ausgeschaltet")
			ws.requestConfigSnapshot()
		}
		os.Remove(ws.rawLogAutoFile())
	}
}

func (ws *WebServer) runMaintenanceRawLog(ctx context.Context) {
	t := time.NewTicker(maintenanceRawLogInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ws.syncMaintenanceRawLog()
		}
	}
}
