// ============================================================================
// sim_zone_fit.go – Anpassung der Zonen-Saetze (siehe sim_zone_cal.go).
// ============================================================================
package main

import "math"

// zoneFitLM: Levenberg-Marquardt ueber Schallgeschwindigkeit, OFS1-5 und
// Nachkorrektur gegen die Foto-Lagen (Reste dx/dy je Schuss, robust nach
// Huber ab 1,5 mm). In der Studie (calib_study_test.go, Leave-one-out)
// deutlich besser uebertragbar als die Mustersuche der Foto-Kalibrierung,
// deren Kostenfunktion zusaetzlich die Selbstkonsistenz der Mikrofone
// enthaelt. Die Offsets sind schwach an den Startwert gebunden
// (zoneRidgeNs). Ergebnis auf Firmware-Aufloesung gerundet.
func zoneFitLM(in []zoneCalInput, start SimParams, opt PhotoCalOptions) ZoneSet {
	const hub = 1.5
	type par struct {
		get  func(p *SimParams) float64
		set  func(p *SimParams, v float64)
		step float64
	}
	var ps []par
	if opt.SoundSpeed {
		ps = append(ps, par{func(p *SimParams) float64 { return p.SoundMps }, func(p *SimParams, v float64) { p.SoundMps = v }, 0.5})
	}
	if opt.MicOffsets {
		for i := 1; i < 6; i++ {
			i := i
			ps = append(ps, par{func(p *SimParams) float64 { return float64(p.MicOffsetNs[i]) },
				func(p *SimParams, v float64) {
					p.MicOffsetNs[i] = int64(math.Round(math.Max(-micOfsMaxNs, math.Min(micOfsMaxNs, v))))
				}, 300})
		}
	}
	if opt.Offset {
		ps = append(ps,
			par{func(p *SimParams) float64 { return float64(p.OffsetXUm) }, func(p *SimParams, v float64) { p.OffsetXUm = int64(math.Round(v)) }, 100},
			par{func(p *SimParams) float64 { return float64(p.OffsetYUm) }, func(p *SimParams, v float64) { p.OffsetYUm = int64(math.Round(v)) }, 100})
	}
	start.Zones = nil
	resid := func(p SimParams) []float64 {
		r := make([]float64, 0, 2*len(in)+5)
		// schwache Bindung der Offsets an den Basissatz (zoneRidgeNs je 1 mm
		// Lagefehler): verhindert, dass bei wenigen Schuessen einzelne
		// Offsets an die Firmware-Grenze laufen
		if zoneRidgeNs > 0 && opt.MicOffsets {
			for i := 1; i < 6; i++ {
				r = append(r, float64(p.MicOffsetNs[i]-start.MicOffsetNs[i])/zoneRidgeNs)
			}
		}
		for _, s := range in {
			res := SolveSimShot(s.Raw, p)
			dx, dy := photoCalCapMM, photoCalCapMM
			if res.PosValid {
				dx, dy = float64(res.XUm)/1000-s.TruthX, float64(res.YUm)/1000-s.TruthY
			}
			for _, v := range []float64{dx, dy} {
				if math.Abs(v) > hub {
					v = math.Copysign(math.Sqrt(hub*(2*math.Abs(v)-hub)), v)
				}
				r = append(r, v)
			}
		}
		return r
	}
	cost := func(r []float64) float64 {
		s := 0.0
		for _, v := range r {
			s += v * v
		}
		return s
	}
	p := start
	cur := resid(p)
	c0 := cost(cur)
	lambda := 1e-2
	n := len(ps)
	for it := 0; it < 100 && n > 0; it++ {
		J := make([][]float64, n)
		for a, q := range ps {
			pp := p
			q.set(&pp, q.get(&p)+q.step)
			h := q.get(&pp) - q.get(&p)
			if h == 0 {
				h = q.step
			}
			rp := resid(pp)
			J[a] = make([]float64, len(cur))
			for j := range cur {
				J[a][j] = (rp[j] - cur[j]) / h
			}
		}
		A := make([][]float64, n)
		g := make([]float64, n)
		for a := 0; a < n; a++ {
			A[a] = make([]float64, n)
			for b := 0; b < n; b++ {
				for j := range cur {
					A[a][b] += J[a][j] * J[b][j]
				}
			}
			for j := range cur {
				g[a] -= J[a][j] * cur[j]
			}
		}
		improved := false
		for tries := 0; tries < 8; tries++ {
			M := make([][]float64, n)
			for a := range A {
				M[a] = make([]float64, n+1)
				copy(M[a], A[a])
				M[a][a] += lambda*A[a][a] + 1e-9
				M[a][n] = g[a]
			}
			d := gaussSolve(M)
			if d == nil {
				lambda *= 10
				continue
			}
			pp := p
			for a, q := range ps {
				q.set(&pp, q.get(&p)+d[a])
			}
			pp.SoundMps = math.Round(math.Max(300, math.Min(400, pp.SoundMps))*10) / 10
			r := resid(pp)
			if c := cost(r); c < c0-1e-9 {
				p, cur, c0 = pp, r, c
				lambda = math.Max(lambda/3, 1e-6)
				improved = true
				break
			}
			lambda *= 10
		}
		if !improved {
			break
		}
	}
	return ZoneSet{SoundMps: p.SoundMps, MicOffsetNs: p.MicOffsetNs, OffsetXUm: p.OffsetXUm, OffsetYUm: p.OffsetYUm}
}

// gaussSolve: Gauss-Elimination mit Pivotsuche auf der erweiterten Matrix
// (n x n+1); nil bei singulaerer Matrix.
func gaussSolve(M [][]float64) []float64 {
	n := len(M)
	for c := 0; c < n; c++ {
		piv := c
		for r := c + 1; r < n; r++ {
			if math.Abs(M[r][c]) > math.Abs(M[piv][c]) {
				piv = r
			}
		}
		if math.Abs(M[piv][c]) < 1e-15 {
			return nil
		}
		M[c], M[piv] = M[piv], M[c]
		for r := 0; r < n; r++ {
			if r == c {
				continue
			}
			f := M[r][c] / M[c][c]
			for k := c; k <= n; k++ {
				M[r][k] -= f * M[c][k]
			}
		}
	}
	x := make([]float64, n)
	for i := range x {
		x[i] = M[i][n] / M[i][i]
	}
	return x
}

// zoneCovered: verteilen sich die Schuesse der Zone so, dass Mikrofon-
// Offsets bestimmbar sind? Liegen sie nur auf einer Seite, wuerden die
// Offsets die andere Seite falsch hochrechnen (12.10.2026: Zone B nur links
// kalibriert -> rechts 9,5 mm daneben).
func zoneCovered(zone string, zs []zoneCalInput) bool {
	cnt := func(f func(s zoneCalInput) bool) int {
		n := 0
		for _, s := range zs {
			if f(s) {
				n++
			}
		}
		return n
	}
	switch zone {
	case "B": // links und rechts neben dem Band
		return cnt(func(s zoneCalInput) bool { return s.TruthX < 0 }) >= 2 && cnt(func(s zoneCalInput) bool { return s.TruthX > 0 }) >= 2
	case "C": // oberhalb und unterhalb des Lochs
		return cnt(func(s zoneCalInput) bool { return s.TruthY < 0 }) >= 2 && cnt(func(s zoneCalInput) bool { return s.TruthY > 0 }) >= 2
	}
	q := map[int]bool{} // A: mindestens drei Quadranten
	for _, s := range zs {
		k := 0
		if s.TruthX > 0 {
			k |= 1
		}
		if s.TruthY > 0 {
			k |= 2
		}
		q[k] = true
	}
	return len(q) >= 3
}

// zoneChooseFit: Satz einer Zone. Kandidaten nach Schusszahl und
// Verteilung (zoneFitOptions/zoneCovered); ab 5 Schuessen entscheidet eine
// Kreuzpruefung (jeder Schuss einmal weggelassen, Fehler bei 5 mm gekappt),
// ob und welcher eigene Satz besser ist als der Basissatz. Saetze mit
// Offsets an der Firmware-Grenze werden verworfen. Liefert Satz (nil =
// Basissatz), Klartext und den mittleren Fehler der Kreuzpruefung.
func zoneChooseFit(zone string, zs []zoneCalInput, global SimParams) (*ZoneSet, string, *float64) {
	type cand struct {
		opt  PhotoCalOptions
		text string
	}
	full, txt, ok := zoneFitOptions(len(zs))
	if !ok {
		return nil, txt, nil
	}
	covered := zoneCovered(zone, zs)
	var cands []cand
	if full.MicOffsets && covered {
		cands = append(cands, cand{full, "Offsets, Nachkorrektur, Schallgeschwindigkeit"})
	}
	if len(zs) >= 4 {
		cands = append(cands, cand{PhotoCalOptions{Offset: true, SoundSpeed: true}, "Nachkorrektur, Schallgeschwindigkeit"})
	}
	cands = append(cands, cand{PhotoCalOptions{Offset: true}, "nur Nachkorrektur"})
	note := ""
	if full.MicOffsets && !covered {
		note = " (Offsets nicht angepasst: Schüsse nur auf einer Seite der Zone)"
	}
	atLimit := func(s ZoneSet) bool {
		for _, o := range s.MicOffsetNs {
			if math.Abs(float64(o)) >= micOfsMaxNs-100 {
				return true
			}
		}
		return false
	}
	errOf := func(sh zoneCalInput, q SimParams) float64 {
		r := SolveSimShot(sh.Raw, q)
		if !r.PosValid {
			return 5
		}
		return math.Min(5, math.Hypot(float64(r.XUm)/1000-sh.TruthX, float64(r.YUm)/1000-sh.TruthY))
	}
	withSet := func(s ZoneSet) SimParams {
		q := global
		q.Zones = nil
		q.SoundMps, q.MicOffsetNs, q.OffsetXUm, q.OffsetYUm = s.SoundMps, s.MicOffsetNs, s.OffsetXUm, s.OffsetYUm
		return q
	}
	if len(zs) < 5 {
		for _, c := range cands {
			if s := zoneFitLM(zs, global, c.opt); !atLimit(s) {
				return &s, c.text + note, nil
			}
		}
		return nil, "Basissatz" + note, nil
	}
	// Kreuzpruefung; Vergleichswert "kein eigener Satz" = Basissatz
	baseQ := global
	baseQ.Zones = nil
	best := 0.0
	for _, sh := range zs {
		best += errOf(sh, baseQ)
	}
	best /= float64(len(zs))
	var bestSet *ZoneSet
	bestText := "Basissatz (eigener Satz in der Kreuzprüfung nicht besser)"
	for _, c := range cands {
		s := zoneFitLM(zs, global, c.opt)
		if atLimit(s) {
			continue
		}
		sum := 0.0
		for k := range zs {
			tr := append(append([]zoneCalInput{}, zs[:k]...), zs[k+1:]...)
			sum += errOf(zs[k], withSet(zoneFitLM(tr, global, c.opt)))
		}
		if m := sum / float64(len(zs)); m < best {
			best, bestText = m, c.text
			s := s
			bestSet = &s
		}
	}
	b := best
	return bestSet, bestText + note, &b
}
