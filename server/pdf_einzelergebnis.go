// ============================================================================
// pdf_einzelergebnis.go – PDF-Einzelergebnis (Ergebniskarte) einer Session:
// Kopfdaten, Gesamtergebnis der Scheibe (inkl. Trefferbild), je Serie
// Ergebnis + Einzelschüsse + eigenes Trefferbild. Nur Wertungsschüsse
// (kind='match') - Probeschüsse gehören nicht in das offizielle Ergebnis.
//
// Serien-Gruppierung (Chunking der geordneten Schussliste in Bloecke von
// shots_per_series) UND die "annulliert zaehlt als 0"-Regel spiegeln bewusst
// 1:1 seriesBoxesHTML()/renderDetail() in web/ergebnisse.html - das PDF ist
// die gedruckte Fassung genau dieser Bildschirmansicht, nicht eine eigene
// Neuberechnung mit eigenen Regeln. Die Scheiben-Grafik (Ringfarben,
// gefuellter Spiegel, Auto-Zoom) spiegelt ebenso 1:1 buildTarget()/
// computeViewR() aus web/ergebnis-ansicht.html.
// ============================================================================
package main

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"

	"github.com/go-pdf/fpdf"
)

// pdfShot: Schuss mit bereits aufgeloesten effektiven Werten (Korrektur hat
// Vorrang vor Originalmessung, siehe SessionShots/ergebnis-ansicht.html eff*).
type pdfShot struct {
	ShotNo         int
	Status         string // valid | annulled | cross_shot_in
	XMM, YMM       float64
	Ring           int
	Decimal        float64
	InnerTen       bool
	CenterDistance float64 // "Teiler": Abstand zur Mitte, in 1/100mm (roher DB-Wert)
}

// displayStatuses: siehe DISPLAY_STATUSES in web/ergebnisse.html - technisch
// verworfene/virtuelle Eintraege verschieben sonst die Seriennummerierung.
var pdfDisplayStatuses = map[string]bool{"valid": true, "annulled": true, "cross_shot_in": true}

func (s *Store) getSessionMatchShots(ctx context.Context, sessionID string) ([]pdfShot, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT shot_no, status::text,
		       COALESCE(corrected_x_mm, x_mm), COALESCE(corrected_y_mm, y_mm),
		       COALESCE(corrected_ring, ring), COALESCE(corrected_decimal_value, decimal_value),
		       COALESCE(corrected_is_inner_ten, is_inner_ten),
		       COALESCE(corrected_center_distance, center_distance)
		FROM shots
		WHERE session_id=$1 AND kind='match'
		ORDER BY shot_no`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pdfShot
	for rows.Next() {
		var sh pdfShot
		if err := rows.Scan(&sh.ShotNo, &sh.Status, &sh.XMM, &sh.YMM,
			&sh.Ring, &sh.Decimal, &sh.InnerTen, &sh.CenterDistance); err != nil {
			return nil, err
		}
		if pdfDisplayStatuses[sh.Status] {
			out = append(out, sh)
		}
	}
	// ShotNo wird hier auf die Position innerhalb der Wertungsschuesse
	// umnummeriert (1..N) - der rohe DB-Wert shot_no zaehlt fortlaufend UEBER
	// die ganze Session inkl. Probeschuessen mit, wodurch die Wertung nicht
	// bei 1 begonnen haette (Bugfix: "Nummerierung beginnt nicht bei 1").
	// Identisches Prinzip wie die Schussnummern in web/index.html
	// (renderShots: shotNo = serStart+i+1, aus der Array-Position statt aus
	// einem gespeicherten Feld).
	for i := range out {
		out[i].ShotNo = i + 1
	}
	return out, rows.Err()
}

// pdfSeries: ein Block von shots_per_series Schuessen.
type pdfSeries struct {
	No    int
	Shots []pdfShot
}

func chunkIntoSeries(shots []pdfShot, shotsPerSeries int) []pdfSeries {
	if shotsPerSeries <= 0 {
		shotsPerSeries = 10
	}
	var out []pdfSeries
	for i := 0; i < len(shots); i += shotsPerSeries {
		end := i + shotsPerSeries
		if end > len(shots) {
			end = len(shots)
		}
		out = append(out, pdfSeries{No: len(out) + 1, Shots: shots[i:end]})
	}
	return out
}

// ringSum/decimalSum: annullierte Schuesse zaehlen als 0 (siehe
// seriesBoxesHTML in web/ergebnisse.html), tauchen aber weiterhin einzeln auf.
func ringSum(shots []pdfShot) int {
	sum := 0
	for _, s := range shots {
		if s.Status != "annulled" {
			sum += s.Ring
		}
	}
	return sum
}
func decimalSum(shots []pdfShot) float64 {
	sum := 0.0
	for _, s := range shots {
		if s.Status != "annulled" {
			sum += s.Decimal
		}
	}
	return sum
}
func innerTenCount(shots []pdfShot) int {
	n := 0
	for _, s := range shots {
		if s.Status != "annulled" && s.InnerTen {
			n++
		}
	}
	return n
}

// fmtWertung: Primaerwert nach Disziplin-Wertung, Alternativwert informativ
// in Klammern - siehe Auftrag "gewertetes Ergebnis darstellen, alternative
// Wertung in Klammern".
func fmtWertung(decimalScoring bool, rings int, decimal float64) string {
	if decimalScoring {
		return fmt.Sprintf("%s (%d)", deFloat1(decimal), rings)
	}
	return fmt.Sprintf("%d (%s)", rings, deFloat1(decimal))
}

// bestTeilerLine: die n Schuesse (nur gueltige, nicht annullierte) mit dem
// kleinsten Teiler, aufsteigend sortiert, in EINER Zeile
// "45(3.), 66(16.), 78(12.)" - Wert(Schussnummer.).
func bestTeilerLine(shots []pdfShot, n int) string {
	var valid []pdfShot
	for _, s := range shots {
		if s.Status == "valid" {
			valid = append(valid, s)
		}
	}
	sort.SliceStable(valid, func(i, j int) bool { return valid[i].CenterDistance < valid[j].CenterDistance })
	if len(valid) > n {
		valid = valid[:n]
	}
	parts := make([]string, len(valid))
	for i, s := range valid {
		parts[i] = fmt.Sprintf("%.0f(%d.)", s.CenterDistance, s.ShotNo)
	}
	return joinComma(parts)
}

// autoZoomFractions: feste Zoomstufen (Anteil des Aussenradius), identisch zu
// ZOOM_FIXED in web/ergebnis-ansicht.html.
var autoZoomFractions = []float64{0.0626, 0.2418, 0.4209, 0.60, 1.00}

// computeAutoViewR: kleinste Zoomstufe, bei der der Mittelpunkt jedes
// uebergebenen Schusses noch im Bild ist - identisch zu computeViewR()
// (Modus "Auto") in web/ergebnis-ansicht.html.
func computeAutoViewR(shots []pdfShot, outerR float64) float64 {
	if outerR <= 0 {
		return 0
	}
	if len(shots) == 0 {
		return outerR
	}
	maxR := 0.0
	for _, s := range shots {
		if d := math.Hypot(s.XMM, s.YMM); d > maxR {
			maxR = d
		}
	}
	for _, f := range autoZoomFractions {
		if r := f * outerR; r >= maxR {
			return r
		}
	}
	return outerR
}

// drawTargetGraphic zeichnet die "echte" Scheibe (grauer Hintergrund,
// gefuellter Spiegel, Ringkonturen, gestrichelter Innenzehner) zentriert in
// ein Quadrat der Kantenlaenge 2*boxHalf um (cx,cy), im Auto-Zoom auf die
// uebergebenen Schuesse zugeschnitten (identisch zu buildTarget()/
// computeViewR() in web/ergebnis-ansicht.html) - moeglichst randlos, damit
// die Grafik den verfuegbaren Platz maximal ausnutzt.
func drawTargetGraphic(pdf *fpdf.Fpdf, geo TargetGeometry, caliberMM float64, cx, cy, boxHalf float64, shots []pdfShot) {
	if len(geo.Rings) == 0 {
		return
	}
	// Im Auto-Zoom kann der eingeblendete Scheibenausschnitt (viewR) viel
	// kleiner sein als der Aussenring, wodurch die auf boxHalf skalierten
	// Ringkreise weit ueber das Quadrat hinausragen wuerden - Clipping haelt
	// die Grafik exakt im vorgesehenen Bereich (siehe Bugfix "Serien-Grafiken
	// ueberlappen sich").
	pdf.ClipRect(cx-boxHalf, cy-boxHalf, 2*boxHalf, 2*boxHalf, false)
	defer pdf.ClipEnd()

	rings := append([]RingGeom{}, geo.Rings...)
	sort.Slice(rings, func(i, j int) bool { return rings[i].D > rings[j].D }) // groesster Durchmesser zuerst
	outerR := rings[0].D / 2
	if outerR <= 0 {
		return
	}

	viewR := computeAutoViewR(shots, outerR)
	pad := math.Max(viewR*0.07, caliberMM*0.8)
	viewHalf := viewR + pad
	scale := boxHalf / viewHalf

	circleXY := func(mmX, mmY float64) (float64, float64) { return cx + mmX*scale, cy - mmY*scale }

	// Hintergrund - deutlich heller als das gedruckte Scheibenpapier, damit
	// sich die Schuesse (auch ausserhalb des Spiegels) klar davon abheben.
	pdf.SetFillColor(248, 247, 244)
	pdf.Circle(cx, cy, outerR*scale, "F")

	// Gefuellter Spiegel (dunkelste Ringe, siehe RingGeom.Filled)
	darkR := 0.0
	for _, rg := range rings {
		if rg.Filled && rg.D/2 > darkR {
			darkR = rg.D / 2
		}
	}
	if darkR > 0 {
		pdf.SetFillColor(28, 28, 28)
		pdf.Circle(cx, cy, darkR*scale, "F")
	}

	// Ringkonturen
	strokeW := math.Max(outerR*0.004, 0.08) * scale
	pdf.SetLineWidth(strokeW)
	for _, rg := range rings {
		if rg.Filled {
			pdf.SetDrawColor(160, 160, 160)
		} else {
			pdf.SetDrawColor(110, 110, 110)
		}
		pdf.Circle(cx, cy, rg.D/2*scale, "D")
	}

	// Innenzehner (gestrichelt, falls vorgesehen)
	if geo.InnerTenD > 0 {
		pdf.SetDrawColor(160, 160, 160)
		if geo.InnerTenDashed {
			dl := strokeW * 5
			pdf.SetDashPattern([]float64{dl, dl}, 0)
		}
		pdf.Circle(cx, cy, geo.InnerTenD/2*scale, "D")
		pdf.SetDashPattern([]float64{}, 0)
	}

	// Schuesse: Randfarbe + Fuellfarbe nach Ring (identisch zu
	// shotColorByRing()/COLORS in web/ergebnis-ansicht.html), mit
	// Schussnummer in Kontrastfarbe darauf - macht Treffer auch im dunklen
	// Spiegel klar erkennbar (Bugfix: "Schuesse auf dunklem Untergrund
	// schlecht sichtbar").
	dotR := math.Max(caliberMM/2*scale, 1.4)
	borderW := math.Max(dotR*0.12, 0.08)
	for _, s := range shots {
		x, y := circleXY(s.XMM, s.YMM)
		fr, fg, fb := shotFillColor(s)
		pdf.SetFillColor(fr, fg, fb)
		pdf.SetDrawColor(26, 26, 26)
		pdf.SetLineWidth(borderW)
		pdf.Circle(x, y, dotR, "FD")

		tr2, tg2, tb2 := contrastTextColor(fr, fg, fb)
		pdf.SetTextColor(tr2, tg2, tb2)
		pdf.SetFontUnitSize(dotR * 1.05)
		pdf.SetXY(x-dotR, y-dotR)
		pdf.CellFormat(2*dotR, 2*dotR, fmt.Sprintf("%d", s.ShotNo), "", 0, "CM", false, 0, "")
	}
	pdf.SetTextColor(0, 0, 0) // nachfolgender Text setzt seine Fontgroesse ohnehin selbst neu
}

// shotFillColor: Fuellfarbe nach Ring, identisch zu COLORS/shotColorByRing()
// in web/ergebnis-ansicht.html (annullierte Schuesse: Grau).
func shotFillColor(s pdfShot) (int, int, int) {
	if s.Status != "valid" {
		return 0x66, 0x66, 0x66
	}
	switch {
	case s.Ring >= 10:
		return 0xe0, 0x40, 0x40
	case s.Ring == 9:
		return 0xf3, 0xfb, 0x06
	default:
		return 0x4a, 0xb8, 0xff
	}
}

// contrastTextColor: Schwarz oder Weiss, je nachdem was auf der Fuellfarbe
// besser lesbar ist (identische Formel wie contrastColor() im Frontend).
func contrastTextColor(r, g, b int) (int, int, int) {
	lum := (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 255
	if lum > 0.5 {
		return 0, 0, 0
	}
	return 255, 255, 255
}

func joinComma(parts []string) string { return joinSep(parts, ", ") }

func joinSep(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

// buildEinzelergebnisPDF erzeugt die Ergebniskarte: Kopf, Gesamtergebnis der
// Scheibe (mit moeglichst grossem Trefferbild rechts), danach je Serie ein
// Block (Trefferbild links, Ergebnis + Einzelschuesse rechts) - 6 Serien je
// Seite, weitere Serien auf Folgeseiten.
func buildEinzelergebnisPDF(header SessionResultHeader, shots []pdfShot, geo TargetGeometry, caliberMM float64) (*fpdf.Fpdf, error) {
	pdf, tr := newReportPDF()
	left, top, right, _ := pdf.GetMargins()
	pageW, _ := pdf.GetPageSize()
	usableW := pageW - left - right

	name := header.ShooterName
	if name == "" {
		name = "Anonym"
	}
	pdf.SetHeaderFunc(func() {
		if pdf.PageNo() == 1 {
			return
		}
		pdf.SetY(top - 6)
		pdf.SetFont("Helvetica", "", 8)
		pdf.SetTextColor(130, 130, 130)
		pdf.CellFormat(usableW, 4, tr(name+" · "+header.Discipline+" · Fortsetzung"), "", 1, "L", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
	})

	// ── Kopf + Gesamtergebnis: Text links, Scheiben-Grafik rechts ────────
	// Die Grafik beginnt oben buendig mit der Titelzeile (blockTop = oberer
	// Seitenrand, wie gefordert "gleicher Abstand nach oben wie die oberste
	// Zeile") und nutzt die gesamte verbleibende Hoehe/Breite - Groesse wird
	// erst NACH dem Textblock final bestimmt (mind. aus Breite und Hoehe),
	// damit sie moeglichst gross wird, ohne den Textblock zu ueberragen.
	const gap = 4.0
	const leftW = 92.0
	rightW := usableW - leftW - gap
	blockTop := pdf.GetY()

	pdf.SetFont("Helvetica", "B", 16)
	pdf.CellFormat(leftW, 9, tr(name), "", 1, "L", false, 0, "")

	pdf.SetFont("Helvetica", "", 10)
	var metaParts []string
	if header.Discipline != "" {
		metaParts = append(metaParts, "Scheibe/Disziplin: "+header.Discipline)
	}
	if header.ClubName != "" {
		metaParts = append(metaParts, "Verein: "+header.ClubName)
	}
	if header.SportKlasse != "" {
		metaParts = append(metaParts, "Sportklasse: "+header.SportKlasse)
	}
	if d := fmtDateDE(header.StartedAt); d != "" {
		metaParts = append(metaParts, "Datum: "+d)
	}
	metaParts = append(metaParts, fmt.Sprintf("Stand: %d", header.LaneNo))
	pdf.SetX(left)
	pdf.MultiCell(leftW, 6, tr(joinSep(metaParts, "  ·  ")), "", "L", false)
	pdf.Ln(3)
	pdf.SetDrawColor(180, 180, 180)
	pdf.Line(left, pdf.GetY(), left+leftW, pdf.GetY())
	pdf.Ln(5)

	pdf.SetX(left)
	pdf.SetFont("Helvetica", "B", 12)
	pdf.CellFormat(leftW, 7, tr("Gesamtergebnis"), "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetX(left)
	pdf.CellFormat(leftW, 6, tr("Ergebnis Gesamt: "+fmtWertung(header.DecimalScoring, ringSum(shots), decimalSum(shots))), "", 1, "L", false, 0, "")
	pdf.SetX(left)
	pdf.CellFormat(leftW, 6, tr(fmt.Sprintf("Innenzehner: %d", innerTenCount(shots))), "", 1, "L", false, 0, "")
	pdf.SetX(left)
	pdf.MultiCell(leftW, 6, tr("5 Beste Teiler: "+bestTeilerLine(shots, 5)), "", "L", false)
	leftBottom := pdf.GetY()

	gfxHalf := math.Min(rightW, leftBottom-blockTop) / 2
	gfxCx := pageW - right - gfxHalf
	gfxCy := blockTop + gfxHalf
	drawTargetGraphic(pdf, geo, caliberMM, gfxCx, gfxCy, gfxHalf, shots)

	blockBottom := math.Max(leftBottom, blockTop+2*gfxHalf)
	pdf.SetY(blockBottom + 3)
	pdf.Line(left, pdf.GetY(), pageW-right, pdf.GetY())
	pdf.Ln(4)

	// ── Serien ────────────────────────────────────────────────────────────
	series := chunkIntoSeries(shots, header.ShotsPerSeries)
	const perPage = 6
	const rowH = 28.0
	const seriesGfxHalf = 13.5

	textX := left + 2*seriesGfxHalf + gap
	textAreaW := usableW - 2*seriesGfxHalf - gap

	for i, ser := range series {
		if i > 0 && i%perPage == 0 {
			pdf.AddPage()
		}
		rowTop := pdf.GetY()

		gfxCx := left + seriesGfxHalf
		gfxCy := rowTop + rowH/2
		pdf.SetDrawColor(210, 210, 210)
		pdf.SetLineWidth(0.15)
		pdf.Rect(left, rowTop, 2*seriesGfxHalf, rowH, "D")
		drawTargetGraphic(pdf, geo, caliberMM, gfxCx, gfxCy, seriesGfxHalf-0.5, ser.Shots)

		pdf.SetXY(textX, rowTop)
		pdf.SetFont("Helvetica", "B", 11)
		label := fmt.Sprintf("Serie %d: %s", ser.No, fmtWertung(header.DecimalScoring, ringSum(ser.Shots), decimalSum(ser.Shots)))
		pdf.CellFormat(textAreaW, 7, tr(label), "", 1, "L", false, 0, "")

		shotsY := rowTop + 9
		n := len(ser.Shots)
		if n > 0 {
			cellW := textAreaW / float64(n)
			pdf.SetFont("Helvetica", "", 10)
			for j, s := range ser.Shots {
				v := deFloat1(s.Decimal)
				if !header.DecimalScoring {
					v = fmt.Sprintf("%d", s.Ring)
				}
				if s.Status == "annulled" {
					v = "(" + v + ")"
				}
				pdf.SetXY(textX+float64(j)*cellW, shotsY)
				pdf.CellFormat(cellW, 6, tr(v), "", 0, "C", false, 0, "")
			}
		}

		pdf.SetXY(left, rowTop+rowH)
		if (i+1)%perPage != 0 && i != len(series)-1 {
			pdf.SetDrawColor(225, 225, 225)
			pdf.Line(left, pdf.GetY(), pageW-right, pdf.GetY())
		}
		pdf.Ln(1.5)
	}

	return pdf, pdf.Error()
}

func (a *APIServer) getSessionPDF(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	ctx := r.Context()

	header, err := a.store.GetSessionResultHeader(ctx, sessionID)
	if err != nil {
		http.Error(w, "Session nicht gefunden", http.StatusNotFound)
		return
	}
	shots, err := a.store.getSessionMatchShots(ctx, sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(shots) == 0 {
		http.Error(w, "Keine Wertungsschüsse für dieses Ergebnis.", http.StatusNotFound)
		return
	}

	var geo TargetGeometry
	var caliberMM float64
	if targetID, _, _, _, err := a.store.SessionTargets(ctx, sessionID); err == nil {
		if td, err := a.store.LoadTargetDef(ctx, targetID); err == nil {
			caliberMM = td.CaliberMM
		}
	}
	geo, _ = a.store.resolveTargetGeometry(ctx, sessionID) // Zero-Value -> PDF ohne Trefferbild

	pdf, err := buildEinzelergebnisPDF(header, shots, geo, caliberMM)
	if err != nil {
		http.Error(w, "PDF-Erzeugung fehlgeschlagen: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="`+pdfFilenameSafe(header.ShooterName)+`.pdf"`)
	if err := pdf.Output(w); err != nil {
		http.Error(w, "PDF-Ausgabe fehlgeschlagen: "+err.Error(), http.StatusInternalServerError)
	}
}
