package main

import (
	"math"
	"testing"
)

func posAlgoTestParams() SimParams {
	p := DefaultSimParams()
	p.UsePiezo = piezoNotRequired()
	p.ClusterRadiusUm = 4000
	return p
}

// TestPosAlgo_ZeroValueIsFirmware: ohne Option exakt das Firmware-Ergebnis.
func TestPosAlgo_ZeroValueIsFirmware(t *testing.T) {
	p := posAlgoTestParams()
	p.PosAlgo = PosAlgoOptions{}
	air := genCalAirNs(12, -7, p.MicOffsetNs, p)
	a := SolveShot(air, nil, p)
	p.PosAlgo = PosAlgoOptions{LSQPelletRMm: 2.25} // nur Radius gesetzt, keine Option aktiv
	if b := SolveShot(air, nil, p); a.XUm != b.XUm || a.YUm != b.YUm {
		t.Errorf("Nullwert veraendert Position: %d/%d -> %d/%d", a.XUm, a.YUm, b.XUm, b.YUm)
	}
}

// TestPosAlgo_LSQExact: Punktquelle, exakte Laufzeiten - die Ausgleichs-
// rechnung muss die wahre Position treffen.
func TestPosAlgo_LSQExact(t *testing.T) {
	p := posAlgoTestParams()
	p.BulletShiftPct = 0
	p.PosAlgo = PosAlgoOptions{LSQ: true}
	for _, pos := range [][2]float64{{0, 0}, {35, -20}, {-60, 45}} {
		r := SolveShot(genCalAirNs(pos[0], pos[1], p.MicOffsetNs, p), nil, p)
		if d := math.Hypot(float64(r.XUm)/1000-pos[0], float64(r.YUm)/1000-pos[1]); !r.PosValid || d > 0.02 {
			t.Errorf("(%.0f,%.0f): Abweichung %.3fmm (valid=%v)", pos[0], pos[1], d, r.PosValid)
		}
	}
}

// TestPosAlgo_LSQRejectsOutlierMic: ein Mikrofon mit grobem Zeitfehler
// (z.B. Echo) wird verworfen, die Position bleibt genau.
func TestPosAlgo_LSQRejectsOutlierMic(t *testing.T) {
	p := posAlgoTestParams()
	p.BulletShiftPct = 0
	air := genCalAirNs(20, 15, p.MicOffsetNs, p)
	air[4][0] += 4000 // ~1.4mm Laufweg zu spaet
	p.PosAlgo = PosAlgoOptions{LSQ: true}
	r := SolveShot(air, nil, p)
	if d := math.Hypot(float64(r.XUm)/1000-20, float64(r.YUm)/1000-15); !r.PosValid || d > 0.05 {
		t.Errorf("Abweichung %.3fmm trotz Ausreisser-Verwerfung (valid=%v)", d, r.PosValid)
	}
}

// TestPosAlgo_ClusterOptionsStayClose: Cluster-Varianten bleiben bei
// exakten Daten nahe der wahren Position.
func TestPosAlgo_ClusterOptionsStayClose(t *testing.T) {
	p := posAlgoTestParams()
	p.BulletShiftPct = 0
	p.PosAlgo = PosAlgoOptions{CorrCluster: true, Iterations: 2, Weighted: true}
	r := SolveShot(genCalAirNs(-25, 30, p.MicOffsetNs, p), nil, p)
	if d := math.Hypot(float64(r.XUm)/1000+25, float64(r.YUm)/1000-30); !r.PosValid || d > 0.05 {
		t.Errorf("Abweichung %.3fmm (valid=%v)", d, r.PosValid)
	}
}
