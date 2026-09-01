// ============================================================================
// statistik.go – Statistik-Seite (Teilnehmer nach Tagen/Klassen, Scheiben
// nach Typ), analog zu gs26 TYP=Statistik + TYP=Statistik-graph*, aber ohne
// Chart.js-Abhängigkeit: statische (nicht animierte) SVG-Balkendiagramme.
// ============================================================================
package main

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strings"
)

func (h *siteHandler) handleStatistik(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	type dayRow struct {
		tag        string
		neu, total int
	}
	var days []dayRow
	rows, err := h.pool.Query(ctx, `
		SELECT to_char((t.created_at AT TIME ZONE 'Europe/Berlin')::date, 'DD.MM.YYYY') AS tag, COUNT(*)
		FROM ps_teilnehmer t WHERE t.preisschiessen_id=$1
		GROUP BY (t.created_at AT TIME ZONE 'Europe/Berlin')::date
		ORDER BY (t.created_at AT TIME ZONE 'Europe/Berlin')::date`, h.preisschiessenID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total := 0
	for rows.Next() {
		var d dayRow
		if err := rows.Scan(&d.tag, &d.neu); err != nil {
			rows.Close()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		total += d.neu
		d.total = total
		days = append(days, d)
	}
	rows.Close()

	klassen, err := h.loadCountPairs(ctx, `
		SELECT COALESCE(sc.name,'(ohne Klasse)'), COUNT(*)
		FROM ps_teilnehmer t LEFT JOIN shooter_classes sc ON sc.id=t.class_id
		WHERE t.preisschiessen_id=$1 GROUP BY sc.name ORDER BY COUNT(*) DESC`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	scheibenTypen, err := h.loadCountPairs(ctx, `
		SELECT sc.name, COUNT(*) FROM ps_kauf_scheiben ks JOIN ps_scheiben sc ON sc.id=ks.scheibe_id
		WHERE ks.preisschiessen_id=$1 GROUP BY sc.name ORDER BY COUNT(*) DESC`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var b strings.Builder
	fmt.Fprint(&b, `<h2 class="page-title">Statistik</h2>`)

	fmt.Fprint(&b, `<table class="result"><thead><tr><th>Tag</th><th class="num">Teilnehmer neu</th><th class="num">Teilnehmer gesamt</th></tr></thead><tbody>`)
	if len(days) == 0 {
		fmt.Fprint(&b, `<tr><td colspan="3">Noch keine Anmeldungen.</td></tr>`)
	}
	for _, d := range days {
		fmt.Fprintf(&b, `<tr><td>%s</td><td class="num">%d</td><td class="num">%d</td></tr>`, d.tag, d.neu, d.total)
	}
	fmt.Fprint(&b, `</tbody></table>`)

	var dayLabels []string
	var dayValues []int
	for _, d := range days {
		dayLabels = append(dayLabels, d.tag)
		dayValues = append(dayValues, d.neu)
	}
	fmt.Fprint(&b, `<br>`+renderBarChart("Teilnehmer je Tag (neu)", dayLabels, dayValues))
	fmt.Fprint(&b, `<br>`+renderBarChart("Teilnehmer je Klasse", labelsOf(klassen), valuesOf(klassen)))
	fmt.Fprint(&b, `<br>`+renderBarChart("Scheiben je Typ", labelsOf(scheibenTypen), valuesOf(scheibenTypen)))

	h.renderLayout(w, ctx, "Statistik", "statistik", b.String())
}

type countPair struct {
	label string
	value int
}

func (h *siteHandler) loadCountPairs(ctx context.Context, query string) ([]countPair, error) {
	rows, err := h.pool.Query(ctx, query, h.preisschiessenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []countPair
	for rows.Next() {
		var p countPair
		if err := rows.Scan(&p.label, &p.value); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func labelsOf(p []countPair) []string {
	out := make([]string, len(p))
	for i, x := range p {
		out[i] = x.label
	}
	return out
}
func valuesOf(p []countPair) []int {
	out := make([]int, len(p))
	for i, x := range p {
		out[i] = x.value
	}
	return out
}

// renderBarChart: statisches (nicht animiertes), horizontales SVG-Balkendiagramm.
func renderBarChart(title string, labels []string, values []int) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<div style="background:#fff;border:1px solid #ddd;border-radius:6px;padding:14px 18px;margin:0 auto;max-width:700px">
<h3 style="text-align:center;margin:0 0 12px 0;font-size:1em">%s</h3>`, html.EscapeString(title))
	if len(values) == 0 {
		fmt.Fprint(&b, `<p style="color:#666;text-align:center">Keine Daten.</p></div>`)
		return b.String()
	}
	max := 1
	for _, v := range values {
		if v > max {
			max = v
		}
	}
	rowH, gap := 22, 6
	h := len(values)*(rowH+gap) + gap
	fmt.Fprintf(&b, `<svg width="100%%" viewBox="0 0 640 %d" style="font-family:sans-serif">`, h)
	y := gap
	colors := []string{"#004080", "#33AA33", "#AA6C33", "#8033AA", "#33AAA0", "#AA3350"}
	for i, v := range values {
		barW := int(float64(v) / float64(max) * 380)
		if barW < 1 {
			barW = 1
		}
		col := colors[i%len(colors)]
		fmt.Fprintf(&b, `<text x="0" y="%d" font-size="12" fill="#333">%s</text>`,
			y+rowH-7, html.EscapeString(truncateLabel(labels[i], 28)))
		fmt.Fprintf(&b, `<rect x="200" y="%d" width="%d" height="%d" fill="%s"/>`, y, barW, rowH-4, col)
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="12" fill="#333">%d</text>`, 205+barW, y+rowH-7, v)
		y += rowH + gap
	}
	fmt.Fprint(&b, `</svg></div>`)
	return b.String()
}

func truncateLabel(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
