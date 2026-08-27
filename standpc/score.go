// ============================================================================
// score.go – Trefferwertung aus Scheibenkoordinaten
//
// Regeln (ISSF/DSB):
//   - "Rand zaehlt" (edge_scoring): Ein Ring ist getroffen, wenn der RAND
//     des Schusslochs den Ring beruehrt -> effektiver Abstand
//     r_eff = r_zentrum - kaliber/2
//   - Ganzring: hoechster Ringwert, dessen Aussenradius >= r_eff
//   - Zehntelwert: lineare Unterteilung jedes Rings in 10 Zonen
//     (10.9 = exakte Mitte ... 0.0 = weit daneben)
//   - Innenzehner: r_eff <= inner_ten_radius
//   - Teiler (traditionelles Schiessen): Abstand Lochmitte->Scheibenmitte
//     in 1/100 mm, OHNE Randabzug
// ============================================================================
package main

import (
	"math"
	"sort"
)

type Scorer struct {
	target     *TargetDef
	rings      []RingDef // nach Durchmesser aufsteigend sortiert (10 zuerst)
	ringStepMM float64   // radialer Abstand zwischen zwei Ringlinien
}

type ScoreResult struct {
	Ring     int
	Decimal  float64
	InnerTen bool
	CenterDistance float64
}

func NewScorer(t *TargetDef) *Scorer {
	rings := make([]RingDef, len(t.Rings))
	copy(rings, t.Rings)
	sort.Slice(rings, func(i, j int) bool {
		return rings[i].DiameterMM < rings[j].DiameterMM
	})
	s := &Scorer{target: t, rings: rings}
	// Ringbreite aus den ersten beiden Ringen ableiten (ISSF: konstant)
	if len(rings) >= 2 {
		s.ringStepMM = (rings[1].DiameterMM - rings[0].DiameterMM) / 2.0
	}
	return s
}

func (s *Scorer) Score(xMM, yMM float64) ScoreResult {
	r := math.Hypot(xMM, yMM) // Abstand Lochmitte -> Scheibenmitte

	// Teiler: Abstand Lochmitte->Scheibenmitte in 1/100 mm, ohne Randabzug
	res := ScoreResult{CenterDistance: math.Round(r*100*10) / 10}

	// "Rand zaehlt": effektiver Wertungsabstand fuer Ring- und Innenzehner-Wertung
	rEff := r
	if s.target.EdgeScoring {
		rEff = math.Max(0, r-s.target.CaliberMM/2.0)
	}

	// Innenzehner
	if s.target.InnerTenDMM > 0 && rEff <= s.target.InnerTenDMM/2.0 {
		res.InnerTen = true
	}

	// Ganzring: kleinster Ring, dessen Radius den Treffer noch einschliesst
	res.Ring = 0
	for _, ring := range s.rings {
		if rEff <= ring.DiameterMM/2.0 {
			res.Ring = ring.Value
			break
		}
	}

	// Zehntelwert: basiert auf Teiler (r, nicht rEff) – Formel: 11 - Teiler/Nenner
	res.Decimal = s.decimalValue(r)
	return res
}

// decimalValue: Zehntelwertung aus dem rohen Trefferabstand r (nicht rEff).
//
// Formel (linear ueber den gesamten Teilerbereich):
//
//	Zehntelwert = 11 - Teiler / Nenner
//	Teiler = r * 100  (1/100 mm)
//	Nenner = (Ring10-Radius + Kaliber/2) * 100  (effektiver Ring10-Aussenradius)
//
// Beispiele (Kaliber 4,5 mm):
//
//	Scheibe 1 LG: Ring10 r=0,25 mm -> Nenner = (0,25+2,25)*100 = 250
//	Scheibe 7 LP: Ring10 r=5,75 mm -> Nenner = (5,75+2,25)*100 = 800
//
// Ergebnis wird auf eine Nachkommastelle abgerundet; Maximum ist 10,9.
func (s *Scorer) decimalValue(r float64) float64 {
	if len(s.rings) == 0 || s.target.CaliberMM <= 0 {
		return 0
	}
	r10 := s.rings[0].DiameterMM / 2.0
	denom := (r10 + s.target.CaliberMM/2.0) * 100 // effektiver Ring10-Radius in 1/100mm
	if denom <= 0 {
		return 0
	}
	teiler := r * 100
	dec := 11.0 - teiler/denom
	if dec > 10.9 {
		dec = 10.9
	}
	if dec < 0 {
		dec = 0
	}
	return math.Floor(dec*10) / 10
}
