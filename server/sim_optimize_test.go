package main

import "testing"

// genCluster9Shots: synthetische Kalibrier-Schuesse mit "wahrer" Geometrie
// trueP (inkl. Mic-Timing-Fehlern trueP.MicOffsetNs).
func genCluster9Shots(trueP SimParams) [][6][]int64 {
	positions := [][2]float64{{3, -2}, {40, -55}, {-20, 30}, {10, 10}, {-35, -40}, {25, 40}, {-50, 5}, {15, -30}}
	var airNs [][6][]int64
	for _, pos := range positions {
		airNs = append(airNs, genCalAirNs(pos[0], pos[1], trueP.MicOffsetNs, trueP))
	}
	return airNs
}

// TestOptimizeCluster9_ImprovesAndRespectsBounds: das Ergebnis darf nie
// schlechter sein als der Ausgangszustand (Startpunkt "aktuelle Parameter"
// ist Teil der Suche) und muss alle Grenzen einhalten.
func TestOptimizeCluster9_ImprovesAndRespectsBounds(t *testing.T) {
	trueP := DefaultSimParams()
	trueP.UsePiezo = piezoNotRequired()
	trueP.StandoffPaperMM, trueP.MicHalfXMm, trueP.SoundMps = 28.7, 115.8, 344
	trueP.MicOffsetNs = [6]int64{0, 180, -240, 90, -60, 310}

	start := trueP
	start.StandoffPaperMM, start.MicHalfXMm, start.SoundMps = 27.5, 114.5, 341
	start.MicOffsetNs = [6]int64{}
	start.ClusterRadiusUm = 4000
	start.BulletShiftPct, start.BulletShiftCapMm = 50, 3

	o := DefaultCluster9Options()
	res := OptimizeCluster9(genCluster9Shots(trueP), start, o)
	b := res.Best.Params
	t.Logf("vorher Σ9Ø=%.3f nachher Σ9Ø=%.3f (%d/%d <10mm), %d Starts, %d Auswertungen, %dms",
		res.Before.MeanSumMM, res.Best.MeanSumMM, res.Best.Good, res.Best.Solved, res.Starts, res.Evals, res.DurationMs)
	t.Logf("gefunden: standoff=%.2f michalfx=%.2f sound=%.2f bshift=%d%%/%.2fmm ofs=%v",
		b.StandoffPaperMM, b.MicHalfXMm, b.SoundMps, b.BulletShiftPct, b.BulletShiftCapMm, b.MicOffsetNs)

	if res.Best.MeanSumMM > res.Before.MeanSumMM+1e-9 {
		t.Errorf("Σ9 verschlechtert: %.3f -> %.3f", res.Before.MeanSumMM, res.Best.MeanSumMM)
	}
	in := func(name string, v float64, r OptRange) {
		if v < r.Min-1e-9 || v > r.Max+1e-9 {
			t.Errorf("%s=%.3f ausserhalb [%.2f, %.2f]", name, v, r.Min, r.Max)
		}
	}
	in("standoff", b.StandoffPaperMM, o.Standoff)
	in("michalfx", b.MicHalfXMm, o.MicHalfX)
	in("sound", b.SoundMps, o.Sound)
	in("bshiftpct", float64(b.BulletShiftPct), o.BShiftPct)
	in("bshiftcap", b.BulletShiftCapMm, o.BShiftCap)
	if b.MicOffsetNs[0] != 0 {
		t.Errorf("Mic0 (Referenz) = %d, want 0", b.MicOffsetNs[0])
	}
}

// TestOptimizeCluster9_DisabledParamsUnchanged: nicht freigegebene
// Parameter bleiben exakt auf dem Ausgangswert.
func TestOptimizeCluster9_DisabledParamsUnchanged(t *testing.T) {
	trueP := DefaultSimParams()
	trueP.UsePiezo = piezoNotRequired()
	trueP.MicOffsetNs = [6]int64{0, 120, 0, -80, 0, 40}
	start := trueP
	start.MicOffsetNs = [6]int64{}
	start.ClusterRadiusUm = 4000

	o := DefaultCluster9Options()
	o.Standoff.Enabled, o.MicHalfX.Enabled, o.Sound.Enabled = false, false, false
	o.BShiftPct.Enabled, o.BShiftCap.Enabled = false, false
	b := OptimizeCluster9(genCluster9Shots(trueP), start, o).Best.Params
	if b.StandoffPaperMM != start.StandoffPaperMM || b.MicHalfXMm != start.MicHalfXMm ||
		b.SoundMps != start.SoundMps || b.BulletShiftPct != start.BulletShiftPct ||
		b.BulletShiftCapMm != start.BulletShiftCapMm {
		t.Errorf("nicht freigegebene Parameter veraendert: %+v", b)
	}
}
