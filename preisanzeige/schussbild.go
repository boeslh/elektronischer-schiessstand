// ============================================================================
// schussbild.go – Schussbild-Ansicht einer einzelnen gekauften Scheibe.
//
// Bildet bewusst 1:1 die "Grafische Ansicht" des Hauptservers nach
// (server/web/ergebnis-ansicht.html: SVG-Scheibe, Zoom-Stufen, Probe/Serien-
// Liste) - nur ohne jede Bearbeitungsmöglichkeit (kein Korrektur-Modus, kein
// "Originalwerte anzeigen"-Umschalter, keine ✎/↺-Buttons, kein role.js): die
// Auswahl-Funktion (Klick auf Serie/Schuss blendet nur ein, was auf der
// Scheibe gezeigt wird) bleibt erhalten, das ist eine reine Ansichtsfilterung,
// keine Bearbeitung.
//
// preisanzeige läuft unabhängig vom Hauptserver-Prozess (siehe main.go) und
// ruft dessen HTTP-API deshalb nicht auf - Ringgeometrie und Schüsse werden
// hier direkt aus derselben Postgres-DB gelesen und als JSON in die Seite
// eingebettet, statt sie wie im Original per fetch() nachzuladen.
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

type ringGeoJSON struct {
	V      int     `json:"v"`
	D      float64 `json:"d"`
	Filled bool    `json:"filled"`
}

type targetGeoJSON struct {
	Rings          []ringGeoJSON `json:"rings"`
	InnerTenD      float64       `json:"inner10_d"`
	InnerTenDashed bool          `json:"inner10_dashed"`
}

type shotJSON struct {
	ShotNo         int      `json:"shot_no"`
	Kind           string   `json:"kind"`
	Status         string   `json:"status"`
	XMM            *float64 `json:"x_mm"`
	YMM            *float64 `json:"y_mm"`
	Ring           *int     `json:"ring"`
	Decimal        *float64 `json:"decimal"`
	InnerTen       bool     `json:"inner_ten"`
	CenterDistance *float64 `json:"center_distance"`
}

// targetFillCutoff: ab welchem Ringwert (>=) der "Spiegel" (mittlerer
// Bereich) schwarz gefüllt ist, je Scheiben-Kürzel im Zielnamen - 1:1 aus
// server/target_geometry.go (targetGeometries) übernommen, dort für dieselbe
// Referenzscheibe im Simulator/in der Grafischen Ansicht verwendet.
var targetFillCutoff = map[string]int{
	"LG": 4, // Ring 4-10 gefüllt, 1-3 weiß
	"ZS": 6,
	"SP": 7,
	"LP": 7,
}

func fillCutoffForTargetName(name string) int {
	for _, word := range strings.Fields(strings.ToUpper(name)) {
		if c, ok := targetFillCutoff[word]; ok {
			return c
		}
	}
	return 0
}

func (h *siteHandler) handleSchussbild(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	kaufScheibeID := r.PathValue("id")

	var scheibeName, teilnehmerName, targetName string
	var teilnehmerNr int
	var sessionID *string
	var innerTenD float64
	var targetID string
	var shotsPerSeries int
	var decimalScoring bool
	var startedAt *time.Time
	err := h.pool.QueryRow(ctx, `
		SELECT sc.name, sh.last_name||' '||sh.first_name, t.teilnehmer_nr,
		       ks.session_id::text, tg.inner_ten_d_mm, tg.id::text, tg.name,
		       d.shots_per_series, d.decimal_scoring, se.started_at
		FROM ps_kauf_scheiben ks
		JOIN ps_kaeufe k ON k.id = ks.kauf_id
		JOIN ps_teilnehmer t ON t.id = k.teilnehmer_id
		JOIN shooters sh ON sh.id = t.shooter_id
		JOIN ps_scheiben sc ON sc.id = ks.scheibe_id
		JOIN disciplines d ON d.id = sc.discipline_id
		JOIN targets tg ON tg.id = d.target_id
		LEFT JOIN sessions se ON se.id = ks.session_id
		WHERE ks.id=$1`, kaufScheibeID,
	).Scan(&scheibeName, &teilnehmerName, &teilnehmerNr, &sessionID, &innerTenD, &targetID, &targetName,
		&shotsPerSeries, &decimalScoring, &startedAt)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	cutoff := fillCutoffForTargetName(targetName)
	var geo targetGeoJSON
	geo.InnerTenD = innerTenD
	rrows, err := h.pool.Query(ctx,
		`SELECT ring_value, diameter_mm FROM target_rings WHERE target_id=$1 ORDER BY diameter_mm DESC`, targetID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var ring10D float64
	for rrows.Next() {
		var v int
		var d float64
		if err := rrows.Scan(&v, &d); err != nil {
			rrows.Close()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		geo.Rings = append(geo.Rings, ringGeoJSON{V: v, D: d, Filled: cutoff > 0 && v >= cutoff})
		if v == 10 {
			ring10D = d
		}
	}
	rrows.Close()
	geo.InnerTenDashed = innerTenD > 0 && ring10D > 0 && innerTenD < ring10D

	var shots []shotJSON
	if sessionID != nil {
		srows, err := h.pool.Query(ctx, `
			SELECT shot_no, kind::text, status::text, x_mm, y_mm, ring, decimal_value,
			       is_inner_ten, center_distance
			FROM shots WHERE session_id=$1 AND status IN ('valid','annulled','cross_shot_in')
			ORDER BY shot_no`, *sessionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for srows.Next() {
			var s shotJSON
			if err := srows.Scan(&s.ShotNo, &s.Kind, &s.Status, &s.XMM, &s.YMM, &s.Ring, &s.Decimal,
				&s.InnerTen, &s.CenterDistance); err != nil {
				srows.Close()
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			shots = append(shots, s)
		}
		srows.Close()
	}

	shotsJSON, _ := json.Marshal(shots)
	geoJSON, _ := json.Marshal(geo)

	dateLine := ""
	if startedAt != nil {
		dateLine = startedAt.In(berlin).Format("02.01.2006  15:04")
	}

	page := strings.NewReplacer(
		"{{SHOOTER}}", html.EscapeString(fmt.Sprintf("Nr. %d – %s", teilnehmerNr, teilnehmerName)),
		"{{DISCIPLINE}}", html.EscapeString(scheibeName),
		"{{DATE}}", html.EscapeString(dateLine),
		"{{BACK_HREF}}", fmt.Sprintf("%s/teilnehmer/%d", h.base(), teilnehmerNr),
		"{{SHOTS_JSON}}", string(shotsJSON),
		"{{GEO_JSON}}", string(geoJSON),
		"{{SHOTS_PER_SERIES}}", fmt.Sprintf("%d", shotsPerSeries),
		"{{DECIMAL_SCORING}}", fmt.Sprintf("%v", decimalScoring),
	).Replace(schussbildPageTemplate)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, page)
}

// schussbildPageTemplate: eigenständige (nicht in renderLayout eingebettete)
// Seite im selben dunklen Look wie server/web/ergebnis-ansicht.html, deren
// SVG-/Zoom-/Listen-Logik hier fast unverändert übernommen ist - entfernt
// sind nur alles rund um Korrektur/Bearbeitung (Klick-Korrekturmodus,
// "Originalwerte anzeigen", ✎/↺-Buttons, role.js/can_correct_results).
const schussbildPageTemplate = `<!DOCTYPE html>
<html lang="de">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Schussbild – {{SHOOTER}}</title>
<style>
  :root {
    --bg:#0f0f1a; --panel:#1a1a2e; --line:#2a2a45; --text:#e0e0f0;
    --dim:#7070a0; --acc:#7ba7ff; --green:#3dcc6e; --red:#e04040;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    background: var(--bg); color: var(--text);
    font-family: system-ui, sans-serif; font-size: 14px;
    display: grid; grid-template-columns: 1fr 340px;
    grid-template-rows: auto 1fr; gap: 12px;
    height: 100vh; padding: 12px;
  }
  header {
    grid-column: 1 / -1; display: flex; align-items: baseline; gap: 12px;
    padding: 4px 8px;
  }
  header a.back { color: var(--dim); text-decoration: none; font-size: 18px; }
  header a.back:hover { color: var(--text); }
  header h1 { font-size: 17px; font-weight: 600; }
  #discipline { font-size: 13px; color: var(--dim); }
  #dateLine   { font-size: 12px; color: var(--dim); margin-left: auto; }

  .panel { background: var(--panel); border: 1px solid var(--line);
           border-radius: 8px; overflow: hidden; }
  #targetWrap { display: flex; align-items: center; justify-content: center; min-width: 0; min-height: 0; }

  #sidebar { display: flex; flex-direction: column; min-height: 0; }

  #summary { padding: 12px 14px; border-bottom: 1px solid var(--line); flex-shrink: 0; }
  #summary .total { display: flex; align-items: baseline; gap: 8px; }
  #summary .total .tv { font-size: 30px; font-weight: 700; color: var(--acc); }
  #summary .total .tl { font-size: 11px; color: var(--dim); text-transform: uppercase; letter-spacing: 1px; }
  #summary .sub { font-size: 12px; color: var(--dim); margin-top: 4px; }

  #zoomBar { display: flex; flex-wrap: wrap; gap: 4px; padding: 8px 12px;
             border-bottom: 1px solid var(--line); flex-shrink: 0; }
  .zoom-btn { font-size: 11px; padding: 4px 8px; border-radius: 4px; cursor: pointer;
              border: 1px solid var(--line); background: var(--bg); color: var(--dim); }
  .zoom-btn:hover { color: var(--text); border-color: var(--dim); }
  .zoom-btn.active { color: #0c1410; background: var(--acc); border-color: var(--acc); font-weight: 600; }

  #selectBar { padding: 6px 12px; border-bottom: 1px solid var(--line); flex-shrink: 0;
               font-size: 12px; color: var(--dim); display: flex; align-items: center; gap: 8px; }
  #selectBar .hint { flex: 1; }
  #selectBar button { font-size: 11px; padding: 3px 10px; border-radius: 4px; cursor: pointer;
                       border: 1px solid var(--line); background: var(--bg); color: var(--text); }
  #selectBar button:hover { border-color: var(--acc); color: var(--acc); }

  #list { flex: 1; overflow-y: auto; }
  .grp-head { display: flex; align-items: center; justify-content: space-between;
              padding: 7px 12px; cursor: pointer; background: var(--bg);
              border-bottom: 1px solid var(--line); position: sticky; top: 0; }
  .grp-head:hover { background: #202038; }
  .grp-head.active { background: var(--acc); color: #0c1410; }
  .grp-head .gn { font-size: 12px; font-weight: 700; text-transform: uppercase; letter-spacing: .5px; }
  .grp-head .gv { font-size: 15px; font-weight: 700; }
  .grp-head.active .gv { color: #0c1410; }

  #list table { width: 100%; border-collapse: collapse; }
  #list td { padding: 5px 12px; text-align: right; font-size: 13px;
             border-bottom: 1px solid var(--line); cursor: pointer; }
  #list td:first-child { text-align: left; color: var(--dim); width: 28px; }
  #list tr:hover td { background: #202038; }
  #list tr.active td { background: var(--acc); color: #0c1410; }
  #list tr.annulled td { opacity: .5; text-decoration: line-through; }
  #list tr.ten td:nth-child(2) { color: var(--green); font-weight: 700; }
  #list tr.active.ten td:nth-child(2) { color: #0c1410; }

  #empty { padding: 40px 20px; text-align: center; color: var(--dim); font-size: 13px; }
</style>
</head>
<body>

<header>
  <a class="back" href="{{BACK_HREF}}" title="Zurück">←</a>
  <h1>{{SHOOTER}}</h1>
  <span id="discipline">{{DISCIPLINE}}</span>
  <span id="dateLine">{{DATE}}</span>
</header>

<div class="panel" id="targetWrap">
  <svg id="target" viewBox="-30 -30 60 60" width="90%" height="90%"></svg>
</div>

<div class="panel" id="sidebar">
  <div id="summary">
    <div class="total">
      <span class="tv" id="totVal">–</span>
      <span class="tl">Gesamt</span>
    </div>
    <div class="sub" id="totSub"></div>
  </div>
  <div id="zoomBar"></div>
  <div id="selectBar">
    <span class="hint" id="selectHint">Alle Schüsse</span>
    <button id="resetBtn" onclick="selectScope(null)">Alle anzeigen</button>
  </div>
  <div id="list"></div>
</div>

<script>
const shotsPerSeries = {{SHOTS_PER_SERIES}};
const decimalScoring = {{DECIMAL_SCORING}};
const RAW_SHOTS = {{SHOTS_JSON}};
const GEO = {{GEO_JSON}};

const CALIBER = 4.5;
const SHOT_STROKE_W = CALIBER * 0.05;
const SHOT_R = CALIBER / 2 - SHOT_STROKE_W / 2;
const NS = 'http://www.w3.org/2000/svg';
const COLORS = { ring10: '#e04040', ring9: '#f3fb06', other: '#4ab8ff', annulled: '#666666' };
const ZOOM_OPTIONS = [
  {key: 'auto', label: 'Auto',     fraction: null},
  {key: '1',    label: 'Komplett', fraction: 1.00},
  {key: '2',    label: 'Groß',     fraction: 0.60},
  {key: '3',    label: 'Mittel',   fraction: 0.4209},
  {key: '4',    label: 'Fein',     fraction: 0.2418},
  {key: '5',    label: 'Maximal',  fraction: 0.0626},
];
const ZOOM_FIXED = ZOOM_OPTIONS.filter(z => z.fraction !== null).sort((a, b) => a.fraction - b.fraction);

function effX(s) { return s.x_mm; }
function effY(s) { return s.y_mm; }
function effRing(s) { return s.ring; }
function effDecimal(s) { return s.decimal; }
function effCenterDistance(s) { return s.center_distance; }
function effInnerTen(s) { return !!s.inner_ten; }

let allShots = [], sightingShots = [], scoringShots = [];
let seriesGroups = {}, seriesNos = [];
let currentOuterR = 0;
let zoomMode = 'auto';
let selection = null;

function processShots(shots) {
  sightingShots = shots.filter(s => s.kind === 'sighting');
  const scoring = shots.filter(s => s.kind !== 'sighting');

  seriesGroups = {};
  scoring.forEach((s, i) => {
    const no = Math.floor(i / shotsPerSeries) + 1;
    s.displayNo = i + 1;
    if (!seriesGroups[no]) seriesGroups[no] = [];
    seriesGroups[no].push(s);
  });
  seriesNos = Object.keys(seriesGroups).map(Number).sort((a, b) => a - b);
  sightingShots.forEach((s, i) => { s.displayNo = i + 1; });

  allShots = [...sightingShots, ...scoring];
  scoringShots = scoring;

  if (!allShots.length) {
    document.getElementById('list').innerHTML = '<div id="empty">Noch keine Schüsse auf dieser Scheibe.</div>';
  }
  renderSummary(scoring);
  renderList();
  renderShots(getVisibleShots());
  updateZoom();
}

function seriesSum(list) {
  const rings   = list.reduce((a, s) => a + (s.status === 'valid' ? effRing(s) : 0), 0);
  const decimal = list.reduce((a, s) => a + (s.status === 'valid' ? (effDecimal(s) || effRing(s)) : 0), 0);
  return { rings, decimal };
}
function fmtSum(sum) { return decimalScoring ? sum.decimal.toFixed(1) : String(sum.rings); }

function renderSummary(scoring) {
  const sum = seriesSum(scoring);
  document.getElementById('totVal').textContent = scoring.length ? fmtSum(sum) : '–';
  const innerTens = scoring.filter(s => s.status === 'valid' && effInnerTen(s)).length;
  const parts = [];
  if (scoring.length) parts.push(scoring.length + ' Schuss' + (scoring.length === 1 ? '' : 'e'));
  if (innerTens > 0) parts.push(innerTens + '× Innenzehner');
  document.getElementById('totSub').textContent = parts.join(' · ');
}

function buildZoomBar() {
  const bar = document.getElementById('zoomBar');
  bar.innerHTML = '';
  ZOOM_OPTIONS.forEach(opt => {
    const btn = document.createElement('button');
    btn.className = 'zoom-btn' + (opt.key === zoomMode ? ' active' : '');
    btn.textContent = opt.label;
    btn.dataset.zoom = opt.key;
    btn.onclick = () => setZoomMode(opt.key);
    bar.appendChild(btn);
  });
}
function setZoomMode(key) {
  zoomMode = key;
  document.querySelectorAll('.zoom-btn').forEach(b => b.classList.toggle('active', b.dataset.zoom === key));
  updateZoom();
}
function applyZoomViewBox(r) {
  if (r <= 0) return;
  const pad = Math.max(r * 0.07, CALIBER * 0.8);
  svg.setAttribute('viewBox', (-(r+pad)) + ' ' + (-(r+pad)) + ' ' + (2*(r+pad)) + ' ' + (2*(r+pad)));
}
function computeViewR() {
  if (currentOuterR <= 0) return 0;
  if (zoomMode !== 'auto') {
    const lvl = ZOOM_OPTIONS.find(z => z.key === zoomMode);
    return lvl && lvl.fraction ? lvl.fraction * currentOuterR : currentOuterR;
  }
  const visible = getVisibleShots();
  if (!visible.length) return currentOuterR;
  let maxR = 0;
  for (const s of visible) {
    const x = effX(s), y = effY(s);
    if (x == null || y == null) continue;
    maxR = Math.max(maxR, Math.sqrt(x * x + y * y));
  }
  for (const lvl of ZOOM_FIXED) {
    const r = lvl.fraction * currentOuterR;
    if (r >= maxR) return r;
  }
  return currentOuterR;
}
function updateZoom() { applyZoomViewBox(computeViewR()); }

const svg = document.getElementById('target');
let shotLayer;

function buildTarget(target) {
  while (svg.firstChild) svg.removeChild(svg.firstChild);
  shotLayer = null;
  currentOuterR = 0;
  if (!target || !target.rings || !target.rings.length) return;

  const rings  = [...target.rings].sort((a, b) => b.d - a.d);
  const outerR = rings[0].d / 2;
  currentOuterR = outerR;

  const strokeW = Math.max(outerR * 0.004, 0.08);
  let minStep = Infinity;
  for (let i = 0; i < rings.length - 1; i++) minStep = Math.min(minStep, (rings[i].d - rings[i+1].d) / 2);
  const fontSize = Math.max(minStep * 0.58, outerR * 0.045);

  const bg = document.createElementNS(NS, 'circle');
  bg.setAttribute('r', outerR);
  bg.setAttribute('fill', '#e8e4dc');
  svg.appendChild(bg);

  let darkR = 0;
  for (const r of rings) if (r.filled) darkR = Math.max(darkR, r.d / 2);
  if (darkR > 0) {
    const sp = document.createElementNS(NS, 'circle');
    sp.setAttribute('r', darkR);
    sp.setAttribute('fill', '#1c1c1c');
    svg.appendChild(sp);
  }

  for (const r of rings) {
    const c = document.createElementNS(NS, 'circle');
    c.setAttribute('r', r.d / 2);
    c.setAttribute('fill', 'none');
    c.setAttribute('stroke', r.filled ? '#888' : '#555');
    c.setAttribute('stroke-width', strokeW);
    svg.appendChild(c);
  }

  if (target.inner10_d > 0) {
    const inn = document.createElementNS(NS, 'circle');
    inn.setAttribute('r', target.inner10_d / 2);
    inn.setAttribute('fill', 'none');
    inn.setAttribute('stroke', '#888');
    inn.setAttribute('stroke-width', strokeW);
    if (target.inner10_dashed) {
      const dl = strokeW * 5;
      inn.setAttribute('stroke-dasharray', dl + ' ' + dl);
    }
    svg.appendChild(inn);
  }

  const labelPositions = [{nx:0,ny:-1},{nx:1,ny:0},{nx:0,ny:1},{nx:-1,ny:0}];
  for (let i = 0; i < rings.length; i++) {
    const r = rings[i];
    if (r.v < 1 || r.v > 9) continue;
    const outerRadius = r.d / 2;
    const innerRadius = (i + 1 < rings.length) ? rings[i + 1].d / 2 : 0;
    const labelR    = (outerRadius + innerRadius) / 2;
    const fillColor = r.filled ? '#bbb' : '#555';
    for (const p of labelPositions) {
      const t = document.createElementNS(NS, 'text');
      t.setAttribute('x', p.nx * labelR);
      t.setAttribute('y', p.ny * labelR);
      t.setAttribute('text-anchor', 'middle');
      t.setAttribute('dominant-baseline', 'middle');
      t.setAttribute('font-size', fontSize);
      t.setAttribute('font-family', 'sans-serif');
      t.setAttribute('fill', fillColor);
      t.setAttribute('pointer-events', 'none');
      t.textContent = r.v;
      svg.appendChild(t);
    }
  }

  shotLayer = document.createElementNS(NS, 'g');
  svg.appendChild(shotLayer);
}

function contrastColor(hex) {
  const r = parseInt(hex.slice(1, 3), 16);
  const g = parseInt(hex.slice(3, 5), 16);
  const b = parseInt(hex.slice(5, 7), 16);
  return (0.299 * r + 0.587 * g + 0.114 * b) / 255 > 0.5 ? '#000000' : '#FFFFFF';
}
function shotColorByRing(s) {
  if (s.status !== 'valid') return COLORS.annulled;
  const ring = effRing(s);
  if (ring >= 10) return COLORS.ring10;
  if (ring === 9)  return COLORS.ring9;
  return COLORS.other;
}

function renderShots(list) {
  if (!shotLayer) return;
  while (shotLayer.firstChild) shotLayer.removeChild(shotLayer.firstChild);
  for (const s of list) {
    if (s.x_mm == null || s.y_mm == null) continue;
    const fillColor = shotColorByRing(s);
    const x = effX(s), y = effY(s);
    const g = document.createElementNS(NS, 'g');
    if (s.status !== 'valid') g.setAttribute('opacity', '0.6');

    const c = document.createElementNS(NS, 'circle');
    c.setAttribute('cx', x); c.setAttribute('cy', -y);
    c.setAttribute('r', SHOT_R);
    c.setAttribute('fill', fillColor);
    c.setAttribute('fill-opacity', '0.82');
    c.setAttribute('stroke', '#1a1a1a');
    c.setAttribute('stroke-width', SHOT_STROKE_W);

    const t = document.createElementNS(NS, 'text');
    t.setAttribute('x', x); t.setAttribute('y', -y);
    t.setAttribute('text-anchor', 'middle');
    t.setAttribute('dominant-baseline', 'central');
    t.setAttribute('font-size', CALIBER * 0.42);
    t.setAttribute('font-family', 'sans-serif');
    t.setAttribute('font-weight', 'bold');
    t.setAttribute('fill', contrastColor(fillColor));
    t.setAttribute('pointer-events', 'none');
    t.textContent = String(s.displayNo);

    g.appendChild(c); g.appendChild(t);
    shotLayer.appendChild(g);
  }
}

function getVisibleShots() {
  if (!selection) return allShots;
  if (selection.type === 'sighting') return sightingShots;
  if (selection.type === 'series')   return seriesGroups[selection.no] || [];
  if (selection.type === 'shot')     return allShots.filter(s => s.shot_no === selection.shotNo);
  return allShots;
}
function scopeEquals(a, b) {
  if (!a || !b) return a === b;
  if (a.type !== b.type) return false;
  if (a.type === 'series') return a.no === b.no;
  if (a.type === 'shot')   return a.shotNo === b.shotNo;
  return true;
}
function selectScope(scope) {
  selection = scopeEquals(selection, scope) ? null : scope;
  renderList();
  renderShots(getVisibleShots());
  updateZoom();
}

function shotVal(s) {
  const dec = effDecimal(s);
  return decimalScoring && dec != null ? dec.toFixed(1) : String(effRing(s));
}

function groupHTML(scope, label, list) {
  const active = scopeEquals(selection, scope);
  const sum = seriesSum(list);
  let out = '<div class="grp-head' + (active ? ' active' : '') + '" onclick=\'selectScope(' + JSON.stringify(scope) + ')\'>' +
    '<span class="gn">' + label + '</span><span class="gv">' + fmtSum(sum) + '</span></div><table><tbody>';
  for (const s of list) {
    const rowActive = selection && selection.type === 'shot' && selection.shotNo === s.shot_no;
    const cls = [rowActive ? 'active' : '', s.status !== 'valid' ? 'annulled' : '',
                 effRing(s) >= 10 ? 'ten' : ''].filter(Boolean).join(' ');
    const cd = effCenterDistance(s);
    out += '<tr class="' + cls + '" onclick=\'selectScope({"type":"shot","shotNo":' + s.shot_no + '})\'>' +
      '<td>' + s.displayNo + '</td><td>' + shotVal(s) + '</td>' +
      '<td>' + (cd != null ? cd.toFixed(1) : '–') + '</td></tr>';
  }
  out += '</tbody></table>';
  return out;
}

function renderList() {
  const el = document.getElementById('list');
  if (!allShots.length) return;
  let out = '';
  if (sightingShots.length) out += groupHTML({type:'sighting'}, 'Probe', sightingShots);
  for (const no of seriesNos) out += groupHTML({type:'series', no}, 'Serie ' + no, seriesGroups[no]);
  el.innerHTML = out;

  const hint = document.getElementById('selectHint');
  if (!selection) hint.textContent = 'Alle Schüsse (' + allShots.length + ')';
  else if (selection.type === 'sighting') hint.textContent = 'Probe (' + sightingShots.length + ')';
  else if (selection.type === 'series') hint.textContent = 'Serie ' + selection.no;
  else hint.textContent = 'Schuss ' + ((getVisibleShots()[0] || {}).displayNo || '');
  document.getElementById('resetBtn').style.visibility = selection ? 'visible' : 'hidden';
}

buildZoomBar();
buildTarget(GEO);
processShots(RAW_SHOTS);
</script>
</body>
</html>
`
