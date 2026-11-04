package main

import (
	"math"
	"testing"
)

func i64p(v int64) *int64 { return &v }

// walkDelayed verzoegert die erste Flanke je Mic amplitudenabhaengig so, wie
// es die WALKK-Korrektur der Firmware (walkDeltas) wieder herausrechnet:
// t_roh = t_wahr - round(k*(w - w_mittel)), w_mittel ueber die gueltigen
// Breiten der beteiligten Mics.
func walkDelayed(air [6][]int64, w [6]*int64, k float64) [6][]int64 {
	var sum float64
	n := 0
	for i := 0; i < 6; i++ {
		if w[i] != nil && len(air[i]) > 0 {
			sum += float64(*w[i])
			n++
		}
	}
	mean := float32(sum / float64(n))
	out := air
	for i := 0; i < 6; i++ {
		if w[i] != nil && len(air[i]) > 0 {
			d := float32(k) * (float32(*w[i]) - mean)
			out[i] = []int64{air[i][0] - int64(math.Round(float64(d)))}
		}
	}
	return out
}

// Mit dem passenden WALKK findet die Neuberechnung exakt die Position des
// unverzoegerten Schusses wieder (Mic 4 ohne gueltige Breite bleibt
// unkorrigiert und zaehlt nicht zum Mittel); ohne Korrektur liegt sie daneben.
func TestSolveSimShot_WalkCorrection(t *testing.T) {
	p := DefaultSimParams()
	truth := genCalAirNs(6.3, -4.1, [6]int64{}, p)
	w := [6]*int64{i64p(42000), i64p(31000), i64p(38500), i64p(27000), nil, i64p(45500)}
	const k = 0.42
	sh := SimShotRaw{AirNs: walkDelayed(truth, w, k), AirWNs: w, HasRaw: true}
	p.UsePiezo = new(bool) // synthetische Schuesse ohne Piezo

	want := SolveShot(truth, nil, p)
	q := p
	q.WalkK = k
	got := SolveSimShot(sh, q)
	if !want.PosValid || !got.PosValid {
		t.Fatalf("ungueltig: want=%+v got=%+v", want, got)
	}
	if d := math.Hypot(float64(got.XUm-want.XUm), float64(got.YUm-want.YUm)); d > 20 {
		t.Errorf("mit WALKK: Abweichung %.0f um (got %d/%d, want %d/%d)", d, got.XUm, got.YUm, want.XUm, want.YUm)
	}
	raw := SolveSimShot(sh, p) // WalkK=0: keine Korrektur
	if d := math.Hypot(float64(raw.XUm-want.XUm), float64(raw.YUm-want.YUm)); d < 500 {
		t.Errorf("ohne WALKK sollte die Position deutlich abweichen, Abweichung nur %.0f um", d)
	}
	// ALGO=RIM: Korrektur steckt schon in air_ns - nicht erneut anwenden
	rim := sh
	rim.Algo = "rim"
	if r := rim.airFor(q); r[0][0] != sh.AirNs[0][0] {
		t.Errorf("RIM: air_ns wurde erneut korrigiert")
	}
	// Original-Rohdaten bleiben unveraendert
	before := sh.AirNs[0][0]
	_ = sh.airFor(q)
	if sh.AirNs[0][0] != before {
		t.Errorf("airFor hat die Rohdaten veraendert")
	}
}

// Schallgeschwindigkeit je Schuss (DHT22): mit SoundFromShot gilt der beim
// Schuss gespeicherte Wert statt der aus TempC.
func TestSolveSimShot_SoundFromShot(t *testing.T) {
	gen := DefaultSimParams()
	gen.TempC = 27.5
	truth := genCalAirNs(14.0, 9.0, [6]int64{}, gen)
	snd := gen.soundMps()
	sh := SimShotRaw{AirNs: truth, HasRaw: true, SoundActMps: &snd}

	p := DefaultSimParams()
	p.TempC = 19.5
	p.UsePiezo = new(bool)
	gen.UsePiezo = p.UsePiezo
	want := SolveShot(truth, nil, gen)

	q := p
	q.SoundFromShot = true
	got := SolveSimShot(sh, q)
	if got.XUm != want.XUm || got.YUm != want.YUm {
		t.Errorf("SoundFromShot: %d/%d, erwartet %d/%d", got.XUm, got.YUm, want.XUm, want.YUm)
	}
	if fixed := SolveSimShot(sh, p); fixed.XUm == want.XUm && fixed.YUm == want.YUm {
		t.Errorf("ohne SoundFromShot muss TempC=19.5 gelten (Ergebnis identisch zu 27.5)")
	}
	// Kalibrierung mit Schall je Schuss muss genau dasselbe liefern wie eine
	// Kalibrierung mit derselben Temperatur als globalem Wert.
	dist := [6]int64{0, 900, 0, 0, 0, 0}
	var air [][6][]int64
	var sounds []float64
	for _, xy := range [][2]float64{{-10, 8}, {12, -6}, {3, 15}, {-14, -12}, {7, 2}, {-2, -9}} {
		air = append(air, genCalAirNs(xy[0], xy[1], dist, gen))
		sounds = append(sounds, snd)
	}
	perShot, c1 := CalibrateMicDist(air, sounds, q)
	global, c2 := CalibrateMicDist(air, nil, gen)
	if perShot != global || math.Abs(c1-c2) > 1e-6 {
		t.Errorf("je Schuss %v (%.4f) != global %v (%.4f)", perShot, c1, global, c2)
	}
	if fixed, _ := CalibrateMicDist(air, nil, p); fixed == global {
		t.Errorf("Kalibrierung mit 19.5 Grad sollte sich unterscheiden")
	}
}

// Uebertragung an den Stand: TEMP mit Nachkommastelle, WALKK nur wenn
// gesetzt.
func TestFirmwareSettings_Firmware415(t *testing.T) {
	p := DefaultSimParams()
	p.TempC = 18.46
	has := func(key string) (string, bool) {
		for _, s := range firmwareSettings(p) {
			if s.Key == key {
				return s.Value, true
			}
		}
		return "", false
	}
	if v, _ := has("TEMP"); v != "18.5" {
		t.Errorf("TEMP=%s, erwartet 18.5", v)
	}
	if _, ok := has("WALKK"); ok {
		t.Error("WALKK=0 darf nicht uebertragen werden")
	}
	p.WalkK = 0.4166
	if v, ok := has("WALKK"); !ok || v != "0.417" {
		t.Errorf("WALKK=%s (%v), erwartet 0.417", v, ok)
	}
}
