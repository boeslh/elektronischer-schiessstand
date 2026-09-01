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

	"github.com/jackc/pgx/v5/pgxpool"
)

type ergebnisRow struct {
	Platz  int
	Name   string
	Verein string
	Klasse string
	Werte  []float64
	Summe  float64
}

type wertungDisplay struct {
	ShortDesc  string
	Typ        string
	Ergebnisse []ergebnisRow
}

type anzeigeConfig struct {
	ReloadSeconds int
	TitleFontSize int
	WertungIDs    []string
}

func loadDisplay(ctx context.Context, pool *pgxpool.Pool, preisschiessenID string) (anzeigeConfig, []wertungDisplay, error) {
	var cfg anzeigeConfig
	err := pool.QueryRow(ctx, `
		SELECT reload_seconds, title_font_size, wertung_ids::text[]
		FROM ps_anzeige_config WHERE preisschiessen_id=$1`, preisschiessenID,
	).Scan(&cfg.ReloadSeconds, &cfg.TitleFontSize, &cfg.WertungIDs)
	if err != nil {
		// Noch keine Konfiguration gespeichert -> sinnvolle Defaults, leere Anzeige.
		cfg = anzeigeConfig{ReloadSeconds: 5, TitleFontSize: 20}
		return cfg, nil, nil
	}

	var out []wertungDisplay
	for _, wid := range cfg.WertungIDs {
		var wd wertungDisplay
		if err := pool.QueryRow(ctx,
			`SELECT short_desc, typ FROM ps_wertungen WHERE id=$1 AND visible`, wid,
		).Scan(&wd.ShortDesc, &wd.Typ); err != nil {
			continue // Wertung gelöscht/unsichtbar -> überspringen
		}

		rows, err := pool.Query(ctx, `
			SELECT platz, nachname || ', ' || vorname, COALESCE(verein,''),
			       COALESCE(klasse,''), werte, summe
			FROM ps_wertung_ergebnisse
			WHERE ps_wertung_id=$1
			ORDER BY platz`, wid)
		if err != nil {
			continue
		}
		for rows.Next() {
			var r ergebnisRow
			if err := rows.Scan(&r.Platz, &r.Name, &r.Verein, &r.Klasse, &r.Werte, &r.Summe); err != nil {
				continue
			}
			wd.Ergebnisse = append(wd.Ergebnisse, r)
		}
		rows.Close()
		out = append(out, wd)
	}
	return cfg, out, nil
}

// handleKiosk rendert die automatisch weiterschaltende Vollbild-Anzeige
// (für einen fest montierten Bildschirm am Stand, siehe README) - eigene
// Route "/ps/{psid}/kiosk", getrennt von der browsbaren Ergebnis-Website
// (site.go), die unter "/ps/{psid}/" läuft.
func (h *siteHandler) handleKiosk(w http.ResponseWriter, r *http.Request) {
	cfg, wertungen, err := loadDisplay(r.Context(), h.pool, h.preisschiessenID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderPage(w, cfg, wertungen)
}

// besteFuenfCells rendert die ersten 5 Werte (Werte ist immer nach bestem
// Wert zuerst sortiert) als <td>-Zellen - zur Kontrolle des Tiebreaks bei
// gleicher Summe (naechstbeste Scheibe/naechstbester Schuss entscheidet,
// siehe server/preisschiessen_wertungen.go computeMeisterPunkt).
func besteFuenfCells(werte []float64) string {
	out := ""
	for i := 0; i < 5; i++ {
		if i < len(werte) {
			out += fmt.Sprintf(`<td style="text-align:right">%.1f</td>`, werte[i])
		} else {
			out += `<td></td>`
		}
	}
	return out
}

func renderPage(w http.ResponseWriter, cfg anzeigeConfig, wertungen []wertungDisplay) {
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="de"><head><meta charset="UTF-8">
<title>Preisschiessen – Ergebnisse</title>
<style>
  :root{--bg:#12161b;--panel:#1b222b;--line:#2b3542;--text:#dce4ec;--dim:#75879a;--acc:#4ab8a0}
  *{box-sizing:border-box;margin:0;padding:0}
  body{background:var(--bg);color:var(--text);font-family:system-ui,sans-serif;height:100vh;overflow:hidden}
  section{display:none;padding:40px;height:100vh}
  section.active{display:block}
  h1{font-size:%dpx;color:var(--acc);margin-bottom:24px}
  table{width:100%%;border-collapse:collapse;font-size:1.6vw}
  th{text-align:left;padding:10px 16px;color:var(--dim);border-bottom:2px solid var(--line);
     text-transform:uppercase;letter-spacing:.5px;font-size:.9vw}
  td{padding:8px 16px;border-bottom:1px solid var(--line)}
  td:first-child{font-weight:700;color:var(--acc)}
  .empty{color:var(--dim);font-style:italic;padding:20px}
</style></head><body>
`, cfg.TitleFontSize)

	if len(wertungen) == 0 {
		fmt.Fprint(w, `<section class="active"><div class="empty">Keine Wertungen zur Anzeige konfiguriert.</div></section>`)
	}
	for i, wd := range wertungen {
		active := ""
		if i == 0 {
			active = " active"
		}
		fmt.Fprintf(w, `<section class="%s"><h1>%s</h1><table><thead><tr>
			<th>Platz</th><th>Name</th><th>Verein</th><th>Klasse</th>
			<th colspan="5">Beste 5</th><th>Summe</th>
			</tr></thead><tbody>`, "wertung"+active, html.EscapeString(wd.ShortDesc))
		for _, r := range wd.Ergebnisse {
			fmt.Fprintf(w, `<tr><td>%d.</td><td>%s</td><td>%s</td><td>%s</td>%s<td>%.2f</td></tr>`,
				r.Platz, html.EscapeString(r.Name), html.EscapeString(r.Verein),
				html.EscapeString(r.Klasse), besteFuenfCells(r.Werte), r.Summe)
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
</body></html>`, reload, reload*max(len(wertungen), 1))
}
