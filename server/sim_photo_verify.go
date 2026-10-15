// ============================================================================
// sim_photo_verify.go – Messwerte gegen die Foto-Kalibrierung pruefen
// (POST /api/sessions/{id}/photo-verify).
//
// Grundlage sind die im Simulator zugeordneten Foto-Loecher (Abschnitt 3b,
// simulator-foto.js): das Loch im Foto gilt als wahre Trefferlage. Daran
// werden gemessen
//   - die Position, die das Geraet beim Schuss gemeldet hat,
//   - das Schusszentrum der Neuberechnung mit den aktuellen Parametern,
//   - die einzelnen Schnittpunkte aller Mikrofon-Dreierkombinationen
//     ("Messpunkte"),
//   - die Rohzeiten je Mikrofon: gemessene Laufzeit gegen die Laufzeit, die
//     sich aus der Fotoposition ergibt. Weil der Abschusszeitpunkt unbekannt
//     ist, zaehlen nur die Unterschiede zwischen den Mikrofonen (Mittel der
//     beteiligten Mics abgezogen). Ein ueber viele Schuesse gleichbleibender
//     Rest eines Mikrofons zeigt einen Offset- (OFS) oder Geometriefehler.
//
// Zusaetzlich lassen sich beliebige Parametervarianten (anderes Verfahren
// der Endposition, WALKK, Pulsbreiten-Regel ...) auf dieselben Schuesse
// rechnen und vergleichen - neue Algorithmen werden so direkt an der
// Fotowahrheit gemessen.
// ============================================================================
package main

import (
	"math"
	"net/http"
	"sort"
)

type photoVerifyPair struct {
	ShotNo int     `json:"shot_no"`
	XMM    float64 `json:"x_mm"` // Fotoposition, Scheibenkoordinaten
	YMM    float64 `json:"y_mm"`
}

type photoVerifyVariant struct {
	Name   string    `json:"name"`
	Params SimParams `json:"params"`
}

// PosErrStats: Abweichungen einer Positionsquelle gegen die Fotopositionen.
type PosErrStats struct {
	N        int     `json:"n"`          // auswertbare Schuesse
	Invalid  int     `json:"invalid"`    // ohne gueltige Position
	MeanDxMM float64 `json:"mean_dx_mm"` // mittlerer Versatz (Messung - Foto)
	MeanDyMM float64 `json:"mean_dy_mm"`
	RmsMM    float64 `json:"rms_mm"` // RMS der Abstaende
	// RmsCenteredMM: RMS nach Abzug des mittleren Versatzes - Streuung der
	// Messung unabhaengig von einer konstanten Verschiebung (OFFSETX/Y)
	RmsCenteredMM float64 `json:"rms_centered_mm"`
	MaxMM         float64 `json:"max_mm"`
	MedianMM      float64 `json:"median_mm"`
}

func posErrStats(pos [][2]float64, valid []bool, truth [][2]float64) PosErrStats {
	var st PosErrStats
	var dx, dy []float64
	for i := range pos {
		if !valid[i] {
			st.Invalid++
			continue
		}
		dx = append(dx, pos[i][0]-truth[i][0])
		dy = append(dy, pos[i][1]-truth[i][1])
	}
	st.N = len(dx)
	if st.N == 0 {
		return st
	}
	var sx, sy, s2 float64
	dist := make([]float64, st.N)
	for i := range dx {
		sx += dx[i]
		sy += dy[i]
		dist[i] = math.Hypot(dx[i], dy[i])
		s2 += dist[i] * dist[i]
		st.MaxMM = math.Max(st.MaxMM, dist[i])
	}
	n := float64(st.N)
	st.MeanDxMM, st.MeanDyMM = sx/n, sy/n
	st.RmsMM = math.Sqrt(s2 / n)
	var c2 float64
	for i := range dx {
		c2 += (dx[i]-st.MeanDxMM)*(dx[i]-st.MeanDxMM) + (dy[i]-st.MeanDyMM)*(dy[i]-st.MeanDyMM)
	}
	st.RmsCenteredMM = math.Sqrt(c2 / n)
	sort.Float64s(dist)
	if st.N%2 == 1 {
		st.MedianMM = dist[st.N/2]
	} else {
		st.MedianMM = (dist[st.N/2-1] + dist[st.N/2]) / 2
	}
	return st
}

type verifyCandidate struct {
	Ref       int     `json:"ref"`
	A         int     `json:"a"`
	B         int     `json:"b"`
	XMM       float64 `json:"x_mm"` // Scheibenkoordinaten inkl. OFFSETX/Y
	YMM       float64 `json:"y_mm"`
	CorrXMM   float64 `json:"corr_x_mm"` // mit Kugeldurchmesser-Korrektur
	CorrYMM   float64 `json:"corr_y_mm"`
	ErrMM     float64 `json:"err_mm"`      // Abstand roh -> Foto
	CorrErrMM float64 `json:"corr_err_mm"` // Abstand korrigiert -> Foto
	Best      bool    `json:"best"`
	InCluster bool    `json:"in_cluster"`
}

// verifyMic: Laufzeit-Rest eines Mikrofons gegen die Fotoposition (ns,
// gemessen - erwartet, Mittel der beteiligten Mics abgezogen). Center:
// Schall vom Lochmittelpunkt, Rim: vom naechstgelegenen Punkt des Lochrands
// (Radius LSQPELLETR bzw. 2,25 mm) - das physikalisch naeher liegende Modell.
type verifyMic struct {
	Mic      int      `json:"mic"`
	Used     bool     `json:"used"`
	CenterNs *float64 `json:"center_ns,omitempty"`
	RimNs    *float64 `json:"rim_ns,omitempty"`
	WidthNs  *int64   `json:"width_ns,omitempty"` // Pulsbreite (falls bekannt)
}

type verifyShot struct {
	ShotNo     int               `json:"shot_no"`
	TruthXMM   float64           `json:"truth_x_mm"`
	TruthYMM   float64           `json:"truth_y_mm"`
	DeviceXMM  float64           `json:"device_x_mm"`
	DeviceYMM  float64           `json:"device_y_mm"`
	DeviceErr  float64           `json:"device_err_mm"`
	SimValid   bool              `json:"sim_valid"`
	SimXMM     float64           `json:"sim_x_mm"`
	SimYMM     float64           `json:"sim_y_mm"`
	SimErr     *float64          `json:"sim_err_mm,omitempty"`
	SoundMps   float64           `json:"sound_mps"` // fuer diesen Schuss verwendet
	Candidates []verifyCandidate `json:"candidates"`
	Mics       [6]verifyMic      `json:"mics"`
	// Varianten: Abweichung je Variante (nil = ungueltig), gleiche
	// Reihenfolge wie im Request
	VariantErr []*float64 `json:"variant_err_mm"`
}

type verifyMicStat struct {
	Mic       int     `json:"mic"`
	N         int     `json:"n"`
	MeanRimNs float64 `json:"mean_rim_ns"`
	StdRimNs  float64 `json:"std_rim_ns"`
	MeanCtrNs float64 `json:"mean_center_ns"`
	// SuggestOfsNs: OFS, bei dem der mittlere Rim-Rest dieses Mics (relativ
	// zu Mic 0) verschwaende - reine Diagnose, die eigentliche Optimierung
	// macht "Parameter optimieren".
	SuggestOfsNs int64 `json:"suggest_ofs_ns"`
}

type verifyVariantResult struct {
	Name  string      `json:"name"`
	Stats PosErrStats `json:"stats"`
}

// micTimeResiduals: Laufzeit-Reste der beteiligten Mics gegen die Position
// (plate-Koordinaten, mm), Mittel abgezogen. pelletR=0 -> Punktquelle.
func micTimeResiduals(tNs [6]int64, seen [6]bool, x, y float64, micX, micY [6]float32,
	standoffMM, soundMmPerNs float32, pelletR float64) (out [6]*float64) {
	var res [6]float64
	var sum float64
	n := 0
	for i := 0; i < 6; i++ {
		if !seen[i] {
			continue
		}
		dxy := math.Hypot(x-float64(micX[i]), y-float64(micY[i]))
		dxy = math.Max(0, dxy-pelletR)
		d := math.Sqrt(dxy*dxy + float64(standoffMM)*float64(standoffMM))
		res[i] = float64(tNs[i]) - d/float64(soundMmPerNs)
		sum += res[i]
		n++
	}
	if n == 0 {
		return
	}
	mean := sum / float64(n)
	for i := 0; i < 6; i++ {
		if seen[i] {
			v := res[i] - mean
			out[i] = &v
		}
	}
	return
}

func (a *APIServer) photoVerifySession(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		Params   SimParams            `json:"params"`
		Pairs    []photoVerifyPair    `json:"pairs"`
		Variants []photoVerifyVariant `json:"variants"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body: " + err.Error())
	}
	if len(body.Pairs) == 0 {
		return nil, errBadRequest("keine zugeordneten Foto-Loecher")
	}
	raw, err := a.store.SessionShotsRaw(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	byNo := make(map[int]SimShotRaw, len(raw))
	for _, s := range raw {
		byNo[s.ShotNo] = s
	}
	return photoVerify(byNo, body.Params, body.Pairs, body.Variants), nil
}

func photoVerify(byNo map[int]SimShotRaw, p SimParams, pairs []photoVerifyPair, variants []photoVerifyVariant) map[string]any {
	pelletR := p.PosAlgo.LSQPelletRMm
	if pelletR <= 0 {
		pelletR = 2.25
	}
	var shots []verifyShot
	var truth, devPos, simPos [][2]float64
	var devValid, simValid []bool
	varPos := make([][][2]float64, len(variants))
	varValid := make([][]bool, len(variants))
	micVals := [6][]float64{}
	micCtr := [6][]float64{}

	for _, pr := range pairs {
		sh, ok := byNo[pr.ShotNo]
		if !ok || !sh.HasRaw {
			continue
		}
		vs := verifyShot{ShotNo: pr.ShotNo, TruthXMM: pr.XMM, TruthYMM: pr.YMM,
			DeviceXMM: sh.XMM, DeviceYMM: sh.YMM, DeviceErr: math.Hypot(sh.XMM-pr.XMM, sh.YMM-pr.YMM)}
		t := [2]float64{pr.XMM, pr.YMM}
		truth = append(truth, t)
		devPos = append(devPos, [2]float64{sh.XMM, sh.YMM})
		devValid = append(devValid, true)

		// bei Zonen-Kalibrierung: Werte der Zone, in der das Loch liegt
		pz := p
		if p.zonedEnabled() {
			pz = zoneParamsFor(p, p.SoundMps, p.Zones.set(p.Zones.ZoneOf(pr.XMM, pr.YMM)))
			pz.Zones = nil
		}
		offX, offY := float64(pz.OffsetXUm)/1000, float64(pz.OffsetYUm)/1000
		pp := sh.paramsFor(pz)
		vs.SoundMps = pp.SoundMps
		res, cands, _ := SolveSimShotDetail(sh, p)
		cx, cy := float64(res.EffOffsetXUm)/1000, float64(res.EffOffsetYUm)/1000
		vs.SimValid = res.PosValid
		if res.PosValid {
			vs.SimXMM, vs.SimYMM = float64(res.XUm)/1000, float64(res.YUm)/1000
			e := math.Hypot(vs.SimXMM-pr.XMM, vs.SimYMM-pr.YMM)
			vs.SimErr = &e
		}
		simPos = append(simPos, [2]float64{vs.SimXMM, vs.SimYMM})
		simValid = append(simValid, res.PosValid)

		vs.Candidates = make([]verifyCandidate, 0, len(cands))
		for _, c := range cands {
			vc := verifyCandidate{Ref: c.Ref, A: c.A, B: c.B,
				XMM: float64(c.X) + cx, YMM: float64(c.Y) + cy,
				CorrXMM: float64(c.CorrX) + cx, CorrYMM: float64(c.CorrY) + cy,
				Best: c.Best, InCluster: c.InCluster}
			vc.ErrMM = math.Hypot(vc.XMM-pr.XMM, vc.YMM-pr.YMM)
			vc.CorrErrMM = math.Hypot(vc.CorrXMM-pr.XMM, vc.CorrYMM-pr.YMM)
			vs.Candidates = append(vs.Candidates, vc)
		}

		// Rohzeiten je Mic (inkl. WALKK-Korrektur, abzueglich OFS - wie
		// die Loesung sie sieht) gegen die Fotoposition (Blechkoordinaten)
		air := sh.airFor(p)
		enabled := p.micEnabled()
		var tNs [6]int64
		var seen [6]bool
		for i := 0; i < 6; i++ {
			vs.Mics[i].Mic = i
			if len(air[i]) > 0 && enabled[i] {
				seen[i] = true
				tNs[i] = air[i][0] - pz.MicOffsetNs[i]
			}
		}
		micX, micY, standoff, sound, _ := solveParams(pp)
		px, py := pr.XMM-offX, pr.YMM-offY
		rim := micTimeResiduals(tNs, seen, px, py, micX, micY, standoff, sound, pelletR)
		ctr := micTimeResiduals(tNs, seen, px, py, micX, micY, standoff, sound, 0)
		var widths [6]*int64
		if sh.Raw.usable() {
			_, widths = sh.Raw.airFromRaw(p)
		} else {
			widths = sh.AirWNs
		}
		for i := 0; i < 6; i++ {
			vs.Mics[i].Used = seen[i]
			vs.Mics[i].RimNs, vs.Mics[i].CenterNs = rim[i], ctr[i]
			vs.Mics[i].WidthNs = widths[i]
			if rim[i] != nil {
				micVals[i] = append(micVals[i], *rim[i])
				micCtr[i] = append(micCtr[i], *ctr[i])
			}
		}

		for k, v := range variants {
			rv := SolveSimShot(sh, v.Params)
			if rv.PosValid {
				x, y := float64(rv.XUm)/1000, float64(rv.YUm)/1000
				e := math.Hypot(x-pr.XMM, y-pr.YMM)
				vs.VariantErr = append(vs.VariantErr, &e)
				varPos[k] = append(varPos[k], [2]float64{x, y})
				varValid[k] = append(varValid[k], true)
			} else {
				vs.VariantErr = append(vs.VariantErr, nil)
				varPos[k] = append(varPos[k], [2]float64{})
				varValid[k] = append(varValid[k], false)
			}
		}
		shots = append(shots, vs)
	}

	micStats := make([]verifyMicStat, 6)
	mean := func(v []float64) float64 {
		if len(v) == 0 {
			return 0
		}
		s := 0.0
		for _, x := range v {
			s += x
		}
		return s / float64(len(v))
	}
	for i := 0; i < 6; i++ {
		ms := verifyMicStat{Mic: i, N: len(micVals[i]), MeanRimNs: mean(micVals[i]), MeanCtrNs: mean(micCtr[i])}
		var s2 float64
		for _, x := range micVals[i] {
			s2 += (x - ms.MeanRimNs) * (x - ms.MeanRimNs)
		}
		if ms.N > 1 {
			ms.StdRimNs = math.Sqrt(s2 / float64(ms.N-1))
		}
		micStats[i] = ms
	}
	// Vorschlag: Rest relativ zu Mic 0 (Referenz, OFS0 fest 0) in OFS
	// verschieben - ein spaet gemessenes Mic (positiver Rest) braucht ein
	// groesseres OFS.
	for i := 0; i < 6; i++ {
		if micStats[i].N > 0 {
			micStats[i].SuggestOfsNs = p.MicOffsetNs[i] + int64(math.Round(micStats[i].MeanRimNs-micStats[0].MeanRimNs))
		} else {
			micStats[i].SuggestOfsNs = p.MicOffsetNs[i]
		}
	}

	vr := make([]verifyVariantResult, len(variants))
	for k, v := range variants {
		vr[k] = verifyVariantResult{Name: v.Name, Stats: posErrStats(varPos[k], varValid[k], truth)}
	}
	return map[string]any{
		"shots":        shots,
		"device_stats": posErrStats(devPos, devValid, truth),
		"sim_stats":    posErrStats(simPos, simValid, truth),
		"mic_stats":    micStats,
		"pellet_r_mm":  pelletR,
		"variants":     vr,
	}
}
