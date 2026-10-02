// ============================================================================
// vereinsabend_export.go – Zusammenfassungen und PDF-Export der Vereinsabend-
// Listen (Tagesergebnisse, Tageswertung, Saisonwertung).
//
// Die Saison-Zusammenfassung (Schiessabende, Anwesende je Abend/Klasse)
// braucht die Einzeltage und wird deshalb hier aus aggregatePunkteSaisonTage
// berechnet. Das PDF baut der Server aus der Tabelle, die der Browser gerade
// anzeigt (Spalten, Sortierung, Filter) - so entspricht der Ausdruck exakt
// der Bildschirmansicht, ohne Sortier-/Filterlogik doppelt zu pflegen.
// ============================================================================
package main

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
)

// ----------------------------------------------------------------------------
// Saison-Zusammenfassung
// ----------------------------------------------------------------------------

type VAKlassenSchnitt struct {
	Klasse       string  `json:"klasse"`
	Durchschnitt float64 `json:"durchschnitt"` // Anwesende dieser Klasse je Schiessabend
}

type VASaisonSummary struct {
	Schiessabende      int                `json:"schiessabende"`
	DurchschnittAnw    float64            `json:"durchschnitt_anwesende"`
	MaxAnw             int                `json:"max_anwesende"`
	MaxAnwDaten        []string           `json:"max_anwesende_daten"` // JJJJ-MM-TT, bei Gleichstand mehrere
	DurchschnittKlasse []VAKlassenSchnitt `json:"durchschnitt_je_klasse"`
}

// saisonSummary: Schiessabend = Tag mit mindestens einem Ergebnis dieser
// Wertung im Zeitraum (Wochentage sind in tage bereits gefiltert).
// Anwesend = Teilnehmer mit einem Ergebnis an diesem Abend, das nicht nur
// Vor-/Nachschuss ist (Vor-/Nachschuetzen waren an dem Abend nicht da).
func saisonSummary(tage []saisonTag, von, bis string) VASaisonSummary {
	abende := map[string]bool{}
	anwJeTag := map[string]int{}
	anwJeKlasse := map[string]int{}
	for _, t := range tage {
		if (von != "" && t.tag < von) || (bis != "" && t.tag > bis) {
			continue
		}
		abende[t.tag] = true
		if !t.anwesendOK {
			continue
		}
		anwJeTag[t.tag]++
		anwJeKlasse[t.meta.klasse]++
	}
	var sum VASaisonSummary
	sum.Schiessabende = len(abende)
	sum.MaxAnwDaten = []string{}
	sum.DurchschnittKlasse = []VAKlassenSchnitt{}
	if sum.Schiessabende == 0 {
		return sum
	}
	total := 0
	for tag, n := range anwJeTag {
		total += n
		if n > sum.MaxAnw {
			sum.MaxAnw, sum.MaxAnwDaten = n, []string{tag}
		} else if n == sum.MaxAnw {
			sum.MaxAnwDaten = append(sum.MaxAnwDaten, tag)
		}
	}
	sort.Strings(sum.MaxAnwDaten)
	sum.DurchschnittAnw = float64(total) / float64(sum.Schiessabende)
	for k, n := range anwJeKlasse {
		sum.DurchschnittKlasse = append(sum.DurchschnittKlasse,
			VAKlassenSchnitt{Klasse: k, Durchschnitt: float64(n) / float64(sum.Schiessabende)})
	}
	sort.Slice(sum.DurchschnittKlasse, func(i, j int) bool {
		return sum.DurchschnittKlasse[i].Klasse < sum.DurchschnittKlasse[j].Klasse
	})
	return sum
}

// ----------------------------------------------------------------------------
// PDF aus der angezeigten Tabelle
// ----------------------------------------------------------------------------

type vaPDFColumn struct {
	Label string `json:"label"`
	Num   bool   `json:"num"` // rechtsbuendig
}

type vaPDFKV struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// vaPDFSection: weitere Tabelle mit eigener Ueberschrift im selben PDF.
type vaPDFSection struct {
	Title   string        `json:"title"`
	Columns []vaPDFColumn `json:"columns"`
	Rows    [][]string    `json:"rows"`
}

type vaPDFRequest struct {
	Title    string        `json:"title"`
	Subtitle string        `json:"subtitle"`
	Summary  []vaPDFKV     `json:"summary"`
	Columns  []vaPDFColumn `json:"columns"`
	Rows     [][]string    `json:"rows"`
	Legend   string        `json:"legend"`
	// Orientation: "P" (hoch) / "L" (quer) erzwingen, leer = automatisch
	// (quer ab 9 Spalten). Die Kompakt-Fassung ohne Verein/Klasse ist hoch.
	Orientation string         `json:"orientation"`
	Sections    []vaPDFSection `json:"sections"`
}

func buildVereinsabendPDF(req vaPDFRequest) *fpdf.Fpdf {
	orientation := "P"
	maxCols := len(req.Columns)
	for _, sec := range req.Sections {
		if len(sec.Columns) > maxCols {
			maxCols = len(sec.Columns)
		}
	}
	if maxCols > 8 {
		orientation = "L"
	}
	if req.Orientation == "P" || req.Orientation == "L" {
		orientation = req.Orientation
	}
	pdf := fpdf.New(orientation, "mm", "A4", "")
	pdf.SetMargins(14, 14, 14)
	pdf.SetAutoPageBreak(false, 14)
	pdf.AliasNbPages("{nb}")
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	stand := time.Now().Format("02.01.2006 15:04")
	pdf.SetFooterFunc(func() {
		l, _, r, _ := pdf.GetMargins()
		pw, _ := pdf.GetPageSize()
		half := (pw - l - r) / 2
		pdf.SetY(-10)
		pdf.SetFont("Helvetica", "", 8)
		pdf.SetTextColor(120, 120, 120)
		pdf.CellFormat(half, 5, tr("Stand "+stand), "", 0, "L", false, 0, "")
		pdf.CellFormat(half, 5, tr(fmt.Sprintf("Seite %d/{nb}", pdf.PageNo())), "", 0, "R", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
	})
	pdf.AddPage()
	left, top, right, _ := pdf.GetMargins()
	pageW, pageH := pdf.GetPageSize()
	usableW := pageW - left - right
	bottom := pageH - 14

	pdf.SetFont("Helvetica", "B", 15)
	pdf.CellFormat(0, 8, tr(req.Title), "", 1, "L", false, 0, "")
	if req.Subtitle != "" {
		pdf.SetFont("Helvetica", "", 10)
		pdf.SetTextColor(80, 80, 80)
		pdf.CellFormat(0, 5.5, tr(req.Subtitle), "", 1, "L", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
	}
	pdf.SetDrawColor(180, 180, 180)
	pdf.Line(left, pdf.GetY()+1.5, pageW-right, pdf.GetY()+1.5)
	pdf.Ln(4)

	// Zusammenfassung
	if len(req.Summary) > 0 {
		pdf.SetFont("Helvetica", "B", 11)
		pdf.CellFormat(0, 6.5, tr("Zusammenfassung"), "", 1, "L", false, 0, "")
		labelW := 0.0
		pdf.SetFont("Helvetica", "", 10)
		for _, kv := range req.Summary {
			if w := pdf.GetStringWidth(tr(kv.Label)); w > labelW {
				labelW = w
			}
		}
		labelW += 4
		for _, kv := range req.Summary {
			pdf.SetFont("Helvetica", "", 10)
			pdf.SetTextColor(80, 80, 80)
			pdf.CellFormat(labelW, 5.5, tr(kv.Label), "", 0, "L", false, 0, "")
			pdf.SetTextColor(0, 0, 0)
			pdf.SetFont("Helvetica", "B", 10)
			pdf.MultiCell(usableW-labelW, 5.5, tr(kv.Value), "", "L", false)
		}
		pdf.Ln(3)
	}

	// Abschnitte: die Einzeltabelle (Columns/Rows) plus ggf. weitere
	// Tabellen mit eigener Ueberschrift (Sections, z.B. Vereins-Wertung).
	sections := req.Sections
	if len(req.Columns) > 0 {
		sections = append([]vaPDFSection{{Columns: req.Columns, Rows: req.Rows}}, sections...)
	}
	const fontSize, rowH = 8.5, 5.6
	for si, sec := range sections {
		if len(sec.Columns) == 0 {
			continue
		}
		if si > 0 {
			pdf.Ln(4)
		}
		// Ueberschrift + Kopf + mindestens zwei Zeilen zusammenhalten
		if pdf.GetY()+8+3*rowH > bottom {
			pdf.AddPage()
			pdf.SetY(top)
		}
		if sec.Title != "" {
			pdf.SetFont("Helvetica", "B", 11)
			pdf.CellFormat(0, 7, tr(sec.Title), "", 1, "L", false, 0, "")
		}

		// Spaltenbreiten aus dem breitesten Inhalt, auf die Seitenbreite skaliert
		widths := make([]float64, len(sec.Columns))
		for i, c := range sec.Columns {
			pdf.SetFont("Helvetica", "B", fontSize)
			widths[i] = pdf.GetStringWidth(tr(c.Label))
			pdf.SetFont("Helvetica", "", fontSize)
			for _, r := range sec.Rows {
				if i < len(r) {
					if w := pdf.GetStringWidth(tr(r[i])); w > widths[i] {
						widths[i] = w
					}
				}
			}
			widths[i] += 3
		}
		total := 0.0
		for _, w := range widths {
			total += w
		}
		scale := usableW / total
		if scale > 1.35 {
			scale = 1.35 // schmale Tabellen nicht ueber die ganze Breite ziehen
		}
		for i := range widths {
			widths[i] *= scale
		}

		header := func() {
			pdf.SetFont("Helvetica", "B", fontSize)
			pdf.SetFillColor(225, 228, 232)
			for i, c := range sec.Columns {
				align := "L"
				if c.Num {
					align = "R"
				}
				pdf.CellFormat(widths[i], rowH+0.6, truncateToWidth(pdf, tr, c.Label, widths[i]-1.5), "B", 0, align, true, 0, "")
			}
			pdf.Ln(-1)
			pdf.SetFont("Helvetica", "", fontSize)
		}
		header()
		for n, r := range sec.Rows {
			if pdf.GetY()+rowH > bottom-2 {
				pdf.AddPage()
				header()
			}
			fill := n%2 == 1
			pdf.SetFillColor(245, 246, 248)
			for i, c := range sec.Columns {
				v := ""
				if i < len(r) {
					v = r[i]
				}
				align := "L"
				if c.Num {
					align = "R"
				}
				pdf.CellFormat(widths[i], rowH, truncateToWidth(pdf, tr, v, widths[i]-1.5), "", 0, align, fill, 0, "")
			}
			pdf.Ln(-1)
		}
		if len(sec.Rows) == 0 {
			pdf.SetFont("Helvetica", "I", fontSize)
			pdf.CellFormat(0, rowH, tr("Keine Einträge"), "", 1, "L", false, 0, "")
		}
	}
	if req.Legend != "" {
		pdf.Ln(2)
		pdf.SetFont("Helvetica", "", 7.5)
		pdf.SetTextColor(100, 100, 100)
		pdf.MultiCell(usableW, 4, tr(req.Legend), "", "L", false)
		pdf.SetTextColor(0, 0, 0)
	}
	return pdf
}

// postVereinsabendPDF: POST /api/preisschiessen/{id}/vereinsabend/pdf - Body
// vaPDFRequest (vom Browser aus der angezeigten Liste gebaut).
func (a *APIServer) postVereinsabendPDF(w http.ResponseWriter, r *http.Request) {
	req, err := decodeBody[vaPDFRequest](r)
	if err != nil {
		http.Error(w, "ungültiger Body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Columns) > 30 || len(req.Rows) > 5000 || len(req.Sections) > 20 {
		http.Error(w, "Liste zu groß", http.StatusBadRequest)
		return
	}
	pdf := buildVereinsabendPDF(req)
	name := strings.TrimSpace(req.Title)
	if req.Subtitle != "" {
		name += " " + req.Subtitle
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="`+pdfFilenameSafe(name)+`.pdf"`)
	if err := pdf.Output(w); err != nil {
		http.Error(w, "PDF-Ausgabe fehlgeschlagen: "+err.Error(), http.StatusInternalServerError)
	}
}
