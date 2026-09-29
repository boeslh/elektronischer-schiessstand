// ============================================================================
// sim_optimize.go – Kalibrierung auf minimalen Schnittpunkt-Cluster Σ9
// (siehe clusterSum9 in simulator.go) ueber Mikrofon-Offsets UND - je nach
// Auswahl - Geometrie-/Korrekturparameter (Standoff, Mic-Halbabstand X,
// Kugeldurchmesser-Korrektur %, Korrektur-Kappung, Schallgeschwindigkeit),
// jeweils innerhalb vorgegebener Grenzen.
//
// Laeuft nur im Simulator auf dem Server (Rechenzeit unkritisch), die
// Firmware-Auswertung selbst bleibt unveraendert - gefunden werden nur
// Parameterwerte, die per SET/CAL IMPORT uebertragen werden koennen.
//
// Verfahren (Σ9 ist stueckweise und nicht differenzierbar - Schnittpunkte
// wandern in den Cluster-Radius hinein oder heraus -, daher ableitungsfreie
// Suche mit mehreren Startpunkten):
//  1. Startpunkte: die aktuellen Parameter sowie ein Raster (min/Mitte/max)
//     ueber Standoff, Mic-Halbabstand und Schallgeschwindigkeit (soweit zur
//     Optimierung freigegeben). Je Startpunkt werden die Offsets zuerst wie
//     bei CAL START auf minimalen Rest-Fehler kalibriert - von beliebigen
//     Offsets aus liegen oft gar keine Schnittpunkte im Cluster-Radius, Σ9
//     waere dann ueberall 9 x 5mm und gaebe der Suche keine Richtung. Der
//     Startpunkt "aktuelle Parameter" behaelt zusaetzlich die aktuellen
//     Offsets (bereits gute Kalibrierung nicht verwerfen).
//  2. Je Startpunkt Kompass-Suche (Koordinatensuche mit Schrittweiten-
//     Halbierung) ueber alle freigegebenen Parameter auf minimale Σ9-Summe.
//  3. Die besten Ergebnisse werden mit kleineren Schritten erneut
//     verfeinert, das beste gewinnt.
//
// ============================================================================
package main

import (
	"math"
	"runtime"
	"sort"
	"sync"
	"time"
)

// OptRange: Grenzen eines optimierbaren Parameters; Enabled=false haelt ihn
// auf dem aktuellen Wert.
type OptRange struct {
	Enabled bool    `json:"enabled"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
}

// Cluster9Options: Optimierungsumfang. Corrected waehlt die Σ9-Variante
// (kugeldurchmesser-korrigierte statt rohe Schnittpunkte) - die Parameter
// der Kugelkorrektur wirken praktisch nur auf die korrigierte Variante.
type Cluster9Options struct {
	Corrected bool     `json:"corrected"`
	Offsets   bool     `json:"offsets"` // Mic-Offsets OFS1-5 (OFS0 bleibt Referenz 0)
	Standoff  OptRange `json:"standoff"`
	MicHalfX  OptRange `json:"mic_half_x"`
	BShiftPct OptRange `json:"bullet_shift_pct"`
	BShiftCap OptRange `json:"bullet_shift_cap"`
	Sound     OptRange `json:"sound"`
}

// DefaultCluster9Options: Grenzen nach Vorgabe (Standoff 27-30mm, Mic-
// Halbabstand 114-117mm, Kugelkorrektur 30-60%, Kappung 3-5mm, Schall
// 340-346 m/s).
func DefaultCluster9Options() Cluster9Options {
	return Cluster9Options{
		Corrected: true,
		Offsets:   true,
		Standoff:  OptRange{true, 27, 30},
		MicHalfX:  OptRange{true, 114, 117},
		BShiftPct: OptRange{true, 30, 60},
		BShiftCap: OptRange{true, 3, 5},
		Sound:     OptRange{true, 340, 346},
	}
}

// Cluster9Candidate: ein Optimierungsergebnis (bestes bzw. weitere gute
// Loesungen aus anderen Startpunkten - zeigt, wie eindeutig das Optimum ist).
type Cluster9Candidate struct {
	Params    SimParams `json:"params"`
	MeanSumMM float64   `json:"mean_sum_mm"` // mittleres Σ9 je loesbarem Schuss
	Good      int       `json:"good"`        // Schuesse mit Σ9 < 10mm
	Solved    int       `json:"solved"`
}

type Cluster9Result struct {
	Best       Cluster9Candidate   `json:"best"`
	Before     Cluster9Candidate   `json:"before"`
	Alternates []Cluster9Candidate `json:"alternates"`
	// AtBound: Parameter, die im besten Ergebnis auf einer Grenze liegen -
	// das Optimum liegt dann evtl. ausserhalb des erlaubten Bereichs.
	AtBound    []string `json:"at_bound"`
	Starts     int      `json:"starts"`
	Evals      int64    `json:"evals"`
	DurationMs int64    `json:"duration_ms"`
}

// optDim: eine Dimension des Suchraums.
type optDim struct {
	lo, hi     float64
	step, minS float64 // Startschritt, Abbruchschritt
	integer    bool
	get        func(p *SimParams) float64
	set        func(p *SimParams, v float64)
}

func (d optDim) clamp(v float64) float64 {
	v = math.Max(d.lo, math.Min(d.hi, v))
	if d.integer {
		v = math.Round(v)
	}
	return v
}

// cluster9Dims baut die Dimensionen des Suchraums aus den Optionen.
func cluster9Dims(o Cluster9Options) []optDim {
	var dims []optDim
	if o.Offsets {
		for k := 1; k < 6; k++ {
			k := k
			dims = append(dims, optDim{
				lo: -micOfsMaxNs, hi: micOfsMaxNs, step: 1000, minS: 1, integer: true,
				get: func(p *SimParams) float64 { return float64(p.MicOffsetNs[k]) },
				set: func(p *SimParams, v float64) { p.MicOffsetNs[k] = int64(v) },
			})
		}
	}
	add := func(r OptRange, step, minS float64, integer bool, get func(*SimParams) float64, set func(*SimParams, float64)) {
		if r.Enabled && r.Max >= r.Min {
			dims = append(dims, optDim{lo: r.Min, hi: r.Max, step: step, minS: minS, integer: integer, get: get, set: set})
		}
	}
	add(o.Standoff, 0.5, 0.01, false,
		func(p *SimParams) float64 { return p.StandoffPaperMM }, func(p *SimParams, v float64) { p.StandoffPaperMM = v })
	add(o.MicHalfX, 0.5, 0.01, false,
		func(p *SimParams) float64 { return p.MicHalfXMm }, func(p *SimParams, v float64) { p.MicHalfXMm = v })
	add(o.BShiftPct, 8, 1, true,
		func(p *SimParams) float64 { return float64(p.BulletShiftPct) }, func(p *SimParams, v float64) { p.BulletShiftPct = int(v) })
	add(o.BShiftCap, 0.5, 0.01, false,
		func(p *SimParams) float64 { return p.BulletShiftCapMm }, func(p *SimParams, v float64) { p.BulletShiftCapMm = v })
	add(o.Sound, 1, 0.05, false,
		func(p *SimParams) float64 { return p.SoundMps }, func(p *SimParams, v float64) { p.SoundMps = v })
	return dims
}

// cluster9Shot: vorbereitete Rohzeiten eines Schusses (ohne Offsets).
type cluster9Shot struct {
	raw  [6]int64
	seen [6]bool
}

func prepCluster9Shots(airNs [][6][]int64, p SimParams) []cluster9Shot {
	en := p.micEnabled()
	out := make([]cluster9Shot, 0, len(airNs))
	for _, shot := range airNs {
		var cs cluster9Shot
		for i := 0; i < 6; i++ {
			if len(shot[i]) > 0 && en[i] {
				cs.seen[i], cs.raw[i] = true, shot[i][0]
			}
		}
		out = append(out, cs)
	}
	return out
}

// evalCluster9 berechnet fuer p die Σ9-Werte aller Schuesse. cost = Summe
// (unloesbarer Schuss: Strafwert 1000mm).
func evalCluster9(shots []cluster9Shot, p SimParams, corrected bool) (cost float64, c Cluster9Candidate) {
	micX, micY, standoffMM, soundMmPerNs, radiusMM := solveParams(p)
	var sum float64
	for _, s := range shots {
		var t [6]int64
		for i := 0; i < 6; i++ {
			if s.seen[i] {
				t[i] = s.raw[i] - p.MicOffsetNs[i]
			}
		}
		x, y, _, _, _, cands, _, _, ok := airPosition(t, s.seen, micX, micY, standoffMM,
			soundMmPerNs, radiusMM, p.BulletShiftPct, float32(p.BulletShiftCapMm))
		if !ok {
			cost += 1000
			continue
		}
		if p.PosAlgo.active() {
			x, y, _, _ = refinePosition(cands, x, y, t, s.seen, micX, micY, standoffMM, soundMmPerNs, radiusMM, p.PosAlgo)
		}
		v, _ := clusterSum9(cands, x, y, radiusMM, corrected)
		cost += float64(v)
		sum += float64(v)
		c.Solved++
		if v < 10 {
			c.Good++
		}
	}
	c.Params = p
	if c.Solved > 0 {
		c.MeanSumMM = sum / float64(c.Solved)
	}
	return cost, c
}

// compassSearch: Koordinatensuche - je Dimension +-Schritt versuchen, jede
// Verbesserung sofort uebernehmen; bringt ein kompletter Durchlauf keine
// Verbesserung, werden alle Schritte halbiert, bis alle unter ihrem
// Abbruchschritt liegen. stepScale verkleinert die Startschritte (Feinlauf).
func compassSearch(cost func(SimParams) float64, p SimParams, dims []optDim, stepScale float64) (SimParams, float64) {
	steps := make([]float64, len(dims))
	for i, d := range dims {
		steps[i] = d.step * stepScale
	}
	best := cost(p)
	for {
		improved := false
		for i, d := range dims {
			if steps[i] < d.minS {
				continue
			}
			cur := d.get(&p)
			for _, sign := range []float64{1, -1} {
				v := d.clamp(cur + sign*steps[i])
				if v == cur {
					continue
				}
				q := p
				d.set(&q, v)
				if c := cost(q); c < best-1e-9 {
					p, best, improved = q, c, true
					break
				}
			}
		}
		if improved {
			continue
		}
		done := true
		for i, d := range dims {
			steps[i] /= 2
			if steps[i] >= d.minS {
				done = false
			}
		}
		if done {
			return p, best
		}
	}
}

// OptimizeCluster9: siehe Dateikopf.
func OptimizeCluster9(airNs [][6][]int64, p SimParams, o Cluster9Options) Cluster9Result {
	start := time.Now()
	shots := prepCluster9Shots(airNs, p)
	dims := cluster9Dims(o)
	var evals int64
	var mu sync.Mutex
	cost := func(q SimParams) float64 {
		c, _ := evalCluster9(shots, q, o.Corrected)
		mu.Lock()
		evals++
		mu.Unlock()
		return c
	}

	// "vorher" mit den tatsaechlich eingestellten (nicht in die Grenzen
	// geholten) Werten bewerten.
	_, before := evalCluster9(shots, p, o.Corrected)

	// Startwerte in die Grenzen holen (z.B. BSHIFTPCT 0 bei Grenze 30-60).
	for _, d := range dims {
		d.set(&p, d.clamp(d.get(&p)))
	}

	// Startpunkte: aktuelle Parameter + Raster ueber Standoff/MicHalfX/Schall.
	type startPt struct {
		p        SimParams
		keepOffs bool
	}
	starts := []startPt{{p, true}}
	levels := func(r OptRange, cur float64) []float64 {
		if !r.Enabled {
			return []float64{cur}
		}
		return []float64{r.Min, (r.Min + r.Max) / 2, r.Max}
	}
	for _, so := range levels(o.Standoff, p.StandoffPaperMM) {
		for _, mx := range levels(o.MicHalfX, p.MicHalfXMm) {
			for _, snd := range levels(o.Sound, p.SoundMps) {
				q := p
				q.StandoffPaperMM, q.MicHalfXMm, q.SoundMps = so, mx, snd
				starts = append(starts, startPt{q, false})
			}
		}
	}

	results := make([]Cluster9Candidate, len(starts))
	costs := make([]float64, len(starts))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())
	for i, s := range starts {
		wg.Add(1)
		go func(i int, s startPt) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			q := s.p
			if o.Offsets && !s.keepOffs {
				ofs, _ := CalibrateMicOffsets(airNs, q) // Rest-Fehler-Vorkalibrierung
				q.MicOffsetNs = ofs
			}
			q, c := compassSearch(cost, q, dims, 1)
			costs[i] = c
			_, results[i] = evalCluster9(shots, q, o.Corrected)
		}(i, s)
	}
	wg.Wait()

	// Beste 3 fein nachoptimieren (kleinere Startschritte).
	idx := make([]int, len(starts))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return costs[idx[a]] < costs[idx[b]] })
	nRefine := 3
	if len(idx) < nRefine {
		nRefine = len(idx)
	}
	refined := make([]Cluster9Candidate, nRefine)
	refCost := make([]float64, nRefine)
	for r := 0; r < nRefine; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			q, c := compassSearch(cost, results[idx[r]].Params, dims, 0.25)
			refCost[r] = c
			_, refined[r] = evalCluster9(shots, q, o.Corrected)
		}(r)
	}
	wg.Wait()

	// Gesamtliste: verfeinerte + uebrige, nach Kosten sortiert.
	type scored struct {
		c    Cluster9Candidate
		cost float64
	}
	var all []scored
	for r := 0; r < nRefine; r++ {
		all = append(all, scored{refined[r], refCost[r]})
	}
	for _, i := range idx[nRefine:] {
		all = append(all, scored{results[i], costs[i]})
	}
	sort.Slice(all, func(a, b int) bool { return all[a].cost < all[b].cost })

	res := Cluster9Result{Best: all[0].c, Before: before, Starts: len(starts), Evals: evals}
	// Weitere unterschiedliche Loesungen (Duplikate der besten ausgelassen).
	for _, s := range all[1:] {
		if len(res.Alternates) >= 4 {
			break
		}
		if math.Abs(s.c.MeanSumMM-res.Best.MeanSumMM) < 1e-6 && s.c.Params.MicOffsetNs == res.Best.Params.MicOffsetNs {
			continue
		}
		res.Alternates = append(res.Alternates, s.c)
	}
	for _, b := range []struct {
		name string
		r    OptRange
		v    float64
	}{
		{"Standoff", o.Standoff, res.Best.Params.StandoffPaperMM},
		{"Mic-Halbabstand X", o.MicHalfX, res.Best.Params.MicHalfXMm},
		{"Kugeldurchm.-Korrektur", o.BShiftPct, float64(res.Best.Params.BulletShiftPct)},
		{"Korrektur-Kappung", o.BShiftCap, res.Best.Params.BulletShiftCapMm},
		{"Schallgeschwindigkeit", o.Sound, res.Best.Params.SoundMps},
	} {
		if b.r.Enabled && (math.Abs(b.v-b.r.Min) < 1e-6 || math.Abs(b.v-b.r.Max) < 1e-6) {
			res.AtBound = append(res.AtBound, b.name)
		}
	}
	res.DurationMs = time.Since(start).Milliseconds()
	return res
}
