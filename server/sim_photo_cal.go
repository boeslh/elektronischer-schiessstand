// ============================================================================
// sim_photo_cal.go – optionale Foto-Kalibrierung fuer den Simulator.
//
// Ergaenzt (ersetzt NICHT) die bisherige Kalibrierung (CalibrateMicOffsets,
// "CAL START"-Port): Dort gibt es keine Wahrheit, nur die Selbstkonsistenz
// der Mikrofon-Laufzeiten - Mikrofon-Offsets lassen sich so bestimmen, eine
// Verschiebung/Skalierung der Positionen aber nicht. Hier liefert ein Foto
// der beschossenen Scheibe die tatsaechlichen Einschlagpositionen (in mm,
// relativ zur Mitte des schwarzen Innenkreises, siehe web/simulator-foto.js),
// und die Parameter werden so optimiert, dass die Neuberechnung (SolveShot,
// d.h. exakt der Firmware-Nachbau) moeglichst genau auf diese Positionen
// faellt.
//
// Optimiert werden je nach Auswahl OFS1-5, OFFSETX/Y, SOUNDSPEED,
// STANDOFFPAPER und MICHALFX. Alle Werte werden schon WAEHREND der Suche auf
// die Aufloesung der Firmware gerundet (SOUNDSPEED ganzzahlig, OFS/OFFSET
// ganzzahlig in ns/um, Standoff/MicHalfX 0.01mm) - sonst waere das
// optimierte Ergebnis im Simulator besser als spaeter auf dem Geraet.
// ============================================================================
package main

import (
	"fmt"
	"math"
	"net/http"
)

// photoCalCapMM: Rest-Fehler je Schuss wird fuer die Kostenfunktion bei
// diesem Wert gekappt (robuste Kostenfunktion) - eine einzelne falsch
// zugeordnete Einschussstelle soll die Kalibrierung nicht dominieren.
// Ein in der Neuberechnung ungueltiger Schuss zaehlt ebenfalls mit genau
// diesem (maximalen) Fehler.
const photoCalCapMM = 10.0

type PhotoCalShot struct {
	ShotNo  int
	AirNs   [6][]int64
	PiezoNs *int64
	TruthX  float64 // mm, Scheibenkoordinaten (x rechts, y oben, Ursprung Kreismitte)
	TruthY  float64
}

type PhotoCalOptions struct {
	MicOffsets bool `json:"mic_offsets"` // OFS1-5 (OFS0 bleibt Referenz)
	Offset     bool `json:"offset"`      // OFFSETX/OFFSETY
	SoundSpeed bool `json:"sound_speed"` // SOUNDSPEED
	Standoff   bool `json:"standoff"`    // STANDOFFPAPER
	MicHalfX   bool `json:"mic_half_x"`  // MICHALFX
}

type PhotoCalShotResult struct {
	ShotNo      int      `json:"shot_no"`
	TruthX      float64  `json:"truth_x_mm"`
	TruthY      float64  `json:"truth_y_mm"`
	BeforeX     *float64 `json:"before_x_mm"` // nil = in der Neuberechnung ungueltig
	BeforeY     *float64 `json:"before_y_mm"`
	BeforeErrMM *float64 `json:"before_err_mm"`
	AfterX      *float64 `json:"after_x_mm"`
	AfterY      *float64 `json:"after_y_mm"`
	AfterErrMM  *float64 `json:"after_err_mm"`
}

type PhotoCalReport struct {
	Params        SimParams `json:"params"`
	RMSBeforeMM   float64   `json:"rms_before_mm"`
	RMSAfterMM    float64   `json:"rms_after_mm"`
	MaxAfterMM    float64   `json:"max_after_mm"`
	InvalidBefore int       `json:"invalid_before"`
	InvalidAfter  int       `json:"invalid_after"`
	FreeParams    int       `json:"free_params"`
	Evaluations   int       `json:"evaluations"`
	// Diagnose (nicht korrigierbar, da die Firmware keine Drehung kennt):
	// Restverdrehung/-massstab zwischen Foto und Neuberechnung nach der
	// Optimierung. Deutliche Werte weisen auf ein verdrehtes Foto-
	// Koordinatensystem (Schrauben nicht achsparallel zur Mikrofonanordnung)
	// bzw. auf einen falschen Referenzdurchmesser hin.
	RotationDeg float64              `json:"rotation_deg"`
	Scale       float64              `json:"scale"`
	Shots       []PhotoCalShotResult `json:"shots"`
}

// photoCalParam beschreibt einen frei optimierbaren (skalaren) Parameter.
type photoCalParam struct {
	name             string
	get              func(*SimParams) float64
	set              func(*SimParams, float64)
	step, minStep    float64
	lo, hi, quantize float64
}

func (c photoCalParam) apply(p *SimParams, v float64) {
	v = math.Max(c.lo, math.Min(c.hi, v))
	v = math.Round(v/c.quantize) * c.quantize
	c.set(p, v)
}

func photoCalParams(opt PhotoCalOptions) []photoCalParam {
	var ps []photoCalParam
	if opt.MicOffsets {
		for i := 1; i < 6; i++ {
			i := i
			ps = append(ps, photoCalParam{
				name: fmt.Sprintf("OFS%d", i),
				get:  func(p *SimParams) float64 { return float64(p.MicOffsetNs[i]) },
				set:  func(p *SimParams, v float64) { p.MicOffsetNs[i] = int64(v) },
				step: 2000, minStep: 2, lo: -micOfsMaxNs, hi: micOfsMaxNs, quantize: 1,
			})
		}
	}
	if opt.SoundSpeed {
		ps = append(ps, photoCalParam{
			name: "SOUNDSPEED",
			get:  func(p *SimParams) float64 { return p.SoundMps },
			set:  func(p *SimParams, v float64) { p.SoundMps = v },
			step: 8, minStep: 1, lo: 300, hi: 400, quantize: 1,
		})
	}
	if opt.Standoff {
		ps = append(ps, photoCalParam{
			name: "STANDOFFPAPER",
			get:  func(p *SimParams) float64 { return p.StandoffPaperMM },
			set:  func(p *SimParams, v float64) { p.StandoffPaperMM = v },
			step: 5, minStep: 0.02, lo: 5, hi: 100, quantize: 0.01,
		})
	}
	if opt.MicHalfX {
		ps = append(ps, photoCalParam{
			name: "MICHALFX",
			get:  func(p *SimParams) float64 { return p.MicHalfXMm },
			set:  func(p *SimParams, v float64) { p.MicHalfXMm = v },
			step: 5, minStep: 0.02, lo: 5, hi: 300, quantize: 0.01,
		})
	}
	return ps
}

// photoCalEval rechnet alle Schuesse mit p neu. Bei opt.Offset wird die
// optimale Verschiebung (OFFSETX/Y) analytisch bestimmt statt gesucht: der
// mittlere Rest-Fehler, in einem zweiten Durchgang ohne Ausreisser (Fehler
// > photoCalCapMM) - das entlastet die Suche um zwei Dimensionen. Liefert
// die robuste Kostensumme (mm^2) und p inkl. der gewaehlten Verschiebung.
func photoCalEval(shots []PhotoCalShot, p SimParams, opt PhotoCalOptions) (cost float64, withOffset SimParams, pos [][2]float64, valid []bool) {
	raw := p
	raw.OffsetXUm, raw.OffsetYUm = 0, 0
	pos = make([][2]float64, len(shots))
	valid = make([]bool, len(shots))
	var consistency float64
	for i, s := range shots {
		r := SolveShot(s.AirNs, s.PiezoNs, raw)
		if r.PosValid {
			valid[i] = true
			pos[i] = [2]float64{float64(r.XUm) / 1000, float64(r.YUm) / 1000}
			// Selbstkonsistenz aller Mikrofone (Rest-Fehler gegen die
			// Kontroll-Mics, dasselbe Mass wie calCostClassicFn): ALGO=CLASSIC
			// waehlt je Schuss die konsistenteste 3er-Kombination und
			// ueberstimmt einzelne falsche Mikrofone - deren OFS wirken sich
			// auf die Positionen dann kaum aus und waeren allein aus dem
			// Foto nicht bestimmbar (Test: Positionen < 0.15mm genau, aber 3
			// von 5 OFS um ~1000ns falsch). Bei ALGO=RIM ist CorrResUm 0.
			c := float64(r.CorrResUm) / 1000
			consistency += math.Min(c*c, photoCalCapMM*photoCalCapMM)
		}
	}

	tx, ty := float64(p.OffsetXUm)/1000, float64(p.OffsetYUm)/1000
	if opt.Offset {
		mean := func(limit float64) (float64, float64, bool) {
			var sx, sy float64
			n := 0
			for i, s := range shots {
				if !valid[i] {
					continue
				}
				dx, dy := s.TruthX-pos[i][0], s.TruthY-pos[i][1]
				if limit > 0 && math.Hypot(dx-tx, dy-ty) > limit {
					continue
				}
				sx += dx
				sy += dy
				n++
			}
			if n == 0 {
				return 0, 0, false
			}
			return sx / float64(n), sy / float64(n), true
		}
		if mx, my, ok := mean(0); ok {
			tx, ty = mx, my
			if mx, my, ok := mean(photoCalCapMM); ok {
				tx, ty = mx, my
			}
		}
		tx = math.Max(-50, math.Min(50, tx)) // SET OFFSETX/Y: +-50000 um
		ty = math.Max(-50, math.Min(50, ty))
		tx, ty = math.Round(tx*1000)/1000, math.Round(ty*1000)/1000
	}

	for i, s := range shots {
		if !valid[i] {
			cost += photoCalCapMM * photoCalCapMM
			continue
		}
		pos[i][0] += tx
		pos[i][1] += ty
		d2 := math.Pow(s.TruthX-pos[i][0], 2) + math.Pow(s.TruthY-pos[i][1], 2)
		cost += math.Min(d2, photoCalCapMM*photoCalCapMM)
	}
	cost += consistency
	withOffset = p
	withOffset.OffsetXUm = int64(math.Round(tx * 1000))
	withOffset.OffsetYUm = int64(math.Round(ty * 1000))
	return cost, withOffset, pos, valid
}

// photoCalSearch: Hooke-Jeeves-Mustersuche ueber die freien Parameter
// (gradientenfrei - die Kostenfunktion ist wegen der Kandidaten-/Cluster-
// Auswahl der Firmware nicht glatt). Erkundung je Parameter +-Schritt; nach
// einer Verbesserung zusaetzlich ein Mustersprung in dieselbe Richtung
// (entscheidend in den schmalen, schraeg liegenden Taelern, in denen sich
// mehrere OFS/SOUNDSPEED gegenseitig kompensieren - reine Achsensuche kroch
// dort vorzeitig fest). Nach Konvergenz wird mit den Anfangsschritten neu
// gestartet, solange sich die Kosten noch spuerbar verbessern.
func photoCalSearch(shots []PhotoCalShot, start SimParams, opt PhotoCalOptions, params []photoCalParam, evals *int) (SimParams, float64) {
	type point struct {
		p          SimParams // freie Parameter (OFFSETX/Y unveraendert)
		withOffset SimParams // inkl. analytisch bestimmter OFFSETX/Y
		cost       float64
	}
	eval := func(p SimParams) point {
		*evals++
		c, withOffset, _, _ := photoCalEval(shots, p, opt)
		return point{p, withOffset, c}
	}
	explore := func(base point, steps []float64) point {
		cur := base
		for i, c := range params {
			for _, dir := range []float64{+1, -1} {
				cand := cur.p
				c.apply(&cand, c.get(&cur.p)+dir*steps[i])
				if c.get(&cand) == c.get(&cur.p) {
					continue
				}
				if e := eval(cand); e.cost < cur.cost-1e-12 {
					cur = e
					break
				}
			}
		}
		return cur
	}

	best := eval(start)
	for restart := 0; restart < 6; restart++ {
		restartCost := best.cost
		steps := make([]float64, len(params))
		for i, c := range params {
			steps[i] = c.step
		}
		base := best
		for iter := 0; iter < 2000; iter++ {
			e := explore(base, steps)
			if e.cost < base.cost-1e-12 {
				// Mustersprungfolge: solange der Sprung (plus Erkundung um
				// ihn herum) weiter verbessert, in derselben Richtung weiter.
				for {
					jump := e.p
					for _, c := range params {
						c.apply(&jump, 2*c.get(&e.p)-c.get(&base.p))
					}
					base = e
					e = explore(eval(jump), steps)
					if e.cost >= base.cost-1e-12 {
						break
					}
				}
				continue
			}
			done := true
			for i, c := range params {
				if steps[i] > c.minStep {
					steps[i] = math.Max(c.minStep, steps[i]/2)
					done = false
				}
			}
			if done {
				break
			}
		}
		if base.cost < best.cost {
			best = base
		}
		if restartCost-best.cost < 1e-6 {
			break
		}
	}
	return best.withOffset, best.cost
}

// PhotoCalibrate optimiert p gegen die Foto-Positionen. Gestartet wird
// zweimal - bei den aktuellen Parametern und (falls OFS optimiert werden)
// zusaetzlich bei OFS=0, wie bei "CAL START" - und das bessere Ergebnis
// genommen, damit eine schlechte Ausgangskalibrierung nicht in einem
// lokalen Minimum festhaelt.
func PhotoCalibrate(shots []PhotoCalShot, p SimParams, opt PhotoCalOptions) (PhotoCalReport, error) {
	params := photoCalParams(opt)
	free := len(params)
	if opt.Offset {
		free += 2
	}
	if free == 0 {
		return PhotoCalReport{}, errBadRequest("keine Parameter zur Optimierung ausgewaehlt")
	}

	rep := PhotoCalReport{FreeParams: free}
	_, _, beforePos, beforeValid := photoCalEval(shots, p, PhotoCalOptions{})
	nValid := 0
	for _, v := range beforeValid {
		if v {
			nValid++
		}
	}
	// Mindestens 3 Schuesse mehr als freie Parameter - sonst passt die
	// Optimierung die Parameter an die Messfehler statt an die Anlage an.
	if nValid < free+3 {
		return PhotoCalReport{}, errBadRequest(fmt.Sprintf(
			"zu wenige gueltige zugeordnete Schuesse (%d) fuer %d freie Parameter - mindestens %d noetig",
			nValid, free, free+3))
	}

	bestP, bestCost := photoCalSearch(shots, p, opt, params, &rep.Evaluations)
	if opt.MicOffsets && p.MicOffsetNs != [6]int64{} {
		zero := p
		zero.MicOffsetNs = [6]int64{}
		if p2, c2 := photoCalSearch(shots, zero, opt, params, &rep.Evaluations); c2 < bestCost {
			bestP = p2
		}
	}
	rep.Params = bestP

	_, _, afterPos, afterValid := photoCalEval(shots, bestP, PhotoCalOptions{})
	var sumB, sumA float64
	var nB, nA int
	// fuer die Drehungs-/Massstabsdiagnose (Procrustes um die Schwerpunkte)
	var tcx, tcy, scx, scy float64
	for i, s := range shots {
		r := PhotoCalShotResult{ShotNo: s.ShotNo, TruthX: s.TruthX, TruthY: s.TruthY}
		if beforeValid[i] {
			x, y := beforePos[i][0], beforePos[i][1]
			e := math.Hypot(s.TruthX-x, s.TruthY-y)
			r.BeforeX, r.BeforeY, r.BeforeErrMM = &x, &y, &e
			sumB += e * e
			nB++
		} else {
			rep.InvalidBefore++
		}
		if afterValid[i] {
			x, y := afterPos[i][0], afterPos[i][1]
			e := math.Hypot(s.TruthX-x, s.TruthY-y)
			r.AfterX, r.AfterY, r.AfterErrMM = &x, &y, &e
			sumA += e * e
			nA++
			rep.MaxAfterMM = math.Max(rep.MaxAfterMM, e)
			tcx += s.TruthX
			tcy += s.TruthY
			scx += x
			scy += y
		} else {
			rep.InvalidAfter++
		}
		rep.Shots = append(rep.Shots, r)
	}
	if nB > 0 {
		rep.RMSBeforeMM = math.Sqrt(sumB / float64(nB))
	}
	if nA > 0 {
		rep.RMSAfterMM = math.Sqrt(sumA / float64(nA))
		tcx, tcy, scx, scy = tcx/float64(nA), tcy/float64(nA), scx/float64(nA), scy/float64(nA)
		var dot, cross, ss float64
		for i, s := range shots {
			if !afterValid[i] {
				continue
			}
			ax, ay := afterPos[i][0]-scx, afterPos[i][1]-scy
			bx, by := s.TruthX-tcx, s.TruthY-tcy
			dot += ax*bx + ay*by
			cross += ax*by - ay*bx
			ss += ax*ax + ay*ay
		}
		rep.RotationDeg = math.Atan2(cross, dot) * 180 / math.Pi
		if ss > 0 {
			rep.Scale = math.Hypot(dot, cross) / ss
		}
	}
	return rep, nil
}

// photoCalibrateSession: POST /api/sessions/{id}/photo-calibrate
func (a *APIServer) photoCalibrateSession(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		Params   SimParams       `json:"params"`
		Optimize PhotoCalOptions `json:"optimize"`
		Pairs    []struct {
			ShotNo int     `json:"shot_no"`
			XMM    float64 `json:"x_mm"`
			YMM    float64 `json:"y_mm"`
		} `json:"pairs"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body: " + err.Error())
	}
	raw, err := a.store.SessionShotsRaw(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	byNo := make(map[int]SimShotRaw, len(raw))
	for _, s := range raw {
		byNo[s.ShotNo] = s
	}
	var shots []PhotoCalShot
	seen := map[int]bool{}
	for _, pr := range body.Pairs {
		s, ok := byNo[pr.ShotNo]
		if !ok || !s.HasRaw || seen[pr.ShotNo] {
			continue
		}
		seen[pr.ShotNo] = true
		shots = append(shots, PhotoCalShot{ShotNo: s.ShotNo, AirNs: s.AirNs, PiezoNs: s.PiezoNs, TruthX: pr.XMM, TruthY: pr.YMM})
	}
	return PhotoCalibrate(shots, body.Params, body.Optimize)
}
