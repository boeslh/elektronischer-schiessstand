// ============================================================================
// pdf.go – gemeinsame PDF-Infrastruktur fuer Ausdrucke/Speichern als PDF
// (Einzelergebnisse, Wettkampf-Auswertungen, ...). Bewusst reines Go ohne
// externe Programme/Headless-Browser (go-pdf/fpdf) - laeuft identisch offline
// am Stand-PC wie am Server, und die erzeugten Bytes lassen sich spaeter ohne
// Zusatzaufwand als E-Mail-Anhang verwenden.
//
// Deutsche Sonderzeichen (äöüß) benoetigen keine eingebetteten Fonts: die
// fpdf-Kernschriften (Helvetica/Arial/Times/Courier) nutzen standardmaessig
// cp1252, das genau diese Zeichen abdeckt - UnicodeTranslatorFromDescriptor("")
// uebersetzt UTF-8-Strings dafuer, siehe newReportPDF().
// ============================================================================
package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
)

// deFloat1 formatiert eine Zehntel-Wertung mit deutschem Dezimalkomma
// (z.B. "95,3" statt "95.3") - so wird sie ueberall im Schiessport notiert.
func deFloat1(v float64) string {
	return strings.Replace(fmt.Sprintf("%.1f", v), ".", ",", 1)
}

// newReportPDF liefert ein vorkonfiguriertes A4-Hochformat-PDF (Helvetica,
// 18mm Seitenrand links/rechts, automatischer Seitenumbruch) plus die
// Uebersetzungsfunktion fuer deutsche Sonderzeichen.
func newReportPDF() (*fpdf.Fpdf, func(string) string) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(18, 16, 18)
	pdf.SetAutoPageBreak(true, 16)
	pdf.AddPage()
	pdf.SetFont("Helvetica", "", 11)
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	return pdf, tr
}

// fmtDateDE wandelt ein ISO-Datum (YYYY-MM-DD, ggf. mit Zeitanteil) in die
// deutsche Schreibweise DD.MM.YYYY um - leer bzw. unparsbar liefert den
// Originalwert zurueck statt eines Fehlers (Datum ist bei Wettkaempfen
// optional).
func fmtDateDE(iso string) string {
	if iso == "" {
		return ""
	}
	s := iso
	if len(s) > 10 {
		s = s[:10]
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return iso
	}
	return t.Format("02.01.2006")
}

// fmtDateRangeDE fasst Start- und Enddatum zusammen ("07.09.2026" oder
// "07.09.2026 – 08.09.2026" bei mehrtaegigen Wettkaempfen, leer wenn beides fehlt).
func fmtDateRangeDE(startsOn, endsOn string) string {
	s, e := fmtDateDE(startsOn), fmtDateDE(endsOn)
	switch {
	case s == "" && e == "":
		return ""
	case s == "":
		return e
	case e == "" || e == s:
		return s
	default:
		return s + " – " + e
	}
}

// truncateToWidth kuerzt s (bereits UTF-8, wird intern uebersetzt) so, dass
// es inklusive "…" in maxWidth mm passt - CellFormat bricht NICHT um und
// laesst zu langen Text sonst einfach in die naechste Zelle ueberlaufen
// (siehe Bugfix "Vereinsname mit Anführungszeichen zerstört Tabellenzeile").
// Kuerzt rune-weise, nicht byte-weise, damit mehrbytige UTF-8-Zeichen (äöüß)
// nicht mitten im Zeichen abgeschnitten werden.
func truncateToWidth(pdf *fpdf.Fpdf, tr func(string) string, s string, maxWidth float64) string {
	t := tr(s)
	if pdf.GetStringWidth(t) <= maxWidth {
		return t
	}
	ellipsis := tr("…")
	runes := []rune(s)
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		t = tr(string(runes)) + ellipsis
		if pdf.GetStringWidth(t) <= maxWidth {
			return t
		}
	}
	return ellipsis
}

// drawSignatureBlock zeichnet 2-3 Unterschriftenfelder nebeneinander, fix am
// unteren Rand der Seite (297mm A4-Hoehe, 16mm untere Marge -> Linie bei
// 265mm, Beschriftung darunter). Reicht der bisherige Seiteninhalt schon in
// diesen Bereich hinein, wird stattdessen eine neue Seite begonnen - so bleibt
// die Position bei den ueblichen kurzen Auswertungen (die auf eine Seite
// passen) immer exakt gleich, ohne bei sehr langen Teilnehmerlisten Inhalt zu
// ueberschreiben.
func drawSignatureBlock(pdf *fpdf.Fpdf, tr func(string) string, labels []string) {
	const lineY = 265.0
	if pdf.GetY() > lineY-10 {
		pdf.AddPage()
	}
	left, _, right, _ := pdf.GetMargins()
	pageW, _ := pdf.GetPageSize()
	usableW := pageW - left - right
	n := float64(len(labels))
	gap := 10.0
	colW := (usableW - gap*(n-1)) / n

	pdf.SetDrawColor(90, 90, 90)
	pdf.SetLineWidth(0.2)
	pdf.SetFont("Helvetica", "", 9)
	for i, label := range labels {
		x := left + float64(i)*(colW+gap)
		pdf.Line(x, lineY, x+colW, lineY)
		pdf.SetXY(x, lineY+1.5)
		pdf.CellFormat(colW, 5, tr(label), "", 0, "C", false, 0, "")
	}
}
