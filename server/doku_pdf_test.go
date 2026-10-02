package main

import (
	"math"
	"math/rand"
	"os"
	"testing"
)

// TestDokuBeispielPDF erzeugt das Beispiel-Einzelergebnis fuer die
// Dokumentation (docs/bilder/pdf-einzelergebnis.png) aus erfundenen Schuessen
// - ohne Datenbank und ohne echte Personendaten. Laeuft nur, wenn
// DOKU_PDF_OUT gesetzt ist (siehe docs/tools/make-images.sh).
func TestDokuBeispielPDF(t *testing.T) {
	out := os.Getenv("DOKU_PDF_OUT")
	if out == "" {
		t.Skip("DOKU_PDF_OUT nicht gesetzt")
	}
	rnd := rand.New(rand.NewSource(7))
	var shots []pdfShot
	for i := 1; i <= 30; i++ {
		// Streuung um die Mitte, leicht nach rechts unten versetzt
		x := rnd.NormFloat64()*4.6 + 0.6
		y := rnd.NormFloat64()*4.6 - 0.4
		d := math.Hypot(x, y)
		dec := math.Max(0, math.Floor((10.9-(math.Max(0, d-2.25)/2.5))*10)/10)
		if dec > 10.9 {
			dec = 10.9
		}
		shots = append(shots, pdfShot{
			ShotNo: i, Status: "valid", XMM: x, YMM: y,
			Ring: int(dec), Decimal: dec, InnerTen: d <= 2.25+0.25,
			CenterDistance: math.Round(d * 100),
		})
	}
	h := SessionResultHeader{
		ShooterName: "Huber, Anna", ClubName: "Musterverein e.V.", SportKlasse: "Damen II",
		Discipline: "LGA-30", DecimalScoring: true, ShotsPerSeries: 10, LaneNo: 4,
		StartedAt: "2026-10-02 19:30:00+02",
	}
	pdf, err := buildEinzelergebnisPDF(h, shots, targetGeometries[1], 4.5)
	if err != nil {
		t.Fatal(err)
	}
	if err := pdf.OutputFileAndClose(out); err != nil {
		t.Fatal(err)
	}
}
