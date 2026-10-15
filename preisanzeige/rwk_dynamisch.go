// ============================================================================
// rwk_dynamisch.go – Anzeige "RWK-Dynamisch" (type='rwk_dynamisch' in
// anzeige_config):
//
//   - links (ca. 80-85 % der Breite) dynamisch alle mit Schuetzen belegten
//     Staende (aktive Session), Raster passend zur Anzahl, bei mehr Staenden
//     als ein Raster fasst im Wechsel. Je Kachel: Name, Trefferbild der
//     laufenden Serie, letzter Schuss, Serien, Gesamt, Schusszahl,
//     Hochrechnung (HR) und - falls die Disziplin eine Schiesszeit hat - die
//     verbleibende Zeit. Gehoert der Schuetze zu einem Rundenwettkampf, traegt
//     die Kachel dessen Paarung und die Mannschaftsfarbe (Heim/Gast).
//   - rechts (ca. 15-20 %) Uhr und die heute laufenden Rundenwettkaempfe
//     (Datum heute, Status "laufend") mit Stand und Hochrechnung je
//     Mannschaft; mehrere Wettkaempfe wechseln im Takt rotate_seconds.
//
// Die Seite laedt nur einmal; danach holt sie alle refresh_seconds die
// Inhalte neu (?partial=1, JSON mit beiden HTML-Teilen) - ohne Neuladen und
// damit ohne Flackern. Welcher Wettkampf bzw. welche Standgruppe sichtbar ist,
// ergibt sich aus der Uhrzeit, ein Datenabruf unterbricht den Wechsel nicht.
// ============================================================================
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type rwkDynParams struct {
	LaneNos         []int `json:"lane_nos"`          // leer = alle belegten Staende
	RotateSeconds   int   `json:"rotate_seconds"`    // Wechsel der Rundenwettkaempfe (Default 10)
	LanePageSeconds int   `json:"lane_page_seconds"` // Wechsel der Standgruppen (Default 10)
	RefreshSeconds  int   `json:"refresh_seconds"`   // Datenabruf (Default 3)
	SidePercent     int   `json:"side_percent"`      // Breite der rechten Spalte in % (Default 18)
	// TeamColors: Mannschaftsfarben "#rrggbb" in der Reihenfolge Paarung 1
	// Heim, Paarung 1 Gast, Paarung 2 Heim, ... (bis 5 Paarungen) - fehlende
	// oder ungueltige Eintraege nehmen die Standardfarbe.
	TeamColors []string `json:"team_colors"`
}

// rwkDefaultTeamColors: gut unterscheidbare Standardfarben fuer bis zu 5
// gleichzeitige Paarungen (Heim, Gast je Paarung).
var rwkDefaultTeamColors = []string{
	"#e8622c", "#2f6fe0", // Paarung 1: orange, blau
	"#c0258f", "#1f9e6e", // Paarung 2: magenta, gruen
	"#8e44ad", "#d4a017", // Paarung 3: violett, gold
	"#c0392b", "#1aa3b8", // Paarung 4: rot, tuerkis
	"#7f8c3d", "#a0522d", // Paarung 5: oliv, braun
}

// palette: Mannschaftsfarben dieser Anzeige (konfiguriert, sonst Standard).
func (p rwkDynParams) palette() []string {
	out := append([]string{}, rwkDefaultTeamColors...)
	for i, c := range p.TeamColors {
		var r, g, b int
		if i < len(out) && len(c) == 7 {
			if _, err := fmt.Sscanf(c, "#%02x%02x%02x", &r, &g, &b); err == nil {
				out[i] = c
			}
		}
	}
	return out
}

func (p *rwkDynParams) defaults() {
	if p.RotateSeconds <= 0 {
		p.RotateSeconds = 10
	}
	if p.LanePageSeconds <= 0 {
		p.LanePageSeconds = 10
	}
	if p.RefreshSeconds <= 0 {
		p.RefreshSeconds = 3
	}
	if p.SidePercent < 10 || p.SidePercent > 40 {
		p.SidePercent = 18
	}
}

// rwkDynMaxPerPage: mehr belegte Staende wechseln in Gruppen.
const rwkDynMaxPerPage = 16

// rwkColorNeutral: Kachelkopf ohne Rundenwettkampf (keiner Mannschaft zugeordnet).
const rwkColorNeutral = "#4a5866"

// rwkColors: Farbe je Mannschaft der heute laufenden Rundenwettkaempfe -
// Paarung i (Reihenfolge wie in der rechten Spalte) bekommt die Farben 2i
// (Heim) und 2i+1 (Gast); so tragen Kachel und rechte Spalte dieselbe Farbe.
type rwkColors struct {
	byTeam  map[string]string
	palette []string
}

func (c rwkColors) pair(i int) (string, string) {
	n := len(c.palette)
	return c.palette[(2*i)%n], c.palette[(2*i+1)%n]
}

// rwkTileExtra: Zusatzangaben einer Kachel neben standTile.
type rwkTileExtra struct {
	MatchShots  int
	DeadlineMs  int64  // Ende der Schiesszeit (Unix ms), 0 = keine Zeitbegrenzung
	TimeMode    string // soft | hard (Farbe der Restzeit, soft wird negativ)
	Label       string // Wettkampf-Zeile im Trefferbild
	HeaderColor string
	Dot         string // Paarungswettkampf: "lead" (gruen) / "trail" (grau) / ""
}

func renderRwkDynamisch(w http.ResponseWriter, r *http.Request, ctx context.Context, pool *pgxpool.Pool, slot anzeigeSlot) {
	var p rwkDynParams
	_ = json.Unmarshal(slot.Params, &p)
	p.defaults()

	events := loadRunningRwks(ctx, pool)
	colors := rwkColors{byTeam: map[string]string{}, palette: p.palette()}
	for i, ev := range events {
		h, g := colors.pair(i)
		ids := loadRwkTeamIDs(ctx, pool, ev.ID)
		if ids[0] != "" && colors.byTeam[ids[0]] == "" {
			colors.byTeam[ids[0]] = h
		}
		if ids[1] != "" && colors.byTeam[ids[1]] == "" {
			colors.byTeam[ids[1]] = g
		}
	}
	lanesHTML, groups := rwkDynLanesHTML(ctx, pool, p, colors)
	rwkHTML, nRwk := rwkDynSideHTML(ctx, pool, events, colors)

	if r.URL.Query().Get("partial") == "1" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"lanes": lanesHTML, "rwk": rwkHTML, "groups": groups, "rwk_count": nRwk})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	writeRwkDynPage(w, slot, p, lanesHTML, rwkHTML)
}

// loadOccupiedLaneNos: Staende mit aktiver Session (Schuetze zugewiesen,
// Probe, Wertung oder Pause), aufsteigend.
func loadOccupiedLaneNos(ctx context.Context, pool *pgxpool.Pool) []int {
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT l.lane_no FROM lanes l
		JOIN sessions se ON se.lane_id = l.id AND se.status IN ('assigned','sighting','match','paused')
		WHERE l.active AND NOT l.virtual
		ORDER BY l.lane_no`)
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

// rwkDynGrid: Spalten/Zeilen fuer n Kacheln in der (etwa 3:2 breiten)
// linken Flaeche.
func rwkDynGrid(n int) (cols, rows int) {
	switch {
	case n <= 1:
		return 1, 1
	case n == 2:
		return 2, 1
	case n <= 4:
		return 2, 2
	case n <= 6:
		return 3, 2
	case n <= 9:
		return 3, 3
	case n <= 12:
		return 4, 3
	}
	return 4, 4
}

// loadRwkTeamIDs: Heim- und Gast-Mannschaft eines Rundenwettkampfs.
func loadRwkTeamIDs(ctx context.Context, pool *pgxpool.Pool, eventID string) [2]string {
	var ids [2]string
	rows, err := pool.Query(ctx, `
		SELECT COALESCE(team_id::text,''), sort_order FROM competition_participants
		WHERE event_id = $1::uuid ORDER BY sort_order LIMIT 2`, eventID)
	if err != nil {
		return ids
	}
	defer rows.Close()
	k := 0
	for rows.Next() && k < 2 {
		var so int
		if rows.Scan(&ids[k], &so) == nil {
			k++
		}
	}
	return ids
}

func rwkDynLanesHTML(ctx context.Context, pool *pgxpool.Pool, p rwkDynParams, colors rwkColors) (string, int) {
	occupied := loadOccupiedLaneNos(ctx, pool)
	laneNos := occupied
	if len(p.LaneNos) > 0 {
		sel := map[int]bool{}
		for _, n := range p.LaneNos {
			sel[n] = true
		}
		laneNos = nil
		for _, n := range occupied {
			if sel[n] {
				laneNos = append(laneNos, n)
			}
		}
	}
	if len(laneNos) == 0 {
		return `<div class="lanes-empty">Zurzeit ist kein Stand belegt.</div>`, 1
	}
	perPage := len(laneNos)
	if perPage > rwkDynMaxPerPage {
		perPage = rwkDynMaxPerPage
	}
	cols, rows := rwkDynGrid(perPage)
	groups := chunkInts(laneNos, cols*rows)
	var b strings.Builder
	for gi, g := range groups {
		fmt.Fprintf(&b, `<div class="lane-page" data-page="%d" style="grid-template-columns:repeat(%d,1fr);grid-template-rows:repeat(%d,1fr)">`, gi, cols, rows)
		for _, no := range g {
			t := loadStandTile(ctx, pool, no)
			x := loadRwkTileExtra(ctx, pool, t.SessionID, colors)
			b.WriteString(renderRwkTile(t, x))
		}
		b.WriteString(`</div>`)
	}
	return b.String(), len(groups)
}

// loadRwkTileExtra: Schusszahl der Disziplin, Schiesszeit und - bei einem
// Rundenwettkampf - Paarung und Seite (Heim/Gast) des Schuetzen.
func loadRwkTileExtra(ctx context.Context, pool *pgxpool.Pool, sessionID string, colors rwkColors) rwkTileExtra {
	x := rwkTileExtra{HeaderColor: rwkColorNeutral}
	if sessionID == "" {
		return x
	}
	var matchTime, sightTime *int
	var combined bool
	var bonus int
	var started, matchStarted *time.Time
	var evType, evName, evID, teamID, discName string
	var stechenNo int
	var switchMode string
	var timeStarted *time.Time
	err := pool.QueryRow(ctx, `
		SELECT d.match_shot_count, d.match_time_s, d.sighting_time_s, d.combined_time,
		       se.time_bonus_s, COALESCE(se.time_started_at, se.started_at), d.name,
		       se.match_started_at, COALESCE(e.time_mode, 'soft'), COALESCE(se.stechen_no, 0),
		       COALESCE(e.switch_mode, 'individuell'), se.time_started_at,
		       COALESCE(e.type,''), COALESCE(e.name,''), COALESCE(e.id::text,''),
		       COALESCE(st.team_id::text, st2.team_id::text, '')
		FROM sessions se
		JOIN disciplines d ON d.id = se.discipline_id
		LEFT JOIN events e ON e.id = se.event_id
		LEFT JOIN starters st ON st.id = se.starter_id
		LEFT JOIN LATERAL (
			SELECT team_id FROM starters s2
			WHERE se.starter_id IS NULL AND s2.event_id = se.event_id AND s2.shooter_id = se.shooter_id
			LIMIT 1) st2 ON TRUE
		WHERE se.id = $1::uuid`, sessionID).Scan(&x.MatchShots, &matchTime, &sightTime, &combined,
		&bonus, &started, &discName, &matchStarted, &x.TimeMode, &stechenNo, &switchMode, &timeStarted, &evType, &evName, &evID, &teamID)
	if err != nil {
		return x
	}
	// Schiesszeit wie am Stand (standpc/timing.go): bei combined_time eine
	// gemeinsame Zeit (match_time_s) ab dem Start, sonst erst die Probezeit,
	// die Wertungszeit ab deren Ablauf bzw. dem frueheren Wechsel in die
	// Wertung; dazu eine Zeitgutschrift. Ohne Zeit in der Disziplin oder im
	// Zeitmodus "uhrzeit" keine Restzeit.
	zentral := switchMode == "zentral" && !combined && sightTime != nil && *sightTime > 0
	if zentral {
		started = timeStarted // zentral: nur ab "Zeit starten"
	}
	if matchTime != nil && *matchTime > 0 && started != nil && x.TimeMode != "uhrzeit" && stechenNo == 0 {
		total := time.Duration(*matchTime+bonus) * time.Second
		if combined || sightTime == nil {
			x.DeadlineMs = started.Add(total).UnixMilli()
		} else {
			probeEnd := started.Add(time.Duration(*sightTime) * time.Second)
			wStart := probeEnd
			if matchStarted != nil && (zentral || matchStarted.Before(probeEnd)) {
				wStart = *matchStarted // zentral: Wertungszeit ab der Freigabe
			}
			if zentral && matchStarted == nil && !time.Now().Before(probeEnd) {
				// Probezeit vorbei, Wertung noch nicht freigegeben: keine Restzeit
			} else if matchStarted == nil && time.Now().Before(probeEnd) {
				x.DeadlineMs = probeEnd.UnixMilli() // noch Probezeit
			} else {
				x.DeadlineMs = wStart.Add(total).UnixMilli()
			}
		}
	}
	short := discShort(discName)
	switch {
	case evType == "runde":
		var heim, gast, heimID, gastID string
		rows, err := pool.Query(ctx, `
			SELECT COALESCE(t.id::text,''), COALESCE(t.name,''), cp.sort_order
			FROM competition_participants cp LEFT JOIN teams t ON t.id = cp.team_id
			WHERE cp.event_id = $1::uuid ORDER BY cp.sort_order LIMIT 2`, evID)
		if err == nil {
			for rows.Next() {
				var id, name string
				var so int
				if rows.Scan(&id, &name, &so) == nil {
					if so == 0 {
						heim, heimID = name, id
					} else {
						gast, gastID = name, id
					}
				}
			}
			rows.Close()
		}
		// Disziplin vorne - lange Mannschaftsnamen werden am Ende abgeschnitten
		x.Label = fmt.Sprintf("RWK %s: %s – %s", short, nz(heim, "Heim"), nz(gast, "Gast"))
		// Farbe wie in der rechten Spalte; laeuft der Wettkampf (noch) nicht
		// als "laufend", die Farben der ersten Paarung
		if c, ok := colors.byTeam[teamID]; ok && teamID != "" {
			x.HeaderColor = c
		} else {
			h, g := colors.pair(0)
			switch teamID {
			case heimID:
				x.HeaderColor = h
			case gastID:
				x.HeaderColor = g
			}
		}
	case evName != "":
		x.Label = short + ": " + evName
	}
	return x
}

// discShort: Disziplin kurz fuer die Wettkampf-Zeile (LG/LP/...).
func discShort(name string) string {
	u := strings.ToUpper(name)
	for _, k := range []string{"LG", "LP", "KK", "ZS", "SP"} {
		if strings.HasPrefix(u, k) {
			return k
		}
	}
	return name
}

// hochrechnung: erwartetes Endergebnis = bisheriger Schnitt x Schusszahl.
func hochrechnung(total float64, shots, matchShots int) (float64, bool) {
	if shots <= 0 || matchShots <= 0 {
		return 0, false
	}
	if shots >= matchShots {
		return total, true
	}
	return total / float64(shots) * float64(matchShots), true
}

func renderRwkTile(t standTile, x rwkTileExtra) string {
	if t.ShooterName == "" && t.Discipline == "" {
		return fmt.Sprintf(`<div class="rtile"><div class="rhead" style="background:rgba(42,51,64,.5)"><span class="rno">%d</span> Frei</div></div>`, t.LaneNo)
	}
	geoJSON, _ := json.Marshal(t.Geo)
	shotsJSON, _ := json.Marshal(t.Shots)

	total := float64(t.TotalRings)
	if t.DecimalScoring {
		total = t.TotalDecimal
	}
	hr := ""
	if v, ok := hochrechnung(total, t.ShotCount, x.MatchShots); ok && !t.ProbeMode {
		hr = fmt.Sprintf(`<span class="hr">HR: %s</span>`, fmtScore(v, t.DecimalScoring))
	}
	series := ""
	for _, sr := range t.Series {
		if t.DecimalScoring {
			series += fmt.Sprintf(`<span>%.1f</span>`, sr.Decimal)
		} else {
			series += fmt.Sprintf(`<span>%d</span>`, sr.Rings)
		}
	}
	count := ""
	if x.MatchShots > 0 {
		count = fmt.Sprintf("%d / %d", t.ShotCount, x.MatchShots)
	}
	if t.ProbeMode {
		count = "Probe"
	}
	labelCls := "rlabel-text"
	if t.StechenNo > 0 {
		// Stechschuss: eigener Hinweis statt Restzeit, Gesamt bleibt das der Wertung
		x.Label = strings.TrimSuffix(fmt.Sprintf("Stechschuss %d · %s", t.StechenNo, x.Label), " · ")
		x.DeadlineMs = 0
		labelCls += " stechen"
		count = fmt.Sprintf("Stechschuss %d", t.StechenNo)
	}
	label := ""
	if x.Label != "" || x.DeadlineMs > 0 {
		timer := ""
		if x.DeadlineMs > 0 {
			timer = fmt.Sprintf(`<span class="timer %s" data-deadline="%d"></span>`, html.EscapeString(x.TimeMode), x.DeadlineMs)
		}
		text := ""
		if x.Label != "" { // ohne Text nur die Restzeit, kein leerer Kasten
			text = fmt.Sprintf(`<span class="%s">%s</span>`, labelCls, html.EscapeString(x.Label))
		}
		label = fmt.Sprintf(`<div class="rlabel">%s%s</div>`, text, timer)
	}
	return fmt.Sprintf(`<div class="rtile">%s
  <div class="rhead" style="background:%s"><span class="rno">%d</span> <span class="rname">%s</span></div>
  <div class="rtarget"><svg class="tgt" data-geo='%s' data-shots='%s' viewBox="-30 -30 60 60"></svg>%s</div>
  <div class="rinfo">
    <div class="rlast">%s</div>
    <div class="rseries">%s</div>
    <div class="rhr">%s</div>
    <div class="rtotal">%s<span class="rcount">%s</span></div>
  </div>
</div>`,
		pairDot(x.Dot), hexAlpha(x.HeaderColor, 0.5), t.LaneNo, html.EscapeString(t.ShooterName),
		html.EscapeString(string(geoJSON)), html.EscapeString(string(shotsJSON)), label,
		fmtLastShot(t), series, hr, fmtTotal(t), count)
}

// pairDot: Punkt oben rechts im Trefferbild (Paarungswettkampf).
func pairDot(dot string) string {
	if dot == "" {
		return ""
	}
	return `<span class="pdot ` + dot + `"></span>`
}

// hexAlpha: "#rrggbb" -> "rgba(r,g,b,a)" (halbtransparente Namensbalken).
func hexAlpha(hex string, a float64) string {
	var r, g, b int
	if _, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b); err != nil {
		return hex
	}
	return fmt.Sprintf("rgba(%d,%d,%d,%.2f)", r, g, b, a)
}

func fmtScore(v float64, decimal bool) string {
	if decimal {
		return fmt.Sprintf("%.1f", v)
	}
	return fmt.Sprintf("%.0f", v)
}

// ── rechte Spalte: laufende Rundenwettkaempfe ────────────────────────────

type rwkSideTeam struct {
	Name    string
	Club    string
	Total   float64
	HR      float64
	Color   string
	Started bool
	Rows    []rwkShooterRow
	Paarung bool // Paarungswettkampf: Points statt Ringe gross anzeigen
	Points  int
}

// rwkShooterRow: Einzelergebnis eines Starters in der rechten Spalte - aus
// der Starterliste des Wettkampfs, bleibt also auch nach dem Ende stehen.
type rwkShooterRow struct {
	Name      string
	Disc      string
	Role      string
	Decimal   bool
	ShotCount int
	Total     float64
	Series    []float64
}

// loadRwkShooterRows: Starter je Mannschaft mit Serien und Gesamt ihrer
// besten Session (dieselbe Auswahl wie loadRundeEntries), Gewertete nach
// Ergebnis absteigend, AK zuletzt.
func loadRwkShooterRows(ctx context.Context, pool *pgxpool.Pool, eventID string) map[string][]rwkShooterRow {
	out := map[string][]rwkShooterRow{}
	rows, err := pool.Query(ctx, `
		SELECT sh.first_name || ' ' || sh.last_name, COALESCE(st.team_id::text,''), COALESCE(st.role,'S'),
		       COALESCE(d.name, de.name, ''), COALESCE(d.decimal_scoring, de.decimal_scoring, FALSE),
		       COALESCE(best.session_id::text,''), COALESCE(best.shot_count,0),
		       COALESCE(best.total_rings,0), COALESCE(best.total_decimal,0)
		FROM starters st
		JOIN shooters sh ON sh.id = st.shooter_id
		LEFT JOIN disciplines d ON d.id = st.discipline_id
		LEFT JOIN events e ON e.id = st.event_id
		LEFT JOIN disciplines de ON de.id = e.discipline_id
		LEFT JOIN LATERAL (
			SELECT se.id AS session_id, COALESCE(vsr.shot_count,0) AS shot_count,
			       COALESCE(vsr.total_rings,0) AS total_rings, COALESCE(vsr.total_decimal,0.0) AS total_decimal
			FROM sessions se
			LEFT JOIN v_session_results vsr ON vsr.session_id = se.id
			WHERE (se.starter_id = st.id
			    OR (se.event_id = $1::uuid AND se.shooter_id = st.shooter_id AND se.status::text <> 'aborted'))
			  AND se.stechen_no IS NULL
			ORDER BY COALESCE(vsr.shot_count,0) DESC, se.finished_at DESC NULLS LAST
			LIMIT 1
		) best ON TRUE
		WHERE st.event_id = $1::uuid`, eventID)
	if err != nil {
		return out
	}
	type tmp struct {
		team, session string
		row           rwkShooterRow
	}
	var all []tmp
	for rows.Next() {
		var x tmp
		var rings int
		var dec float64
		if rows.Scan(&x.row.Name, &x.team, &x.row.Role, &x.row.Disc, &x.row.Decimal, &x.session, &x.row.ShotCount, &rings, &dec) == nil {
			x.row.Total = float64(rings)
			if x.row.Decimal {
				x.row.Total = dec
			}
			x.row.Disc = discShort(x.row.Disc)
			all = append(all, x)
		}
	}
	rows.Close()
	for i := range all {
		if all[i].session == "" || all[i].row.ShotCount == 0 {
			continue
		}
		srows, err := pool.Query(ctx, `
			SELECT COALESCE(rings,0), COALESCE(decimal_total,0) FROM v_series_results
			WHERE session_id = $1::uuid AND series_no IS NOT NULL ORDER BY series_no`, all[i].session)
		if err != nil {
			continue
		}
		for srows.Next() {
			var r int64
			var d float64
			if srows.Scan(&r, &d) == nil {
				if all[i].row.Decimal {
					all[i].row.Series = append(all[i].row.Series, d)
				} else {
					all[i].row.Series = append(all[i].row.Series, float64(r))
				}
			}
		}
		srows.Close()
	}
	for _, x := range all {
		out[x.team] = append(out[x.team], x.row)
	}
	for k := range out {
		list := out[k]
		sort.SliceStable(list, func(i, j int) bool {
			ai, aj := list[i].Role == "AK", list[j].Role == "AK"
			if ai != aj {
				return !ai
			}
			return list[i].Total > list[j].Total
		})
	}
	return out
}

// loadRunningRwks: heute laufende Rundenwettkaempfe (Status "running", Datum
// heute innerhalb starts_on..ends_on).
func loadRunningRwks(ctx context.Context, pool *pgxpool.Pool) []struct{ ID, Name string } {
	rows, err := pool.Query(ctx, `
		SELECT id::text, name FROM events
		WHERE type = 'runde' AND status = 'running' AND active
		  AND starts_on IS NOT NULL AND starts_on <= current_date
		  AND COALESCE(ends_on, starts_on) >= current_date
		ORDER BY name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []struct{ ID, Name string }
	for rows.Next() {
		var e struct{ ID, Name string }
		if rows.Scan(&e.ID, &e.Name) == nil {
			out = append(out, e)
		}
	}
	return out
}

// rwkTeamHR: Hochrechnung einer Mannschaft - je gewertetem Schuetzen (S/E,
// wie in der Rundenwettkampf-Wertung die besten bestOf nach Hochrechnung,
// 0 = alle) Schnitt x Schusszahl; Schuetzen ohne Schuss zaehlen noch 0.
func rwkTeamHR(entries []rundeEntry, teamID string, isDecimal bool, matchShots map[string]int, bestOf int) float64 {
	var hrs []float64
	for _, e := range entries {
		if e.TeamID != teamID || e.Role == "AK" {
			continue
		}
		v, ok := hochrechnung(e.value(isDecimal), e.ShotCount, matchShots[e.LastName+"\x00"+e.FirstName])
		if !ok {
			v = 0
		}
		hrs = append(hrs, v)
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(hrs)))
	if bestOf > 0 && len(hrs) > bestOf {
		hrs = hrs[:bestOf]
	}
	sum := 0.0
	for _, v := range hrs {
		sum += v
	}
	return sum
}

func rwkDynSideHTML(ctx context.Context, pool *pgxpool.Pool, events []struct{ ID, Name string }, colors rwkColors) (string, int) {
	if len(events) == 0 {
		return `<div class="rwk-empty">Heute läuft kein Rundenwettkampf.</div>`, 0
	}
	var b strings.Builder
	for i, ev := range events {
		var teams [2]rwkSideTeam
		var ids [2]string
		rows, err := pool.Query(ctx, `
			SELECT COALESCE(t.id::text,''), COALESCE(t.name,''), COALESCE(c.name,''), cp.sort_order
			FROM competition_participants cp
			LEFT JOIN teams t ON t.id = cp.team_id
			LEFT JOIN clubs c ON c.id = t.club_id
			WHERE cp.event_id = $1::uuid ORDER BY cp.sort_order LIMIT 2`, ev.ID)
		if err == nil {
			k := 0
			for rows.Next() && k < 2 {
				var so int
				if rows.Scan(&ids[k], &teams[k].Name, &teams[k].Club, &so) == nil {
					k++
				}
			}
			rows.Close()
		}
		teams[0].Color, teams[1].Color = colors.pair(i)

		entries := loadRundeEntries(ctx, pool, ev.ID)
		matchShots := loadStarterMatchShots(ctx, pool, ev.ID)
		isDecimal := false
		for _, e := range entries {
			if e.DecimalScoring {
				isDecimal = true
			}
		}
		bestOf := loadBestOf(ctx, pool, ev.ID)
		for k := 0; k < 2; k++ {
			_, teams[k].Total = teamDisplayList(entries, ids[k], isDecimal, bestOf)
			teams[k].HR = rwkTeamHR(entries, ids[k], isDecimal, matchShots, bestOf)
			for _, e := range entries {
				if e.TeamID == ids[k] && e.ShotCount > 0 {
					teams[k].Started = true
				}
			}
		}
		if loadRundeModus(ctx, pool, ev.ID) == "paarung" {
			pr := computeRundePaarung(entries, ids[0], ids[1], isDecimal, matchShots)
			teams[0].Paarung, teams[1].Paarung = true, true
			teams[0].Points, teams[1].Points = pr.HeimPts, pr.GastPts
		}
		rowsByTeam := loadRwkShooterRows(ctx, pool, ev.ID)
		teams[0].Rows, teams[1].Rows = rowsByTeam[ids[0]], rowsByTeam[ids[1]]
		b.WriteString(renderRwkCard(i, ev.Name, teams, isDecimal))
	}
	if len(events) > 1 {
		b.WriteString(`<div class="rwk-dots">`)
		for i := range events {
			fmt.Fprintf(&b, `<span data-idx="%d"></span>`, i)
		}
		b.WriteString(`</div>`)
	}
	return b.String(), len(events)
}

// renderRwkCard: ein Rundenwettkampf in der rechten Spalte.
func renderRwkCard(i int, name string, teams [2]rwkSideTeam, isDecimal bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<div class="rwk-card" data-idx="%d"><div class="rwk-title">%s</div>`, i, html.EscapeString(name))
	for k := 0; k < 2; k++ {
		t := teams[k]
		diffTxt := ""
		if t.Started || teams[1-k].Started {
			diff := t.HR - teams[1-k].HR
			if isDecimal {
				diffTxt = fmt.Sprintf(`<span class="rwk-diff">(%+.1f)</span>`, diff)
			} else {
				diffTxt = fmt.Sprintf(`<span class="rwk-diff">(%+.0f)</span>`, diff)
			}
		}
		big := fmtScore(t.Total, isDecimal)
		if t.Paarung {
			// Punkte nach HR gross, Ringe in die HR-Zeile
			diffTxt += " · " + big
			big = fmt.Sprintf(`%d<span class="rwk-pts">Pkt</span>`, t.Points)
		}
		fmt.Fprintf(&b, `<div class="rwk-team" style="background:%s">
  <div class="rwk-team-names"><div class="rwk-team-name">%s</div><div class="rwk-team-club">%s</div></div>
  <div class="rwk-team-hr">HR: %s<br>%s</div>
  <div class="rwk-team-total">%s</div>
</div>`, t.Color, html.EscapeString(nz(t.Name, []string{"Heim", "Gast"}[k])), html.EscapeString(t.Club),
			fmtScore(t.HR, isDecimal), diffTxt, big)
		if len(t.Rows) > 0 {
			b.WriteString(`<div class="rwk-rows">`)
			for _, r := range t.Rows {
				var sr []string
				for _, v := range r.Series {
					sr = append(sr, fmtScore(v, r.Decimal))
				}
				tot := "–"
				if r.ShotCount > 0 {
					tot = fmtScore(r.Total, r.Decimal)
				}
				cls := "rwk-row"
				if r.Role == "AK" {
					cls += " ak"
				}
				fmt.Fprintf(&b, `<div class="%s"><span class="tag">%s</span><span class="nm">%s</span><span class="sr">%s</span><span class="tt">%s</span></div>`,
					cls, html.EscapeString(r.Disc), html.EscapeString(r.Name), strings.Join(sr, " "), tot)
			}
			b.WriteString(`</div>`)
		}
	}
	b.WriteString(`</div>`)
	return b.String()
}

// loadStarterMatchShots: Schusszahl der Disziplin je Starter (Name als
// Schluessel wie in rundeEntry) - fuer die Hochrechnung.
func loadStarterMatchShots(ctx context.Context, pool *pgxpool.Pool, eventID string) map[string]int {
	out := map[string]int{}
	rows, err := pool.Query(ctx, `
		SELECT sh.last_name, sh.first_name, COALESCE(d.match_shot_count, de.match_shot_count, 0)
		FROM starters st
		JOIN shooters sh ON sh.id = st.shooter_id
		LEFT JOIN disciplines d ON d.id = st.discipline_id
		LEFT JOIN events e ON e.id = st.event_id
		LEFT JOIN disciplines de ON de.id = e.discipline_id
		WHERE st.event_id = $1::uuid`, eventID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var ln, fn string
		var n int
		if rows.Scan(&ln, &fn, &n) == nil {
			out[ln+"\x00"+fn] = n
		}
	}
	return out
}

func writeRwkDynPage(w io.Writer, slot anzeigeSlot, p rwkDynParams, lanesHTML, rwkHTML string) {
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="de"><head><meta charset="UTF-8">
<title>%s</title>
<style>
  :root{--bg:#0c0f14;--panel:#141a22;--line:#26303c;--text:#e6edf3;--dim:#7d8b99;--acc:#4ab8a0}
  *{box-sizing:border-box;margin:0;padding:0}
  body{background:var(--bg);color:var(--text);font-family:system-ui,sans-serif;height:100vh;overflow:hidden;
       display:grid;grid-template-columns:1fr %d%%;gap:10px;padding:10px}
  #lanes{min-width:0;min-height:0;position:relative}
  .lane-page{display:none;gap:10px;height:100%%}
  .lane-page.active{display:grid}
  .lanes-empty,.rwk-empty{color:var(--dim);display:flex;align-items:center;justify-content:center;height:100%%;font-size:1.4em;text-align:center}
%s  #side{min-width:0;display:flex;flex-direction:column;gap:10px;background:var(--panel);border:1px solid var(--line);border-radius:8px;padding:10px;overflow:hidden}
  #clock{font-size:clamp(20px,3vw,60px);font-weight:800;text-align:center;font-variant-numeric:tabular-nums}
  #rwk{flex:1;min-height:0;position:relative}
  .rwk-card{display:none;flex-direction:column;gap:8px}
  .rwk-card.active{display:flex}
  .rwk-title{font-weight:700;font-size:clamp(12px,1.1vw,22px);color:var(--text)}
  .rwk-team{border-radius:8px;padding:10px;display:grid;grid-template-columns:1fr auto;grid-template-rows:auto auto;gap:4px 8px;color:#fff}
  .rwk-team-names{grid-column:1/3;min-width:0}
  .rwk-team-name{font-weight:700;font-size:clamp(12px,1.05vw,21px);white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
  .rwk-team-club{font-size:clamp(10px,.8vw,15px);opacity:.85;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
  .rwk-team-hr{font-size:clamp(11px,.95vw,19px);font-weight:600}
  .rwk-diff{opacity:.9}
  .rwk-team-total{font-size:clamp(20px,2.4vw,48px);font-weight:800;text-align:right;align-self:end}
  .rwk-pts{font-size:.4em;font-weight:600;margin-left:.2em}
  .rwk-rows{display:flex;flex-direction:column;gap:2px;margin:-4px 0 4px;padding:0 4px}
  .rwk-row{display:grid;grid-template-columns:auto 1fr auto auto;gap:6px;align-items:baseline;font-size:clamp(10px,.82vw,16px);
           border-bottom:1px solid rgba(255,255,255,.06);padding:2px 0}
  .rwk-row.ak{opacity:.55}
  .rwk-row .tag{font-size:.75em;font-weight:700;color:var(--dim)}
  .rwk-row .nm{white-space:nowrap;overflow:hidden;text-overflow:ellipsis;min-width:0}
  .rwk-row .sr{color:var(--dim);font-variant-numeric:tabular-nums;white-space:nowrap}
  .rwk-row .tt{font-weight:800;font-variant-numeric:tabular-nums;text-align:right;min-width:2.4em}
  .rwk-dots{display:flex;gap:6px;justify-content:center;margin-top:8px}
  .rwk-dots span{width:8px;height:8px;border-radius:50%%;background:var(--line)}
  .rwk-dots span.active{background:var(--acc)}
</style>
<script>
%s
</script>
</head><body>
<div id="lanes">%s</div>
<div id="side"><div id="clock"></div><div id="rwk">%s</div></div>
<script>
const ROTATE_S = %d, LANE_PAGE_S = %d, REFRESH_S = %d;
// drawAll/tickTimers: rwkTileJS (im Kopf, gemeinsam mit der Rundenanzeige)
// Sichtbare Standgruppe / sichtbarer Wettkampf aus der Uhrzeit - so wechselt
// die Anzeige gleichmaessig weiter, auch wenn zwischendurch neue Daten kommen.
function rotate() {
  const now = Date.now() / 1000;
  const pages = document.querySelectorAll('.lane-page');
  const pi = pages.length ? Math.floor(now / LANE_PAGE_S) %% pages.length : 0;
  pages.forEach((p, i) => p.classList.toggle('active', i === pi));
  const cards = document.querySelectorAll('.rwk-card');
  const ci = cards.length ? Math.floor(now / ROTATE_S) %% cards.length : 0;
  cards.forEach((c, i) => c.classList.toggle('active', i === ci));
  document.querySelectorAll('.rwk-dots span').forEach((d, i) => d.classList.toggle('active', i === ci));
}
function tick() {
  const d = new Date();
  document.getElementById('clock').textContent =
    String(d.getHours()).padStart(2, '0') + ':' + String(d.getMinutes()).padStart(2, '0') + ' Uhr';
  tickTimers(); // gemeinsam mit den anderen Anzeigen (rwkTileJS)
  rotate();
}
async function refresh() {
  try {
    const r = await fetch(location.pathname + '?partial=1', {cache: 'no-store'});
    if (!r.ok) return;
    const j = await r.json();
    document.getElementById('lanes').innerHTML = j.lanes;
    document.getElementById('rwk').innerHTML = j.rwk;
    drawAll();
    tick();
  } catch (e) { /* naechster Versuch */ }
}
drawAll();
tick();
setInterval(tick, 1000);
setInterval(refresh, REFRESH_S * 1000);
// einmal je Stunde komplett neu laden (Speicher, neue Programmversion)
setTimeout(() => location.reload(), 3600 * 1000);
</script>
</body></html>`, html.EscapeString(nz(slot.Name, "RWK-Dynamisch")), p.SidePercent, rwkTileCSS, standTargetJS+rwkTileJS,
		lanesHTML, rwkHTML, p.RotateSeconds, p.LanePageSeconds, p.RefreshSeconds)
}

// rwkTileCSS: Kachel-Stil (Namensbalken, Trefferbild, Ergebniszeile) -
// gemeinsam fuer RWK-Dynamisch und die Scheibenansicht der Rundenwettkampf-
// Anzeige (rundenanzeige.go).
const rwkTileCSS = `  .rtile{background:var(--panel);border:2px solid var(--line);border-radius:8px;display:flex;flex-direction:column;min-height:0;overflow:hidden;position:relative}
  .rhead{position:absolute;top:0;left:0;right:0;z-index:2;padding:3px 10px;font-weight:600;font-size:clamp(14px,1.5vw,28px);
         white-space:nowrap;overflow:hidden;text-overflow:ellipsis;color:#fff;text-shadow:0 1px 3px rgba(0,0,0,.8)}
  .rno{display:inline-block;min-width:1.4em;font-weight:800;background:rgba(0,0,0,.35);border-radius:4px;text-align:center;margin-right:6px}
  .rtarget{flex:1;min-height:0;position:relative;display:flex;align-items:center;justify-content:center}
  .rtarget svg{width:100%;height:100%}
  .rlabel{position:absolute;left:6px;right:6px;bottom:4px;display:flex;gap:8px;align-items:center;font-size:clamp(10px,0.9vw,17px)}
  .rlabel-text{background:rgba(10,40,55,.85);border:1px solid #2aa3b8;border-radius:4px;padding:1px 6px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;min-width:0}
  .rlabel-text.stechen{background:rgba(90,20,20,.9);border-color:#e05c5c;color:#ffd0d0;font-weight:700}
  .timer{background:rgba(10,40,55,.85);border:1px solid #2aa3b8;border-radius:4px;padding:1px 6px;font-variant-numeric:tabular-nums;flex-shrink:0}
  .timer.soft{color:#3dcc6e}
  .timer.hard{color:#ff8080}
  .timer.low{border-color:#e05c5c}
  .rinfo{flex-shrink:0;display:flex;gap:.7em;align-items:baseline;padding:0 10px 3px;line-height:1.1;white-space:nowrap}
  .rlast{font-size:clamp(15px,1.7vw,34px);font-weight:700}
  .rseries{flex:1;min-width:0;display:flex;gap:.55em;justify-content:center;font-size:clamp(12px,1.3vw,26px);font-weight:700;overflow:hidden}
  .rhr{flex-shrink:0}
  .hr{border:1px solid var(--dim);border-radius:4px;padding:0 5px;font-size:clamp(10px,.9vw,18px);font-weight:700}
  .rtotal{font-size:clamp(16px,1.9vw,38px);font-weight:800;text-align:right}
  .rcount{display:block;color:var(--dim);font-size:clamp(9px,.75vw,15px);font-weight:500;text-align:right}
  .pdot{position:absolute;top:.35em;right:.4em;z-index:3;width:clamp(14px,1.4vw,28px);height:clamp(14px,1.4vw,28px);border-radius:50%;
        border:2px solid rgba(0,0,0,.45);box-shadow:0 1px 4px rgba(0,0,0,.6)}
  .pdot.lead{background:#2ecc40}
  .pdot.trail{background:#8a949e}
  .pdot ~ .rhead{padding-right:calc(clamp(14px,1.4vw,28px) + 1em)}
`

// rwkTileJS: Trefferbilder zeichnen und Restzeit-Uhren aktualisieren.
const rwkTileJS = `
function drawAll() {
  document.querySelectorAll('svg.tgt').forEach((svg, i) => {
    svg.id = 'tgt-' + i;
    try { drawStandTarget(svg.id, JSON.parse(svg.dataset.geo), JSON.parse(svg.dataset.shots)); } catch (e) { /* Kachel ohne Geometrie */ }
  });
}
function tickTimers() {
  // soft: zaehlt nach Ablauf negativ weiter, hard: bleibt bei 00:00
  document.querySelectorAll('.timer').forEach(el => {
    let rest = Math.ceil((+el.dataset.deadline - Date.now()) / 1000);
    if (rest < 0 && el.classList.contains('hard')) rest = 0;
    const a = Math.abs(rest);
    el.textContent = (rest < 0 ? '−' : '') + String(Math.floor(a / 60)).padStart(2, '0') + ':' + String(a % 60).padStart(2, '0');
    el.classList.toggle('low', rest < 300);
  });
}
`
