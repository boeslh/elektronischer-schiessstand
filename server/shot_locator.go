// ============================================================================
// shot_locator.go – Server-seitiger Port von shot_locator.h (Firmware Rev
// 4.11.0, SET ALGO=RIM) fuer den Kalibrier-Simulator.
//
// 1:1-Port der Datei shot_locator.h (portabler C++-Header, laut Datei-Kopf
// "identisch auf ESP32 und PC" - siehe auch test/host_sim.cpp, dieselbe
// Portierungs-Idee, nur nach Go statt in dessen eigenem C++-Host-Test). Wie
// simulator.go bewusst mit float32 gerechnet (Firmware "float", nicht
// "double"), um das Rundungsverhalten moeglichst exakt nachzubilden.
//
// WICHTIG - Zeitbasis: shot_locator.h erwartet Flankenzeiten relativ zum
// PIEZO-ANKER (bei ALGO=RIM meldet die Firmware air_ns entsprechend um),
// waehrend simulator.go/SolveShot() bislang (ALGO=CLASSIC) Zeiten relativ
// zur insgesamt ERSTEN Flanke verwendet - siehe rimTimesFromPiezo() fuer die
// Umrechnung anhand des gespeicherten piezo_ns (gleiche Zeitbasis wie
// air_ns, siehe SimShotRaw-Kommentar in store.go). Das funktioniert auch
// fuer Schuesse, die urspruenglich mit ALGO=CLASSIC erfasst wurden - piezo_ns
// wird unabhaengig vom Auswertepfad in derselben (Erste-Flanke-relativen)
// Zeitbasis gespeichert.
// ============================================================================
package main

import "math"

const (
	rimNMic     = 6
	rimMaxEdges = 8
)

// rimGeometry entspricht shot_locator.h::Geometry.
type rimGeometry struct {
	micX, micY     [rimNMic]float32
	standoffMm     float32
	soundMmPerNs   float32
	pelletRadiusMm float32
	maxDistMm      float32
}

// rimGate entspricht shot_locator.h::Gate.
type rimGate struct {
	active           bool
	t0MinNs, t0MaxNs float32
}

// rimParams entspricht shot_locator.h::Params (Defaults wie im Header).
type rimParams struct {
	inlierTolNs  float32
	huberNs      float32
	sigmaPriorNs float32
	minMics      int
	maxIter      int
}

func defaultRimParams(minMics int) rimParams {
	return rimParams{
		inlierTolNs:  3000.0,
		huberNs:      800.0,
		sigmaPriorNs: 400.0,
		minMics:      minMics,
		maxIter:      15,
	}
}

// rimEdges entspricht shot_locator.h::Edges - Kandidatenflanken je Mic, ns
// relativ zum Anker (Piezo), aufsteigend.
type rimEdges struct {
	n       [rimNMic]int
	t       [rimNMic][rimMaxEdges]float32
	enabled [rimNMic]bool
}

// rimResult entspricht shot_locator.h::Result.
type rimResult struct {
	valid             bool
	xMm, yMm          float32
	t0Ns              float32
	sigmaXMm, sigmaYMm float32
	rmsNs             float32
	nUsed             uint8
	usedMask          uint8
	edgeIdx           [rimNMic]int8
	residNs           [rimNMic]float32
	t0InGate          bool
}

// rimTimesFromPiezo rechnet die "erste-Flanke-relativen" Rohdaten (wie in
// SimShotRaw.AirNs/SolveShot() ueberall sonst verwendet) auf eine zum Piezo-
// Anker relative Zeitbasis um, wie sie shot_locator.h/rimLocate() erwartet -
// siehe Datei-Kopf. piezoNs=nil (Piezo nicht ausgeloest, z.B. SET PIEZO=0)
// bedeutet: keine Umrechnung moeglich, ALGO=RIM kann diesen Schuss nicht
// auswerten (die Firmware selbst braucht den Piezo als alleinigen Trigger
// fuer ALGO=RIM, siehe HELP-Text "Piezo ist der EINZIGE Trigger").
func rimTimesFromPiezo(airNs [rimNMic][]int64, piezoNs *int64) (rimEdges, bool) {
	var e rimEdges
	if piezoNs == nil {
		return e, false
	}
	anchor := *piezoNs
	for i := 0; i < rimNMic; i++ {
		e.enabled[i] = true
		for k := 0; k < len(airNs[i]) && k < rimMaxEdges; k++ {
			e.t[i][e.n[i]] = float32(airNs[i][k] - anchor)
			e.n[i]++
		}
	}
	return e, true
}

// rimPathMm entspricht shot_locator.h::pathMm() - Laufweg vom naechstgelegenen
// Punkt des Lochrands (Radius pelletRadiusMm um (x,y)) zum Mikrofon i.
func rimPathMm(g rimGeometry, i int, x, y float32) (d, dDdx, dDdy float32) {
	dx, dy := x-g.micX[i], y-g.micY[i]
	rho := float32(math.Sqrt(float64(dx*dx + dy*dy)))
	if rho < 1e-3 {
		rho = 1e-3
	}
	rr := rho - g.pelletRadiusMm
	if rr < 0 {
		rr = 0
	}
	D := float32(math.Sqrt(float64(rr*rr + g.standoffMm*g.standoffMm)))
	k := rr / (D * rho)
	return D, k * dx, k * dy
}

// rimSolveTrio entspricht shot_locator.h::solveTrio() - geschlossene Loesung
// fuer 3 Mics (Punktquelle), liefert x, y und s0=c*t0 (mm) als Startwert fuer
// die Ausgleichsrechnung (der Lochrand kommt erst im Fit).
func rimSolveTrio(g rimGeometry, m [3]int, sMm [3]float32) (x, y, s0 float32, ok bool) {
	r := 0
	for k := 1; k < 3; k++ {
		if sMm[k] < sMm[r] {
			r = k
		}
	}
	a, b := (r+1)%3, (r+2)%3
	Xr, Yr := g.micX[m[r]], g.micY[m[r]]
	ra, rb := sMm[a]-sMm[r], sMm[b]-sMm[r]
	Xa, Ya := g.micX[m[a]], g.micY[m[a]]
	Xb, Yb := g.micX[m[b]], g.micY[m[b]]
	Kr := Xr*Xr + Yr*Yr
	A1, B1, C1, D1 := 2*(Xa-Xr), 2*(Ya-Yr), 2*ra, Xa*Xa+Ya*Ya-Kr-ra*ra
	A2, B2, C2, D2 := 2*(Xb-Xr), 2*(Yb-Yr), 2*rb, Xb*Xb+Yb*Yb-Kr-rb*rb
	det := A1*B2 - A2*B1
	if abs32(det) < 1e-6 {
		return 0, 0, 0, false
	}
	x0, x1 := (D1*B2-D2*B1)/det, (C2*B1-C1*B2)/det
	y0, y1 := (A1*D2-A2*D1)/det, (A2*C1-A1*C2)/det
	px, py := x0-Xr, y0-Yr
	z2 := g.standoffMm * g.standoffMm
	qa := 1.0 - x1*x1 - y1*y1
	qb := -2.0 * (px*x1 + py*y1)
	qc := -(px*px + py*py + z2)
	var d float32
	if abs32(qa) < 1e-6 {
		if abs32(qb) < 1e-6 {
			return 0, 0, 0, false
		}
		d = -qc / qb
	} else {
		disc := qb*qb - 4*qa*qc
		if disc < 0 {
			return 0, 0, 0, false
		}
		sq := float32(math.Sqrt(float64(disc)))
		d1, d2 := (-qb+sq)/(2*qa), (-qb-sq)/(2*qa)
		if d1 > 0 && (d2 <= 0 || d1 < d2) {
			d = d1
		} else if d2 > 0 {
			d = d2
		} else {
			return 0, 0, 0, false
		}
	}
	if d <= 0 {
		return 0, 0, 0, false
	}
	return x0 + x1*d, y0 + y1*d, sMm[r] - d, true
}

// rimSolve3 entspricht shot_locator.h::solve3() - 3x3 symmetrisches
// Gleichungssystem per Cholesky-Zerlegung; inv (Kovarianz) optional.
func rimSolve3(A [3][3]float32, bv *[3]float32, x *[3]float32, inv *[3][3]float32) bool {
	var L [3][3]float32
	for i := 0; i < 3; i++ {
		for j := 0; j <= i; j++ {
			s := A[i][j]
			for k := 0; k < j; k++ {
				s -= L[i][k] * L[j][k]
			}
			if i == j {
				if s <= 1e-12 {
					return false
				}
				L[i][i] = float32(math.Sqrt(float64(s)))
			} else {
				L[i][j] = s / L[j][j]
			}
		}
	}
	sub := func(rhs [3]float32) [3]float32 {
		var y, out [3]float32
		for i := 0; i < 3; i++ {
			s := rhs[i]
			for k := 0; k < i; k++ {
				s -= L[i][k] * y[k]
			}
			y[i] = s / L[i][i]
		}
		for i := 2; i >= 0; i-- {
			s := y[i]
			for k := i + 1; k < 3; k++ {
				s -= L[k][i] * out[k]
			}
			out[i] = s / L[i][i]
		}
		return out
	}
	if bv != nil && x != nil {
		*x = sub(*bv)
	}
	if inv != nil {
		for c := 0; c < 3; c++ {
			var e [3]float32
			e[c] = 1
			col := sub(e)
			for r := 0; r < 3; r++ {
				inv[r][c] = col[r]
			}
		}
	}
	return true
}

// rimScoreHypothesis entspricht shot_locator.h::scoreHypothesis() - waehlt
// je Mic die zur Hypothese (x,y,s0) passendste Flanke, liefert Inlier-Zahl
// und MSAC-Kosten (mm^2).
func rimScoreHypothesis(g rimGeometry, e rimEdges, x, y, s0, tolMm float32) (pick [rimNMic]int8, n int, cost float32) {
	for i := 0; i < rimNMic; i++ {
		pick[i] = -1
		if !e.enabled[i] || e.n[i] == 0 {
			continue
		}
		D, _, _ := rimPathMm(g, i, x, y)
		best := float32(1e30)
		bi := -1
		for k := 0; k < e.n[i]; k++ {
			r := abs32(e.t[i][k]*g.soundMmPerNs - s0 - D)
			if r < best {
				best = r
				bi = k
			}
		}
		if best < tolMm {
			pick[i] = int8(bi)
			n++
			cost += best * best
		} else {
			cost += tolMm * tolMm
		}
	}
	return
}

// rimLocate entspricht shot_locator.h::locate() - siehe Datei-Kopf dort fuer
// die 5 Verfahrensschritte (Gate/Hypothesen/Ausgleichsrechnung/Lochrand-
// Modell/Guete).
func rimLocate(g rimGeometry, in rimEdges, gate rimGate, p rimParams) (rimResult, bool) {
	var res rimResult
	for i := range res.edgeIdx {
		res.edgeIdx[i] = -1
	}
	c := g.soundMmPerNs

	// --- 1. Gate: nur physikalisch moegliche Flanken behalten -------------
	var e rimEdges
	for i := 0; i < rimNMic; i++ {
		e.enabled[i] = in.enabled[i]
		if !in.enabled[i] {
			continue
		}
		lo := gate.t0MinNs + g.standoffMm/c - p.inlierTolNs
		hi := gate.t0MaxNs + g.maxDistMm/c + p.inlierTolNs
		for k := 0; k < in.n[i] && e.n[i] < rimMaxEdges; k++ {
			t := in.t[i][k]
			if gate.active && (t < lo || t > hi) {
				continue
			}
			e.t[i][e.n[i]] = t
			e.n[i]++
		}
	}
	var micsAvail []int
	for i := 0; i < rimNMic; i++ {
		if e.n[i] > 0 {
			micsAvail = append(micsAvail, i)
		}
	}
	if len(micsAvail) < 3 {
		return res, false
	}

	// --- 2. Hypothesen (MSAC) ---------------------------------------------
	tolMm := p.inlierTolNs * c
	bestN := -1
	bestCost := float32(1e30)
	var bx, by, bs float32
	var bestPick [rimNMic]int8
	nAvail := len(micsAvail)
	for a := 0; a < nAvail; a++ {
		for b := a + 1; b < nAvail; b++ {
			for d := b + 1; d < nAvail; d++ {
				m := [3]int{micsAvail[a], micsAvail[b], micsAvail[d]}
				na, nb, nd := e.n[m[0]], e.n[m[1]], e.n[m[2]]
				if na > 2 {
					na = 2
				}
				if nb > 2 {
					nb = 2
				}
				if nd > 2 {
					nd = 2
				}
				for ka := 0; ka < na; ka++ {
					for kb := 0; kb < nb; kb++ {
						for kd := 0; kd < nd; kd++ {
							s := [3]float32{e.t[m[0]][ka] * c, e.t[m[1]][kb] * c, e.t[m[2]][kd] * c}
							x, y, s0, ok := rimSolveTrio(g, m, s)
							if !ok {
								continue
							}
							if abs32(x) > 400 || abs32(y) > 400 {
								continue
							}
							// Startwert ist eine Punktquelle - den (fast)
							// gemeinsamen Laufweg-Versatz durch den Lochrand
							// in s0 nachziehen.
							s0 = 0
							for q := 0; q < 3; q++ {
								D, _, _ := rimPathMm(g, m[q], x, y)
								s0 += s[q] - D
							}
							s0 /= 3.0
							if gate.active {
								t0 := s0 / c
								if t0 < gate.t0MinNs-p.inlierTolNs || t0 > gate.t0MaxNs+p.inlierTolNs {
									continue
								}
							}
							pick, n, cost := rimScoreHypothesis(g, e, x, y, s0, tolMm)
							if n > bestN || (n == bestN && cost < bestCost) {
								bestN, bestCost = n, cost
								bx, by, bs = x, y, s0
								bestPick = pick
							}
						}
					}
				}
			}
		}
	}
	if bestN < 3 {
		return res, false
	}

	// --- 3. Robuste Ausgleichsrechnung (Gauss-Newton + Huber) --------------
	x, y, s0 := bx, by, bs
	pick := bestPick
	kH := p.huberNs * c
	var JtWJ [3][3]float32
	for pass := 0; pass < 2; pass++ {
		for it := 0; it < p.maxIter; it++ {
			var A [3][3]float32
			var bvec [3]float32
			for i := 0; i < rimNMic; i++ {
				if pick[i] < 0 {
					continue
				}
				D, gx, gy := rimPathMm(g, i, x, y)
				r := e.t[i][pick[i]]*c - s0 - D
				w := float32(1.0)
				if abs32(r) > kH {
					w = kH / abs32(r)
				}
				J := [3]float32{gx, gy, 1.0}
				for u := 0; u < 3; u++ {
					bvec[u] += w * J[u] * r
					for v := 0; v < 3; v++ {
						A[u][v] += w * J[u] * J[v]
					}
				}
			}
			var step [3]float32
			if !rimSolve3(A, &bvec, &step, nil) {
				return res, false
			}
			x += step[0]
			y += step[1]
			s0 += step[2]
			JtWJ = A
			if abs32(step[0]) < 1e-4 && abs32(step[1]) < 1e-4 {
				break
			}
		}
		if pass == 0 {
			pick, _, _ = rimScoreHypothesis(g, e, x, y, s0, tolMm)
		}
	}

	// --- 4. Ergebnis + Kovarianz --------------------------------------------
	var ss float32
	n := 0
	var mask uint8
	for i := 0; i < rimNMic; i++ {
		res.residNs[i] = 0
		if e.n[i] == 0 {
			continue
		}
		D, _, _ := rimPathMm(g, i, x, y)
		best := float32(1e30)
		bi := 0
		for k := 0; k < e.n[i]; k++ {
			r := e.t[i][k]*c - s0 - D
			if abs32(r) < abs32(best) {
				best = r
				bi = k
			}
		}
		res.residNs[i] = best / c
		if pick[i] >= 0 {
			res.edgeIdx[i] = int8(bi)
			ss += best * best
			n++
			mask |= 1 << uint(i)
		}
	}
	var inv [3][3]float32
	if !rimSolve3(JtWJ, nil, nil, &inv) {
		return res, false
	}
	sPrior := p.sigmaPriorNs * c
	s2 := sPrior * sPrior
	if n > 3 {
		est := ss / float32(n-3)
		if est > s2 {
			s2 = est
		}
	}
	res.xMm = x
	res.yMm = y
	res.t0Ns = s0 / c
	res.sigmaXMm = float32(math.Sqrt(float64(s2 * inv[0][0])))
	res.sigmaYMm = float32(math.Sqrt(float64(s2 * inv[1][1])))
	if n > 0 {
		res.rmsNs = float32(math.Sqrt(float64(ss/float32(n)))) / c
	}
	res.nUsed = uint8(n)
	res.usedMask = mask
	res.t0InGate = !gate.active ||
		(res.t0Ns >= gate.t0MinNs-p.inlierTolNs && res.t0Ns <= gate.t0MaxNs+p.inlierTolNs)
	res.valid = n >= p.minMics && res.t0InGate
	return res, true
}
