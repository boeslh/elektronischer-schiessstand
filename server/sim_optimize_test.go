package main

import "testing"

// genCluster9Shots: synthetische Kalibrier-Schuesse mit "wahrer" Geometrie
// trueP (inkl. Entfernungsfehlern der Mikrofone trueP.MicDistUm).
func genCluster9Shots(trueP SimParams) [][6][]int64 {
	positions := [][2]float64{{3, -2}, {40, -55}, {-20, 30}, {10, 10}, {-35, -40}, {25, 40}, {-50, 5}, {15, -30}}
	var airNs [][6][]int64
	for _, pos := range positions {
		airNs = append(airNs, genCalAirNs(pos[0], pos[1], trueP.MicDistUm, trueP))
	}
	return airNs
}

// TestOptimizeCluster9_ImprovesAndRespectsBounds: das Ergebnis darf nie
// schlechter sein als der Ausgangszustand (Startpunkt "aktuelle Parameter"
// ist Teil der Suche) und muss alle Grenzen einhalten.
func TestOptimizeCluster9_ImprovesAndRespectsBounds(t *testing.T) {
	trueP := DefaultSimParams()
	trueP.UsePiezo = piezoNotRequired()
	trueP.TempC = 22
	trueP.MicDistUm = [6]int64{0, 600, -800, 300, -200, 1000}

	start := trueP
	start.TempC = 16
	start.MicDistUm = [6]int64{}
	start.ClusterRadiusUm = 4000
	start.BulletShiftPct, start.BulletShiftCapMm = 50, 3

	o := DefaultCluster9Options()
	res := OptimizeCluster9(genCluster9Shots(trueP), start, o)
	b := res.Best.Params
	t.Logf("vorher Σ9Ø=%.3f nachher Σ9Ø=%.3f (%d/%d <10mm), %d Starts, %d Auswertungen, %dms",
		res.Before.MeanSumMM, res.Best.MeanSumMM, res.Best.Good, res.Best.Solved, res.Starts, res.Evals, res.DurationMs)
	t.Logf("gefunden: temp=%.2f bshift=%d%%/%.2fmm dist=%v",
		b.TempC, b.BulletShiftPct, b.BulletShiftCapMm, b.MicDistUm)

	if res.Best.MeanSumMM > res.Before.MeanSumMM+1e-9 {
		t.Errorf("Σ9 verschlechtert: %.3f -> %.3f", res.Before.MeanSumMM, res.Best.MeanSumMM)
	}
	in := func(name string, v float64, r OptRange) {
		if v < r.Min-1e-9 || v > r.Max+1e-9 {
			t.Errorf("%s=%.3f ausserhalb [%.2f, %.2f]", name, v, r.Min, r.Max)
		}
	}
	in("temp", b.TempC, o.Temp)
	in("bshiftpct", float64(b.BulletShiftPct), o.BShiftPct)
	in("bshiftcap", b.BulletShiftCapMm, o.BShiftCap)
	if b.MicDistUm[0] != 0 {
		t.Errorf("Mic0 (Referenz) = %d, want 0", b.MicDistUm[0])
	}
}

// TestOptimizeCluster9_DisabledParamsUnchanged: nicht freigegebene
// Parameter bleiben exakt auf dem Ausgangswert.
func TestOptimizeCluster9_DisabledParamsUnchanged(t *testing.T) {
	trueP := DefaultSimParams()
	trueP.UsePiezo = piezoNotRequired()
	trueP.MicDistUm = [6]int64{0, 400, 0, -300, 0, 150}
	start := trueP
	start.MicDistUm = [6]int64{}
	start.ClusterRadiusUm = 4000

	o := DefaultCluster9Options()
	o.Temp.Enabled = false
	o.BShiftPct.Enabled, o.BShiftCap.Enabled = false, false
	b := OptimizeCluster9(genCluster9Shots(trueP), start, o).Best.Params
	if b.TempC != start.TempC || b.BulletShiftPct != start.BulletShiftPct ||
		b.BulletShiftCapMm != start.BulletShiftCapMm {
		t.Errorf("nicht freigegebene Parameter veraendert: %+v", b)
	}
}
