// ============================================================================
// site.go – öffentliche Ergebnis-Website (Startseite, Auswertungslisten,
// Vereins-/Teilnehmer-Detail, Suche), im Aufbau/Stil an das bisherige gs26-
// Anzeigeportal angelehnt (Blau/Cyan-Farbschema, linke Listen-Navigation,
// "Preis"-Spalte je Platz, Teilnehmer-Suche mit Autovervollständigung,
// Kennzahlen-Kacheln auf der Startseite) - siehe gs26/frontend/*.php.
//
// Liest wie display.go NUR vorberechnete/billige Daten: Teilnehmer-Wertungen
// aus dem Cache ps_wertung_ergebnisse, Vereins-Auswertungen live (billig,
// siehe vereine.go), Gewinne aus ps_gewinne. Keine eigene Berechnung.
// ============================================================================
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var berlin *time.Location

func init() {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		loc = time.Local
	}
	berlin = loc
}

type siteHandler struct {
	pool               *pgxpool.Pool
	preisschiessenID   string
	preisschiessenName string
}

type sidebarItem struct {
	Key   string
	Label string
}

// base: Pfad-Präfix für alle internen Links/Requests innerhalb dieses
// Preisschießens (siehe registerSiteRoutes) - ein laufender Prozess bedient
// beliebig viele Preisschießen gleichzeitig, ausgewählt über {psid} im Pfad,
// nicht mehr über eine feste Konfiguration wie früher.
func (h *siteHandler) base() string { return "/ps/" + h.preisschiessenID }

// withPS löst {psid} aus dem Pfad zu einem siteHandler für genau dieses
// Preisschießen auf (Name wird für Titel/Kopfzeile einmal geladen) und
// delegiert an fn - so bleiben alle bestehenden Handler-Methoden unverändert,
// sie greifen weiterhin einfach auf h.preisschiessenID/h.preisschiessenName zu.
func withPS(pool *pgxpool.Pool, fn func(*siteHandler, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		psID := r.PathValue("psid")
		var name string
		if err := pool.QueryRow(r.Context(),
			`SELECT name FROM preisschiessen WHERE id=$1`, psID,
		).Scan(&name); err != nil {
			http.NotFound(w, r)
			return
		}
		fn(&siteHandler{pool: pool, preisschiessenID: psID, preisschiessenName: name}, w, r)
	}
}

func registerSiteRoutes(mux *http.ServeMux, pool *pgxpool.Pool) {
	mux.HandleFunc("GET /{$}", handlePicker(pool))
	mux.HandleFunc("GET /ps/{psid}/{$}", withPS(pool, (*siteHandler).handleHome))
	mux.HandleFunc("GET /ps/{psid}/liste/{key}", withPS(pool, (*siteHandler).handleListe))
	mux.HandleFunc("GET /ps/{psid}/verein/{id}", withPS(pool, (*siteHandler).handleVerein))
	mux.HandleFunc("GET /ps/{psid}/teilnehmer/{nr}", withPS(pool, (*siteHandler).handleTeilnehmer))
	mux.HandleFunc("GET /ps/{psid}/schussbild/{id}", withPS(pool, (*siteHandler).handleSchussbild))
	mux.HandleFunc("GET /ps/{psid}/statistik", withPS(pool, (*siteHandler).handleStatistik))
	mux.HandleFunc("GET /ps/{psid}/api/suche", withPS(pool, (*siteHandler).handleSuche))
	mux.HandleFunc("GET /ps/{psid}/kiosk", withPS(pool, (*siteHandler).handleKiosk))
}

// handlePicker: Startseite des Prozesses - listet alle Preisschießen auf
// (unabhängig von der Konfigurationsdatei, die nur noch DB-Zugang und
// Listen-Adresse enthält), Klick führt in den jeweiligen /ps/{id}/-Bereich.
func handlePicker(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := pool.Query(r.Context(), `
			SELECT id::text, name, active, COALESCE(starts_on::text,''), COALESCE(ends_on::text,''),
			       (SELECT COUNT(*) FROM ps_teilnehmer t WHERE t.preisschiessen_id = p.id)
			FROM preisschiessen p ORDER BY active DESC, starts_on DESC NULLS LAST, name`)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var b strings.Builder
		fmt.Fprint(&b, `<h2 class="page-title">Preisschießen</h2><table class="result">
			<thead><tr><th>Name</th><th>Zeitraum</th><th class="num">Teilnehmer</th><th>Status</th></tr></thead><tbody>`)
		any := false
		for rows.Next() {
			any = true
			var id, name, startsOn, endsOn string
			var active bool
			var teilnehmer int
			if err := rows.Scan(&id, &name, &active, &startsOn, &endsOn, &teilnehmer); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			zeitraum := "–"
			if startsOn != "" || endsOn != "" {
				zeitraum = startsOn + " – " + endsOn
			}
			status := "Inaktiv"
			if active {
				status = "Aktiv"
			}
			fmt.Fprintf(&b, `<tr><td><a href="/ps/%s/">%s</a></td><td>%s</td><td class="num">%d</td><td>%s</td></tr>`,
				id, html.EscapeString(name), html.EscapeString(zeitraum), teilnehmer, status)
		}
		fmt.Fprint(&b, `</tbody></table>`)
		if !any {
			fmt.Fprint(&b, `<p>Noch keine Preisschießen angelegt.</p>`)
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html><html lang="de"><head><meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>Preisschießen</title>
<style>%s</style></head><body>
<div class="header"><h1>Preisschießen</h1></div>
<div class="layout"><div class="content" style="flex:1">%s</div></div>
</body></html>`, siteCSS, b.String())
	}
}

// ----------------------------------------------------------------------------
// Layout
// ----------------------------------------------------------------------------

const siteCSS = `
*{box-sizing:border-box}
body{font-family:sans-serif;font-size:10pt;background-color:#80ffff;color:black;margin:0}
a{color:#004080}
.header{display:flex;align-items:center;justify-content:center;padding:14px 10px;background:#fff;border-bottom:2px solid #004080}
.header h1{font-size:1.8em;margin:0;color:#004080}
.layout{display:flex;align-items:flex-start;max-width:1400px;margin:0 auto}
.sidebar{width:260px;flex-shrink:0;padding:10px}
.content{flex:1;padding:16px 24px;min-width:0}
.updated{font-size:.82em;color:#333;padding:6px 8px}
table{width:100%;border-collapse:collapse}
table.liste th{padding:6px;border:1px solid black;background:#004080;color:#fff}
table.liste td{padding:6px;border-left:1px solid black;border-right:1px solid black;background:#80ffff}
table.liste tr:nth-child(even) td{background:#cbf5f8}
table.liste tr:last-child td{border-bottom:1px solid black}
table.liste tr.active td{font-weight:700;background:#fff59d}
table.result{overflow-x:auto;display:block}
table.result th{padding:6px 10px;border:1px solid black;background:#004080;color:#fff;white-space:nowrap}
table.result td{padding:6px 10px;border-left:1px solid black;border-right:1px solid black;white-space:nowrap}
table.result tr:nth-child(odd) td{background:#cbf5f8}
table.result tr:nth-child(even) td{background:#eafeff}
table.result tr:last-child td{border-bottom:1px solid black}
.num{text-align:right}
.page-title{text-align:center}
.stats{display:flex;flex-wrap:wrap;gap:16px;justify-content:center;margin:24px 0}
.stat-card{background:#f8f8f8;border:1px solid #ddd;border-radius:6px;padding:20px 28px;min-width:130px;text-align:center}
.stat-value{font-size:2em;font-weight:bold;color:#333;line-height:1.2}
.stat-label{font-size:.85em;color:#666;margin-top:4px}
.suche{max-width:480px;margin:28px auto 8px}
.suche h3{text-align:center;margin-bottom:12px}
.suche-row{display:flex;gap:8px;margin-bottom:10px}
.suche-row input{flex:1;padding:7px 10px;font-size:1em;border:1px solid #aaa;border-radius:4px}
.suche-row button{padding:7px 14px;font-size:1em;border:none;border-radius:4px;background:#33AA33;color:#fff;cursor:pointer}
.suche-row button:hover{background:#228822}
#ac-wrap{position:relative}
#ac-box{position:absolute;z-index:100;background:#fff;border:1px solid #aaa;border-radius:4px;width:100%;max-height:220px;overflow-y:auto;display:none}
#ac-box div{padding:7px 10px;cursor:pointer;font-size:.97em}
#ac-box div:hover{background:#d4f0d4}
@media (max-width:800px){.layout{flex-direction:column}.sidebar{width:100%}}
`

func (h *siteHandler) loadSidebar(ctx context.Context) ([]sidebarItem, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT id::text, short_desc FROM ps_wertungen
		WHERE preisschiessen_id=$1 AND visible ORDER BY sort_order, short_desc`, h.preisschiessenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []sidebarItem
	for rows.Next() {
		var id, label string
		if err := rows.Scan(&id, &label); err != nil {
			return nil, err
		}
		items = append(items, sidebarItem{Key: "wertung:" + id, Label: label})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, typ := range vereinTypOrder {
		items = append(items, sidebarItem{Key: "verein:" + typ, Label: vereinTypLabels[typ]})
	}
	items = append(items, sidebarItem{Key: "statistik", Label: "Statistik"})
	return items, nil
}

func (h *siteHandler) lastUpdated(ctx context.Context) string {
	var t *time.Time
	err := h.pool.QueryRow(ctx,
		`SELECT last_finished_at FROM ps_auswertung_status WHERE preisschiessen_id=$1`, h.preisschiessenID,
	).Scan(&t)
	if err != nil || t == nil {
		return "noch nicht berechnet"
	}
	return t.In(berlin).Format("02.01.2006 15:04:05")
}

func (h *siteHandler) renderLayout(w http.ResponseWriter, ctx context.Context, title, activeKey, body string) {
	sidebar, err := h.loadSidebar(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html><html lang="de"><head><meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s – %s</title><style>%s</style></head><body>
<div class="header"><a href="/" style="font-size:.8em;color:#004080;text-decoration:none;margin-right:14px">&#8592; Alle Preisschießen</a><h1>%s</h1></div>
<div class="layout">
<div class="sidebar">
<table class="liste"><thead><tr><th>Listen</th></tr></thead><tbody>
<tr%s><td><a href="%s/">Startseite</a></td></tr>`,
		html.EscapeString(title), html.EscapeString(h.preisschiessenName), siteCSS,
		html.EscapeString(h.preisschiessenName),
		activeRowAttr(activeKey == "home"), h.base())
	for _, it := range sidebar {
		href := h.base() + "/liste/" + url.PathEscape(it.Key)
		if it.Key == "statistik" {
			href = h.base() + "/statistik"
		}
		fmt.Fprintf(w, `<tr%s><td><a href="%s">%s</a></td></tr>`,
			activeRowAttr(activeKey == it.Key), href, html.EscapeString(it.Label))
	}
	fmt.Fprintf(w, `</tbody></table>
<div class="updated">Aktualisiert: %s</div>
</div>
<div class="content">%s</div>
</div>
</body></html>`, html.EscapeString(h.lastUpdated(ctx)), body)
}

func activeRowAttr(active bool) string {
	if active {
		return ` class="active"`
	}
	return ""
}

// ----------------------------------------------------------------------------
// Startseite
// ----------------------------------------------------------------------------

type statCard struct{ value, label string }

func (h *siteHandler) loadStats(ctx context.Context) []statCard {
	var mitglieder, vereine, teilnehmer, scheiben, schuss int
	h.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(c.member_count),0)::int FROM ps_verein_teilnahme vt
		JOIN clubs c ON c.id=vt.club_id WHERE vt.preisschiessen_id=$1`, h.preisschiessenID).Scan(&mitglieder)
	h.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM ps_verein_teilnahme WHERE preisschiessen_id=$1`, h.preisschiessenID).Scan(&vereine)
	h.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM ps_teilnehmer WHERE preisschiessen_id=$1`, h.preisschiessenID).Scan(&teilnehmer)
	h.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM ps_kauf_scheiben WHERE preisschiessen_id=$1`, h.preisschiessenID).Scan(&scheiben)
	h.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM shots s JOIN ps_kauf_scheiben ks ON ks.session_id=s.session_id
		WHERE ks.preisschiessen_id=$1 AND s.status<>'annulled'`, h.preisschiessenID).Scan(&schuss)
	return []statCard{
		{formatInt(mitglieder), "Mitglieder"},
		{formatInt(vereine), "Vereine"},
		{formatInt(teilnehmer), "Teilnehmer"},
		{formatInt(scheiben), "Scheiben"},
		{formatInt(schuss), "Schuss"},
	}
}

func (h *siteHandler) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var b strings.Builder
	fmt.Fprintf(&b, `<h2 class="page-title">Willkommen beim %s</h2>`, html.EscapeString(h.preisschiessenName))
	fmt.Fprint(&b, `<div class="stats">`)
	for _, s := range h.loadStats(ctx) {
		fmt.Fprintf(&b, `<div class="stat-card"><div class="stat-value">%s</div><div class="stat-label">%s</div></div>`,
			s.value, s.label)
	}
	fmt.Fprintf(&b, `</div>
<div class="suche">
  <h3>Teilnehmer suchen</h3>
  <div class="suche-row">
    <input type="number" id="tn-nr" placeholder="Teilnehmer-Nr." min="1">
    <button onclick="tnGoNr()">Suchen</button>
  </div>
  <div class="suche-row" id="ac-wrap">
    <input type="text" id="tn-name" placeholder="Name (mind. 3 Zeichen)" autocomplete="off">
    <div id="ac-box"></div>
  </div>
</div>
<script>
const BASE = '%s';
function tnGoNr(){
  var v = document.getElementById('tn-nr').value;
  if (v) location.href = BASE + '/teilnehmer/' + encodeURIComponent(v);
}
(function(){
  var inp = document.getElementById('tn-name'), box = document.getElementById('ac-box'), timer = null;
  inp.addEventListener('keydown', function(e){ if (e.key === 'Enter') doSearchNow(); });
  inp.addEventListener('input', function(){
    clearTimeout(timer);
    var q = inp.value.trim();
    if (q.length < 3) { box.style.display = 'none'; return; }
    timer = setTimeout(doSearchNow, 250);
  });
  document.addEventListener('click', function(e){ if (!e.target.closest('#ac-wrap')) box.style.display = 'none'; });
  function doSearchNow(){
    var q = inp.value.trim();
    if (q.length < 3) return;
    fetch(BASE + '/api/suche?q=' + encodeURIComponent(q)).then(function(r){ return r.json(); }).then(function(res){
      box.innerHTML = '';
      if (!res.length) { box.style.display = 'none'; return; }
      res.forEach(function(it){
        var d = document.createElement('div');
        d.textContent = it.nr + ' – ' + it.label;
        d.onclick = function(){ location.href = BASE + '/teilnehmer/' + it.nr; };
        box.appendChild(d);
      });
      box.style.display = 'block';
    });
  }
})();
</script>`, h.base())
	h.renderLayout(w, ctx, "Startseite", "home", b.String())
}

// ----------------------------------------------------------------------------
// Auswertungslisten (Wertung ODER Vereins-Auswertung)
// ----------------------------------------------------------------------------

func (h *siteHandler) handleListe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key := r.PathValue("key")
	kind, id, ok := strings.Cut(key, ":")
	if !ok {
		http.NotFound(w, r)
		return
	}
	var title, body string
	var err error
	switch kind {
	case "wertung":
		title, body, err = h.renderWertungListe(ctx, id)
	case "verein":
		title, body, err = h.renderVereinListe(ctx, id)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	h.renderLayout(w, ctx, title, key, body)
}

func (h *siteHandler) renderWertungListe(ctx context.Context, wertungID string) (string, string, error) {
	var shortDesc, wertungsfeld string
	var anzSumme int
	if err := h.pool.QueryRow(ctx, `
		SELECT short_desc, COALESCE(wertungsfeld,''), anz_summe FROM ps_wertungen
		WHERE id=$1 AND preisschiessen_id=$2`, wertungID, h.preisschiessenID,
	).Scan(&shortDesc, &wertungsfeld, &anzSumme); err != nil {
		return "", "", fmt.Errorf("Wertung nicht gefunden")
	}
	showSumme := anzSumme > 1

	gewinne, err := h.loadGewinne(ctx, `wertung_id=$1`, wertungID)
	if err != nil {
		return "", "", err
	}

	rows, err := h.pool.Query(ctx, `
		SELECT platz, start_nr, nachname||' '||vorname, werte, summe
		FROM ps_wertung_ergebnisse WHERE ps_wertung_id=$1 ORDER BY platz`, wertungID)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()

	var b strings.Builder
	fmt.Fprintf(&b, `<h2 class="page-title">%s</h2>`, html.EscapeString(shortDesc))
	fmt.Fprint(&b, `<table class="result"><thead><tr><th>Pl.</th><th>Teiln.</th><th>Name</th>`)
	if showSumme {
		fmt.Fprint(&b, `<th class="num">Summe</th>`)
	}
	// Kein Buchstabenpräfix: bei Ring-Wertungen ist eine Spalte eine ganze
	// Scheibe (v_series_results je Session), bei Teiler-Wertungen ein
	// einzelner Schuss (v_scoring_shots), bei Adler wechseln sich beide
	// Bedeutungen zeilenweise ab - ein einheitliches "S"/"T" wäre in keinem
	// der drei Fälle durchgehend richtig, siehe loadWertungRows.
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&b, `<th class="num">%d</th>`, i)
	}
	fmt.Fprint(&b, `<th>Preis</th></tr></thead><tbody>`)
	any := false
	for rows.Next() {
		any = true
		var platz, startNr int
		var name string
		var werte []float64
		var summe float64
		if err := rows.Scan(&platz, &startNr, &name, &werte, &summe); err != nil {
			return "", "", err
		}
		fmt.Fprintf(&b, `<tr><td>%d</td><td><a href="%s/teilnehmer/%d">%d</a></td><td>%s</td>`,
			platz, h.base(), startNr, startNr, html.EscapeString(name))
		if showSumme {
			fmt.Fprintf(&b, `<td class="num">%s</td>`, formatWert(summe, ""))
		}
		for i := 0; i < 10; i++ {
			val := ""
			if i < len(werte) {
				val = formatWert(werte[i], wertungsfeld)
			}
			fmt.Fprintf(&b, `<td class="num">%s</td>`, val)
		}
		fmt.Fprintf(&b, `<td>%s</td></tr>`, gewinne[platz])
	}
	fmt.Fprint(&b, `</tbody></table>`)
	if !any {
		fmt.Fprint(&b, `<p>Noch keine Ergebnisse berechnet.</p>`)
	}
	return shortDesc, b.String(), nil
}

func (h *siteHandler) renderVereinListe(ctx context.Context, typ string) (string, string, error) {
	label, ok := vereinTypLabels[typ]
	if !ok {
		return "", "", fmt.Errorf("unbekannte Vereins-Auswertung")
	}
	rows, err := computeVereinsAuswertung(ctx, h.pool, h.preisschiessenID, typ)
	if err != nil {
		return "", "", err
	}
	gewinne, err := h.loadGewinne(ctx, `verein_typ=$1`, typ)
	if err != nil {
		return "", "", err
	}

	valueHeader := map[string]string{"anzahl": "Anzahl", "prozent": "Prozent", "punkte": "Punkte"}[typ]
	var b strings.Builder
	fmt.Fprintf(&b, `<h2 class="page-title">%s</h2>`, html.EscapeString(label))
	if len(rows) == 0 {
		fmt.Fprint(&b, `<p>Noch keine Vereine für die Vereins-Auswertung konfiguriert.</p>`)
		return label, b.String(), nil
	}
	fmt.Fprintf(&b, `<table class="result"><thead><tr><th>Platz</th><th>Verein</th>
		<th class="num">Teilnehmer</th><th class="num">%s</th><th>Preis</th></tr></thead><tbody>`, valueHeader)
	for _, r := range rows {
		var val string
		switch typ {
		case "prozent":
			if r.Prozent != nil {
				val = formatWert(*r.Prozent, "") + " %"
			} else {
				val = "–"
			}
		case "punkte":
			val = formatWert(r.Punkte, "")
		default:
			val = strconv.Itoa(r.Anzahl)
		}
		gastLabel := ""
		if r.Gastgeber {
			gastLabel = ` <span style="color:#666;font-style:italic">(Gastgeber)</span>`
		}
		fmt.Fprintf(&b, `<tr><td>%d</td><td><a href="%s/verein/%s">%s</a>%s</td>
			<td class="num">%d</td><td class="num">%s</td><td>%s</td></tr>`,
			r.Platz, h.base(), r.ClubID, html.EscapeString(r.ClubName), gastLabel, r.Anzahl, val, gewinne[r.Platz])
	}
	fmt.Fprint(&b, `</tbody></table>`)
	return label, b.String(), nil
}

// loadGewinne liest die Platz->Preis-Zuordnung für EINE Auswertungsliste
// (whereCol ist "wertung_id=$1" oder "verein_typ=$1"; bei verein_typ wird
// zusätzlich preisschiessen_id gebraucht, siehe Aufrufer in renderVereinListe).
func (h *siteHandler) loadGewinne(ctx context.Context, whereCol, arg string) (map[int]string, error) {
	q := `SELECT platz, betrag, sachpreis FROM ps_gewinne WHERE ` + whereCol
	args := []any{arg}
	if whereCol == "verein_typ=$1" {
		q = `SELECT platz, betrag, sachpreis FROM ps_gewinne WHERE preisschiessen_id=$1 AND verein_typ=$2`
		args = []any{h.preisschiessenID, arg}
	}
	rows, err := h.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scanGewinne(rows)
}

func scanGewinne(rows pgx.Rows) (map[int]string, error) {
	defer rows.Close()
	out := map[int]string{}
	for rows.Next() {
		var platz int
		var betrag *float64
		var sachpreis *string
		if err := rows.Scan(&platz, &betrag, &sachpreis); err != nil {
			return nil, err
		}
		out[platz] = formatPreis(betrag, sachpreis)
	}
	return out, rows.Err()
}

func formatPreis(betrag *float64, sachpreis *string) string {
	var parts []string
	if betrag != nil {
		parts = append(parts, formatWert(*betrag, "")+"€")
	}
	if sachpreis != nil && *sachpreis != "" {
		parts = append(parts, html.EscapeString(*sachpreis))
	}
	return strings.Join(parts, " + ")
}

// ----------------------------------------------------------------------------
// Verein-Detail
// ----------------------------------------------------------------------------

func (h *siteHandler) handleVerein(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	var name string
	if err := h.pool.QueryRow(ctx, `SELECT name FROM clubs WHERE id=$1`, id).Scan(&name); err != nil {
		http.NotFound(w, r)
		return
	}
	rows, err := h.pool.Query(ctx, `
		SELECT t.teilnehmer_nr, sh.last_name||' '||sh.first_name, COALESCE(sc.name,''), t.created_at
		FROM ps_teilnehmer t JOIN shooters sh ON sh.id=t.shooter_id
		LEFT JOIN shooter_classes sc ON sc.id=t.class_id
		WHERE t.preisschiessen_id=$1 AND sh.club_id=$2
		ORDER BY t.teilnehmer_nr`, h.preisschiessenID, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var b strings.Builder
	fmt.Fprintf(&b, `<h2 class="page-title">%s</h2>`, html.EscapeString(name))
	fmt.Fprint(&b, `<table class="result"><thead><tr><th>Nr</th><th>Teilnehmer</th><th>Klasse</th><th>Anmeldung</th></tr></thead><tbody>`)
	any := false
	for rows.Next() {
		any = true
		var nr int
		var pname, klasse string
		var created time.Time
		if err := rows.Scan(&nr, &pname, &klasse, &created); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(&b, `<tr><td><a href="%s/teilnehmer/%d">%d</a></td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			h.base(), nr, nr, html.EscapeString(pname), html.EscapeString(klasse), created.In(berlin).Format("02.01.2006"))
	}
	fmt.Fprint(&b, `</tbody></table>`)
	if !any {
		fmt.Fprint(&b, `<p>Keine Teilnehmer dieses Vereins in diesem Preisschießen.</p>`)
	}
	h.renderLayout(w, ctx, name, "", b.String())
}

// ----------------------------------------------------------------------------
// Teilnehmer-Detail
// ----------------------------------------------------------------------------

func (h *siteHandler) handleTeilnehmer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nr, err := strconv.Atoi(r.PathValue("nr"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var teilnehmerID, name, verein, klasse, clubID string
	var created time.Time
	err = h.pool.QueryRow(ctx, `
		SELECT t.id, sh.last_name||' '||sh.first_name, COALESCE(c.name,''), COALESCE(sc.name,''),
		       COALESCE(sh.club_id::text,''), t.created_at
		FROM ps_teilnehmer t
		JOIN shooters sh ON sh.id=t.shooter_id
		LEFT JOIN clubs c ON c.id = sh.club_id
		LEFT JOIN shooter_classes sc ON sc.id = t.class_id
		WHERE t.preisschiessen_id=$1 AND t.teilnehmer_nr=$2`, h.preisschiessenID, nr,
	).Scan(&teilnehmerID, &name, &verein, &klasse, &clubID, &created)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	platzierungen, gesamtPreisgeld, err := h.loadPlatzierungen(ctx, teilnehmerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rows, err := h.pool.Query(ctx, `
		SELECT ks.id, sc.name, COALESCE(vr.total_rings,0), COALESCE(vr.total_decimal,0),
		       vr.best_center_distance, se.finished_at, se.status::text
		FROM ps_kauf_scheiben ks
		JOIN ps_kaeufe k ON k.id = ks.kauf_id
		JOIN ps_scheiben sc ON sc.id = ks.scheibe_id
		LEFT JOIN sessions se ON se.id = ks.session_id
		LEFT JOIN v_session_results vr ON vr.session_id = ks.session_id
		WHERE k.teilnehmer_id=$1 AND k.returned_at IS NULL
		ORDER BY ks.serial_no`, teilnehmerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var b strings.Builder
	fmt.Fprintf(&b, `<h2 class="page-title">Nr. %d – %s</h2>`, nr, html.EscapeString(name))
	fmt.Fprint(&b, `<table class="result" style="max-width:520px;margin:0 auto 20px"><tbody>`)
	fmt.Fprintf(&b, `<tr><td>Name:</td><td>%s</td></tr>`, html.EscapeString(name))
	if clubID != "" {
		fmt.Fprintf(&b, `<tr><td>Verein:</td><td><a href="%s/verein/%s">%s</a></td></tr>`, h.base(), clubID, html.EscapeString(verein))
	} else {
		fmt.Fprintf(&b, `<tr><td>Verein:</td><td>%s</td></tr>`, html.EscapeString(verein))
	}
	fmt.Fprintf(&b, `<tr><td>Klasse:</td><td>%s</td></tr>`, html.EscapeString(klasse))
	fmt.Fprintf(&b, `<tr><td>Anmeldetag:</td><td>%s</td></tr>`, created.In(berlin).Format("02.01.2006"))
	fmt.Fprintf(&b, `<tr><td>Preisgeld:</td><td><strong>%s</strong></td></tr>`, formatEuro(gesamtPreisgeld))
	fmt.Fprint(&b, `</tbody></table>`)

	fmt.Fprint(&b, `<p class="page-title" style="font-weight:600">Platzierungen</p>`)
	// Kein Buchstabenpräfix bei den Einzelwerten (siehe renderWertungListe) -
	// die Summe-Spalte bleibt strukturell immer da (mehrere Listen mit
	// unterschiedlichem anz_summe stehen hier untereinander), ihr Wert aber
	// nur befüllt, wenn die jeweilige Liste tatsächlich eine Summenwertung
	// ist (anz_summe > 1) - sonst wäre sie nur eine Dopplung des besten Werts.
	fmt.Fprint(&b, `<table class="result"><thead><tr><th>Pl.</th><th>Liste</th><th class="num">Summe</th>`)
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&b, `<th class="num">%d</th>`, i)
	}
	fmt.Fprint(&b, `<th>Preis</th></tr></thead><tbody>`)
	if len(platzierungen) == 0 {
		fmt.Fprint(&b, `<tr><td colspan="13">Noch in keiner Liste platziert.</td></tr>`)
	}
	for _, p := range platzierungen {
		summeCell := ""
		if p.anzSumme > 1 {
			summeCell = formatWert(p.summe, "")
		}
		fmt.Fprintf(&b, `<tr><td>%d</td><td><a href="%s/liste/wertung:%s">%s</a></td><td class="num">%s</td>`,
			p.platz, h.base(), p.wertungID, html.EscapeString(p.shortDesc), summeCell)
		for i := 0; i < 10; i++ {
			val := ""
			if i < len(p.werte) {
				val = formatWert(p.werte[i], p.wertungsfeld)
			}
			fmt.Fprintf(&b, `<td class="num">%s</td>`, val)
		}
		fmt.Fprintf(&b, `<td>%s</td></tr>`, p.preis)
	}
	fmt.Fprint(&b, `</tbody></table>`)

	fmt.Fprint(&b, `<p class="page-title" style="font-weight:600;margin-top:24px">Scheiben im Detail</p>`)
	fmt.Fprint(&b, `<table class="result"><thead><tr><th>Scheibe</th><th>Status</th><th class="num">Ring</th>
		<th class="num">Zehntel</th><th class="num">Bester Teiler</th><th>Zeitpunkt</th><th></th></tr></thead><tbody>`)
	any := false
	for rows.Next() {
		any = true
		var kaufScheibeID, scheibeName string
		var totalRings int
		var totalDecimal float64
		var bestCD *float64
		var finishedAt *time.Time
		var status *string
		if err := rows.Scan(&kaufScheibeID, &scheibeName, &totalRings, &totalDecimal, &bestCD, &finishedAt, &status); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		cd := "–"
		if bestCD != nil {
			cd = formatWert(*bestCD, "")
		}
		zeit := "–"
		if finishedAt != nil {
			zeit = finishedAt.In(berlin).Format("02.01.2006 15:04:05")
		}
		statusLabel := "gekauft"
		if status != nil {
			statusLabel = sessionStatusLabels[*status]
			if statusLabel == "" {
				statusLabel = *status
			}
		}
		fmt.Fprintf(&b, `<tr><td>%s</td><td>%s</td><td class="num">%d</td><td class="num">%s</td>
			<td class="num">%s</td><td>%s</td><td><a href="%s/schussbild/%s">Schussbild</a></td></tr>`,
			html.EscapeString(scheibeName), html.EscapeString(statusLabel), totalRings,
			formatWert(totalDecimal, ""), cd, zeit, h.base(), kaufScheibeID)
	}
	fmt.Fprint(&b, `</tbody></table>`)
	if !any {
		fmt.Fprint(&b, `<p>Noch keine Scheiben gekauft.</p>`)
	}
	h.renderLayout(w, ctx, fmt.Sprintf("Nr. %d", nr), "", b.String())
}

type platzierungRow struct {
	wertungID, shortDesc, wertungsfeld string
	anzSumme                           int
	platz                              int
	werte                              []float64
	summe                              float64
	preis                              string
}

// loadPlatzierungen liefert für einen Teilnehmer alle Listen, in denen er
// bereits platziert ist (aus dem Ergebnis-Cache ps_wertung_ergebnisse),
// inkl. des dort für seinen Platz konfigurierten Gewinns - sowie die Summe
// aller Geldbeträge (Sachpreise fließen nicht in die Summe ein, werden aber
// je Liste in der Preis-Spalte mit angezeigt).
func (h *siteHandler) loadPlatzierungen(ctx context.Context, teilnehmerID string) ([]platzierungRow, float64, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT w.id::text, w.short_desc, COALESCE(w.wertungsfeld,''), w.anz_summe, r.platz, r.werte, r.summe
		FROM ps_wertung_ergebnisse r JOIN ps_wertungen w ON w.id = r.ps_wertung_id
		WHERE r.teilnehmer_id=$1 ORDER BY w.sort_order, w.short_desc`, teilnehmerID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []platzierungRow
	var wertungIDs []string
	for rows.Next() {
		var p platzierungRow
		if err := rows.Scan(&p.wertungID, &p.shortDesc, &p.wertungsfeld, &p.anzSumme, &p.platz, &p.werte, &p.summe); err != nil {
			return nil, 0, err
		}
		out = append(out, p)
		wertungIDs = append(wertungIDs, p.wertungID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(out) == 0 {
		return out, 0, nil
	}

	type gewinnKey struct {
		wertungID string
		platz     int
	}
	gewinne := map[gewinnKey]string{}
	betraege := map[gewinnKey]float64{}
	gRows, err := h.pool.Query(ctx, `
		SELECT wertung_id::text, platz, betrag, sachpreis FROM ps_gewinne
		WHERE wertung_id = ANY($1::uuid[])`, wertungIDs)
	if err != nil {
		return nil, 0, err
	}
	for gRows.Next() {
		var wid string
		var platz int
		var betrag *float64
		var sachpreis *string
		if err := gRows.Scan(&wid, &platz, &betrag, &sachpreis); err != nil {
			gRows.Close()
			return nil, 0, err
		}
		k := gewinnKey{wid, platz}
		gewinne[k] = formatPreis(betrag, sachpreis)
		if betrag != nil {
			betraege[k] = *betrag
		}
	}
	if err := gRows.Err(); err != nil {
		return nil, 0, err
	}
	gRows.Close()

	var gesamt float64
	for i := range out {
		k := gewinnKey{out[i].wertungID, out[i].platz}
		out[i].preis = gewinne[k]
		gesamt += betraege[k]
	}
	return out, gesamt, nil
}

// sessionStatusLabels übersetzt session_status (siehe migrations/001_schema.sql)
// für die Anzeige.
var sessionStatusLabels = map[string]string{
	"assigned": "zugewiesen",
	"sighting": "Probe",
	"match":    "Wertung läuft",
	"paused":   "pausiert",
	"finished": "beendet",
	"aborted":  "abgebrochen",
}

func formatEuro(v float64) string {
	if v == 0 {
		return "–"
	}
	return formatWert(v, "") + " €"
}

// ----------------------------------------------------------------------------
// Suche (Autovervollständigung)
// ----------------------------------------------------------------------------

func (h *siteHandler) handleSuche(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	w.Header().Set("Content-Type", "application/json")
	if len(q) < 2 {
		w.Write([]byte("[]"))
		return
	}
	rows, err := h.pool.Query(r.Context(), `
		SELECT t.teilnehmer_nr, sh.last_name||', '||sh.first_name
		FROM ps_teilnehmer t JOIN shooters sh ON sh.id=t.shooter_id
		WHERE t.preisschiessen_id=$1 AND (sh.last_name ILIKE '%'||$2||'%' OR sh.first_name ILIKE '%'||$2||'%')
		ORDER BY sh.last_name LIMIT 20`, h.preisschiessenID, q)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type item struct {
		Nr    int    `json:"nr"`
		Label string `json:"label"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.Nr, &it.Label); err != nil {
			continue
		}
		out = append(out, it)
	}
	json.NewEncoder(w).Encode(out)
}

// ----------------------------------------------------------------------------
// Formatierung
// ----------------------------------------------------------------------------

// formatWert: ganze Zahlen ohne Nachkommastelle, sonst eine Nachkommastelle
// (Punkt statt Komma, wie im gs26-Vorbild). feld="teiler" blendet den
// Auffüllwert (99999, siehe server/preisschiessen_wertungen.go
// computeMeisterPunkt) als "–" statt als Zahl aus.
func formatWert(v float64, feld string) string {
	if feld == "teiler" && v >= 99999 {
		return "–"
	}
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

func formatInt(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var out []byte
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, '.')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}
