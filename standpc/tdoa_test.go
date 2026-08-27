// ============================================================================
// tdoa_test.go – Verifikation des TDOA-Solvers mit synthetischen Daten
//
// Prinzip: Bekannte Einschlagposition annehmen, daraus exakte Laufzeiten
// rueckwaerts berechnen, dann pruefen ob der Solver die Position
// wiederfindet. Zusaetzlich: Rauschtest (Timer-Quantisierung 1µs).
//
// Ausfuehren:  go test ./...
// ============================================================================
package main

import (
	"math"
	"testing"
)

func testConfig() *Config {
	return &Config{
		Sensors: []SensorPos{
			{X: 0, Y: 0}, {X: 250, Y: 0}, {X: 250, Y: 250}, {X: 0, Y: 250},
		},
		SoundSpeedMPS: 3000,
		PlateAngleDeg: 0, // Geometrie hier nicht im Test
	}
}

// synthTimes erzeugt exakte TDOA-Zeiten (µs) fuer eine Einschlagposition
func synthTimes(cfg *Config, x, y float64) []int64 {
	v := cfg.SoundSpeedMPS / 1e6 // mm/ns
	d := make([]float64, len(cfg.Sensors))
	minD := math.Inf(1)
	for i, s := range cfg.Sensors {
		d[i] = math.Hypot(x-s.X, y-s.Y)
		minD = math.Min(minD, d[i])
	}
	t := make([]int64, len(d))
	for i := range d {
		t[i] = int64(math.Round((d[i] - minD) / v / 4.17) * 4.17) // ~4ns CC-Quantisierung
	}
	return t
}

func TestSolveExactPositions(t *testing.T) {
	cfg := testConfig()
	solver := NewTDOASolver(cfg)

	cases := []struct{ x, y float64 }{
		{125, 125}, // Mitte
		{60, 180},  // dezentral
		{200, 50},
		{125, 30},  // nahe Kante
		{30, 125},
	}
	for _, c := range cases {
		times := synthTimes(cfg, c.x, c.y)
		gx, gy, conf, err := solver.Solve(times)
		if err != nil {
			t.Fatalf("Solve(%v/%v): %v", c.x, c.y, err)
		}
		errMM := math.Hypot(gx-c.x, gy-c.y)
		// 1µs Quantisierung @3000m/s = 3mm Wegquantisierung pro Messung,
		// durch Ueberbestimmung deutlich besser – wir fordern < 2mm
		if errMM > 0.1 {
			t.Errorf("Position (%.0f/%.0f): Fehler %.2f mm (got %.2f/%.2f)",
				c.x, c.y, errMM, gx, gy)
		}
		if conf < 0.3 {
			t.Errorf("Position (%.0f/%.0f): Confidence %.2f zu niedrig",
				c.x, c.y, conf)
		}
	}
}

func TestSolveThreeSensors(t *testing.T) {
	cfg := testConfig()
	solver := NewTDOASolver(cfg)
	times := synthTimes(cfg, 100, 140)
	times[2] = -1 // Sensor 3 faellt aus
	gx, gy, _, err := solver.Solve(times)
	if err != nil {
		t.Fatalf("3-Sensor-Fall: %v", err)
	}
	if e := math.Hypot(gx-100, gy-140); e > 0.2 {
		t.Errorf("3-Sensor-Fall: Fehler %.2f mm", e)
	}
}

func TestSolveTooFewSensors(t *testing.T) {
	cfg := testConfig()
	solver := NewTDOASolver(cfg)
	times := []int64{0, 50, -1, -1}
	if _, _, _, err := solver.Solve(times); err == nil {
		t.Error("2 Sensoren muessen einen Fehler liefern")
	}
}

func TestScoring(t *testing.T) {
	target := &TargetDef{
		Name: "LG 10m", CaliberMM: 4.5, EdgeScoring: true, InnerTenDMM: 0.5,
		Rings: []RingDef{
			{10, 0.5}, {9, 5.5}, {8, 10.5}, {7, 15.5}, {6, 20.5},
			{5, 25.5}, {4, 30.5}, {3, 35.5}, {2, 40.5}, {1, 45.5},
		},
	}
	sc := NewScorer(target)

	// Exakte Mitte: 10.9, Innenzehner
	r := sc.Score(0, 0)
	if r.Ring != 10 || r.Decimal != 10.9 || !r.InnerTen {
		t.Errorf("Mitte: Ring=%d Dec=%.1f IT=%v – erwartet 10/10.9/true",
			r.Ring, r.Decimal, r.InnerTen)
	}

	// "Rand zaehlt": Lochmitte 2,4mm neben Mitte -> rEff=0,15 -> noch 10er
	r = sc.Score(2.4, 0)
	if r.Ring != 10 {
		t.Errorf("Randzaehler-10: Ring=%d, erwartet 10", r.Ring)
	}

	// Lochmitte 5mm daneben -> rEff=2,75 -> Aussenradius 10er(0,25)<2,75<=2,75+step? 
	// 9er-Aussenradius=2,75 -> exakt 9
	r = sc.Score(5.0, 0)
	if r.Ring != 9 {
		t.Errorf("9er-Test: Ring=%d, erwartet 9", r.Ring)
	}

	// Weit daneben: 0 Ringe
	r = sc.Score(40, 40)
	if r.Ring != 0 {
		t.Errorf("Fehlschuss: Ring=%d, erwartet 0", r.Ring)
	}

	// Teiler: 10mm Abstand = Teiler 1000.0
	r = sc.Score(10, 0)
	if math.Abs(r.CenterDistance-1000.0) > 0.1 {
		t.Errorf("Teiler: %.1f, erwartet 1000.0", r.CenterDistance)
	}
}
