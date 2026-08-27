// ============================================================================
// hybrid.go – Zweistufige Auswertung: Stahl-TDOA (grob) + Luft-TOA (fein)
//
// Physik-Hintergrund:
//   Die Stahlwelle (~3000 m/s) erreicht die Stahlsensoren zuerst und strahlt
//   unterwegs Luftschall ab ("Vorlaeufer"), der die Luftmikrofone VOR dem
//   direkten Luftschall (343 m/s) vom Einschlagpunkt erreicht. Die Firmware
//   liefert deshalb ALLE Flanken je Mikrofon (air_ns); dieser Solver:
//
//   1. nimmt die Stahl-Grobposition (±0,5 mm) und schaetzt daraus den
//      Aufprallzeitpunkt t0 (relativ zum ersten Stahl-Hit)
//   2. berechnet je Mikrofon das ERWARTUNGSFENSTER fuer den direkten
//      Luftschall und waehlt die frueheste Flanke darin (Gating)
//   3. loest aus >=3 gegateten Flanken per TOA-Multilateration
//      (Unbekannte: x, y, t0) die Feinposition – Gauss-Newton, 3x3
//
//   Fallback: Liefert das Gating <3 brauchbare Flanken oder konvergiert
//   die Loesung nicht plausibel, bleibt die Stahl-Grobposition gueltig.
//
// Koordinaten: Blechsystem wie der Stahl-Solver; Mikrofone haben
// zusaetzlich z = Abstand VOR der Blechebene (Einschlag liegt bei z=0).
// ============================================================================
package main

import (
	"math"
)

type AirSensor struct {
	X float64 `json:"x_mm"`
	Y float64 `json:"y_mm"`
	Z float64 `json:"z_mm"` // Abstand vor der Blechebene
}

type HybridConfig struct {
	Enabled          bool        `json:"enabled"`
	AirSensors       []AirSensor `json:"air_sensors"`
	AirSoundSpeedMPS float64     `json:"air_sound_speed_mps"` // ~343
	GateUs           float64     `json:"gate_us"`             // Fensterbreite ±
}

type AirSolver struct {
	mics   []AirSensor
	vAir   float64 // mm/ns
	gateNs float64
}

func NewAirSolver(hc *HybridConfig) *AirSolver {
	v := hc.AirSoundSpeedMPS
	if v <= 0 {
		v = 343
	}
	// Gate eng halten: Die Grobposition ist ±0,5 mm genau -> die
	// Vorhersage des direkten Schalls stimmt auf ~±2 µs. Ein enges
	// Fenster haelt Nachklingel-Stoerflanken draussen.
	gate := hc.GateUs
	if gate <= 0 {
		gate = 15
	}
	return &AirSolver{
		mics:   hc.AirSensors,
		vAir:   v / 1e6,
		gateNs: gate * 1000,
	}
}

// dist3D: Abstand Einschlag (x,y,0) -> Mikrofon (mx,my,mz)
func dist3D(x, y float64, m AirSensor) float64 {
	dx, dy := x-m.X, y-m.Y
	return math.Sqrt(dx*dx + dy*dy + m.Z*m.Z)
}

// Refine verfeinert die Stahl-Grobposition mit den Luftflanken.
//
//	coarseX/Y : Grobposition (Blechkoordinaten, mm)
//	steel     : der Stahl-Solver (liefert Referenzsensor-Geometrie + v)
//	steelTNs  : Stahl-Zeitstempel (fuer den Referenzsensor-Index, t==0)
//	airNs     : Flankenlisten je Mikrofon (ns rel. erster Stahl-Hit)
//
// Rueckgabe ok=false -> Aufrufer behaelt die Grobposition.
func (a *AirSolver) Refine(coarseX, coarseY float64, steel *TDOASolver,
	steelTNs []int64, airNs [][]int64) (x, y float64, ok bool, used int) {

	if len(a.mics) < 3 || len(airNs) != len(a.mics) {
		return 0, 0, false, 0
	}

	// --- Schritt 1: t0 schaetzen ---------------------------------------
	// Referenz = Stahlsensor mit t==0. Sein Abstand zur Grobposition
	// bestimmt, wie lange die Stahlwelle bis dorthin lief:
	//   t0 = -(d_ref / v_stahl)   (Aufprall VOR dem ersten Stahl-Hit)
	refIdx := -1
	for i, t := range steelTNs {
		if t == 0 {
			refIdx = i
			break
		}
	}
	if refIdx < 0 || refIdx >= len(steel.sensors) {
		return 0, 0, false, 0
	}
	ref := steel.sensors[refIdx]
	dRef := math.Hypot(coarseX-ref.X, coarseY-ref.Y)
	t0 := -dRef / steel.soundSpeed // ns, negativ

	// --- Schritt 2: Gating – je Mikrofon die Flanke im Fenster ---------
	var picks []pick
	for i, edges := range airNs {
		tPred := t0 + dist3D(coarseX, coarseY, a.mics[i])/a.vAir
		bestT, bestDiff := 0.0, math.Inf(1)
		for _, e := range edges {
			diff := math.Abs(float64(e) - tPred)
			// Flanke am NAECHSTEN zur Vorhersage (nicht die frueheste):
			// robust gegen Stoerflanken, die zufaellig frueher im
			// Fenster liegen
			if diff <= a.gateNs && diff < bestDiff {
				bestDiff = diff
				bestT = float64(e)
			}
		}
		if !math.IsInf(bestDiff, 1) {
			picks = append(picks, pick{a.mics[i], bestT})
		}
	}
	if len(picks) < 3 {
		return 0, 0, false, len(picks)
	}

	// --- Schritt 3: TOA-Multilateration mit Ausreisserpruefung ---------
	// Erst alle Picks; ist das RMS-Residuum verdaechtig hoch, war
	// vermutlich EINE Flanke falsch (Stoerflanke im Gate). Dann
	// Leave-One-Out: jede Teilmenge ohne je ein Mikrofon loesen und
	// die Loesung mit dem kleinsten Residuum nehmen.
	px, py, pt0, rms, okAll := a.solveTOA(picks, coarseX, coarseY, t0)
	if !okAll {
		return 0, 0, false, len(picks)
	}
	const rmsLimitNs = 2000 // 2 µs – sauberer Fit liegt weit darunter
	if rms > rmsLimitNs && len(picks) >= 4 {
		bestRMS := rms
		for skip := 0; skip < len(picks); skip++ {
			sub := make([]pick, 0, len(picks)-1)
			for j, p := range picks {
				if j != skip {
					sub = append(sub, p)
				}
			}
			if sx, sy, st0, srms, ok2 := a.solveTOA(sub, coarseX,
				coarseY, t0); ok2 && srms < bestRMS {
				px, py, pt0, bestRMS = sx, sy, st0, srms
			}
		}
		rms = bestRMS
		_ = pt0
		if rms > rmsLimitNs {
			return 0, 0, false, len(picks) // bleibt unzuverlaessig
		}
	}

	// --- Plausibilitaet ------------------------------------------------
	// Feinloesung darf nicht weiter als 2x Gate-Strecke von der
	// Grobloesung wegspringen (sonst falsche Flanke erwischt)
	maxJump := 2 * a.gateNs * a.vAir
	if math.Hypot(px-coarseX, py-coarseY) > maxJump {
		return 0, 0, false, len(picks)
	}
	if math.IsNaN(px) || math.IsNaN(py) {
		return 0, 0, false, len(picks)
	}
	return px, py, true, len(picks)
}

// solveTOA: Gauss-Newton ueber (x, y, t0); liefert RMS-Residuum in ns
func (a *AirSolver) solveTOA(picks []pick, x0, y0, t00 float64) (
	px, py, pt0, rms float64, ok bool) {

	px, py, pt0 = x0, y0, t00
	const maxIter = 30
	for iter := 0; iter < maxIter; iter++ {
		var H [3][3]float64
		var g [3]float64
		for _, p := range picks {
			d := dist3D(px, py, p.mic)
			if d < 1e-9 {
				d = 1e-9
			}
			r := pt0 + d/a.vAir - p.t
			row := [3]float64{
				(px - p.mic.X) / (d * a.vAir),
				(py - p.mic.Y) / (d * a.vAir),
				1.0,
			}
			for m := 0; m < 3; m++ {
				for n := 0; n < 3; n++ {
					H[m][n] += row[m] * row[n]
				}
				g[m] += row[m] * r
			}
		}
		lam := 1e-9 * (H[0][0] + H[1][1] + H[2][2])
		for m := 0; m < 3; m++ {
			H[m][m] += lam
		}
		dx, ok3 := solve3(H, [3]float64{-g[0], -g[1], -g[2]})
		if !ok3 {
			return 0, 0, 0, 0, false
		}
		px += dx[0]
		py += dx[1]
		pt0 += dx[2]
		if math.Hypot(dx[0], dx[1]) < 0.0005 {
			break
		}
	}
	var sum float64
	for _, p := range picks {
		r := pt0 + dist3D(px, py, p.mic)/a.vAir - p.t
		sum += r * r
	}
	rms = math.Sqrt(sum / float64(len(picks)))
	if math.IsNaN(px) || math.IsNaN(py) {
		return 0, 0, 0, 0, false
	}
	return px, py, pt0, rms, true
}

// pick: gegatete Flanke eines Mikrofons
type pick struct {
	mic AirSensor
	t   float64 // ns
}

// solve3: loest H*x=b (3x3) per Cramer
func solve3(H [3][3]float64, b [3]float64) ([3]float64, bool) {
	det := H[0][0]*(H[1][1]*H[2][2]-H[1][2]*H[2][1]) -
		H[0][1]*(H[1][0]*H[2][2]-H[1][2]*H[2][0]) +
		H[0][2]*(H[1][0]*H[2][1]-H[1][1]*H[2][0])
	if math.Abs(det) < 1e-15 {
		return [3]float64{}, false
	}
	inv := 1 / det
	var x [3]float64
	x[0] = inv * (b[0]*(H[1][1]*H[2][2]-H[1][2]*H[2][1]) -
		H[0][1]*(b[1]*H[2][2]-H[1][2]*b[2]) +
		H[0][2]*(b[1]*H[2][1]-H[1][1]*b[2]))
	x[1] = inv * (H[0][0]*(b[1]*H[2][2]-H[1][2]*b[2]) -
		b[0]*(H[1][0]*H[2][2]-H[1][2]*H[2][0]) +
		H[0][2]*(H[1][0]*b[2]-b[1]*H[2][0]))
	x[2] = inv * (H[0][0]*(H[1][1]*b[2]-b[1]*H[2][1]) -
		H[0][1]*(H[1][0]*b[2]-b[1]*H[2][0]) +
		b[0]*(H[1][0]*H[2][1]-H[1][1]*H[2][0]))
	return x, true
}
