// ============================================================================
// werbung.go – Werbebilder für den Display-Server, dateibasiert (nicht in der
// DB) unter <werbungBaseDir>/<preisschiessen_id>/{main,lists}/*.{jpg,...}.
//
// main  -> unten auf der Startseite (siehe handleHome in site.go)
// lists -> alle werbung_intervall Teilnehmer-Zeilen in den Ergebnislisten
//
//	(renderWertungListe/renderVereinListe in site.go), Intervall aus
//	ps_anzeige_config (siehe server/preisschiessen_wertungen.go).
//
// Rotation: pro Verzeichnis ein atomarer Zähler, der bei jedem Anzeigen um 1
// erhöht wird - images[zähler % len(images)] verteilt die Bilder exakt
// gleichmäßig (kein Zufall, kein persistenter Zustand über Neustarts hinweg
// nötig, da rein kosmetisch). Leeres/fehlendes Verzeichnis -> keine Werbung,
// ohne Fehler oder Lücke im Layout.
// ============================================================================
package main

import (
	"context"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// werbungBaseDir wird einmalig in main() aus der Konfiguration gesetzt
// (Default /opt/ps/bilder, siehe loadConfig).
var werbungBaseDir string

var werbungExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
}

var adCounters sync.Map // dir (string) -> *uint64

// listAdImages liefert die (sortierten) Bilddateinamen eines Werbe-Verzeichnisses.
// Fehlendes/leeres Verzeichnis liefert einfach eine leere Liste, kein Fehler.
func listAdImages(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if werbungExts[strings.ToLower(filepath.Ext(e.Name()))] {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// nextAdImage wählt reihum (fair, gleichverteilt) das nächste Bild aus dir.
// ok=false, wenn das Verzeichnis keine Bilder enthält.
func nextAdImage(dir string) (name string, ok bool) {
	images := listAdImages(dir)
	if len(images) == 0 {
		return "", false
	}
	ctrIface, _ := adCounters.LoadOrStore(dir, new(uint64))
	ctr := ctrIface.(*uint64)
	n := atomic.AddUint64(ctr, 1) - 1
	return images[n%uint64(len(images))], true
}

// adDir: Verzeichnis für ein Preisschießen/Unterbereich ("main" oder "lists").
func (h *siteHandler) adDir(sub string) string {
	return filepath.Join(werbungBaseDir, h.preisschiessenID, sub)
}

func (h *siteHandler) adImgURL(sub, name string) string {
	return h.base() + "/werbung/" + sub + "/" + url.PathEscape(name)
}

// renderAdBlock: Werbeblock für die Startseite (sub="main"), "" wenn keine
// Bilder vorhanden sind.
func (h *siteHandler) renderAdBlock(sub string) string {
	name, ok := nextAdImage(h.adDir(sub))
	if !ok {
		return ""
	}
	return `<div style="text-align:center;margin-top:28px"><img src="` +
		html.EscapeString(h.adImgURL(sub, name)) +
		`" style="max-width:100%;max-height:220px"></div>`
}

// renderAdRow: Werbe-Tabellenzeile für Ergebnislisten (sub="lists"), über
// colspan Spalten hinweg, "" wenn keine Bilder vorhanden sind.
func (h *siteHandler) renderAdRow(sub string, colspan int) string {
	name, ok := nextAdImage(h.adDir(sub))
	if !ok {
		return ""
	}
	return `<tr><td colspan="` + strconv.Itoa(colspan) + `" style="text-align:center;padding:10px;background:#fff !important">` +
		`<img src="` + html.EscapeString(h.adImgURL(sub, name)) + `" style="max-width:100%;max-height:160px"></td></tr>`
}

// loadWerbungIntervall liest das konfigurierte Intervall (Standard 20, falls
// noch keine ps_anzeige_config-Zeile existiert - die wird normalerweise vom
// Hauptserver beim ersten Aufruf der Anzeige-Konfiguration angelegt, siehe
// server/preisschiessen_wertungen.go GetAnzeigeConfig).
func (h *siteHandler) loadWerbungIntervall(ctx context.Context) int {
	var n int
	err := h.pool.QueryRow(ctx,
		`SELECT werbung_intervall FROM ps_anzeige_config WHERE preisschiessen_id=$1`, h.preisschiessenID,
	).Scan(&n)
	if err != nil || n < 1 {
		return 20
	}
	return n
}

// loadShowScheibe: zusätzliche Spalte mit dem Namen der geschossenen
// Scheibe(n) - gilt gleichermaßen für die browsbare Ergebnis-Website
// (renderWertungListe) und den Kiosk-Modus (display.go), siehe
// PSWertungErgebnis.Scheiben im Hauptserver.
func (h *siteHandler) loadShowScheibe(ctx context.Context) bool {
	var b bool
	if err := h.pool.QueryRow(ctx,
		`SELECT show_scheibe FROM ps_anzeige_config WHERE preisschiessen_id=$1`, h.preisschiessenID,
	).Scan(&b); err != nil {
		return false
	}
	return b
}

// handleWerbungImage liefert eine einzelne Werbebild-Datei aus. Der
// angeforderte Dateiname wird gegen die aktuelle Verzeichnis-Auflistung
// geprüft (nicht nur per filepath.Base saniert) - so kann nie eine beliebige
// Datei außerhalb des Werbe-Verzeichnisses ausgeliefert werden.
func (h *siteHandler) handleWerbungImage(w http.ResponseWriter, r *http.Request) {
	sub := r.PathValue("sub")
	if sub != "main" && sub != "lists" {
		http.NotFound(w, r)
		return
	}
	file := filepath.Base(r.PathValue("file"))
	dir := h.adDir(sub)
	found := false
	for _, n := range listAdImages(dir) {
		if n == file {
			found = true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(dir, file))
}
