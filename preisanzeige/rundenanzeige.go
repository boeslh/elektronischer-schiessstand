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
	"io"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

type rundeParams struct {
	EventID string `json:"event_id"`
}

type rundeEntry struct {
	LastName, FirstName string
	TeamID              string
	Role                string // S | E | AK
	DecimalScoring      bool
	ShotCount           int
	TotalRings          int
	TotalDecimal        float64
	InnerTens           int
}

func (e rundeEntry) value(isDecimal bool) float64 {
	if isDecimal {
		return e.TotalDecimal
	}
	return float64(e.TotalRings)
}

func renderRundenanzeige(w io.Writer, ctx context.Context, pool *pgxpool.Pool, slot anzeigeSlot) {
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

	heimList, heimTotal := teamDisplayList(entries, heim.ID, isDecimal)
	gastList, gastTotal := teamDisplayList(entries, gast.ID, isDecimal)

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
    <div class="score">%s : %s</div>
    <div class="team-name">Mannschaft %s</div>
  </div>`,
		html.EscapeString(nz(heim.Name, "Heim")), fmtTot(heimTotal), fmtTot(gastTotal), html.EscapeString(nz(gast.Name, "Gast")))
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
			sh.last_name, sh.first_name,
			COALESCE(t.id::text, ''),
			COALESCE(st.role, 'S'),
			COALESCE(d.decimal_scoring, FALSE),
			COALESCE(best.shot_count,    0),
			COALESCE(best.total_rings,   0),
			COALESCE(best.total_decimal, 0.0),
			COALESCE(best.inner_tens,    0)
		FROM starters st
		JOIN  shooters sh ON sh.id = st.shooter_id
		LEFT JOIN teams      t ON t.id = st.team_id
		LEFT JOIN disciplines d ON d.id = st.discipline_id
		LEFT JOIN LATERAL (
			SELECT
				COALESCE(vsr.shot_count,    0)   AS shot_count,
				COALESCE(vsr.total_rings,   0)   AS total_rings,
				COALESCE(vsr.total_decimal, 0.0) AS total_decimal,
				COALESCE(vsr.inner_tens,    0)   AS inner_tens
			FROM sessions se
			LEFT JOIN v_session_results vsr ON vsr.session_id = se.id
			WHERE (se.starter_id = st.id
			    OR (se.event_id = $1::uuid AND se.shooter_id = st.shooter_id
			        AND se.status::text <> 'aborted'))
			ORDER BY COALESCE(vsr.shot_count,0) DESC, se.finished_at DESC NULLS LAST
			LIMIT 1
		) best ON TRUE
		WHERE st.event_id = $1::uuid`, eventID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []rundeEntry
	for rows.Next() {
		var e rundeEntry
		if rows.Scan(&e.LastName, &e.FirstName, &e.TeamID, &e.Role, &e.DecimalScoring,
			&e.ShotCount, &e.TotalRings, &e.TotalDecimal, &e.InnerTens) == nil {
			out = append(out, e)
		}
	}
	return out
}

// teamDisplayList: hoechstens 5 Schuetzen (S/E vor AK, AK nur zum
// Auffuellen falls weniger als 5 S/E vorhanden) - identische Sortier-/
// Wertungsregel wie computeRundenTeams() in server/pdf_runde.go.
func teamDisplayList(entries []rundeEntry, teamID string, isDecimal bool) ([]rundeEntry, float64) {
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

	const maxDisplay = 5
	wertungCnt := len(countable)
	if wertungCnt > maxDisplay {
		wertungCnt = maxDisplay
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
  .score{font-size:2.6em;font-weight:800;color:var(--acc)}
</style></head><body>
`

const rundeFoot = `
<script>setTimeout(() => location.reload(), 6000);</script>
</body></html>`
