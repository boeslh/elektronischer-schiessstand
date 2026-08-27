// ============================================================================
// tdoa.go – TDOA-Positionsberechnung (Time Difference of Arrival)
//
// Problem: Gegeben N Sensoren an bekannten Positionen S_i auf dem Blech und
// die relativen Eintreffzeiten dt_i des Koerperschalls (Referenz = zuerst
// getroffener Sensor). Gesucht: Einschlagpunkt P = (x, y).
//
// Modell:   |P - S_i| - |P - S_ref| = v * dt_i      (Hyperbel je Sensorpaar)
//
// Loesung:  Gauss-Newton auf den Residuen
//             r_i(P) = (|P-S_i| - |P-S_ref|) - v*dt_i
//           2 Unbekannte (x,y), N-1 Residuen -> ab 3 Sensoren loesbar,
//           ab 4 Sensoren ueberbestimmt (Redundanz -> Confidence-Wert).
//
// Bewusst ohne externe Mathe-Bibliothek: Die 2x2-Normalengleichung wird
// direkt geloest (Cramer). ~100 Zeilen, keine gonum-Abhaengigkeit.
// ============================================================================
package main

import (
	"fmt"
	"math"
)

type TDOASolver struct {
	sensors    []SensorPos
	soundSpeed float64 // mm/ns  (intern umgerechnet!)
	plateAngle float64 // rad
	offsetX    float64
	offsetY    float64
	plateCX    float64 // Blechmittelpunkt als Startwert
	plateCY    float64
}

func NewTDOASolver(cfg *Config) *TDOASolver {
	return NewTDOASolverParams(cfg.Sensors, cfg.SoundSpeedMPS,
		cfg.PlateAngleDeg, cfg.PlateOffsetX, cfg.PlateOffsetY)
}

// NewTDOASolverParams baut einen Solver aus expliziten Kalibrierwerten –
// genutzt fuer die zur Laufzeit vom Server uebernommene Kalibrierung.
func NewTDOASolverParams(sensors []SensorPos, soundSpeedMPS,
	plateAngleDeg, offsetX, offsetY float64) *TDOASolver {

	s := &TDOASolver{
		sensors: sensors,
		// m/s -> mm/ns:  v[mm/ns] = v[m/s] * 1000 mm/m / 1e9 ns/s = v/1e6
		soundSpeed: soundSpeedMPS / 1e6,
		plateAngle: plateAngleDeg * math.Pi / 180.0,
		offsetX:    offsetX,
		offsetY:    offsetY,
	}
	for _, p := range sensors {
		s.plateCX += p.X
		s.plateCY += p.Y
	}
	n := float64(len(sensors))
	s.plateCX /= n
	s.plateCY /= n
	return s
}

// Solve berechnet die Einschlagposition auf dem Blech.
// tNs: Zeitstempel je Sensor in ns relativ zum ersten Hit; -1 = kein Hit.
// Rueckgabe: x, y (Blechkoordinaten mm), confidence (0..1), Fehler.
func (s *TDOASolver) Solve(tNs []int64) (x, y, confidence float64, err error) {
	if len(tNs) != len(s.sensors) {
		return 0, 0, 0, fmt.Errorf("erwartet %d Zeitwerte, erhalten %d",
			len(s.sensors), len(tNs))
	}

	// Gueltige Sensoren einsammeln; Referenz = Sensor mit t==0 (erster Hit)
	type meas struct {
		pos SensorPos
		dt  float64 // ns relativ zur Referenz
	}
	var ms []meas
	refIdx := -1
	for i, t := range tNs {
		if t < 0 {
			continue // Sensor hat nicht ausgeloest
		}
		if t == 0 {
			refIdx = i
		}
		ms = append(ms, meas{s.sensors[i], float64(t)})
	}
	if len(ms) < 3 {
		return 0, 0, 0, fmt.Errorf("nur %d Sensoren – mind. 3 noetig", len(ms))
	}
	if refIdx < 0 {
		return 0, 0, 0, fmt.Errorf("kein Referenzsensor (t=0) im Telegramm")
	}
	ref := s.sensors[refIdx]

	// ---- Gauss-Newton-Iteration ----
	// Startwert: Blechmitte (konvergiert fuer alle realistischen Treffer)
	px, py := s.plateCX, s.plateCY

	dist := func(x, y float64, p SensorPos) float64 {
		return math.Hypot(x-p.X, y-p.Y)
	}

	const maxIter = 25
	const epsPos = 0.001 // mm – Konvergenzkriterium

	var lastRMS float64
	for iter := 0; iter < maxIter; iter++ {
		// Normalengleichung J^T J * delta = -J^T r  aufbauen (2x2-System)
		var jtj00, jtj01, jtj11, jtr0, jtr1 float64
		dRef := dist(px, py, ref)
		if dRef < 1e-9 {
			dRef = 1e-9
		}
		// Gradient des Referenzterms
		gRefX := (px - ref.X) / dRef
		gRefY := (py - ref.Y) / dRef

		var sumR2 float64
		nRes := 0
		for _, m := range ms {
			if m.pos == ref {
				continue
			}
			dI := dist(px, py, m.pos)
			if dI < 1e-9 {
				dI = 1e-9
			}
			// Residuum: gemessene vs. modellierte Laufzeitdifferenz (in mm)
			r := (dI - dRef) - s.soundSpeed*m.dt
			// Jacobian-Zeile: d r / d(px,py)
			jx := (px-m.pos.X)/dI - gRefX
			jy := (py-m.pos.Y)/dI - gRefY

			jtj00 += jx * jx
			jtj01 += jx * jy
			jtj11 += jy * jy
			jtr0 += jx * r
			jtr1 += jy * r
			sumR2 += r * r
			nRes++
		}
		lastRMS = math.Sqrt(sumR2 / float64(nRes))

		// 2x2 loesen (Cramer) mit Levenberg-Daempfung fuer Stabilitaet
		lambda := 1e-6 * (jtj00 + jtj11)
		jtj00 += lambda
		jtj11 += lambda
		det := jtj00*jtj11 - jtj01*jtj01
		if math.Abs(det) < 1e-12 {
			return 0, 0, 0, fmt.Errorf("singulaere Geometrie (det~0)")
		}
		dx := (-jtr0*jtj11 + jtr1*jtj01) / det
		dy := (-jtj00*jtr1 + jtj01*jtr0) / det

		px += dx
		py += dy

		if math.Hypot(dx, dy) < epsPos {
			break
		}
	}

	// Plausibilitaet: Loesung muss im (grosszuegigen) Blechbereich liegen
	if !s.insidePlate(px, py, 50 /*mm Toleranz*/) {
		return 0, 0, 0, fmt.Errorf(
			"Loesung ausserhalb des Blechs (%.0f/%.0f)", px, py)
	}

	// Confidence aus RMS-Residuum: 0mm -> 1.0; >=2mm -> 0
	confidence = math.Max(0, 1.0-lastRMS/2.0)
	return px, py, confidence, nil
}

func (s *TDOASolver) insidePlate(x, y, tol float64) bool {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, p := range s.sensors {
		minX = math.Min(minX, p.X)
		minY = math.Min(minY, p.Y)
		maxX = math.Max(maxX, p.X)
		maxY = math.Max(maxY, p.Y)
	}
	return x >= minX-tol && x <= maxX+tol && y >= minY-tol && y <= maxY+tol
}

// PlateToTarget transformiert Blechkoordinaten in Scheibenkoordinaten.
//
// Das Blech steht um plateAngle gegen die (senkrechte) Scheibe geneigt und
// ist um (offsetX, offsetY) versetzt. Bei horizontaler Kippachse wird die
// VERTIKALE Achse des Blechs auf der Scheibe um cos(angle) gestaucht:
//
//	target_x = (plate_x - cx) + offsetX
//	target_y = (plate_y - cy) * cos(angle) + offsetY
//
// (cx, cy) = Blechmittelpunkt; Feinfehler korrigiert die Kalibrierung
// ueber offsetX/offsetY und ggf. angepasste Sensorkoordinaten.
func (s *TDOASolver) PlateToTarget(plateX, plateY float64) (x, y float64) {
	x = (plateX - s.plateCX) + s.offsetX
	y = (plateY-s.plateCY)*math.Cos(s.plateAngle) + s.offsetY
	return x, y
}
