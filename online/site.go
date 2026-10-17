// ============================================================================
// site.go – Routen, Seitenrahmen und oeffentliche Seiten (Vereine,
// Preisschiessen). Rundenwettkaempfe: rwk.go.
// ============================================================================
package main

import (
	"encoding/json"
	"fmt"
	"html"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

var berlin = func() *time.Location {
	l, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		return time.UTC
	}
	return l
}()

func urlQueryEscape(s string) string { return url.QueryEscape(s) }

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.handleIndex)
	mux.HandleFunc("GET /v/{slug}/{$}", a.handleVerein)
	mux.HandleFunc("GET /v/{slug}/ps/{id}/{$}", a.handlePSHome)
	mux.HandleFunc("GET /v/{slug}/ps/{id}/liste/{key}", a.handlePSListe)
	mux.HandleFunc("GET /v/{slug}/ps/{id}/teilnehmer/{nr}", a.handlePSTeilnehmer)
	mux.HandleFunc("GET /v/{slug}/rwk/{id}/{$}", a.handleRWK)
	mux.HandleFunc("GET /v/{slug}/rwk/{id}/daten", a.handleRWKData)
	mux.HandleFunc("GET /v/{slug}/live", a.handleLive)
	mux.HandleFunc("GET /v/{slug}/live/daten", a.handleLiveData)

	mux.HandleFunc("GET /admin/{$}", a.handleAdmin)
	mux.HandleFunc("GET /admin/login", a.handleLoginPage)
	mux.HandleFunc("POST /admin/login", a.handleLoginPage)
	mux.HandleFunc("POST /admin/logout", a.handleLogout)
	mux.HandleFunc("POST /admin/aktion/{action}", a.handleAdminAction)

	mux.HandleFunc("GET /api/v1/ping", a.handlePing)
	mux.HandleFunc("PUT /api/v1/publish/{kind}/{source}", a.handlePublish)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	h := securityHeaders(mux)
	base := a.cfg.BasePath
	if base == "" {
		return h
	}
	// Unterverzeichnis: nur Anfragen unter base annehmen, Praefix abtrennen
	strip := http.StripPrefix(base, h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == base:
			http.Redirect(w, r, base+"/", http.StatusMovedPermanently)
		case strings.HasPrefix(r.URL.Path, base+"/"):
			strip.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// url: interne Adresse mit Unterverzeichnis (fuer Weiterleitungen).
func (a *App) url(path string) string { return a.cfg.BasePath + path }

// withBase: interne Verweise (href="/..." und action="/...") im fertigen
// HTML um das Unterverzeichnis ergaenzen - alle Seiten erzeugen ihre Links
// absolut ab "/", externe Links beginnen nie mit "/".
func (a *App) withBase(s string) string {
	if a.cfg.BasePath == "" {
		return s
	}
	s = strings.ReplaceAll(s, `href="/`, `href="`+a.cfg.BasePath+`/`)
	return strings.ReplaceAll(s, `action="/`, `action="`+a.cfg.BasePath+`/`)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Strict-Transport-Security", "max-age=31536000")
		frame := "*" // oeffentliche Anzeigen duerfen z. B. in eine Vereinsseite eingebunden werden
		if strings.HasPrefix(r.URL.Path, "/admin") {
			frame = "'none'"
			h.Set("Cache-Control", "no-store")
		}
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; "+
			"style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors "+frame)
		next.ServeHTTP(w, r)
	})
}

const siteCSS = `
:root{--bg:#f4f6f8;--card:#fff;--line:#d6dde4;--text:#1d2733;--dim:#66737f;--acc:#1f6fb2;--head:#004080;--ok:#2e8b57;--err:#c0392b}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--text);font-family:system-ui,-apple-system,"Segoe UI",sans-serif;font-size:15px}
header.top{background:var(--head);color:#fff;padding:12px 16px;display:flex;gap:16px;align-items:baseline;flex-wrap:wrap}
header.top a{color:#fff;text-decoration:none}
header.top h1{font-size:1.25em;margin:0}
header.top .crumb{opacity:.8;font-size:.9em}
main{max-width:1200px;margin:0 auto;padding:16px}
a{color:var(--acc)}
.card{background:var(--card);border:1px solid var(--line);border-radius:8px;padding:14px 16px;margin-bottom:16px;overflow-x:auto}
.card.narrow{max-width:460px}
.card h2{margin:0 0 10px;font-size:1.1em}
table{border-collapse:collapse;width:100%}
th,td{padding:6px 8px;border-bottom:1px solid var(--line);text-align:left;white-space:nowrap}
th{background:#eef2f6;font-size:.85em;color:var(--dim)}
td.num,th.num{text-align:right}
tr.hl td{background:#fff6d6}
label{display:block;margin:8px 0;font-size:.9em;color:var(--dim)}
input,select{display:block;width:100%;padding:7px 9px;border:1px solid var(--line);border-radius:5px;font-size:.95em;margin-top:3px}
.row{display:flex;gap:8px;flex-wrap:wrap;align-items:center;margin-top:10px}
.row input,.row select{width:auto;flex:1;min-width:150px;margin:0}
button{background:var(--acc);color:#fff;border:none;border-radius:5px;padding:7px 14px;font-size:.92em;cursor:pointer}
button.sec{background:#e1e7ed;color:var(--text)}
button.danger{background:#fbe3e0;color:var(--err)}
form{margin:0}
.ok{background:#e6f4ec;color:var(--ok);padding:8px 12px;border-radius:5px}
.err{background:#fbe3e0;color:var(--err);padding:8px 12px;border-radius:5px}
.hint{color:var(--dim);font-size:.88em}
code.secret{display:block;padding:10px;background:#1d2733;color:#ffe08a;border-radius:5px;word-break:break-all;white-space:normal;font-size:1em}
.layout{display:flex;gap:16px;align-items:flex-start}
.side{width:240px;flex-shrink:0}
.side a{display:block;padding:6px 10px;border-radius:5px;text-decoration:none;color:var(--text)}
.side a.active{background:var(--acc);color:#fff}
.content{flex:1;min-width:0}
.stats{display:flex;flex-wrap:wrap;gap:12px}
.stat{background:var(--card);border:1px solid var(--line);border-radius:8px;padding:12px 18px;min-width:120px;text-align:center}
.stat b{display:block;font-size:1.7em}
.badge{display:inline-block;padding:1px 8px;border-radius:10px;font-size:.8em;background:#e1e7ed}
.badge.live{background:#e6f4ec;color:var(--ok)}
footer{color:var(--dim);font-size:.8em;text-align:center;padding:20px}
@media(max-width:800px){.layout{flex-direction:column}.side{width:100%}}
`

// page: Seitenrahmen; crumb = Navigationszeile im Kopf (bereits HTML).
func (a *App) page(w http.ResponseWriter, title, crumb, body string, admin bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	home := `<a href="/">` + html.EscapeString(a.cfg.SiteTitle) + `</a>`
	if admin {
		home += ` <span class="crumb">· <a href="/admin/">Verwaltung</a></span>`
	}
	fmt.Fprint(w, a.withBase(fmt.Sprintf(`<!DOCTYPE html><html lang="de"><head><meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>%s</title><style>%s</style></head><body>
<header class="top"><h1>%s</h1><span class="crumb">%s</span></header><main>%s</main>
<footer>%s</footer></body></html>`, html.EscapeString(title), siteCSS, home, crumb, body,
		html.EscapeString(a.cfg.SiteTitle))))
}

// ── Startseite und Vereinsseite ──────────────────────────────────────────

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	rows, err := a.pool.Query(r.Context(), `
		SELECT v.name, v.slug, count(p.id), max(p.updated_at)
		FROM vereine v JOIN publications p ON p.verein_id = v.id
		GROUP BY v.id ORDER BY v.name`)
	if err != nil {
		http.Error(w, "Fehler", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var b strings.Builder
	b.WriteString(`<div class="card"><h2>Vereine</h2><table><tr><th>Verein</th><th class="num">Anzeigen</th><th>Zuletzt aktualisiert</th></tr>`)
	n := 0
	for rows.Next() {
		var name, slug string
		var cnt int
		var upd time.Time
		rows.Scan(&name, &slug, &cnt, &upd)
		fmt.Fprintf(&b, `<tr><td><a href="/v/%s/">%s</a></td><td class="num">%d</td><td>%s</td></tr>`,
			slug, html.EscapeString(name), cnt, fmtTime(&upd))
		n++
	}
	b.WriteString(`</table>`)
	if n == 0 {
		b.WriteString(`<p class="hint">Noch keine Veröffentlichungen.</p>`)
	}
	b.WriteString(`</div>`)
	a.page(w, a.cfg.SiteTitle, "", b.String(), false)
}

type vereinInfo struct{ ID, Name, Slug string }

func (a *App) loadVerein(r *http.Request) (vereinInfo, bool) {
	var v vereinInfo
	err := a.pool.QueryRow(r.Context(), `SELECT id::text, name, slug FROM vereine WHERE slug=$1`, r.PathValue("slug")).
		Scan(&v.ID, &v.Name, &v.Slug)
	return v, err == nil
}

func (a *App) vereinCrumb(v vereinInfo) string {
	return `· <a href="/v/` + v.Slug + `/">` + html.EscapeString(v.Name) + `</a>`
}

func (a *App) handleVerein(w http.ResponseWriter, r *http.Request) {
	v, ok := a.loadVerein(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	rows, err := a.pool.Query(r.Context(), `
		SELECT id::text, kind, title, COALESCE(to_char(starts_on,'DD.MM.YYYY'),''), status, updated_at
		FROM publications WHERE verein_id=$1::uuid
		ORDER BY starts_on DESC NULLS LAST, updated_at DESC`, v.ID)
	if err != nil {
		http.Error(w, "Fehler", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var ps, rwk strings.Builder
	live := 0
	for rows.Next() {
		var id, kind, title, datum, status string
		var upd time.Time
		rows.Scan(&id, &kind, &title, &datum, &status, &upd)
		badge := `<span class="badge">` + html.EscapeString(statusLabel(status)) + `</span>`
		if status == "running" || status == "laufend" {
			badge = `<span class="badge live">` + html.EscapeString(statusLabel(status)) + `</span>`
		}
		line := fmt.Sprintf(`<tr><td><a href="/v/%s/%s/%s/">%s</a></td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			v.Slug, map[string]string{"preisschiessen": "ps", "rwk": "rwk"}[kind], id, html.EscapeString(title), datum, badge, fmtTime(&upd))
		if kind == "rwk" {
			rwk.WriteString(line)
			if status == "running" && time.Since(upd) < 10*time.Minute {
				live++
			}
		} else {
			ps.WriteString(line)
		}
	}
	var b strings.Builder
	head := `<table><tr><th>Name</th><th>Datum</th><th>Status</th><th>Aktualisiert</th></tr>`
	if live > 0 {
		fmt.Fprintf(&b, `<div class="card"><a href="/v/%s/live"><b>▶ Live-Anzeige</b></a> – alle %d laufenden Rundenwettkämpfe im Wechsel</div>`, v.Slug, live)
	}
	if rwk.Len() > 0 {
		b.WriteString(`<div class="card"><h2>Rundenwettkämpfe</h2>` + head + rwk.String() + `</table></div>`)
	}
	if ps.Len() > 0 {
		b.WriteString(`<div class="card"><h2>Preisschießen</h2>` + head + ps.String() + `</table></div>`)
	}
	if b.Len() == 0 {
		b.WriteString(`<div class="card"><p class="hint">Noch keine Veröffentlichungen.</p></div>`)
	}
	a.page(w, v.Name, a.vereinCrumb(v), b.String(), false)
}

// ── Preisschiessen ───────────────────────────────────────────────────────

func (a *App) loadPS(w http.ResponseWriter, r *http.Request) (vereinInfo, string, *PSSnapshot, bool) {
	v, ok := a.loadVerein(r)
	if !ok {
		http.NotFound(w, r)
		return v, "", nil, false
	}
	var data []byte
	id := r.PathValue("id")
	if err := a.pool.QueryRow(r.Context(), `SELECT data FROM publications WHERE id=$1::uuid AND verein_id=$2::uuid AND kind='preisschiessen'`,
		id, v.ID).Scan(&data); err != nil {
		http.NotFound(w, r)
		return v, "", nil, false
	}
	var s PSSnapshot
	if json.Unmarshal(data, &s) != nil {
		http.Error(w, "Daten fehlerhaft", http.StatusInternalServerError)
		return v, "", nil, false
	}
	return v, id, &s, true
}

func psBase(v vereinInfo, id string) string { return "/v/" + v.Slug + "/ps/" + id }

func (a *App) psLayout(w http.ResponseWriter, v vereinInfo, id string, s *PSSnapshot, active, title, body string) {
	var b strings.Builder
	base := psBase(v, id)
	b.WriteString(`<div class="layout"><nav class="side card">`)
	cls := func(k string) string {
		if k == active {
			return ` class="active"`
		}
		return ""
	}
	fmt.Fprintf(&b, `<a href="%s/"%s>Startseite</a>`, base, cls("home"))
	for _, l := range s.Wertungen {
		fmt.Fprintf(&b, `<a href="%s/liste/%s"%s>%s</a>`, base, url.PathEscape("w-"+l.Key), cls("w-"+l.Key), html.EscapeString(l.Name))
	}
	for _, l := range s.Vereine {
		fmt.Fprintf(&b, `<a href="%s/liste/%s"%s>%s</a>`, base, url.PathEscape("v-"+l.Typ), cls("v-"+l.Typ), html.EscapeString(l.Name))
	}
	fmt.Fprintf(&b, `<p class="hint">Stand: %s</p></nav><div class="content">%s</div></div>`, html.EscapeString(s.Berechnet), body)
	crumb := a.vereinCrumb(v) + ` · <a href="` + base + `/">` + html.EscapeString(s.Name) + `</a>`
	a.page(w, title+" – "+s.Name, crumb, b.String(), false)
}

func fmtNum(v float64, feld string) string {
	if feld == "teiler" && v >= 99999 {
		return "–"
	}
	if v == math.Trunc(v) {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strings.Replace(strconv.FormatFloat(v, 'f', 1, 64), ".", ",", 1)
}

func fmtDate(iso string) string {
	if t, err := time.Parse("2006-01-02", iso); err == nil {
		return t.Format("02.01.2006")
	}
	return iso
}

func (a *App) handlePSHome(w http.ResponseWriter, r *http.Request) {
	v, id, s, ok := a.loadPS(w, r)
	if !ok {
		return
	}
	var b strings.Builder
	datum := fmtDate(s.StartsOn)
	if s.EndsOn != "" && s.EndsOn != s.StartsOn {
		datum += " – " + fmtDate(s.EndsOn)
	}
	fmt.Fprintf(&b, `<div class="card"><h2>%s</h2><p>%s%s</p><div class="stats">`, html.EscapeString(s.Name), html.EscapeString(datum),
		map[bool]string{true: ` · <span class="badge">beendet</span>`, false: ` · <span class="badge live">läuft</span>`}[s.Finished])
	for _, st := range []struct {
		v int
		l string
	}{{s.Stats.Vereine, "Vereine"}, {s.Stats.Teilnehmer, "Teilnehmer"}, {s.Stats.Scheiben, "Scheiben"}, {s.Stats.Schuss, "Schuss"}} {
		fmt.Fprintf(&b, `<div class="stat"><b>%d</b>%s</div>`, st.v, st.l)
	}
	b.WriteString(`</div></div>`)
	// Teilnehmersuche (im Browser, aus den Listen dieser Seite)
	seen := map[int]string{}
	for _, l := range s.Wertungen {
		for _, z := range l.Zeilen {
			if _, ok := seen[z.Nr]; !ok {
				seen[z.Nr] = z.Name
			}
		}
	}
	type tn struct {
		Nr   int    `json:"nr"`
		Name string `json:"name"`
	}
	var list []tn
	for nr, name := range seen {
		list = append(list, tn{nr, name})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	js, _ := json.Marshal(list)
	fmt.Fprintf(&b, `<div class="card narrow"><h2>Teilnehmer suchen</h2>
<input id="q" placeholder="Name oder Startnummer" autocomplete="off"><div id="hits"></div></div>
<script>
const TN = %s, BASE = %q;
const q = document.getElementById('q'), hits = document.getElementById('hits');
q.addEventListener('input', () => {
  const s = q.value.trim().toLowerCase();
  hits.innerHTML = '';
  if (s.length < 2 && !/^\d+$/.test(s)) return;
  TN.filter(t => t.name.toLowerCase().includes(s) || String(t.nr) === s).slice(0, 15).forEach(t => {
    const a = document.createElement('a'); a.href = BASE + '/teilnehmer/' + t.nr;
    a.textContent = t.nr + ' – ' + t.name; a.style.display = 'block'; a.style.padding = '4px 0';
    hits.appendChild(a);
  });
});
</script>`, js, a.url(psBase(v, id)))
	a.psLayout(w, v, id, s, "home", "Startseite", b.String())
}

func (a *App) handlePSListe(w http.ResponseWriter, r *http.Request) {
	v, id, s, ok := a.loadPS(w, r)
	if !ok {
		return
	}
	key := r.PathValue("key")
	var b strings.Builder
	title := ""
	switch {
	case strings.HasPrefix(key, "w-"):
		var l *PSListe
		for i := range s.Wertungen {
			if "w-"+s.Wertungen[i].Key == key {
				l = &s.Wertungen[i]
			}
		}
		if l == nil {
			http.NotFound(w, r)
			return
		}
		title = l.Name
		fmt.Fprintf(&b, `<div class="card"><h2>%s</h2>`, html.EscapeString(l.Name))
		if l.Beschreibung != "" {
			fmt.Fprintf(&b, `<p class="hint">%s</p>`, html.EscapeString(l.Beschreibung))
		}
		cols := 0
		for _, z := range l.Zeilen {
			if len(z.Werte) > cols {
				cols = len(z.Werte)
			}
		}
		if cols > 10 {
			cols = 10
		}
		b.WriteString(`<table><tr><th class="num">Pl.</th><th class="num">Nr.</th><th>Name</th><th>Verein</th>`)
		if l.AnzSumme > 1 {
			b.WriteString(`<th class="num">Summe</th>`)
		}
		for i := 1; i <= cols; i++ {
			fmt.Fprintf(&b, `<th class="num">%d</th>`, i)
		}
		b.WriteString(`<th>Preis</th></tr>`)
		for _, z := range l.Zeilen {
			fmt.Fprintf(&b, `<tr><td class="num">%d</td><td class="num"><a href="%s/teilnehmer/%d">%d</a></td><td>%s</td><td>%s</td>`,
				z.Platz, psBase(v, id), z.Nr, z.Nr, html.EscapeString(z.Name), html.EscapeString(z.Verein))
			if l.AnzSumme > 1 {
				fmt.Fprintf(&b, `<td class="num"><b>%s</b></td>`, fmtNum(z.Summe, ""))
			}
			for i := 0; i < cols; i++ {
				val := ""
				if i < len(z.Werte) {
					val = fmtNum(z.Werte[i], l.Feld)
				}
				fmt.Fprintf(&b, `<td class="num">%s</td>`, val)
			}
			fmt.Fprintf(&b, `<td>%s</td></tr>`, html.EscapeString(z.Preis))
		}
		b.WriteString(`</table>`)
		if len(l.Zeilen) == 0 {
			b.WriteString(`<p class="hint">Noch keine Ergebnisse.</p>`)
		}
		b.WriteString(`</div>`)
	case strings.HasPrefix(key, "v-"):
		var l *PSVereinLst
		for i := range s.Vereine {
			if "v-"+s.Vereine[i].Typ == key {
				l = &s.Vereine[i]
			}
		}
		if l == nil {
			http.NotFound(w, r)
			return
		}
		title = l.Name
		fmt.Fprintf(&b, `<div class="card"><h2>%s</h2><table><tr><th class="num">Platz</th><th>Verein</th><th class="num">Teilnehmer</th><th class="num">%s</th><th>Preis</th></tr>`,
			html.EscapeString(l.Name), html.EscapeString(l.WertTitel))
		for _, z := range l.Zeilen {
			g := ""
			if z.Gastgeber {
				g = ` <span class="hint">(Gastgeber)</span>`
			}
			fmt.Fprintf(&b, `<tr><td class="num">%d</td><td>%s%s</td><td class="num">%d</td><td class="num">%s</td><td>%s</td></tr>`,
				z.Platz, html.EscapeString(z.Verein), g, z.Teilnehmer, html.EscapeString(z.Wert), html.EscapeString(z.Preis))
		}
		b.WriteString(`</table></div>`)
	default:
		http.NotFound(w, r)
		return
	}
	a.psLayout(w, v, id, s, key, title, b.String())
}

func (a *App) handlePSTeilnehmer(w http.ResponseWriter, r *http.Request) {
	v, id, s, ok := a.loadPS(w, r)
	if !ok {
		return
	}
	nr, err := strconv.Atoi(r.PathValue("nr"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var b strings.Builder
	name, verein := "", ""
	var rows strings.Builder
	for _, l := range s.Wertungen {
		for _, z := range l.Zeilen {
			if z.Nr != nr {
				continue
			}
			name, verein = z.Name, z.Verein
			fmt.Fprintf(&rows, `<tr><td><a href="%s/liste/%s">%s</a></td><td class="num">%d</td><td class="num">%s</td><td>%s</td></tr>`,
				psBase(v, id), url.PathEscape("w-"+l.Key), html.EscapeString(l.Name), z.Platz,
				fmtNum(z.Summe, l.Feld), html.EscapeString(z.Preis))
		}
	}
	if name == "" {
		http.NotFound(w, r)
		return
	}
	fmt.Fprintf(&b, `<div class="card"><h2>%d – %s</h2><p>%s</p><table><tr><th>Liste</th><th class="num">Platz</th><th class="num">Ergebnis</th><th>Preis</th></tr>%s</table></div>`,
		nr, html.EscapeString(name), html.EscapeString(verein), rows.String())
	a.psLayout(w, v, id, s, "", name, b.String())
}
