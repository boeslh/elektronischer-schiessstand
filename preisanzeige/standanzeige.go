// ============================================================================
// standanzeige.go – Raster-Standanzeige (type='stand' in anzeige_config):
// mehrere Stände gleichzeitig, je Kachel Trefferbild (analog Stand-PC),
// Name, Disziplin, Gesamtergebnis, letzter Schuss. Mehr ausgewählte Stände
// als das Raster fasst -> Rotation in Gruppen (aufsteigend nach
// Standnummer), Rotations-/Reload-Mechanik identisch zu display.go
// renderPage() (mehrere <section>, client-seitiges Durchschalten, nach
// einem Zyklus kompletter Seiten-Reload fuer frische Daten).
// ============================================================================
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"

	"github.com/jackc/pgx/v5/pgxpool"
)

type standParams struct {
	LaneNos       []int  `json:"lane_nos"`
	GridSize      string `json:"grid_size"`
	ReloadSeconds int    `json:"reload_seconds"`
}

// standShot: ein Schuss fuer die Kachel-Grafik, durchnummeriert innerhalb
// seiner Schussart (Probe/Wertung) - dieselbe Konvention wie schussbild.go
// (dortiges displayNo) und pdf_einzelergebnis.go.
type standShot struct {
	No      int      `json:"no"`
	XMM     *float64 `json:"x_mm"`
	YMM     *float64 `json:"y_mm"`
	Ring    *int     `json:"ring"`
	Decimal *float64 `json:"decimal"`
}

// seriesSum: Ringsumme einer abgeschlossenen (oder laufenden) Wertungsserie.
type seriesSum struct {
	No      int
	Rings   int
	Decimal float64
}

// standTile: alles fuer eine Kachel - leere Felder = Stand frei.
type standTile struct {
	LaneNo         int
	ShooterName    string
	Discipline     string
	DecimalScoring bool
	Geo            targetGeoJSON
	TotalRings     int
	TotalDecimal   float64
	ShotCount      int
	Shots          []standShot // Schuesse der aktuellen Serie (oder alle Probeschuesse, falls noch keine Wertungsserie)
	LastShot       *standShot  // letzter Schuss aus Shots, fuer die Ergebniszeile
	Series         []seriesSum // die letzten (max. 6) Serien, aelteste faellt zuerst raus
	ProbeMode      bool        // noch kein Wertungsschuss (kind='match') in dieser Session
}

// maxDisplayedSeries: mehr Serien werden von vorne (aelteste zuerst) verworfen.
const maxDisplayedSeries = 6

func renderStandanzeige(w io.Writer, ctx context.Context, pool *pgxpool.Pool, slot anzeigeSlot) {
	var p standParams
	_ = json.Unmarshal(slot.Params, &p)

	laneNos := p.LaneNos
	if len(laneNos) == 0 {
		laneNos = loadActiveLaneNos(ctx, pool)
	}

	gridSize := resolveGridSize(p.GridSize, len(laneNos))
	groups := chunkInts(laneNos, gridSize)

	var tiles [][]standTile
	for _, g := range groups {
		var row []standTile
		for _, no := range g {
			row = append(row, loadStandTile(ctx, pool, no))
		}
		tiles = append(tiles, row)
	}

	reload := p.ReloadSeconds
	if reload <= 0 {
		reload = 8
	}

	fmt.Fprint(w, standanzeigeHead(gridSize))
	for i, group := range tiles {
		active := ""
		if i == 0 {
			active = " active"
		}
		fmt.Fprintf(w, `<section class="%s">`, "grid"+active)
		for _, t := range group {
			fmt.Fprint(w, renderStandTile(t))
		}
		fmt.Fprint(w, `</section>`)
	}
	fmt.Fprintf(w, standanzeigeFoot, reload, reload*max(len(tiles), 1))
}

// loadActiveLaneNos: leeres lane_nos in der Konfiguration = alle aktiven UND
// gerade online Stände (siehe server/anzeige_config.go defaultAnzeigeSlot1) -
// macht Slot 1 ohne jede manuelle Standauswahl direkt nutzbar. "Online" wird
// per live_last_seen_at bestimmt, das der Stand-PC alle 3s per PUT
// /api/lanes/{no}/livestate aktualisiert (server/api.go setLiveState,
// server/store.go SetLaneLastSeen) - derselbe 10s-Schwellwert wie die
// grün/rot-Anzeige in server/web/lanes.html (connDotClass). Eine explizite
// Standauswahl (lane_nos gesetzt) wird NICHT gefiltert - dort ist die Auswahl
// bewusst, ein gerade offline gemeldeter Stand zeigt dann seinen letzten
// DB-Stand statt kommentarlos zu verschwinden.
func loadActiveLaneNos(ctx context.Context, pool *pgxpool.Pool) []int {
	rows, err := pool.Query(ctx, `
		SELECT lane_no FROM lanes
		WHERE active AND NOT virtual AND live_last_seen_at > now() - interval '10 seconds'
		ORDER BY lane_no`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var n int
		if rows.Scan(&n) == nil {
			out = append(out, n)
		}
	}
	return out
}

var standGridSizes = []int{4, 6, 9, 15}

func resolveGridSize(configured string, count int) int {
	if configured != "auto" && configured != "" {
		for _, g := range standGridSizes {
			if fmt.Sprintf("%d", g) == configured {
				return g
			}
		}
	}
	for _, g := range standGridSizes {
		if count <= g {
			return g
		}
	}
	return standGridSizes[len(standGridSizes)-1]
}

func chunkInts(list []int, size int) [][]int {
	if size <= 0 {
		size = 4
	}
	var out [][]int
	for i := 0; i < len(list); i += size {
		end := i + size
		if end > len(list) {
			end = len(list)
		}
		out = append(out, list[i:end])
	}
	if len(out) == 0 {
		out = [][]int{{}}
	}
	return out
}

// seriesSums fasst die Wertungsschuesse in Bloecken von shotsPerSeries
// zusammen (die letzte Serie ggf. noch unvollstaendig) und liefert davon nur
// die letzten maxDisplayedSeries - bei mehr Serien faellt die aelteste zuerst
// aus der Anzeige.
func seriesSums(match []standShot, shotsPerSeries int) []seriesSum {
	if shotsPerSeries <= 0 {
		shotsPerSeries = len(match)
	}
	var out []seriesSum
	for i := 0; i < len(match); i += shotsPerSeries {
		end := i + shotsPerSeries
		if end > len(match) {
			end = len(match)
		}
		sum := seriesSum{No: i/shotsPerSeries + 1}
		for _, s := range match[i:end] {
			if s.Ring != nil {
				sum.Rings += *s.Ring
			}
			if s.Decimal != nil {
				sum.Decimal += *s.Decimal
			}
		}
		out = append(out, sum)
	}
	if n := len(out); n > maxDisplayedSeries {
		out = out[n-maxDisplayedSeries:]
	}
	return out
}

func loadStandTile(ctx context.Context, pool *pgxpool.Pool, laneNo int) standTile {
	t := standTile{LaneNo: laneNo}

	var sessionID, targetID, targetName string
	var innerTenD float64
	var shotsPerSeries int
	err := pool.QueryRow(ctx, `
		SELECT se.id::text, COALESCE(sh.last_name||', '||sh.first_name,''),
		       d.name, d.decimal_scoring, d.shots_per_series, tg.id::text, tg.name, COALESCE(tg.inner_ten_d_mm,0)
		FROM lanes l
		JOIN sessions se ON se.lane_id = l.id AND se.status IN ('assigned','sighting','match','paused')
		JOIN disciplines d ON d.id = se.discipline_id
		JOIN targets tg ON tg.id = d.target_id
		LEFT JOIN shooters sh ON sh.id = se.shooter_id
		WHERE l.lane_no = $1
		ORDER BY se.started_at DESC NULLS LAST LIMIT 1`, laneNo,
	).Scan(&sessionID, &t.ShooterName, &t.Discipline, &t.DecimalScoring, &shotsPerSeries, &targetID, &targetName, &innerTenD)
	if err != nil {
		return t // kein aktiver Stand -> "Frei"
	}

	pool.QueryRow(ctx, `
		SELECT COALESCE(shot_count,0), COALESCE(total_rings,0), COALESCE(total_decimal,0)
		FROM v_session_results WHERE session_id=$1`, sessionID,
	).Scan(&t.ShotCount, &t.TotalRings, &t.TotalDecimal)

	// Alle Schuesse laden und je Schussart (Probe/Wertung) durchnummerieren -
	// dieselbe Konvention wie schussbild.go/pdf_einzelergebnis.go (Position
	// innerhalb der eigenen Schussart, nicht die rohe fortlaufende shot_no).
	var sighting, match []standShot
	srows, err := pool.Query(ctx, `
		SELECT kind::text,
		       COALESCE(corrected_x_mm,x_mm), COALESCE(corrected_y_mm,y_mm),
		       COALESCE(corrected_ring,ring), COALESCE(corrected_decimal_value,decimal_value)
		FROM shots WHERE session_id=$1 AND status IN ('valid','annulled','cross_shot_in')
		ORDER BY shot_no`, sessionID)
	if err == nil {
		for srows.Next() {
			var kind string
			var s standShot
			if srows.Scan(&kind, &s.XMM, &s.YMM, &s.Ring, &s.Decimal) == nil {
				if kind == "sighting" {
					s.No = len(sighting) + 1
					sighting = append(sighting, s)
				} else {
					s.No = len(match) + 1
					match = append(match, s)
				}
			}
		}
		srows.Close()
	}
	t.ProbeMode = len(match) == 0

	// Kachel zeigt die aktuelle (letzte, ggf. noch unvollstaendige) Serie -
	// vor dem ersten Wertungsschuss ersatzweise alle bisherigen Probeschuesse.
	if len(match) > 0 {
		spS := shotsPerSeries
		if spS <= 0 {
			spS = len(match)
		}
		seriesStart := ((len(match) - 1) / spS) * spS
		t.Shots = match[seriesStart:]
		t.Series = seriesSums(match, spS)
	} else {
		t.Shots = sighting
	}
	if n := len(t.Shots); n > 0 {
		last := t.Shots[n-1]
		t.LastShot = &last
	}

	cutoff := fillCutoffForTargetName(targetName)
	t.Geo.InnerTenD = innerTenD
	rrows, err := pool.Query(ctx, `SELECT ring_value, diameter_mm FROM target_rings WHERE target_id=$1 ORDER BY diameter_mm DESC`, targetID)
	if err == nil {
		var ring10D float64
		for rrows.Next() {
			var v int
			var d float64
			if rrows.Scan(&v, &d) == nil {
				t.Geo.Rings = append(t.Geo.Rings, ringGeoJSON{V: v, D: d, Filled: cutoff > 0 && v >= cutoff})
				if v == 10 {
					ring10D = d
				}
			}
		}
		rrows.Close()
		t.Geo.InnerTenDashed = innerTenD > 0 && ring10D > 0 && innerTenD < ring10D
	}
	return t
}

func fmtTotal(t standTile) string {
	if t.ShotCount == 0 {
		return "–"
	}
	if t.DecimalScoring {
		return fmt.Sprintf("%.1f", t.TotalDecimal)
	}
	return fmt.Sprintf("%d", t.TotalRings)
}

func fmtLastShot(t standTile) string {
	if t.LastShot == nil || t.LastShot.Ring == nil {
		return "–"
	}
	if t.DecimalScoring && t.LastShot.Decimal != nil {
		return fmt.Sprintf("%.1f", *t.LastShot.Decimal)
	}
	return fmt.Sprintf("%d", *t.LastShot.Ring)
}

// fmtSeriesRow: kompakte Kette kleiner Kaesten mit den Serienwerten (ohne
// Seriennummer), neueste Serie zuletzt - t.Series enthaelt bereits nur noch
// maximal maxDisplayedSeries Eintraege (aelteste zuerst verworfen, siehe
// seriesSums). Direkt hinter dem Gesamtergebnis platziert, keine eigene Zeile.
func fmtSeriesRow(t standTile) string {
	if len(t.Series) == 0 {
		return ""
	}
	chips := ""
	for _, sr := range t.Series {
		val := fmt.Sprintf("%d", sr.Rings)
		if t.DecimalScoring {
			val = fmt.Sprintf("%.1f", sr.Decimal)
		}
		chips += fmt.Sprintf(`<span class="s-chip">%s</span>`, val)
	}
	return `<span class="tile-series">` + chips + `</span>`
}

func renderStandTile(t standTile) string {
	if t.ShooterName == "" && t.ShotCount == 0 && len(t.Shots) == 0 && t.Discipline == "" {
		return fmt.Sprintf(`<div class="tile"><div class="lane-no">%d</div><div class="tile-empty">Frei</div></div>`, t.LaneNo)
	}

	geoJSON, _ := json.Marshal(t.Geo)
	shotsJSON, _ := json.Marshal(t.Shots)
	svgID := fmt.Sprintf("tgt-%d", t.LaneNo)

	triangle := ""
	if t.ProbeMode {
		triangle = `<div class="probe-flag" title="Probe-Modus"></div>`
	}

	return fmt.Sprintf(`<div class="tile">
  <div class="lane-no">%d</div>
  %s
  <div class="tile-target"><svg id="%s" viewBox="-30 -30 60 60"></svg></div>
  <div class="tile-info">
    <div class="tile-name-row">
      <span class="tile-name">%s</span>
      <span class="tile-last"><span class="s-chip s-chip-lg">%s</span></span>
    </div>
    <div class="tile-disc">%s</div>
    <div class="tile-scores"><span class="tile-total">%s</span>%s</div>
  </div>
  <script>drawStandTarget(%q, %s, %s);</script>
</div>`,
		t.LaneNo, triangle, svgID,
		html.EscapeString(t.ShooterName), fmtLastShot(t), html.EscapeString(t.Discipline),
		fmtTotal(t), fmtSeriesRow(t),
		svgID, geoJSON, shotsJSON)
}

func standanzeigeHead(gridSize int) string {
	cols, rows := 2, 2
	switch gridSize {
	case 6:
		cols, rows = 3, 2
	case 9:
		cols, rows = 3, 3
	case 15:
		cols, rows = 5, 3
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="de"><head><meta charset="UTF-8">
<title>Standanzeige</title>
<style>
  :root{--bg:#11151a;--panel:#1a2028;--line:#2a3340;--text:#d8e0e8;--dim:#6a7888}
  *{box-sizing:border-box;margin:0;padding:0}
  body{background:var(--bg);color:var(--text);font-family:system-ui,sans-serif;height:100vh;overflow:hidden}
  section.grid{display:none;grid-template-columns:repeat(%d,1fr);grid-template-rows:repeat(%d,1fr);
               gap:10px;height:100vh;padding:10px}
  section.grid.active{display:grid}
  .tile{background:var(--panel);border:1px solid var(--line);border-radius:8px;
        display:flex;flex-direction:column;min-height:0;position:relative;overflow:hidden}
  .lane-no{position:absolute;top:2px;left:8px;font-size:1.5em;font-weight:800;color:var(--text);
           text-shadow:0 1px 4px rgba(0,0,0,.85);z-index:2;line-height:1}
  .tile-empty{flex:1;display:flex;align-items:center;justify-content:center;
              color:var(--dim);font-size:1.1em}
  .tile-target{flex:1;min-height:0;width:100%%;display:flex;align-items:center;justify-content:center}
  .tile-target svg{width:100%%;height:100%%}
  .tile-info{flex-shrink:0;padding:2px 10px 8px}
  .tile-name-row{display:flex;align-items:baseline;gap:6px}
  .tile-name{flex:1;min-width:0;font-weight:700;font-size:1.05em;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
  .tile-disc{font-size:.8em;color:var(--dim);white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
  .tile-scores{display:flex;align-items:baseline;gap:6px;margin-top:2px}
  .tile-total{font-size:1.6em;font-weight:700;color:#4ab8a0}
  .tile-last{flex-shrink:0;white-space:nowrap}
  .tile-series{display:flex;flex-wrap:wrap;gap:3px;overflow:hidden;max-height:2.6em}
  .s-chip{font-size:.85em;font-weight:700;color:var(--text);background:var(--line);
    border-radius:4px;padding:1px 5px;white-space:nowrap}
  .s-chip-lg{font-size:1.7em;padding:2px 10px;border-radius:6px}
  .probe-flag{position:absolute;top:0;right:0;width:0;height:0;
    border-style:solid;border-width:0 32px 32px 0;border-color:transparent #888 transparent transparent;
    opacity:.5;z-index:2}
</style>
<script>
// Muss VOR den Kachel-Markup stehen (siehe renderStandTile: jede Kachel ruft
// drawStandTarget() direkt in einem eigenen <script>-Tag auf, sobald der
// Parser dort ankommt) - stand die Funktion frueher in standanzeigeFoot
// (also NACH allen Kacheln), war sie beim ersten Aufruf noch undefiniert
// (ReferenceError, kein einziges Trefferbild wurde gezeichnet). Bugfix:
// "anzeige/1 zeigt keine Grafiken".
function contrastColor(hex) {
  const r = parseInt(hex.slice(1,3),16), g = parseInt(hex.slice(3,5),16), b = parseInt(hex.slice(5,7),16);
  return (0.299*r + 0.587*g + 0.114*b) / 255 > 0.5 ? '#000000' : '#FFFFFF';
}
function shotColorByRing(s) {
  if (s.ring == null) return '#666666';
  if (s.ring >= 10) return '#e04040';
  if (s.ring === 9)  return '#f3fb06';
  return '#4ab8ff';
}
const STAND_ZOOM_FRACTIONS = [0.0626, 0.2418, 0.4209, 0.60, 1.00];
// Kaliber in mm - reale Groesse des Trefferpunkts (wie schussbild.go CALIBER),
// skaliert also automatisch mit der Scheibengeometrie statt einem fixen
// Bruchteil des Aussenrings (der bei kleinem Zoom viel zu klein wirkte).
const STAND_CALIBER = 4.5;
const STAND_SHOT_STROKE_W = STAND_CALIBER * 0.05;
const STAND_SHOT_R = STAND_CALIBER / 2 - STAND_SHOT_STROKE_W / 2;

function drawStandTarget(svgID, geo, shots) {
  const svg = document.getElementById(svgID);
  if (!svg || !geo || !geo.rings || !geo.rings.length) return;
  shots = shots || [];
  const NS = 'http://www.w3.org/2000/svg';
  const rings = [...geo.rings].sort((a,b) => b.d - a.d);
  const outerR = rings[0].d / 2;

  // Auto-Zoom analog der Auswertungsansicht (schussbild.go computeViewR):
  // kleinste feste Zoomstufe waehlen, die alle gezeigten Schuesse noch zeigt.
  // Ohne Schuss (noch nichts gefallen) volle Scheibe zeigen statt auf die
  // kleinste Stufe (0) zu "zoomen".
  let viewR = outerR;
  let maxDist = 0;
  let hasShot = false;
  for (const s of shots) {
    if (s.x_mm == null || s.y_mm == null) continue;
    hasShot = true;
    maxDist = Math.max(maxDist, Math.sqrt(s.x_mm*s.x_mm + s.y_mm*s.y_mm));
  }
  if (hasShot) {
    for (const f of STAND_ZOOM_FRACTIONS) {
      const r = f * outerR;
      if (r >= maxDist) { viewR = r; break; }
    }
  }
  const pad = Math.max(viewR * 0.08, outerR * 0.02);
  svg.setAttribute('viewBox', (-(viewR+pad))+' '+(-(viewR+pad))+' '+(2*(viewR+pad))+' '+(2*(viewR+pad)));

  const bg = document.createElementNS(NS,'circle');
  bg.setAttribute('r', outerR); bg.setAttribute('fill', '#e8e4dc');
  svg.appendChild(bg);

  let darkR = 0;
  for (const r of rings) if (r.filled) darkR = Math.max(darkR, r.d/2);
  if (darkR > 0) {
    const sp = document.createElementNS(NS,'circle');
    sp.setAttribute('r', darkR); sp.setAttribute('fill', '#1c1c1c');
    svg.appendChild(sp);
  }
  const strokeW = Math.max(outerR*0.006, 0.15);
  for (const r of rings) {
    const c = document.createElementNS(NS,'circle');
    c.setAttribute('r', r.d/2); c.setAttribute('fill','none');
    c.setAttribute('stroke', r.filled ? '#888' : '#555'); c.setAttribute('stroke-width', strokeW);
    svg.appendChild(c);
  }
  if (geo.inner10_d > 0) {
    const inn = document.createElementNS(NS,'circle');
    inn.setAttribute('r', geo.inner10_d/2); inn.setAttribute('fill','none');
    inn.setAttribute('stroke','#888'); inn.setAttribute('stroke-width', strokeW);
    if (geo.inner10_dashed) { const dl = strokeW*5; inn.setAttribute('stroke-dasharray', dl+' '+dl); }
    svg.appendChild(inn);
  }

  // Ringzahlen (analog schussbild.go buildTarget): je Ring 1-9 an vier
  // Positionen (N/O/S/W) auf der Mitte zwischen diesem und dem naechst
  // inneren Ring beschriftet.
  let minStep = Infinity;
  for (let i = 0; i < rings.length - 1; i++) minStep = Math.min(minStep, (rings[i].d - rings[i+1].d) / 2);
  const labelFontSize = Math.max(minStep * 0.58, outerR * 0.045);
  const labelPositions = [{nx:0,ny:-1},{nx:1,ny:0},{nx:0,ny:1},{nx:-1,ny:0}];
  for (let i = 0; i < rings.length; i++) {
    const r = rings[i];
    if (r.v < 1 || r.v > 9) continue;
    const outerRadius = r.d / 2;
    const innerRadius = (i + 1 < rings.length) ? rings[i + 1].d / 2 : 0;
    const labelR = (outerRadius + innerRadius) / 2;
    const fillColor = r.filled ? '#bbb' : '#555';
    for (const p of labelPositions) {
      const t = document.createElementNS(NS,'text');
      t.setAttribute('x', p.nx * labelR); t.setAttribute('y', p.ny * labelR);
      t.setAttribute('text-anchor', 'middle');
      t.setAttribute('dominant-baseline', 'middle');
      t.setAttribute('font-size', labelFontSize);
      t.setAttribute('font-family', 'sans-serif');
      t.setAttribute('fill', fillColor);
      t.setAttribute('pointer-events', 'none');
      t.textContent = r.v;
      svg.appendChild(t);
    }
  }

  for (const s of shots) {
    if (s.x_mm == null || s.y_mm == null) continue;
    const fillColor = shotColorByRing(s);
    const g = document.createElementNS(NS,'g');

    const c = document.createElementNS(NS,'circle');
    c.setAttribute('cx', s.x_mm); c.setAttribute('cy', -s.y_mm);
    c.setAttribute('r', STAND_SHOT_R);
    c.setAttribute('fill', fillColor); c.setAttribute('fill-opacity','0.85');
    c.setAttribute('stroke', '#1a1a1a'); c.setAttribute('stroke-width', STAND_SHOT_STROKE_W);
    g.appendChild(c);

    const t = document.createElementNS(NS,'text');
    t.setAttribute('x', s.x_mm); t.setAttribute('y', -s.y_mm);
    t.setAttribute('text-anchor', 'middle');
    t.setAttribute('dominant-baseline', 'central');
    t.setAttribute('font-size', STAND_CALIBER*0.42);
    t.setAttribute('font-family', 'sans-serif');
    t.setAttribute('font-weight', 'bold');
    t.setAttribute('fill', contrastColor(fillColor));
    t.textContent = String(s.no);
    g.appendChild(t);

    svg.appendChild(g);
  }
}
</script>
</head><body>
`, cols, rows)
}

// standanzeigeFoot: Rotations-/Reload-Skript. Muss NACH allen Kacheln
// stehen, weil es per document.querySelectorAll('section.grid') auf das
// bereits gerenderte Markup zugreift (anders als die Funktionsdefinitionen
// in standanzeigeHead, die vor den Kachel-Aufrufen stehen muessen).
const standanzeigeFoot = `
<script>
const sections = document.querySelectorAll('section.grid');
let idx = 0;
if (sections.length > 1) {
  setInterval(() => {
    sections[idx].classList.remove('active');
    idx = (idx + 1) %% sections.length;
    sections[idx].classList.add('active');
  }, %d * 1000);
}
setTimeout(() => location.reload(), %d * 1000);
</script>
</body></html>`
