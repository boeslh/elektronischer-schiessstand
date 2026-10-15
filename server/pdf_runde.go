// ============================================================================
// pdf_runde.go – PDF-Auswertung eines Rundenwettkampfs (2 antretende
// Mannschaften). Rechenlogik (Gruppierung nach Mannschaft, Sortierung,
// bestOf-Teamwertung) spiegelt bewusst 1:1 renderRunde() in
// web/auswertung.html - das serverseitige PDF ist die offizielle, gedruckte
// Fassung derselben Auswertung, die am Bildschirm zu sehen ist.
// ============================================================================
package main

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/go-pdf/fpdf"
)

// rundenTeamResult: eine Mannschaft mit ihren (bereits sortierten) Startern
// und der berechneten Teamwertung - siehe computeRundenTeams.
type rundenTeamResult struct {
	Name       string
	Sorted     []RundenwettkampfEntry // countable (sortiert) + danach AK (sortiert)
	Countable  []RundenwettkampfEntry
	WertungCnt int
	Total      float64
}

// computeRundenTeams gruppiert Starter nach Mannschaft, sortiert je Mannschaft
// (Zehntel/Ringe absteigend, Stecher Innenzehner) und berechnet die
// Teamwertung aus den besten bestOf Startern (0 = alle) - identisch zu
// renderRunde()/cmpEntry()/entryValue() in web/auswertung.html.
func computeRundenTeams(entries []RundenwettkampfEntry, bestOf int) (teams []*rundenTeamResult, isDecimal bool) {
	for _, e := range entries {
		if e.DecimalScoring {
			isDecimal = true
			break
		}
	}
	value := func(e RundenwettkampfEntry) float64 {
		if isDecimal {
			return e.TotalDecimal
		}
		return float64(e.TotalRings)
	}
	less := func(a, b RundenwettkampfEntry) bool { // a vor b?
		av, bv := value(a), value(b)
		if av != bv {
			return av > bv
		}
		return a.InnerTens > b.InnerTens
	}

	type bucket struct {
		key     string
		name    string
		entries []RundenwettkampfEntry
	}
	byKey := map[string]*bucket{}
	var order []string
	for _, e := range entries {
		key, name := e.TeamID, e.TeamName
		if key == "" {
			key, name = "__none__", "(Ohne Mannschaft)"
		}
		b, ok := byKey[key]
		if !ok {
			b = &bucket{key: key, name: name}
			byKey[key] = b
			order = append(order, key)
		}
		b.entries = append(b.entries, e)
	}

	for _, key := range order {
		b := byKey[key]
		var countable, ak []RundenwettkampfEntry
		for _, e := range b.entries {
			if e.Role == "AK" {
				ak = append(ak, e)
			} else {
				countable = append(countable, e)
			}
		}
		sort.SliceStable(countable, func(i, j int) bool { return less(countable[i], countable[j]) })
		sort.SliceStable(ak, func(i, j int) bool { return less(ak[i], ak[j]) })

		wertungCnt := len(countable)
		if bestOf > 0 && bestOf < wertungCnt {
			wertungCnt = bestOf
		}
		var total float64
		for _, e := range countable[:wertungCnt] {
			total += value(e)
		}

		t := &rundenTeamResult{Name: b.name, Countable: countable, WertungCnt: wertungCnt, Total: total}
		t.Sorted = append(append([]RundenwettkampfEntry{}, countable...), ak...)
		teams = append(teams, t)
	}

	sort.SliceStable(teams, func(i, j int) bool {
		if teams[i].Name == "(Ohne Mannschaft)" {
			return false
		}
		if teams[j].Name == "(Ohne Mannschaft)" {
			return true
		}
		return teams[i].Total > teams[j].Total
	})
	return teams, isDecimal
}

// teamClubName: der Verein der Mannschaft - eine Mannschaft tritt immer fuer
// genau einen Verein an, deshalb reicht der Verein irgendeines Mitglieds
// (S/E vor AK, siehe Sorted-Reihenfolge). Leer bei "(Ohne Mannschaft)" ohne
// Mitglieder.
func teamClubName(t *rundenTeamResult) string {
	if len(t.Sorted) > 0 {
		return t.Sorted[0].ClubName
	}
	return ""
}

// buildRundenwettkampfPDF erzeugt die druckfertige Auswertung: Kopf (Name,
// Datum, Disziplin, Liga, Ort), Mannschaftswertung, je Mannschaft eine
// Ergebnistabelle, unten Unterschriftenfelder fuer beide Mannschaften und den
// Schiedsrichter.
func buildRundenwettkampfPDF(comp Competition, entries []RundenwettkampfEntry, bestOf int) (*fpdf.Fpdf, error) {
	pdf, tr := newReportPDF()

	pdf.SetFont("Helvetica", "B", 16)
	pdf.CellFormat(0, 9, tr(comp.Name), "", 1, "L", false, 0, "")

	pdf.SetFont("Helvetica", "", 10)
	var metaParts []string
	if d := fmtDateRangeDE(comp.StartsOn, comp.EndsOn); d != "" {
		metaParts = append(metaParts, "Datum: "+d)
	}
	if comp.DisciplineName != "" {
		metaParts = append(metaParts, "Disziplin: "+comp.DisciplineName)
	}
	if comp.Liga != "" {
		metaParts = append(metaParts, "Liga: "+comp.Liga)
	}
	if comp.Location != "" {
		metaParts = append(metaParts, "Ort: "+comp.Location)
	}
	for i, part := range metaParts {
		if i > 0 {
			pdf.CellFormat(6, 6, tr("·"), "", 0, "C", false, 0, "")
		}
		pdf.CellFormat(pdf.GetStringWidth(tr(part))+2, 6, tr(part), "", 0, "L", false, 0, "")
	}
	pdf.Ln(9)
	pdf.SetDrawColor(180, 180, 180)
	left, _, right, _ := pdf.GetMargins()
	pageW, _ := pdf.GetPageSize()
	pdf.Line(left, pdf.GetY(), pageW-right, pdf.GetY())
	pdf.Ln(5)

	teams, isDecimal := computeRundenTeams(entries, bestOf)
	wertungLabel := "Ringe"
	if isDecimal {
		wertungLabel = "Zehntel"
	}

	fmtVal := func(e RundenwettkampfEntry) string {
		if e.ShotCount == 0 {
			return "–"
		}
		if isDecimal {
			return fmt.Sprintf("%.1f", e.TotalDecimal)
		}
		return fmt.Sprintf("%d", e.TotalRings)
	}
	fmtTotal := func(t *rundenTeamResult) string {
		if isDecimal {
			return fmt.Sprintf("%.1f", t.Total)
		}
		return fmt.Sprintf("%.0f", t.Total)
	}

	// ── Mannschaftswertung ──────────────────────────────────────────────
	rankedTeams := make([]*rundenTeamResult, 0, len(teams))
	for _, t := range teams {
		if t.Name != "(Ohne Mannschaft)" && len(t.Countable) > 0 {
			rankedTeams = append(rankedTeams, t)
		}
	}
	if len(rankedTeams) > 1 {
		pdf.SetFont("Helvetica", "B", 12)
		pdf.CellFormat(0, 8, tr("Mannschaftswertung ("+wertungLabel+")"), "", 1, "L", false, 0, "")
		pdf.SetFont("Helvetica", "B", 10)
		pdf.SetFillColor(235, 235, 235)
		widths := []float64{12, 90, 40, 30}
		headers := []string{"Platz", "Mannschaft", "Schützen", wertungLabel}
		for i, h := range headers {
			pdf.CellFormat(widths[i], 7, tr(h), "1", 0, "L", true, 0, "")
		}
		pdf.Ln(-1)
		pdf.SetFont("Helvetica", "", 10)
		for i, t := range rankedTeams {
			pdf.CellFormat(widths[0], 7, fmt.Sprintf("%d.", i+1), "1", 0, "L", false, 0, "")
			pdf.CellFormat(widths[1], 7, truncateToWidth(pdf, tr, t.Name, widths[1]-2), "1", 0, "L", false, 0, "")
			pdf.CellFormat(widths[2], 7, fmt.Sprintf("%d (von %d)", t.WertungCnt, len(t.Countable)), "1", 0, "L", false, 0, "")
			pdf.CellFormat(widths[3], 7, fmtTotal(t), "1", 0, "L", false, 0, "")
			pdf.Ln(-1)
		}
		pdf.Ln(6)
	}

	// ── Paarungen (Paarungswettkampf) ──────────────────────────────────
	if comp.RundeModus == "paarung" {
		drawPaarungPDF(pdf, tr, computePaarung(entries, false), fmtVal)
	}

	// ── Detail je Mannschaft ────────────────────────────────────────────
	// Kein Verein je Zeile: eine Mannschaft tritt immer fuer genau einen
	// Verein an, der deshalb einmal unter dem Mannschaftsnamen steht statt
	// bei jedem Mitglied wiederholt zu werden - macht Platz fuer eine ueber
	// die gesamte Seitenbreite gehende Tabelle.
	usableW := pageW - left - right
	detailWidths := []float64{14, usableW - 14 - 20 - 35, 20, 35}
	detailHeaders := []string{"Platz", "Name", "Rolle", wertungLabel}
	for _, t := range teams {
		pdf.SetFont("Helvetica", "B", 12)
		title := t.Name
		if len(rankedTeams) > 1 {
			title += " – Gesamt " + fmtTotal(t)
		}
		pdf.CellFormat(0, 7, tr(title), "", 1, "L", false, 0, "")
		if club := teamClubName(t); club != "" {
			pdf.SetFont("Helvetica", "", 9)
			pdf.SetTextColor(120, 120, 120)
			pdf.CellFormat(0, 5, tr(club), "", 1, "L", false, 0, "")
			pdf.SetTextColor(0, 0, 0)
		}
		pdf.Ln(1)

		pdf.SetFont("Helvetica", "B", 10)
		pdf.SetFillColor(235, 235, 235)
		for i, h := range detailHeaders {
			pdf.CellFormat(detailWidths[i], 7, tr(h), "1", 0, "L", true, 0, "")
		}
		pdf.Ln(-1)
		pdf.SetFont("Helvetica", "", 10)
		for idx, e := range t.Sorted {
			isAK := e.Role == "AK"
			counts := !isAK && idx < t.WertungCnt
			rank := "–"
			if counts {
				rank = fmt.Sprintf("%d.", idx+1)
			}
			name := truncateToWidth(pdf, tr, e.LastName+", "+e.FirstName, detailWidths[1]-2)
			pdf.CellFormat(detailWidths[0], 6.5, tr(rank), "1", 0, "L", false, 0, "")
			pdf.CellFormat(detailWidths[1], 6.5, name, "1", 0, "L", false, 0, "")
			pdf.CellFormat(detailWidths[2], 6.5, tr(e.Role), "1", 0, "C", false, 0, "")
			pdf.CellFormat(detailWidths[3], 6.5, tr(fmtVal(e)), "1", 0, "L", false, 0, "")
			pdf.Ln(-1)
		}
		pdf.Ln(6)
	}

	labels := []string{}
	if len(teams) >= 1 {
		labels = append(labels, "Unterschrift "+teams[0].Name)
	}
	if len(teams) >= 2 {
		labels = append(labels, "Unterschrift "+teams[1].Name)
	}
	labels = append(labels, "Unterschrift Schiedsrichter")
	drawSignatureBlock(pdf, tr, labels)

	return pdf, pdf.Error()
}

func (a *APIServer) getRundenwettkampfPDF(w http.ResponseWriter, r *http.Request) {
	eventID := r.URL.Query().Get("event_id")
	if eventID == "" {
		http.Error(w, "event_id erforderlich", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	comp, err := a.store.GetCompetition(ctx, eventID)
	if err != nil {
		http.Error(w, "Wettkampf nicht gefunden", http.StatusNotFound)
		return
	}
	// Wertung (beste N) aus dem Wettkampf; best_of im Aufruf nur noch fuer
	// gespeicherte Auswertungen mit abweichender Vorgabe
	bestOf := 0
	if comp.BestOf != nil {
		bestOf = *comp.BestOf
	}
	if v := r.URL.Query().Get("best_of"); v != "" {
		fmt.Sscanf(v, "%d", &bestOf)
	}
	entries, err := a.store.ListRundenwettkampfResults(ctx, eventID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(entries) == 0 {
		http.Error(w, "Keine Starter für diesen Wettkampf gefunden.", http.StatusNotFound)
		return
	}

	pdf, err := buildRundenwettkampfPDF(comp, entries, bestOf)
	if err != nil {
		http.Error(w, "PDF-Erzeugung fehlgeschlagen: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="`+pdfFilenameSafe(comp.Name)+`.pdf"`)
	if err := pdf.Output(w); err != nil {
		http.Error(w, "PDF-Ausgabe fehlgeschlagen: "+err.Error(), http.StatusInternalServerError)
	}
}

// pdfFilenameSafe: einfache Dateinamens-Bereinigung fuer den
// Content-Disposition-Header (keine Anfuehrungszeichen/Steuerzeichen).
func pdfFilenameSafe(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if r == '"' || r == '\\' || r < 0x20 {
			continue
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return "auswertung"
	}
	return string(out)
}

// drawPaarungPDF: Tabelle der Paarungen mit Punkten und Endstand.
func drawPaarungPDF(pdf *fpdf.Fpdf, tr func(string) string, pr paarungResult, fmtVal func(RundenwettkampfEntry) string) {
	if len(pr.Pairs) == 0 {
		return
	}
	pdf.SetFont("Helvetica", "B", 12)
	pdf.CellFormat(0, 8, tr("Paarungswertung"), "", 1, "L", false, 0, "")
	left, _, right, _ := pdf.GetMargins()
	pageW, _ := pdf.GetPageSize()
	nameW := (pageW - left - right - 14 - 2*22 - 2*14) / 2
	widths := []float64{14, nameW, 22, 14, 14, 22, nameW}
	headers := []string{"Paar.", pr.HeimName, "Ergebnis", "Pkt", "Pkt", "Ergebnis", pr.GastName}
	pdf.SetFont("Helvetica", "B", 10)
	pdf.SetFillColor(235, 235, 235)
	for i, h := range headers {
		pdf.CellFormat(widths[i], 7, truncateToWidth(pdf, tr, h, widths[i]-2), "1", 0, "C", true, 0, "")
	}
	pdf.Ln(-1)
	pdf.SetFont("Helvetica", "", 10)
	name := func(e *RundenwettkampfEntry) string {
		if e == nil {
			return "–"
		}
		return e.LastName + ", " + e.FirstName
	}
	val := func(e *RundenwettkampfEntry) string {
		if e == nil {
			return "–"
		}
		return fmtVal(*e)
	}
	pt := func(p paarungPair, side int) string {
		if p.Stechen {
			return "St."
		}
		if p.Winner == side {
			if p.ByStechen {
				return "1 (St.)"
			}
			return "1"
		}
		if p.Winner >= 0 {
			return "0"
		}
		return ""
	}
	for _, p := range pr.Pairs {
		pdf.CellFormat(widths[0], 6.5, tr(fmt.Sprintf("%d", p.No)), "1", 0, "C", false, 0, "")
		pdf.CellFormat(widths[1], 6.5, truncateToWidth(pdf, tr, name(p.Heim), widths[1]-2), "1", 0, "L", false, 0, "")
		pdf.CellFormat(widths[2], 6.5, tr(val(p.Heim)), "1", 0, "R", false, 0, "")
		pdf.CellFormat(widths[3], 6.5, tr(pt(p, 0)), "1", 0, "C", false, 0, "")
		pdf.CellFormat(widths[4], 6.5, tr(pt(p, 1)), "1", 0, "C", false, 0, "")
		pdf.CellFormat(widths[5], 6.5, tr(val(p.Gast)), "1", 0, "R", false, 0, "")
		pdf.CellFormat(widths[6], 6.5, truncateToWidth(pdf, tr, name(p.Gast), widths[6]-2), "1", 0, "L", false, 0, "")
		pdf.Ln(-1)
	}
	fmtT := func(v float64) string {
		if pr.IsDecimal {
			return fmt.Sprintf("%.1f", v)
		}
		return fmt.Sprintf("%.0f", v)
	}
	tp := func(side int) string {
		if pr.TotalPoint == side {
			return "1"
		}
		if pr.TotalPoint >= 0 {
			return "0"
		}
		return ""
	}
	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(widths[0]+widths[1], 6.5, tr("Mannschaftsergebnis"), "1", 0, "L", false, 0, "")
	pdf.CellFormat(widths[2], 6.5, tr(fmtT(pr.HeimTotal)), "1", 0, "R", false, 0, "")
	pdf.CellFormat(widths[3], 6.5, tr(tp(0)), "1", 0, "C", false, 0, "")
	pdf.CellFormat(widths[4], 6.5, tr(tp(1)), "1", 0, "C", false, 0, "")
	pdf.CellFormat(widths[5], 6.5, tr(fmtT(pr.GastTotal)), "1", 0, "R", false, 0, "")
	pdf.CellFormat(widths[6], 6.5, "", "1", 0, "L", false, 0, "")
	pdf.Ln(-1)
	pdf.Ln(2)
	pdf.SetFont("Helvetica", "B", 12)
	result := fmt.Sprintf("Endstand %d : %d", pr.HeimPoints, pr.GastPoints)
	switch pr.Winner {
	case 0:
		result += " – Sieger: " + pr.HeimName
	case 1:
		result += " – Sieger: " + pr.GastName
	default:
		result += " – noch nicht entschieden"
	}
	pdf.CellFormat(0, 7, tr(result), "", 1, "L", false, 0, "")
	if pr.TotalPoint < 0 && len(pr.Pairs)%2 == 0 && pr.HeimPoints == pr.GastPoints {
		pdf.SetFont("Helvetica", "", 9)
		pdf.CellFormat(0, 5, tr("Bei Punktgleichstand zählt das Mannschaftsergebnis als weiterer Punkt."), "", 1, "L", false, 0, "")
	}
	stechenTxt := func(e *RundenwettkampfEntry) string {
		if e == nil {
			return ""
		}
		var parts []string
		for _, s := range e.Stechen {
			switch {
			case !s.Shot:
				parts = append(parts, "…")
			case pr.IsDecimal || s.No > stechenRingRounds:
				parts = append(parts, fmt.Sprintf("%.1f", s.Decimal))
			default:
				parts = append(parts, fmt.Sprintf("%d", s.Ring))
			}
		}
		return strings.Join(parts, " / ")
	}
	note := false
	for _, p := range pr.Pairs {
		if !p.Stechen && !p.ByStechen {
			continue
		}
		pdf.SetFont("Helvetica", "", 9)
		if !note {
			pdf.CellFormat(0, 5, tr("St. = Gleichstand, Entscheidung durch Stechschüsse (ab dem 4. Stechschuss Zehntel)."), "", 1, "L", false, 0, "")
			note = true
		}
		if p.Heim != nil && p.Gast != nil && (len(p.Heim.Stechen) > 0 || len(p.Gast.Stechen) > 0) {
			pdf.CellFormat(0, 5, tr(fmt.Sprintf("Paarung %d – Stechschüsse: %s %s : %s %s", p.No,
				p.Heim.LastName, stechenTxt(p.Heim), stechenTxt(p.Gast), p.Gast.LastName)), "", 1, "L", false, 0, "")
		}
	}
	pdf.Ln(6)
}
