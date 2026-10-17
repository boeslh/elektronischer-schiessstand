// ============================================================================
// rwk.go – Rundenwettkampf online: Liste oder Scheiben (je Mannschaft bzw.
// je Paarung), Aktualisierung alle 3 Sekunden; dazu die Live-Seite eines
// Vereins, die alle laufenden Rundenwettkaempfe im Wechsel zeigt.
//
// Mannschaftsergebnis, Hochrechnung, Punkte und Fuehrung je Paarung rechnet
// der Schiessstand-Server (server/online_snapshot.go) - hier wird nur
// dargestellt.
// ============================================================================
package main

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"
)

var teamColors = [2]string{"#e6622c", "#2f6fdc"}

// liveMaxAge: so lange gilt ein laufender Wettkampf ohne neue Daten noch als live.
const liveMaxAge = 10 * time.Minute

func (a *App) loadRWK(r *http.Request) (vereinInfo, *RWKSnapshot, time.Time, bool) {
	v, ok := a.loadVerein(r)
	if !ok {
		return v, nil, time.Time{}, false
	}
	var data []byte
	var upd time.Time
	if err := a.pool.QueryRow(r.Context(), `SELECT data, updated_at FROM publications
		WHERE id=$1::uuid AND verein_id=$2::uuid AND kind='rwk'`, r.PathValue("id"), v.ID).Scan(&data, &upd); err != nil {
		return v, nil, upd, false
	}
	var s RWKSnapshot
	if json.Unmarshal(data, &s) != nil {
		return v, nil, upd, false
	}
	return v, &s, upd, true
}

func ansichtOf(r *http.Request) string {
	if r.URL.Query().Get("ansicht") == "liste" {
		return "liste"
	}
	return "scheiben"
}

func (a *App) handleRWK(w http.ResponseWriter, r *http.Request) {
	v, s, upd, ok := a.loadRWK(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	ansicht := ansichtOf(r)
	other, otherLabel := "liste", "Liste"
	if ansicht == "liste" {
		other, otherLabel = "scheiben", "Scheiben"
	}
	info := []string{}
	for _, x := range []string{s.Liga, s.Disziplin, fmtDate(s.Datum), s.Ort} {
		if x != "" {
			info = append(info, html.EscapeString(x))
		}
	}
	body := fmt.Sprintf(`<div class="card rwkhead"><b>%s</b> · %s · <span id="st">%s</span>
<span style="float:right"><a href="?ansicht=%s">%s</a></span></div>
<div id="rwk">%s</div>
<style>%s</style>
<script>%s
%s
drawAll();
async function refresh() {
  try {
    const r = await fetch('daten?ansicht=%s', {cache: 'no-store'});
    if (!r.ok) return;
    const j = await r.json();
    document.getElementById('rwk').innerHTML = j.html;
    document.getElementById('st').textContent = j.stand;
    drawAll();
  } catch (e) {}
}
setInterval(refresh, 3000);
</script>`, html.EscapeString(s.Name), strings.Join(info, " · "), html.EscapeString(standText(s, upd)),
		other, otherLabel, rwkHTML(s, ansicht), rwkCSS, standTargetJS, rwkJS, ansicht)
	a.page(w, s.Name, a.vereinCrumb(v), body, false)
}

func (a *App) handleRWKData(w http.ResponseWriter, r *http.Request) {
	_, s, upd, ok := a.loadRWK(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "nicht gefunden"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"html": rwkHTML(s, ansichtOf(r)), "stand": standText(s, upd)})
}

func standText(s *RWKSnapshot, upd time.Time) string {
	t := "Stand " + upd.In(berlin).Format("15:04:05")
	switch s.Status {
	case "running":
		if time.Since(upd) > liveMaxAge {
			return t + " (keine neuen Daten)"
		}
		return "live · " + t
	case "finished", "archived":
		return "beendet · " + t
	}
	return t
}

func fmtScore(v float64, dec bool) string {
	if dec {
		return strings.Replace(fmt.Sprintf("%.1f", v), ".", ",", 1)
	}
	return fmt.Sprintf("%.0f", v)
}

// rwkHTML: Inhalt der Anzeige (ohne Kopf) - fuer Seite und Aktualisierung.
func rwkHTML(s *RWKSnapshot, ansicht string) string {
	var b strings.Builder
	team := func(t int) []int {
		var out []int
		for i, st := range s.Starter {
			if st.Team == t {
				out = append(out, i)
			}
		}
		if len(out) > 10 {
			out = out[:10]
		}
		return out
	}
	mid := func() string {
		h, g := s.Teams[0], s.Teams[1]
		score := fmtScore(h.Total, s.Decimal) + " : " + fmtScore(g.Total, s.Decimal)
		sub := "HR " + fmtScore(h.HR, s.Decimal) + " : " + fmtScore(g.HR, s.Decimal)
		if s.Modus == "paarung" {
			score = fmt.Sprintf("%d : %d", h.Points, g.Points)
			sub = "Punkte (nach HR) · Ringe " + fmtScore(h.Total, s.Decimal) + " : " + fmtScore(g.Total, s.Decimal) +
				" · HR " + fmtScore(h.HR, s.Decimal) + " : " + fmtScore(g.HR, s.Decimal)
		}
		return fmt.Sprintf(`<div class="mid"><div class="mteam" style="background:%s">%s</div>
<div class="mscore">%s<div class="msub">%s</div></div>
<div class="mteam r" style="background:%s">%s</div></div>`,
			teamColors[0], html.EscapeString(nz(h.Name, "Heim")), score, html.EscapeString(sub),
			teamColors[1], html.EscapeString(nz(g.Name, "Gast")))
	}
	dots := map[int]string{}
	if s.Modus == "paarung" {
		for _, p := range s.Pairs {
			if p.Leader < 0 {
				continue
			}
			lead, trail := p.Heim, p.Gast
			if p.Leader == 1 {
				lead, trail = p.Gast, p.Heim
			}
			if lead >= 0 {
				dots[lead] = "lead"
			}
			if trail >= 0 {
				dots[trail] = "trail"
			}
		}
	}

	if ansicht == "liste" {
		block := func(idx []int) {
			b.WriteString(`<div class="card lst"><table><tr><th>Name</th><th>Serien</th><th class="num">Schuss</th><th class="num">HR</th><th class="num">Ergebnis</th></tr>`)
			for _, i := range idx {
				st := s.Starter[i]
				var sr []string
				for _, x := range st.Series {
					sr = append(sr, fmtScore(x, s.Decimal))
				}
				tag := ""
				if st.Role == "AK" {
					tag = ` <span class="hint">AK</span>`
				} else if st.Role == "E" {
					tag = ` <span class="hint">E</span>`
				}
				if st.PairNo > 0 && s.Modus == "paarung" {
					tag = fmt.Sprintf(` <span class="hint">Paarung %d</span>`, st.PairNo) + tag
				}
				if d := dots[i]; d != "" {
					tag += ` <span class="ldot ` + d + `"></span>`
				}
				cls := ""
				if !st.Counts {
					cls = ` style="color:#66737f"`
				}
				total := "–"
				if st.Shots > 0 {
					total = fmtScore(st.Total, s.Decimal)
				}
				fmt.Fprintf(&b, `<tr%s><td>%s%s</td><td>%s</td><td class="num">%d/%d</td><td class="num">%s</td><td class="num"><b>%s</b></td></tr>`,
					cls, html.EscapeString(st.Name), tag, strings.Join(sr, " · "), st.Shots, st.MatchShots,
					fmtScore(st.HR, s.Decimal), total)
			}
			b.WriteString(`</table></div>`)
		}
		block(team(0))
		b.WriteString(mid())
		block(team(1))
		return b.String()
	}

	// Scheiben
	row := func(idx []int, t int, cols int) {
		fmt.Fprintf(&b, `<div class="trow" style="grid-template-columns:repeat(%d,minmax(0,1fr))">`, cols)
		for _, i := range idx {
			if i < 0 {
				b.WriteString(`<div class="tile"><div class="th" style="background:` + teamColors[t] + `88">nicht besetzt</div><div class="tw"></div></div>`)
				continue
			}
			b.WriteString(tileHTML(s, i, teamColors[t], dots[i]))
		}
		b.WriteString(`</div>`)
	}
	if s.Modus == "paarung" && len(s.Pairs) > 0 {
		var h, g []int
		for _, p := range s.Pairs {
			h, g = append(h, p.Heim), append(g, p.Gast)
		}
		row(h, 0, len(s.Pairs))
		b.WriteString(mid())
		row(g, 1, len(s.Pairs))
		return b.String()
	}
	h, g := team(0), team(1)
	cols := len(h)
	if len(g) > cols {
		cols = len(g)
	}
	if cols < 1 {
		cols = 1
	}
	row(h, 0, cols)
	b.WriteString(mid())
	row(g, 1, cols)
	return b.String()
}

func nz(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func tileHTML(s *RWKSnapshot, i int, color, dot string) string {
	st := s.Starter[i]
	geo := TargetGeo{}
	shots := []TileShot{}
	last, label := "–", ""
	if st.Tile != nil {
		geo = s.Geos[st.Tile.Geo]
		shots = st.Tile.Shots
		if st.Tile.Last != "" {
			last = st.Tile.Last
		}
		if st.Tile.Probe {
			label = "Probe"
		}
	}
	if st.Stechen > 0 {
		label = fmt.Sprintf("Stechschuss %d", st.Stechen)
	} else if st.PairNo > 0 && s.Modus == "paarung" {
		label = fmt.Sprintf("Paarung %d", st.PairNo)
	}
	geoJSON, _ := json.Marshal(geo)
	shotsJSON, _ := json.Marshal(shots)
	var sr []string
	for _, x := range st.Series {
		sr = append(sr, fmtScore(x, s.Decimal))
	}
	total := "–"
	if st.Shots > 0 {
		total = fmtScore(st.Total, s.Decimal)
	}
	dotHTML := ""
	if dot != "" {
		dotHTML = `<span class="pdot ` + dot + `"></span>`
	}
	labelHTML := ""
	if label != "" {
		labelHTML = `<span class="tl">` + html.EscapeString(label) + `</span>`
	}
	ak := ""
	if st.Role == "AK" {
		ak = " (AK)"
	}
	return fmt.Sprintf(`<div class="tile">%s<div class="th" style="background:%s">%s%s</div>
<div class="tw"><svg class="tgt" data-geo='%s' data-shots='%s' viewBox="-30 -30 60 60"></svg>%s</div>
<div class="ti"><span class="tlast">%s</span><span class="tser">%s</span><span class="thr">HR %s</span><span class="ttot">%s<small>%d/%d</small></span></div></div>`,
		dotHTML, color+"cc", html.EscapeString(st.Name), ak,
		html.EscapeString(string(geoJSON)), html.EscapeString(string(shotsJSON)), labelHTML,
		html.EscapeString(last), strings.Join(sr, " "), fmtScore(st.HR, s.Decimal), total, st.Shots, st.MatchShots)
}

const rwkCSS = `
main{max-width:none}
.rwkhead{padding:8px 14px}
.trow{display:grid;gap:10px;margin-bottom:10px}
.tile{background:#141a22;border-radius:8px;overflow:hidden;position:relative;display:flex;flex-direction:column;color:#e6edf3;min-height:0}
.th{padding:4px 10px;font-weight:600;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;color:#fff;padding-right:2em}
.tw{position:relative;aspect-ratio:1/1;background:#0c0f14}
.tw svg{width:100%;height:100%;display:block}
.tl{position:absolute;left:6px;bottom:6px;background:rgba(10,40,55,.85);border:1px solid #2aa3b8;border-radius:4px;padding:1px 6px;font-size:.8em}
.ti{display:flex;gap:.6em;align-items:baseline;padding:3px 10px 5px;white-space:nowrap}
.tlast{font-size:1.3em;font-weight:700}
.tser{flex:1;text-align:center;overflow:hidden;font-weight:600;color:#c9d4de}
.thr{font-size:.8em;border:1px solid #7d8b99;border-radius:4px;padding:0 4px}
.ttot{font-size:1.4em;font-weight:800;text-align:right}
.ttot small{display:block;font-size:.5em;font-weight:500;color:#7d8b99}
.pdot{position:absolute;top:5px;right:6px;z-index:2;width:16px;height:16px;border-radius:50%;border:2px solid rgba(0,0,0,.45)}
.pdot.lead,.ldot.lead{background:#2ecc40}.pdot.trail,.ldot.trail{background:#8a949e}
.ldot{display:inline-block;width:.7em;height:.7em;border-radius:50%;vertical-align:middle}
.mid{display:grid;grid-template-columns:1fr auto 1fr;gap:12px;align-items:center;margin:4px 0 12px}
.mteam{border-radius:8px;padding:6px 14px;font-weight:700;font-size:1.2em;color:#fff;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.mteam.r{text-align:right}
.mscore{text-align:center;font-size:2.2em;font-weight:800;color:#1f6fb2;line-height:1.1}
.msub{font-size:.38em;font-weight:600;color:#66737f}
@media(max-width:800px){.trow{grid-template-columns:repeat(2,minmax(0,1fr))!important}.mscore{font-size:1.6em}}
`

const rwkJS = `
function drawAll() {
  document.querySelectorAll('svg.tgt').forEach((svg, i) => {
    svg.id = 'tgt-' + i;
    try { drawStandTarget(svg.id, JSON.parse(svg.dataset.geo), JSON.parse(svg.dataset.shots)); } catch (e) {}
  });
}
`

// ── Live-Seite eines Vereins ─────────────────────────────────────────────

func (a *App) liveList(r *http.Request, vid string) []struct {
	ID   string
	Snap *RWKSnapshot
	Upd  time.Time
} {
	rows, err := a.pool.Query(r.Context(), `SELECT id::text, data, updated_at FROM publications
		WHERE verein_id=$1::uuid AND kind='rwk' AND status='running' AND updated_at > now() - $2::interval
		ORDER BY title`, vid, fmt.Sprintf("%d seconds", int(liveMaxAge.Seconds())))
	var out []struct {
		ID   string
		Snap *RWKSnapshot
		Upd  time.Time
	}
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var data []byte
		var upd time.Time
		if rows.Scan(&id, &data, &upd) != nil {
			continue
		}
		var s RWKSnapshot
		if json.Unmarshal(data, &s) == nil {
			out = append(out, struct {
				ID   string
				Snap *RWKSnapshot
				Upd  time.Time
			}{id, &s, upd})
		}
	}
	return out
}

func (a *App) liveHTML(r *http.Request, v vereinInfo) string {
	list := a.liveList(r, v.ID)
	if len(list) == 0 {
		return `<div class="card"><p class="hint">Zurzeit läuft kein Rundenwettkampf.</p></div>`
	}
	var b strings.Builder
	for i, x := range list {
		fmt.Fprintf(&b, `<section class="live" data-idx="%d"><div class="card rwkhead"><b>%s</b> · %s <span class="hint">(%d/%d)</span></div>%s</section>`,
			i, html.EscapeString(x.Snap.Name), html.EscapeString(standText(x.Snap, x.Upd)), i+1, len(list), rwkHTML(x.Snap, "scheiben"))
	}
	return b.String()
}

func (a *App) handleLive(w http.ResponseWriter, r *http.Request) {
	v, ok := a.loadVerein(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	body := fmt.Sprintf(`<div id="live">%s</div><style>%s
section.live{display:none}section.live.on{display:block}</style>
<script>%s
%s
function rotate(){const s=document.querySelectorAll('section.live');if(!s.length)return;
 const i=Math.floor(Date.now()/10000)%%s.length;s.forEach((e,k)=>e.classList.toggle('on',k===i));}
drawAll();rotate();setInterval(rotate,1000);
setInterval(async()=>{try{const r=await fetch('live/daten',{cache:'no-store'});if(!r.ok)return;
 document.getElementById('live').innerHTML=(await r.json()).html;drawAll();rotate();}catch(e){}},3000);
</script>`, a.liveHTML(r, v), rwkCSS, standTargetJS, rwkJS)
	a.page(w, "Live – "+v.Name, a.vereinCrumb(v)+" · Live", body, false)
}

func (a *App) handleLiveData(w http.ResponseWriter, r *http.Request) {
	v, ok := a.loadVerein(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "nicht gefunden"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"html": a.liveHTML(r, v)})
}
