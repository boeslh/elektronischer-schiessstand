// hybrid_test.go – Regressionstest der Luft-TOA-Verfeinerung
package main

import (
	"math"
	"testing"
)

func hybridTestSetup() (*TDOASolver, *AirSolver) {
	cfg := testConfig()
	hc := &HybridConfig{
		Enabled:          true,
		AirSoundSpeedMPS: 343,
		GateUs:           15,
		AirSensors: []AirSensor{
			{-20, -20, 40}, {270, -20, 40}, {270, 270, 40}, {-20, 270, 40},
		},
	}
	return NewTDOASolver(cfg), NewAirSolver(hc)
}

// synthAir erzeugt Vorlaeufer + direkten Schall + Stoerflanke je Mikrofon
func synthAir(steel *TDOASolver, air *AirSolver, x, y, t0 float64) [][]int64 {
	out := make([][]int64, len(air.mics))
	vSteel := steel.soundSpeed // mm/ns
	for i, m := range air.mics {
		lat := math.Hypot(x-m.X, y-m.Y)
		pre := t0 + lat/vSteel + m.Z/air.vAir
		direct := t0 + dist3D(x, y, m)/air.vAir
		clutter := pre + 150_000 // Nachklingel-Stoerflanke
		out[i] = []int64{int64(pre), int64(direct), int64(clutter)}
	}
	return out
}

func TestHybridRefine(t *testing.T) {
	steel, air := hybridTestSetup()
	cases := []struct{ x, y float64 }{
		{125, 125}, {128, 129}, {120, 117}, {132, 118},
	}
	for _, c := range cases {
		// Stahlzeiten + t0 exakt
		times := synthTimes(steel2cfg(steel), c.x, c.y)
		minD := math.Inf(1)
		for _, s := range steel.sensors {
			minD = math.Min(minD, math.Hypot(c.x-s.X, c.y-s.Y))
		}
		t0 := -minD / steel.soundSpeed
		airNs := synthAir(steel, air, c.x, c.y, t0)

		// Grobposition absichtlich um 0,4mm versetzt (realer Stahl-Fehler)
		fx, fy, ok, used := air.Refine(c.x+0.4, c.y-0.3, steel, times, airNs)
		if !ok {
			t.Fatalf("(%v/%v): Refine fehlgeschlagen (used=%d)", c.x, c.y, used)
		}
		if e := math.Hypot(fx-c.x, fy-c.y); e > 0.05 {
			t.Errorf("(%v/%v): Fehler %.3f mm (>0.05)", c.x, c.y, e)
		}
	}
}

func TestHybridRejectsBadGating(t *testing.T) {
	steel, air := hybridTestSetup()
	// Nur Stoerflanken, kein direkter Schall -> Refine muss ablehnen
	airNs := [][]int64{{999_999}, {999_999}, {999_999}, {999_999}}
	times := synthTimes(steel2cfg(steel), 125, 125)
	if _, _, ok, _ := air.Refine(125, 125, steel, times, airNs); ok {
		t.Error("Refine akzeptierte reine Stoerflanken")
	}
}

// steel2cfg: Hilfsbruecke fuer synthTimes aus tdoa_test.go
func steel2cfg(s *TDOASolver) *Config {
	return &Config{Sensors: s.sensors, SoundSpeedMPS: s.soundSpeed * 1e6}
}
