// ============================================================================
// display.go – liest ps_anzeige_config + ps_wertung_ergebnisse und rendert
// eine automatisch weiterschaltende HTML-Anzeige (analog gs26/frontend/
// refresh.php + _rolling_core.php, aber gegen den neuen Postgres-Cache statt
// gegen gs26_SerienP/TeilerP in MySQL).
// ============================================================================
package main

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ergebnisRow struct {
	Platz    int
	Name     string
	Verein   string
	Klasse   string
	Werte    []float64
	Summe    float64
	Scheiben []string
}

type wertungDisplay struct {
	ShortDesc    string
	Typ          string
	Wertungsfeld string
	Ergebnisse   []ergebnisRow
}

// vereinSection: eine der drei festen Vereins-Auswertungen (Anzahl/Prozent/
// Punkte), live berechnet wie in site.go renderVereinListe - anders als bei
// den Teilnehmer-Wertungen gibt es dafür keinen Ergebnis-Cache.
type vereinSection struct {
	Typ        string
	Label      string
	Ergebnisse []vereinErgebnisZeile
}

// kioskSection: ein Eintrag aus PSAnzeigeConfig.AnzeigeItems, aufgelöst zu
// entweder einer Teilnehmer-Wertung oder einer Vereins-Auswertung - siehe
// loadDisplay und renderPage.
type kioskSection struct {
	Kind    string // "wertung" | "verein"
	Wertung wertungDisplay
	Verein  vereinSection
}

type anzeigeConfig struct {
	ReloadSeconds int
	TitleFontSize int
	ListFontSize  int
	// AnzeigeItems: "wertung:<uuid>" bzw. "verein:anzahl"/"verein:prozent"/
	// "verein:punkte", siehe server/preisschiessen_wertungen.go
	// PSAnzeigeConfig.AnzeigeItems und kioskSection.
	AnzeigeItems []string
	Farben       farbenConfig
	// Spaltensteuerung im Kiosk-Modus - Verein/Klasse einzeln aus-/
	// einblendbar, dafür eine konfigurierbare Anzahl an Einzelergebnis-
	// Spalten (0-10), siehe renderPage.
	ShowVerein   bool
	ShowKlasse   bool
	AnzahlEinzel int
	// ShowScheibe: zusätzliche Spalte mit dem Namen der geschossenen
	// Scheibe(n) - gilt gleichermaßen für Kiosk-Modus und die browsbare
	// Ergebnis-Website (site.go), siehe PSWertungErgebnis.Scheiben.
	ShowScheibe bool
}

// loadDisplay lädt die Anzeige-Konfiguration und die daraus aufgelösten
// Kiosk-Sections. itemsColumn wählt zwischen den zwei unabhängigen
// Kiosk-Anzeigen ("anzeige_items" für /kiosk, "anzeige_items_2" für
// /kiosk2) - alle übrigen Einstellungen gelten für beide gemeinsam, siehe
// server/preisschiessen_wertungen.go PSAnzeigeConfig.AnzeigeItems2.
// itemsColumn kommt ausschließlich aus dem eigenen Code (nie aus der
// Anfrage), daher ist die String-Interpolation im SQL hier unbedenklich.
func loadDisplay(ctx context.Context, pool *pgxpool.Pool, preisschiessenID, itemsColumn string) (anzeigeConfig, []kioskSection, error) {
	var cfg anzeigeConfig
	cfg.Farben = loadFarbenConfig(ctx, pool, preisschiessenID)
	err := pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT reload_seconds, title_font_size, list_font_size, %s,
		       kiosk_show_verein, kiosk_show_klasse, kiosk_anzahl_einzelergebnisse, show_scheibe
		FROM ps_anzeige_config WHERE preisschiessen_id=$1`, itemsColumn), preisschiessenID,
	).Scan(&cfg.ReloadSeconds, &cfg.TitleFontSize, &cfg.ListFontSize, &cfg.AnzeigeItems,
		&cfg.ShowVerein, &cfg.ShowKlasse, &cfg.AnzahlEinzel, &cfg.ShowScheibe)
	if err != nil {
		// Noch keine Konfiguration gespeichert -> sinnvolle Defaults, leere Anzeige.
		cfg.ReloadSeconds, cfg.TitleFontSize, cfg.ListFontSize = 5, 20, 18
		cfg.ShowVerein, cfg.ShowKlasse, cfg.AnzahlEinzel = true, true, 5
		return cfg, nil, nil
	}

	var out []kioskSection
	for _, item := range cfg.AnzeigeItems {
		if typ, ok := strings.CutPrefix(item, "verein:"); ok {
			label, ok := vereinTypLabels[typ]
			if !ok {
				continue // unbekannter Typ -> überspringen
			}
			ergebnisse, err := computeVereinsAuswertung(ctx, pool, preisschiessenID, typ)
			if err != nil {
				continue
			}
			out = append(out, kioskSection{Kind: "verein", Verein: vereinSection{Typ: typ, Label: label, Ergebnisse: ergebnisse}})
			continue
		}
		wid, ok := strings.CutPrefix(item, "wertung:")
		if !ok {
			continue
		}
		var wd wertungDisplay
		if err := pool.QueryRow(ctx,
			`SELECT short_desc, typ, COALESCE(wertungsfeld,'') FROM ps_wertungen WHERE id=$1 AND visible`, wid,
		).Scan(&wd.ShortDesc, &wd.Typ, &wd.Wertungsfeld); err != nil {
			continue // Wertung gelöscht/unsichtbar -> überspringen
		}

		rows, err := pool.Query(ctx, `
			SELECT platz, nachname || ', ' || vorname, COALESCE(verein,''),
			       COALESCE(klasse,''), werte, summe, scheiben
			FROM ps_wertung_ergebnisse
			WHERE ps_wertung_id=$1
			ORDER BY platz`, wid)
		if err != nil {
			continue
		}
		for rows.Next() {
			var r ergebnisRow
			if err := rows.Scan(&r.Platz, &r.Name, &r.Verein, &r.Klasse, &r.Werte, &r.Summe, &r.Scheiben); err != nil {
				continue
			}
			wd.Ergebnisse = append(wd.Ergebnisse, r)
		}
		rows.Close()
		out = append(out, kioskSection{Kind: "wertung", Wertung: wd})
	}
	return cfg, out, nil
}

// handleKiosk rendert die automatisch weiterschaltende Vollbild-Anzeige
// (für einen fest montierten Bildschirm am Stand, siehe README) - eigene
// Route "/ps/{psid}/kiosk", getrennt von der browsbaren Ergebnis-Website
// (site.go), die unter "/ps/{psid}/" läuft.
func (h *siteHandler) handleKiosk(w http.ResponseWriter, r *http.Request) {
	cfg, sections, err := loadDisplay(r.Context(), h.pool, h.preisschiessenID, "anzeige_items")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderPage(w, cfg, sections)
}

// handleKiosk2: zweite, unabhängig bestückbare Kiosk-Anzeige ("/kiosk2") -
// z.B. für einen zweiten Bildschirm mit anderen Wertungen als /kiosk, siehe
// loadDisplay.
func (h *siteHandler) handleKiosk2(w http.ResponseWriter, r *http.Request) {
	cfg, sections, err := loadDisplay(r.Context(), h.pool, h.preisschiessenID, "anzeige_items_2")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderPage(w, cfg, sections)
}

// einzelergebnisseCells rendert die ersten n Werte (Werte ist immer nach
// bestem Wert zuerst sortiert) als <td>-Zellen - zur Kontrolle des Tiebreaks
// bei gleicher Summe (naechstbeste Scheibe/naechstbester Schuss entscheidet,
// siehe server/preisschiessen_wertungen.go computeMeisterPunkt). Formatierung
// über formatWert (site.go) statt fest codierter Nachkommastellen - eine
// Wertung mit wertungsfeld="ring" (Ganzring) zeigt so nur dann eine
// Nachkommastelle, wenn ein Scheiben-Faktor ≠ 1 (siehe ps_wertung_scheiben)
// tatsächlich einen nicht-ganzzahligen Wert ergibt.
func einzelergebnisseCells(werte []float64, n int, feld string) string {
	out := ""
	for i := 0; i < n; i++ {
		if i < len(werte) {
			out += fmt.Sprintf(`<td style="text-align:right">%s</td>`, formatWert(werte[i], feld))
		} else {
			out += `<td></td>`
		}
	}
	return out
}

// vereinValueHeader/vereinValue: Spaltenkopf bzw. formatierter Wert der
// Kennzahl-Spalte einer Vereins-Auswertung, analog site.go renderVereinListe.
var vereinValueHeader = map[string]string{"anzahl": "Anzahl", "prozent": "Prozent", "punkte": "Punkte"}

func vereinValue(typ string, r vereinErgebnisZeile) string {
	switch typ {
	case "prozent":
		if r.Prozent != nil {
			return formatWert(*r.Prozent, "") + " %"
		}
		return "–"
	case "punkte":
		return formatWert(r.Punkte, "")
	default:
		return strconv.Itoa(r.Anzahl)
	}
}

func renderPage(w http.ResponseWriter, cfg anzeigeConfig, sections []kioskSection) {
	listFontSize := cfg.ListFontSize
	if listFontSize <= 0 {
		listFontSize = 18
	}
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="de"><head><meta charset="UTF-8">
<title>Preisschiessen – Ergebnisse</title>
<style>
  :root{--bg:%s;--text:%s;--row-even:%s;--row-odd:%s;--line:#2b3542;--dim:#75879a}
  *{box-sizing:border-box;margin:0;padding:0}
  body{background:var(--bg);color:var(--text);font-family:system-ui,sans-serif;height:100vh;overflow:hidden}
  section{display:none;padding:20px 40px 40px;height:100vh}
  section.active{display:block}
  h1{font-size:%dpx;color:var(--text);margin-bottom:12px}
  table{width:100%%;border-collapse:collapse;font-size:%dpx}
  th{text-align:left;padding:10px 16px;color:var(--text);border-bottom:2px solid var(--line);
     text-transform:uppercase;letter-spacing:.5px;font-size:.55em}
  td{padding:8px 16px;border-bottom:1px solid var(--line)}
  td:first-child{font-weight:700;color:var(--text)}
  td:nth-child(2){font-weight:700}
  td.wertung{font-weight:700}
  tbody tr:nth-child(odd) td{background:var(--row-odd)}
  tbody tr:nth-child(even) td{background:var(--row-even)}
  .empty{color:var(--dim);font-style:italic;padding:20px}
</style></head><body>
`, cfg.Farben.Bg, cfg.Farben.Text, cfg.Farben.RowEven, cfg.Farben.RowOdd, cfg.TitleFontSize, listFontSize)

	if len(sections) == 0 {
		fmt.Fprint(w, `<section class="active"><div class="empty">Keine Wertungen zur Anzeige konfiguriert.</div></section>`)
	}
	anzahlEinzel := cfg.AnzahlEinzel
	if anzahlEinzel < 0 {
		anzahlEinzel = 0
	} else if anzahlEinzel > 10 {
		anzahlEinzel = 10
	}
	vereinTh, klasseTh, einzelTh, scheibeTh := "", "", "", ""
	if cfg.ShowVerein {
		vereinTh = "<th>Verein</th>"
	}
	if cfg.ShowKlasse {
		klasseTh = "<th>Klasse</th>"
	}
	if anzahlEinzel > 0 {
		einzelTh = fmt.Sprintf(`<th colspan="%d">Einzelergebnisse</th>`, anzahlEinzel)
	}
	if cfg.ShowScheibe {
		scheibeTh = "<th>Scheibe</th>"
	}
	for i, sec := range sections {
		active := ""
		if i == 0 {
			active = " active"
		}
		if sec.Kind == "verein" {
			valueHeader := vereinValueHeader[sec.Verein.Typ]
			fmt.Fprintf(w, `<section class="%s"><h1>%s</h1><table><thead><tr>
				<th>Platz</th><th>Verein</th><th class="num">Teilnehmer</th><th>%s</th>
				</tr></thead><tbody>`, "verein"+active, html.EscapeString(sec.Verein.Label), valueHeader)
			for _, r := range sec.Verein.Ergebnisse {
				gastLabel := ""
				if r.Gastgeber {
					gastLabel = ` <span style="color:var(--dim);font-style:italic">(Gastgeber)</span>`
				}
				fmt.Fprintf(w, `<tr><td>%d.</td><td>%s%s</td><td class="num">%d</td><td class="wertung">%s</td></tr>`,
					r.Platz, html.EscapeString(r.ClubName), gastLabel, r.Anzahl, vereinValue(sec.Verein.Typ, r))
			}
			fmt.Fprint(w, `</tbody></table></section>`)
			continue
		}
		wd := sec.Wertung
		fmt.Fprintf(w, `<section class="%s"><h1>%s</h1><table><thead><tr>
			<th>Platz</th><th>Name</th>%s%s%s<th>Wertung</th>%s
			</tr></thead><tbody>`, "wertung"+active, html.EscapeString(wd.ShortDesc), vereinTh, klasseTh, einzelTh, scheibeTh)
		for _, r := range wd.Ergebnisse {
			vereinTd, klasseTd, scheibeTd := "", "", ""
			if cfg.ShowVerein {
				vereinTd = fmt.Sprintf(`<td>%s</td>`, html.EscapeString(r.Verein))
			}
			if cfg.ShowKlasse {
				klasseTd = fmt.Sprintf(`<td>%s</td>`, html.EscapeString(r.Klasse))
			}
			if cfg.ShowScheibe {
				scheibeTd = fmt.Sprintf(`<td>%s</td>`, html.EscapeString(strings.Join(r.Scheiben, ", ")))
			}
			fmt.Fprintf(w, `<tr><td>%d.</td><td>%s</td>%s%s%s<td class="wertung">%s</td>%s</tr>`,
				r.Platz, html.EscapeString(r.Name), vereinTd, klasseTd,
				einzelergebnisseCells(r.Werte, anzahlEinzel, wd.Wertungsfeld), formatWert(r.Summe, wd.Wertungsfeld), scheibeTd)
		}
		fmt.Fprint(w, `</tbody></table></section>`)
	}

	reload := cfg.ReloadSeconds
	if reload <= 0 {
		reload = 5
	}
	fmt.Fprintf(w, `
<script>
const sections = document.querySelectorAll('section');
let idx = 0;
if (sections.length > 1) {
  setInterval(() => {
    sections[idx].classList.remove('active');
    idx = (idx + 1) %% sections.length;
    sections[idx].classList.add('active');
  }, %d * 1000);
}
// Volle Seite neu laden nach einem kompletten Zyklus, damit neue
// Berechnungsergebnisse (periodisch im Hintergrund erzeugt) einfliessen.
setTimeout(() => location.reload(), %d * 1000);
</script>
</body></html>`, reload, reload*max(len(sections), 1))
}
