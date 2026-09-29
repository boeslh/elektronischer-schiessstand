// ============================================================================
// sim_posalgo.go – Varianten der Endpositions-Bestimmung. CorrCluster,
// Iterations und LSQ sind seit Firmware Rev 4.12.0 auch auf dem Geraet
// umgesetzt (SET CLUSTERCORR/CLUSTERITER/LSQ/LSQPELLETR, dort mit identischem
// Ergebnis, per Host-Test gegen echte Schuesse geprueft); Weighted gibt es
// nur im Simulator.
//
// Die Firmware bestimmt die Endposition (Stufe 2 in airPosition()) als
// ungewichteten Mittelwert aller Schnittpunkte im Cluster-Radius um die
// Stufe-1-Loesung - dabei ist nur die Stufe-1-Loesung kugeldurchmesser-
// korrigiert, alle anderen Schnittpunkte gehen roh ein. Die Optionen hier
// ersetzen diese Stufe 2 (einzeln oder kombiniert); alle aus = exakt das
// Verhalten bis Firmware 4.11.5. Geschaetzter Mehraufwand am ESP32 je Option
// (bezogen auf die Auswertung eines Schusses bis 4.11.5, dominiert von den bis zu 20
// solveAirPair()-Loesungen):
//
//	CorrCluster: Kugelkorrektur je Schnittpunkt (bis zu 20 x 3 Wurzeln)  ~ +60%
//	Iterations:  je Durchgang 20 Abstaende                               ~ +5% je Durchgang
//	Weighted:    Gewicht je Schnittpunkt                                  ~ +2%
//	LSQ:         Gauss-Newton (6 Iter. x 6 Mics), bis 2 Wiederholungen    ~ +30..90%
//
// Alle zusammen damit deutlich unter dem Faktor 5.
// ============================================================================
package main

import "math"

// PosAlgoOptions: Varianten der Endpositions-Bestimmung (Nullwert =
// Verhalten bis Firmware 4.11.5). Alle ausser Weighted werden per
// firmwareSettings an das Geraet uebertragen.
type PosAlgoOptions struct {
	// CorrCluster: ALLE Schnittpunkte kugeldurchmesser-korrigiert
	// (airCandidate.CorrX/CorrY) auswaehlen und mitteln - konsistent statt
	// "ein korrigierter + n rohe Punkte".
	CorrCluster bool `json:"corr_cluster,omitempty"`
	// Iterations: zusaetzliche Durchgaenge, in denen der Cluster um den
	// zuletzt berechneten Mittelpunkt neu ausgewaehlt wird (Mean-Shift) -
	// robuster, wenn die Stufe-1-Loesung selbst am Rand des Clusters liegt.
	Iterations int `json:"iterations,omitempty"`
	// Weighted: Mittelung gewichtet mit 1/(Rest-Fehler^2 + 0.25mm^2) statt
	// ungewichtet - konsistente Mikrofon-Kombinationen zaehlen mehr.
	Weighted bool `json:"weighted,omitempty"`
	// LSQ: Endposition zusaetzlich per Ausgleichsrechnung (Gauss-Newton)
	// ueber ALLE ausgeloesten Mikrofone gleichzeitig verfeinern, Start im
	// Cluster-Mittelpunkt. Modell: Schall vom naechstgelegenen
	// Punkt des Lochrands (Radius LSQPelletRMm, 0 = Punktquelle), ein
	// deutlich abweichendes Mikrofon wird einmalig verworfen. Ersetzt die
	// Kugeldurchmesser-Korrektur (BSHIFTPCT/BSHIFTCAP wirken dann nur noch
	// auf die Clusterbildung als Startwert).
	LSQ          bool    `json:"lsq,omitempty"`
	LSQPelletRMm float64 `json:"lsq_pellet_r_mm,omitempty"`
}

func (o PosAlgoOptions) clusterActive() bool {
	return o.CorrCluster || o.Iterations > 0 || o.Weighted
}

func (o PosAlgoOptions) active() bool { return o.clusterActive() || o.LSQ }

// refinePosition bestimmt die Endposition nach o neu. fwX/fwY ist das
// Ergebnis der Stufe 2 bis Firmware 4.11.5, das unveraendert bleibt, soweit
// keine Option es ersetzt. clusterHits/precMM werden wie in Firmware 4.12.0
// relativ zur Endposition neu bestimmt - bei CorrCluster gegen die
// korrigierten, sonst gegen die rohen Schnittpunkte.
func refinePosition(cands []airCandidate, fwX, fwY float32, tNs [6]int64, seen [6]bool,
	micX, micY [6]float32, standoffMM, soundMmPerNs, radiusMM float32, o PosAlgoOptions) (x, y float32, clusterHits int, precMM float32) {

	x, y = fwX, fwY
	if o.clusterActive() {
		best := -1
		for i := range cands {
			if cands[i].Best {
				best = i
			}
		}
		if best >= 0 {
			pt := func(c airCandidate) (float32, float32) {
				if o.CorrCluster || c.Best {
					return c.CorrX, c.CorrY // Firmware: nur die Stufe-1-Loesung korrigiert
				}
				return c.X, c.Y
			}
			cx, cy := cands[best].CorrX, cands[best].CorrY // Stufe-1-Loesung (korrigiert)
			for pass := 0; pass <= o.Iterations; pass++ {
				var sx, sy, sw float64
				for _, c := range cands {
					px, py := pt(c)
					if math.Hypot(float64(px-cx), float64(py-cy)) > float64(radiusMM) {
						continue
					}
					w := 1.0
					if o.Weighted {
						r := float64(c.ResidualMM)
						w = 1 / (r*r + 0.25*0.25)
					}
					sx += w * float64(px)
					sy += w * float64(py)
					sw += w
				}
				if sw == 0 {
					break
				}
				nx, ny := float32(sx/sw), float32(sy/sw)
				moved := math.Hypot(float64(nx-cx), float64(ny-cy))
				cx, cy = nx, ny
				if moved < 1e-4 {
					break
				}
			}
			x, y = cx, cy
		}
	}
	if o.LSQ {
		if lx, ly, ok := lsqPosition(x, y, tNs, seen, micX, micY, standoffMM, soundMmPerNs, float32(o.LSQPelletRMm)); ok {
			x, y = lx, ly
		}
	}

	// cluster_hits/precision (zwei naechste Schnittpunkte, RMS) wie Firmware
	// 4.12.0 solveAirPosition().
	var d1, d2 float32 = -1, -1
	for _, c := range cands {
		px, py := c.X, c.Y
		if o.CorrCluster {
			px, py = c.CorrX, c.CorrY
		}
		dist := float32(math.Hypot(float64(px-x), float64(py-y)))
		if dist <= radiusMM {
			clusterHits++
		}
		if d1 < 0 || dist < d1 {
			d2, d1 = d1, dist
		} else if d2 < 0 || dist < d2 {
			d2 = dist
		}
	}
	var n, sq float32
	for _, d := range []float32{d1, d2} {
		if d >= 0 {
			n++
			sq += d * d
		}
	}
	if n > 0 {
		precMM = float32(math.Sqrt(float64(sq / n)))
	}
	return x, y, clusterHits, precMM
}

// lsqPosition: Gauss-Newton ueber (x, y, s0) mit Residuen
//
//	r_m = D_m(x,y) - (s0 + c*t_m)
//
// fuer alle ausgeloesten Mikrofone (D_m wie rimPathMm: Laufweg vom Lochrand).
// Braucht >= 4 Mikrofone (3 sind bereits exakt durch die Schnittpunkte
// bestimmt), Ausreisser-Verwerfung siehe unten (nur bei >= 5). Verwirft das Ergebnis, wenn es mehr als 10mm
// vom Startpunkt wegwandert (Divergenz).
func lsqPosition(x0, y0 float32, tNs [6]int64, seen [6]bool, micX, micY [6]float32,
	standoffMM, soundMmPerNs, pelletR float32) (float32, float32, bool) {

	use := seen
	count := func() int {
		n := 0
		for _, u := range use {
			if u {
				n++
			}
		}
		return n
	}
	h2 := float64(standoffMM) * float64(standoffMM)
	path := func(i int, x, y float64) (d, ddx, ddy float64) {
		dx, dy := x-float64(micX[i]), y-float64(micY[i])
		rho := math.Max(math.Hypot(dx, dy), 1e-3)
		rr := math.Max(rho-float64(pelletR), 0)
		d = math.Sqrt(rr*rr + h2)
		k := rr / (d * rho)
		return d, k * dx, k * dy
	}
	c := float64(soundMmPerNs)

	solve := func() (x, y float64, res [6]float64, ok bool) {
		x, y = float64(x0), float64(y0)
		var s0 float64
		n := 0
		for i := 0; i < 6; i++ {
			if use[i] {
				d, _, _ := path(i, x, y)
				s0 += d - c*float64(tNs[i])
				n++
			}
		}
		s0 /= float64(n)
		for it := 0; it < 6; it++ {
			var A [3][3]float64
			var b [3]float64
			for i := 0; i < 6; i++ {
				if !use[i] {
					continue
				}
				d, gx, gy := path(i, x, y)
				r := d - (s0 + c*float64(tNs[i]))
				J := [3]float64{gx, gy, -1}
				for a := 0; a < 3; a++ {
					for bb := 0; bb < 3; bb++ {
						A[a][bb] += J[a] * J[bb]
					}
					b[a] -= J[a] * r
				}
			}
			dlt, okS := solve3(A, b)
			if !okS {
				return 0, 0, res, false
			}
			x, y, s0 = x+dlt[0], y+dlt[1], s0+dlt[2]
			if math.Hypot(dlt[0], dlt[1]) < 1e-5 {
				break
			}
		}
		if math.Hypot(x-float64(x0), y-float64(y0)) > 10 {
			return 0, 0, res, false
		}
		for i := 0; i < 6; i++ {
			if use[i] {
				d, _, _ := path(i, x, y)
				res[i] = d - (s0 + c*float64(tNs[i]))
			}
		}
		return x, y, res, true
	}

	if count() < 4 {
		return x0, y0, false
	}
	x, y, res, ok := solve()
	if !ok {
		return x0, y0, false
	}
	// Ausreisser: die (bis zu) zwei Mikrofone mit dem groessten Residuum
	// probeweise weglassen - ein grob falsches Mikrofon verteilt seinen Fehler
	// in der Ausgleichsrechnung auf alle, eine reine Schwelle greift daher
	// nicht zuverlaessig. Uebernommen wird eine Loesung ohne dieses Mikrofon,
	// wenn ihr RMS-Residuum weniger als halb so gross ist.
	if count() >= 5 && rmsRes(res, use) > 0.1 {
		bestRMS := rmsRes(res, use)
		order := worstMics(res, use)
		for k := 0; k < 2 && k < len(order); k++ {
			full := use
			use[order[k]] = false
			if x2, y2, r2, ok2 := solve(); ok2 && rmsRes(r2, use) < 0.5*bestRMS {
				x, y, bestRMS = x2, y2, rmsRes(r2, use)
				use = full
				use[order[k]] = false
				break
			}
			use = full
		}
	}
	return float32(x), float32(y), true
}

// solve3 loest das 3x3-System A*x = b (Cramer).
func solve3(A [3][3]float64, b [3]float64) ([3]float64, bool) {
	det := func(m [3][3]float64) float64 {
		return m[0][0]*(m[1][1]*m[2][2]-m[1][2]*m[2][1]) -
			m[0][1]*(m[1][0]*m[2][2]-m[1][2]*m[2][0]) +
			m[0][2]*(m[1][0]*m[2][1]-m[1][1]*m[2][0])
	}
	d := det(A)
	if math.Abs(d) < 1e-12 {
		return [3]float64{}, false
	}
	var out [3]float64
	for k := 0; k < 3; k++ {
		m := A
		for r := 0; r < 3; r++ {
			m[r][k] = b[r]
		}
		out[k] = det(m) / d
	}
	return out, true
}

func rmsRes(res [6]float64, use [6]bool) float64 {
	var sum float64
	n := 0
	for i := 0; i < 6; i++ {
		if use[i] {
			sum += res[i] * res[i]
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return math.Sqrt(sum / float64(n))
}

// worstMics: benutzte Mikrofone nach absteigendem |Residuum|.
func worstMics(res [6]float64, use [6]bool) []int {
	var idx []int
	for i := 0; i < 6; i++ {
		if use[i] {
			idx = append(idx, i)
		}
	}
	for i := 1; i < len(idx); i++ {
		for j := i; j > 0 && math.Abs(res[idx[j]]) > math.Abs(res[idx[j-1]]); j-- {
			idx[j], idx[j-1] = idx[j-1], idx[j]
		}
	}
	return idx
}
