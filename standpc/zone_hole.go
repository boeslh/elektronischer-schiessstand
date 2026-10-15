// ============================================================================
// zone_hole.go – Lochradius der Vorsatzscheibe fuer die Zonen-Kalibrierung
// (SET ZHOLE, Firmware Rev 4.17.0) automatisch je Scheibentyp setzen.
//
// Zone A der Zonen-Kalibrierung ist das Loch der Vorsatzscheibe, durch das nur
// das Schussband getroffen wird. Dessen Groesse entspricht dem schwarzen
// Spiegel der Scheibe (LG Ø 30,5 mm, LP Ø 59,5 mm) und haengt damit von der
// Disziplin ab. Der Stand-PC gleicht ZHOLE deshalb laufend mit der aktuellen
// Disziplin ab - nur fuer LG- und LP-Scheiben (nur dort gibt es die
// Vorsatzscheibe mit Loch) und nur bei Firmware, die Zonen kennt.
// ============================================================================
package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"strconv"
	"time"
)

const zoneHoleSyncInterval = 3 * time.Second

// zoneHoleUm: Lochradius in 0.001 mm fuer die Scheibe (Durchmesser des
// schwarzen Spiegels / 2), ok=false fuer Scheiben ohne Vorsatzscheibe.
func zoneHoleUm(tg TargetGeometry) (int, bool) {
	if tg.Name != "LG" && tg.Name != "LP" {
		return 0, false
	}
	d := 0.0
	for _, r := range tg.Rings {
		if r.Filled && r.D > d {
			d = r.D
		}
	}
	if d <= 0 {
		return 0, false
	}
	return int(math.Round(d / 2 * 1000)), true
}

// syncZoneHole: ein Durchlauf des Abgleichs (siehe Dateikopf).
func (ws *WebServer) syncZoneHole() {
	if ws.sessions == nil || ws.deviceState == nil || ws.cmds == nil {
		return
	}
	def := ws.sessions.CurrentDiscipline()
	if def == nil {
		return
	}
	tg, ok := ws.targets[strconv.Itoa(def.TargetNo)]
	if !ok {
		return
	}
	want, ok := zoneHoleUm(tg)
	if !ok {
		return
	}
	c := ws.deviceState.Snapshot().Config
	if c == nil || c.Zones == nil || c.ZHoleUm == nil {
		return // config noch unbekannt bzw. Firmware ohne Zonen (vor 4.17.0)
	}
	if *c.ZHoleUm == want {
		return
	}
	if err := ws.sendChecked(fmt.Sprintf("SET ZHOLE=%d", want)); err != nil {
		log.Printf("Zonen: ZHOLE=%d fuer %s setzen fehlgeschlagen: %v", want, tg.Name, err)
		return
	}
	log.Printf("Zonen: Lochradius fuer %s-Scheibe auf %.2f mm gesetzt (SET ZHOLE=%d)", tg.Name, float64(want)/1000, want)
	ws.requestConfigSnapshot()
}

func (ws *WebServer) runZoneHoleSync(ctx context.Context) {
	t := time.NewTicker(zoneHoleSyncInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ws.syncZoneHole()
		}
	}
}
