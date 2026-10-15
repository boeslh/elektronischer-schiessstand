// ============================================================================
// sim_zone_assign.go – Loch-Schuss-Zuordnung fuer die One-Click-Kalibrierung.
//
// Die Zuordnung im Browser (simulator-foto.js assignHoles) gleicht nur eine
// gemeinsame Aehnlichkeitstransformation (Verschiebung, Drehung, Massstab)
// aus. Weichen die Ausgangsparameter je Bereich der Scheibe unterschiedlich
// stark ab (Zonen!), bleiben Schuesse ohne Loch oder werden vertauscht - am
// 12.10.2026 fehlten so die Schuesse rechts neben dem Band, Zone B wurde nur
// an der linken Seite kalibriert und lag rechts um 9,5 mm daneben.
//
// Hier wird abwechselnd der Basissatz an den aktuellen Paaren kalibriert und
// mit ihm ALLE Schuesse neu berechnet und den Loechern optimal zugeordnet
// (Ungarische Methode, minimale Summe der quadrierten Abstaende), bis sich
// die Zuordnung nicht mehr aendert.
// ============================================================================
package main

import "math"

// zoneAssignGateMM: groesster zulaessiger Abstand Neuberechnung <-> Loch.
const zoneAssignGateMM = 10.0

type zoneCalPair struct {
	ShotNo int     `json:"shot_no"`
	Hole   int     `json:"hole"` // Index in den uebergebenen Loechern
	XMM    float64 `json:"x_mm"`
	YMM    float64 `json:"y_mm"`
	SimX   float64 `json:"sim_x"` // Neuberechnung mit dem Basissatz
	SimY   float64 `json:"sim_y"`
}

// hungarian: Zuordnung Zeilen -> Spalten mit minimalen Gesamtkosten
// (quadratische Kostenmatrix, O(n^3), Potential-Variante).
func hungarian(cost [][]float64) []int {
	n := len(cost)
	const inf = math.MaxFloat64 / 4
	u := make([]float64, n+1)
	v := make([]float64, n+1)
	p := make([]int, n+1)
	way := make([]int, n+1)
	for i := 1; i <= n; i++ {
		p[0] = i
		j0 := 0
		minv := make([]float64, n+1)
		used := make([]bool, n+1)
		for j := range minv {
			minv[j] = inf
		}
		for {
			used[j0] = true
			i0, delta, j1 := p[j0], inf, 0
			for j := 1; j <= n; j++ {
				if used[j] {
					continue
				}
				cur := cost[i0-1][j-1] - u[i0] - v[j]
				if cur < minv[j] {
					minv[j], way[j] = cur, j0
				}
				if minv[j] < delta {
					delta, j1 = minv[j], j
				}
			}
			for j := 0; j <= n; j++ {
				if used[j] {
					u[p[j]] += delta
					v[j] -= delta
				} else {
					minv[j] -= delta
				}
			}
			j0 = j1
			if p[j0] == 0 {
				break
			}
		}
		for {
			j1 := way[j0]
			p[j0] = p[j1]
			j0 = j1
			if j0 == 0 {
				break
			}
		}
	}
	rowToCol := make([]int, n)
	for j := 1; j <= n; j++ {
		if p[j] > 0 {
			rowToCol[p[j]-1] = j - 1
		}
	}
	return rowToCol
}

// assignHolesOptimal: Schuesse (Lagen) zu Loechern; Paare ueber gateMM
// bleiben unzugeordnet (Dummy-Zeilen/-Spalten mit Kosten gate^2).
func assignHolesOptimal(pos [][2]float64, valid []bool, holes [][2]float64, gateMM float64) map[int]int {
	ns, nh := len(pos), len(holes)
	n := ns + nh
	gate2 := gateMM * gateMM
	cost := make([][]float64, n)
	for i := 0; i < n; i++ {
		cost[i] = make([]float64, n)
		for j := 0; j < n; j++ {
			switch {
			case i < ns && j < nh:
				if !valid[i] {
					cost[i][j] = 4 * gate2
					continue
				}
				d2 := math.Pow(pos[i][0]-holes[j][0], 2) + math.Pow(pos[i][1]-holes[j][1], 2)
				if d2 > gate2 {
					d2 = 4 * gate2 // verboten (teurer als "frei lassen")
				}
				cost[i][j] = d2
			case i < ns || j < nh:
				cost[i][j] = gate2 // Schuss bzw. Loch bleibt frei
			}
		}
	}
	a := hungarian(cost)
	out := map[int]int{}
	for i := 0; i < ns; i++ {
		j := a[i]
		if j < nh && valid[i] && cost[i][j] <= gate2 {
			out[i] = j
		}
	}
	return out
}

// zoneAssign: siehe Dateikopf. initial = Paare aus dem Browser (Start),
// liefert die endgueltigen Paare.
func zoneAssign(cands []SimShotRaw, holes [][2]float64, initial []zoneCalInput, p SimParams, opt PhotoCalOptions) ([]zoneCalInput, []zoneCalPair) {
	cur := initial
	base := p
	base.Zones = nil
	var pairs []zoneCalPair
	key := func(in []zoneCalInput) string {
		b := make([]byte, 0, len(in)*8)
		for _, s := range in {
			b = append(b, byte(s.Raw.ShotNo), byte(s.Raw.ShotNo>>8), byte(int(s.TruthX*100)), byte(int(s.TruthY*100)))
		}
		return string(b)
	}
	for iter := 0; iter < 5; iter++ {
		fitted := base
		if len(cur) >= 6 {
			var cal []PhotoCalShot
			for _, s := range cur {
				cal = append(cal, PhotoCalShot{ShotNo: s.Raw.ShotNo, AirNs: s.Raw.airFor(base), PiezoNs: s.Raw.PiezoNs, TruthX: s.TruthX, TruthY: s.TruthY})
			}
			if rep, err := PhotoCalibrate(cal, base, opt); err == nil {
				fitted = rep.Params
				fitted.Zones = nil
			}
		}
		pos := make([][2]float64, len(cands))
		valid := make([]bool, len(cands))
		for i, sh := range cands {
			r := SolveSimShot(sh, fitted)
			if r.PosValid {
				valid[i] = true
				pos[i] = [2]float64{float64(r.XUm) / 1000, float64(r.YUm) / 1000}
			}
		}
		asg := assignHolesOptimal(pos, valid, holes, zoneAssignGateMM)
		var next []zoneCalInput
		pairs = pairs[:0]
		for i, sh := range cands {
			j, ok := asg[i]
			if !ok {
				continue
			}
			next = append(next, zoneCalInput{Raw: sh, TruthX: holes[j][0], TruthY: holes[j][1]})
			pairs = append(pairs, zoneCalPair{ShotNo: sh.ShotNo, Hole: j, XMM: holes[j][0], YMM: holes[j][1], SimX: pos[i][0], SimY: pos[i][1]})
		}
		if key(next) == key(cur) {
			cur = next
			break
		}
		cur = next
	}
	return cur, pairs
}
