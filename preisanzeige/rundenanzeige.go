// ============================================================================
// rundenanzeige.go – Scoreboard-Anzeige eines Rundenwettkampfs
// (type='runde' in anzeige_config, params.event_id). Datenabfrage und
// Mannschafts-/Wertungslogik sind bewusst 1:1 aus server/store.go
// ListRundenwettkampfResults() + server/pdf_runde.go computeRundenTeams()
// portiert (preisanzeige kann server/ nicht importieren, siehe Dateikopf
// anzeige.go) - dieselbe Berechnung wie die Server-Auswertung und das PDF.
// ============================================================================
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type rundeParams struct {
	EventID string `json:"event_id"`
	// Ansicht: "liste" (Default: Schuetzen mit Ergebnis) oder "scheiben"
	// (je Mannschaft bis zu 5 Trefferbilder wie bei RWK-Dynamisch)
	Ansicht string `json:"ansicht"`
}

type rundeEntry struct {
	SessionID           string // beste Session des Starters ("" = noch nicht angetreten)
	LastName, FirstName string
	TeamID              string
	Role                string // S | E | AK
	DecimalScoring      bool
	ShotCount           int
	TotalRings          int
	TotalDecimal        float64
	InnerTens           int
	PairNo              int // Paarungswettkampf, 0 = keine Paarung
	StarterID           string
	Stechen             []rundeStechen // Stechschuesse (rwk_paarung.go)
}

func (e rundeEntry) value(isDecimal bool) float64 {
	if isDecimal {
		return e.TotalDecimal
	}
	return float64(e.TotalRings)
}

func renderRundenanzeige(w http.ResponseWriter, r *http.Request, ctx context.Context, pool *pgxpool.Pool, slot anzeigeSlot) {
	var p rundeParams
	_ = json.Unmarshal(slot.Params, &p)

	var compName string
	if err := pool.QueryRow(ctx, `SELECT name FROM events WHERE id=$1::uuid AND type='runde'`, p.EventID).Scan(&compName); err != nil {
		fmt.Fprint(w, rundeErrorPage("Kein Rundenwettkampf konfiguriert oder nicht gefunden."))
		return
	}

	type teamMeta struct{ ID, Name string }
	var heim, gast teamMeta
	rows, err := pool.Query(ctx, `
		SELECT COALESCE(t.id::text,''), COALESCE(t.name,''), cp.sort_order
		FROM competition_participants cp
		LEFT JOIN teams t ON t.id = cp.team_id
		WHERE cp.event_id=$1::uuid ORDER BY cp.sort_order LIMIT 2`, p.EventID)
	if err == nil {
		for rows.Next() {
			var m teamMeta
			var sortOrder int
			if rows.Scan(&m.ID, &m.Name, &sortOrder) == nil {
				if sortOrder == 0 {
					heim = m
				} else {
					gast = m
				}
			}
		}
		rows.Close()
	}

	entries := loadRundeEntries(ctx, pool, p.EventID)
	isDecimal := false
	for _, e := range entries {
		if e.DecimalScoring {
			isDecimal = true
			break
		}
	}

	bestOf := loadBestOf(ctx, pool, p.EventID)
	heimList, heimTotal := teamDisplayList(entries, heim.ID, isDecimal, bestOf)
	gastList, gastTotal := teamDisplayList(entries, gast.ID, isDecimal, bestOf)
	matchShots := loadStarterMatchShots(ctx, pool, p.EventID)
	heimHR := rwkTeamHR(entries, heim.ID, isDecimal, matchShots, bestOf)
	gastHR := rwkTeamHR(entries, gast.ID, isDecimal, matchShots, bestOf)

	if loadRundeModus(ctx, pool, p.EventID) == "paarung" {
		pr := computeRundePaarung(entries, heim.ID, gast.ID, isDecimal, matchShots)
		if p.Ansicht == "scheiben" {
			body := rundePaarungScheibenHTML(ctx, pool, heim.Name, gast.Name, pr, heimTotal, gastTotal, isDecimal)
			if r.URL.Query().Get("partial") == "1" {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{"html": body})
				return
			}
			fmt.Fprintf(w, rundeScheibenPage, html.EscapeString(compName), rwkTileCSS, standTargetJS, rwkTileJS, body)
			return
		}
		fmt.Fprintf(w, rundeHead, html.EscapeString(compName))
		fmt.Fprint(w, rundePaarungListeHTML(heim.Name, gast.Name, pr, heimTotal, gastTotal, isDecimal))
		fmt.Fprint(w, rundeFoot)
		return
	}

	if p.Ansicht == "scheiben" {
		body := rundeScheibenHTML(ctx, pool, compName, heim.Name, gast.Name, heimList, gastList,
			heimTotal, gastTotal, heimHR, gastHR, isDecimal)
		if r.URL.Query().Get("partial") == "1" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"html": body})
			return
		}
		fmt.Fprintf(w, rundeScheibenPage, html.EscapeString(compName), rwkTileCSS, standTargetJS, rwkTileJS, body)
		return
	}

	fmtTot := func(v float64) string {
		if isDecimal {
			return fmt.Sprintf("%.1f", v)
		}
		return fmt.Sprintf("%.0f", v)
	}

	fmt.Fprintf(w, rundeHead, html.EscapeString(compName))
	fmt.Fprint(w, `<div class="team-block top">`)
	for _, e := range heimList {
		fmt.Fprint(w, rundeRow(e, isDecimal))
	}
	fmt.Fprint(w, `</div>`)
	fmt.Fprintf(w, `<div class="mid">
    <div class="team-name">Mannschaft %s</div>
    <div class="score">%s : %s<div class="score-hr">HR %s : %s</div></div>
    <div class="team-name">Mannschaft %s</div>
  </div>`,
		html.EscapeString(nz(heim.Name, "Heim")), fmtTot(heimTotal), fmtTot(gastTotal), fmtTot(heimHR), fmtTot(gastHR), html.EscapeString(nz(gast.Name, "Gast")))
	fmt.Fprint(w, `<div class="team-block bottom">`)
	for _, e := range gastList {
		fmt.Fprint(w, rundeRow(e, isDecimal))
	}
	fmt.Fprint(w, `</div>`)
	fmt.Fprint(w, rundeFoot)
}

func nz(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func loadRundeEntries(ctx context.Context, pool *pgxpool.Pool, eventID string) []rundeEntry {
	rows, err := pool.Query(ctx, `
		SELECT
			COALESCE(best.session_id::text, ''),
			sh.last_name, sh.first_name,
			COALESCE(t.id::text, ''),
			COALESCE(st.role, 'S'),
			COALESCE(d.decimal_scoring, FALSE),
			COALESCE(best.shot_count,    0),
			COALESCE(best.total_rings,   0),
			COALESCE(best.total_decimal, 0.0),
			COALESCE(best.inner_tens,    0),
			COALESCE(st.pair_no, 0), st.id::text
		FROM starters st
		JOIN  shooters sh ON sh.id = st.shooter_id
		LEFT JOIN teams      t ON t.id = st.team_id
		LEFT JOIN disciplines d ON d.id = st.discipline_id
		LEFT JOIN LATERAL (
			SELECT
				se.id                            AS session_id,
				COALESCE(vsr.shot_count,    0)   AS shot_count,
				COALESCE(vsr.total_rings,   0)   AS total_rings,
				COALESCE(vsr.total_decimal, 0.0) AS total_decimal,
				COALESCE(vsr.inner_tens,    0)   AS inner_tens
			FROM sessions se
			LEFT JOIN v_session_results vsr ON vsr.session_id = se.id
			WHERE (se.starter_id = st.id
			    OR (se.event_id = $1::uuid AND se.shooter_id = st.shooter_id
			        AND se.status::text <> 'aborted'))
			  AND se.stechen_no IS NULL
			ORDER BY COALESCE(vsr.shot_count,0) DESC, se.finished_at DESC NULLS LAST
			LIMIT 1
		) best ON TRUE
		WHERE st.event_id = $1::uuid`, eventID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []rundeEntry
	defer func() { attachRundeStechen(ctx, pool, eventID, out) }()
	for rows.Next() {
		var e rundeEntry
		if rows.Scan(&e.SessionID, &e.LastName, &e.FirstName, &e.TeamID, &e.Role, &e.DecimalScoring,
			&e.ShotCount, &e.TotalRings, &e.TotalDecimal, &e.InnerTens, &e.PairNo, &e.StarterID) == nil {
			out = append(out, e)
		}
	}
	return out
}

// loadBestOf: Mannschaftswertung des Wettkampfs - die besten N Schuetzen
// zaehlen (events.best_of, 0 = alle).
func loadBestOf(ctx context.Context, pool *pgxpool.Pool, eventID string) int {
	n := 5
	_ = pool.QueryRow(ctx, `SELECT best_of FROM events WHERE id=$1::uuid`, eventID).Scan(&n)
	return n
}

// displayCount: angezeigte Schuetzen je Mannschaft - mindestens 5 (S/E vor
// AK, AK nur zum Auffuellen), bei groesserer Wertung entsprechend mehr,
// hoechstens 10.
func displayCount(bestOf, countable int) int {
	n := bestOf
	if n <= 0 {
		n = countable
	}
	if n < 5 {
		n = 5
	}
	if n > 10 {
		n = 10
	}
	return n
}

// teamDisplayList: Anzeigeliste und Mannschaftsergebnis aus den besten
// bestOf Schuetzen (0 = alle) - identische Sortier-/Wertungsregel wie
// computeRundenTeams() in server/pdf_runde.go.
func teamDisplayList(entries []rundeEntry, teamID string, isDecimal bool, bestOf int) ([]rundeEntry, float64) {
	var countable, ak []rundeEntry
	for _, e := range entries {
		if e.TeamID != teamID {
			continue
		}
		if e.Role == "AK" {
			ak = append(ak, e)
		} else {
			countable = append(countable, e)
		}
	}
	less := func(list []rundeEntry) func(i, j int) bool {
		return func(i, j int) bool {
			vi, vj := list[i].value(isDecimal), list[j].value(isDecimal)
			if vi != vj {
				return vi > vj
			}
			return list[i].InnerTens > list[j].InnerTens
		}
	}
	sort.SliceStable(countable, less(countable))
	sort.SliceStable(ak, less(ak))

	maxDisplay := displayCount(bestOf, len(countable))
	wertungCnt := len(countable)
	if bestOf > 0 && wertungCnt > bestOf {
		wertungCnt = bestOf
	}
	var total float64
	for _, e := range countable[:wertungCnt] {
		total += e.value(isDecimal)
	}

	display := append([]rundeEntry{}, countable...)
	if len(display) > maxDisplay {
		display = display[:maxDisplay]
	} else if remaining := maxDisplay - len(display); remaining > 0 && len(ak) > 0 {
		n := remaining
		if n > len(ak) {
			n = len(ak)
		}
		display = append(display, ak[:n]...)
	}
	return display, total
}

func rundeRow(e rundeEntry, isDecimal bool) string {
	val := "–"
	if e.ShotCount > 0 {
		if isDecimal {
			val = fmt.Sprintf("%.1f", e.TotalDecimal)
		} else {
			val = fmt.Sprintf("%d", e.TotalRings)
		}
	}
	akBadge := ""
	if e.Role == "AK" {
		akBadge = ` <span class="ak-badge">AK</span>`
	}
	return fmt.Sprintf(`<div class="shooter-row"><span class="shooter-name">%s, %s%s</span><span class="shooter-val">%s</span></div>`,
		html.EscapeString(e.LastName), html.EscapeString(e.FirstName), akBadge, val)
}

func rundeErrorPage(msg string) string {
	return fmt.Sprintf(`<!DOCTYPE html><html lang="de"><head><meta charset="UTF-8"><title>Rundenwettkampf</title></head>
<body style="background:#12161b;color:#75879a;font-family:system-ui,sans-serif;
display:flex;align-items:center;justify-content:center;height:100vh;font-size:1.3em">%s</body></html>`, html.EscapeString(msg))
}

const rundeHead = `<!DOCTYPE html>
<html lang="de"><head><meta charset="UTF-8">
<title>%s</title>
<style>
  :root{--bg:#11151a;--panel:#1a2028;--line:#2a3340;--text:#d8e0e8;--dim:#6a7888;--acc:#4ab8a0}
  *{box-sizing:border-box;margin:0;padding:0}
  body{background:var(--bg);color:var(--text);font-family:system-ui,sans-serif;
       height:100vh;display:flex;flex-direction:column;padding:24px;gap:14px}
  .team-block{flex:1;background:var(--panel);border:1px solid var(--line);border-radius:10px;
              padding:16px 28px;display:flex;flex-direction:column;justify-content:center;gap:6px;min-height:0}
  .shooter-row{display:flex;justify-content:space-between;align-items:baseline;font-size:1.6em}
  .shooter-name{font-weight:600}
  .shooter-val{font-weight:700;color:var(--acc);min-width:2.5em;text-align:right}
  .ak-badge{font-size:.5em;color:var(--dim);border:1px solid var(--line);border-radius:3px;padding:1px 5px}
  .mid{flex-shrink:0;display:flex;align-items:center;justify-content:center;gap:32px;padding:6px 0}
  .team-name{font-size:1.5em;font-weight:700}
  .score{font-size:2.6em;font-weight:800;color:var(--acc);text-align:center}
  .score-hr{font-size:.4em;font-weight:600;color:var(--dim)}
  .mid2{flex-shrink:0;display:grid;grid-template-columns:1fr auto 1fr;gap:14px;align-items:center}
  .mteam{font-size:1.5em;font-weight:700;border-radius:8px;padding:4px 12px}
  .mteam:last-child{text-align:right}
  .mscore{text-align:center;font-size:2.6em;font-weight:800;color:var(--acc);line-height:1.05}
  .mhr{font-size:.32em;font-weight:600;color:var(--dim)}
  .pno{display:inline-block;min-width:1.6em;color:var(--dim);font-weight:700}
  .ldot{display:inline-block;width:.6em;height:.6em;border-radius:50%%;margin-left:.4em;vertical-align:middle}
  .ldot.lead{background:#2ecc40}.ldot.trail{background:#8a949e}
</style></head><body>
`

const rundeFoot = `
<script>setTimeout(() => location.reload(), 6000);</script>
</body></html>`

// ── Scheibenansicht ──────────────────────────────────────────────────────

// rundeScheibenHTML: oben die Heim-, unten die Gast-Mannschaft mit je bis zu
// 5 Trefferbildern (Reihenfolge wie die Wertung: S/E nach Ergebnis, AK zum
// Auffuellen), dazwischen Stand und Hochrechnung.
func rundeScheibenHTML(ctx context.Context, pool *pgxpool.Pool, compName, heimName, gastName string,
	heimList, gastList []rundeEntry, heimTotal, gastTotal, heimHR, gastHR float64, isDecimal bool) string {
	colors := rwkColors{byTeam: map[string]string{}, palette: rwkDefaultTeamColors}
	heimColor, gastColor := colors.pair(0)
	cols := len(heimList)
	if len(gastList) > cols {
		cols = len(gastList)
	}
	if cols < 1 {
		cols = 1
	}
	row := func(list []rundeEntry, color string) string {
		var b strings.Builder
		fmt.Fprintf(&b, `<div class="team-row" style="grid-template-columns:repeat(%d,1fr)">`, cols)
		for _, e := range list {
			name := e.LastName + ", " + e.FirstName
			if e.SessionID == "" {
				fmt.Fprintf(&b, `<div class="rtile"><div class="rhead" style="background:%s">%s</div><div class="rwait">noch nicht angetreten</div></div>`,
					hexAlpha(color, 0.5), html.EscapeString(name))
				continue
			}
			t := loadSessionTile(ctx, pool, e.SessionID)
			x := loadRwkTileExtra(ctx, pool, e.SessionID, colors)
			x.HeaderColor = color
			x.Label = ""
			if e.Role == "AK" {
				x.Label = "außer Konkurrenz"
			}
			b.WriteString(renderRwkTile(t, x))
		}
		b.WriteString(`</div>`)
		return b.String()
	}
	diff := heimHR - gastHR
	diffTxt := fmt.Sprintf("%+.0f", diff)
	if isDecimal {
		diffTxt = fmt.Sprintf("%+.1f", diff)
	}
	mid := fmt.Sprintf(`<div class="mid2">
  <div class="mteam" style="background:%s">%s</div>
  <div class="mscore">%s : %s<div class="mhr">HR %s : %s (%s)</div></div>
  <div class="mteam" style="background:%s">%s</div>
</div>`, heimColor, html.EscapeString(nz(heimName, "Heim")), fmtScore(heimTotal, isDecimal), fmtScore(gastTotal, isDecimal),
		fmtScore(heimHR, isDecimal), fmtScore(gastHR, isDecimal), diffTxt, gastColor, html.EscapeString(nz(gastName, "Gast")))
	return row(heimList, heimColor) + mid + row(gastList, gastColor)
}

// ── Paarungswettkampf ────────────────────────────────────────────────────

// pairPoints: "3 : 2" plus Hinweis auf den Mannschaftspunkt.
func pairPointsMid(heimName, gastName string, pr rundePaarung, heimTotal, gastTotal float64, isDecimal bool, heimColor, gastColor string) string {
	extra := ""
	switch pr.TotalPoint {
	case 0:
		extra = " · Mannschaftspunkt Heim"
	case 1:
		extra = " · Mannschaftspunkt Gast"
	}
	style := func(c string) string {
		if c == "" {
			return ""
		}
		return ` style="background:` + c + `"`
	}
	return fmt.Sprintf(`<div class="mid2">
  <div class="mteam"%s>%s</div>
  <div class="mscore">%d : %d<div class="mhr">Punkte nach HR%s<br>Ringe %s : %s · HR %s : %s</div></div>
  <div class="mteam"%s>%s</div>
</div>`, style(heimColor), html.EscapeString(nz(heimName, "Heim")), pr.HeimPts, pr.GastPts, extra,
		fmtScore(heimTotal, isDecimal), fmtScore(gastTotal, isDecimal),
		fmtScore(pr.HeimSum, isDecimal), fmtScore(pr.GastSum, isDecimal),
		style(gastColor), html.EscapeString(nz(gastName, "Gast")))
}

// rundePaarungScheibenHTML: je Paarung eine Spalte, Heim oben, Gast genau
// darunter; der nach HR vordere Schuetze bekommt oben rechts einen gruenen,
// der andere einen grauen Punkt.
func rundePaarungScheibenHTML(ctx context.Context, pool *pgxpool.Pool, heimName, gastName string,
	pr rundePaarung, heimTotal, gastTotal float64, isDecimal bool) string {
	colors := rwkColors{byTeam: map[string]string{}, palette: rwkDefaultTeamColors}
	heimColor, gastColor := colors.pair(0)
	if len(pr.Pairs) == 0 {
		return `<div class="rwait">Noch keine Paarungen zugeordnet.</div>`
	}
	tile := func(e *rundeEntry, no int, color, dot string) string {
		if e != nil && len(e.Stechen) > 0 {
			// Stechschuss: Scheibe des laufenden/letzten Durchgangs
			cp := *e
			cp.SessionID = e.Stechen[len(e.Stechen)-1].SessionID
			e = &cp
		}
		if e == nil {
			return fmt.Sprintf(`<div class="rtile"><div class="rhead" style="background:%s">Paarung %d</div><div class="rwait">nicht besetzt</div></div>`,
				hexAlpha(color, 0.5), no)
		}
		name := e.LastName + ", " + e.FirstName
		if e.SessionID == "" {
			return fmt.Sprintf(`<div class="rtile"><div class="rhead" style="background:%s">%s</div><div class="rwait">noch nicht angetreten</div></div>`,
				hexAlpha(color, 0.5), html.EscapeString(name))
		}
		t := loadSessionTile(ctx, pool, e.SessionID)
		x := loadRwkTileExtra(ctx, pool, e.SessionID, colors)
		x.HeaderColor = color
		x.Label = fmt.Sprintf("Paarung %d", no)
		x.Dot = dot
		return renderRwkTile(t, x)
	}
	row := func(side int) string {
		var b strings.Builder
		fmt.Fprintf(&b, `<div class="team-row" style="grid-template-columns:repeat(%d,1fr)">`, len(pr.Pairs))
		for _, p := range pr.Pairs {
			if side == 0 {
				b.WriteString(tile(p.Heim, p.No, heimColor, p.dotFor(0)))
			} else {
				b.WriteString(tile(p.Gast, p.No, gastColor, p.dotFor(1)))
			}
		}
		b.WriteString(`</div>`)
		return b.String()
	}
	return row(0) + pairPointsMid(heimName, gastName, pr, heimTotal, gastTotal, isDecimal, heimColor, gastColor) + row(1)
}

// rundePaarungListeHTML: Listenansicht - Zeilen in Paarungsreihenfolge, die
// Schuetzen einer Paarung stehen in beiden Bloecken an gleicher Stelle.
func rundePaarungListeHTML(heimName, gastName string, pr rundePaarung, heimTotal, gastTotal float64, isDecimal bool) string {
	var b strings.Builder
	block := func(cls string, side int) {
		fmt.Fprintf(&b, `<div class="team-block %s">`, cls)
		for _, p := range pr.Pairs {
			e, v := p.Heim, p.HeimHR
			if side == 1 {
				e, v = p.Gast, p.GastHR
			}
			name, val := "– nicht besetzt –", "–"
			if e != nil {
				name = e.LastName + ", " + e.FirstName
				if e.ShotCount > 0 {
					val = fmtScore(e.value(isDecimal), isDecimal)
					if v != e.value(isDecimal) {
						val += ` <span class="ak-badge">HR ` + fmtScore(v, isDecimal) + `</span>`
					}
				}
			}
			dot := ""
			if d := p.dotFor(side); d != "" {
				dot = `<span class="ldot ` + d + `"></span>`
			}
			fmt.Fprintf(&b, `<div class="shooter-row"><span class="shooter-name"><span class="pno">%d</span>%s</span><span class="shooter-val">%s%s</span></div>`,
				p.No, html.EscapeString(name), val, dot)
		}
		b.WriteString(`</div>`)
	}
	block("top", 0)
	b.WriteString(pairPointsMid(heimName, gastName, pr, heimTotal, gastTotal, isDecimal, "", ""))
	block("bottom", 1)
	return b.String()
}

const rundeScheibenPage = `<!DOCTYPE html>
<html lang="de"><head><meta charset="UTF-8">
<title>%s</title>
<style>
  :root{--bg:#0c0f14;--panel:#141a22;--line:#26303c;--text:#e6edf3;--dim:#7d8b99;--acc:#4ab8a0}
  *{box-sizing:border-box;margin:0;padding:0}
  body{background:var(--bg);color:var(--text);font-family:system-ui,sans-serif;height:100vh;overflow:hidden;padding:10px}
  #content{height:100%%;display:flex;flex-direction:column;gap:8px}
  .team-row{flex:1;min-height:0;display:grid;gap:10px}
  .rwait{flex:1;display:flex;align-items:center;justify-content:center;color:var(--dim);font-size:clamp(12px,1.2vw,22px)}
  .mid2{flex-shrink:0;display:grid;grid-template-columns:1fr auto 1fr;gap:14px;align-items:center}
  .mteam{border-radius:8px;padding:6px 14px;font-weight:700;font-size:clamp(14px,1.6vw,30px);color:#fff;
         white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
  .mteam:last-child{text-align:right}
  .mscore{text-align:center;font-size:clamp(22px,2.8vw,56px);font-weight:800;color:var(--acc);line-height:1.05}
  .mhr{font-size:.38em;font-weight:600;color:var(--dim)}
%s
</style>
<script>
%s
%s
</script>
</head><body>
<div id="content">%s</div>
<script>
drawAll(); tickTimers();
setInterval(tickTimers, 1000);
setInterval(async () => {
  try {
    const r = await fetch(location.pathname + '?partial=1', {cache: 'no-store'});
    if (!r.ok) return;
    document.getElementById('content').innerHTML = (await r.json()).html;
    drawAll(); tickTimers();
  } catch (e) { /* naechster Versuch */ }
}, 3000);
setTimeout(() => location.reload(), 3600 * 1000);
</script>
</body></html>`
