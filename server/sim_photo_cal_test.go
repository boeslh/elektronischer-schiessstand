package main

import (
	"math"
	"testing"
)

// photoCalSyntheticShots erzeugt Kalibrier-Schuesse rund um den schwarzen
// Innenkreis (dort, wo sie auf dem Foto sichtbar sind) mit bekannten
// "wahren" Anlagenparametern truth. Foto-Wahrheit = Blechposition +
// wahre OFFSETX/Y.
func photoCalSyntheticShots(truth SimParams) []PhotoCalShot {
	var shots []PhotoCalShot
	no := 1
	for _, r := range []float64{35, 50, 62} {
		for k := 0; k < 6; k++ {
			a := float64(k)*math.Pi/3 + r/40 // je Ring leicht verdreht
			x, y := r*math.Cos(a), r*math.Sin(a)
			shots = append(shots, PhotoCalShot{
				ShotNo: no,
				AirNs:  genCalAirNs(x, y, truth.MicOffsetNs, truth),
				TruthX: x + float64(truth.OffsetXUm)/1000,
				TruthY: y + float64(truth.OffsetYUm)/1000,
			})
			no++
		}
	}
	return shots
}

func TestPhotoCalibrate_RecoversKnownParams(t *testing.T) {
	truth := DefaultSimParams()
	truth.UsePiezo = piezoNotRequired() // synthetische Daten ohne piezo_ns
	truth.MicOffsetNs = [6]int64{0, 900, -600, 400, -1200, 300}
	truth.SoundMps = 338
	truth.OffsetXUm, truth.OffsetYUm = 2500, -1800
	shots := photoCalSyntheticShots(truth)

	start := DefaultSimParams()
	start.UsePiezo = piezoNotRequired()
	start.SoundMps = 346

	rep, err := PhotoCalibrate(shots, start, PhotoCalOptions{MicOffsets: true, Offset: true, SoundSpeed: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.RMSBeforeMM < 1 {
		t.Fatalf("Testaufbau: Ausgangsfehler %.2f mm zu klein, Test waere aussagelos", rep.RMSBeforeMM)
	}
	if rep.RMSAfterMM > 0.15 || rep.InvalidAfter > 0 {
		t.Errorf("RMS nach Optimierung %.3f mm (vorher %.2f), ungueltig %d - erwartet < 0.15 mm",
			rep.RMSAfterMM, rep.RMSBeforeMM, rep.InvalidAfter)
	}
	if rep.Params.SoundMps != 338 {
		t.Errorf("SOUNDSPEED = %v, want 338", rep.Params.SoundMps)
	}
	if d := math.Hypot(float64(rep.Params.OffsetXUm-truth.OffsetXUm), float64(rep.Params.OffsetYUm-truth.OffsetYUm)); d > 150 {
		t.Errorf("OFFSETX/Y = %d/%d, want ~%d/%d", rep.Params.OffsetXUm, rep.Params.OffsetYUm, truth.OffsetXUm, truth.OffsetYUm)
	}
	for i := 1; i < 6; i++ {
		if d := rep.Params.MicOffsetNs[i] - truth.MicOffsetNs[i]; d < -120 || d > 120 {
			t.Errorf("OFS%d = %d, want ~%d", i, rep.Params.MicOffsetNs[i], truth.MicOffsetNs[i])
		}
	}
	if math.Abs(rep.RotationDeg) > 0.2 || math.Abs(rep.Scale-1) > 0.005 {
		t.Errorf("Restdrehung %.3f Grad / Massstab %.4f - erwartet ~0 / ~1", rep.RotationDeg, rep.Scale)
	}

	// Entscheidend: die Kalibrierung muss auch fuer Treffer gelten, die NICHT
	// im Foto waren - vor allem im Zentrum (dort sind Foto-Loecher unsichtbar,
	// weil sie ins schwarze Band gehen).
	var maxErr float64
	for _, v := range [][2]float64{{0, 0}, {3, -2}, {-8, 5}, {12, 12}, {-20, -15}, {25, -5}, {0, 28}} {
		air := genCalAirNs(v[0], v[1], truth.MicOffsetNs, truth)
		r := SolveShot(air, nil, rep.Params)
		if !r.PosValid {
			t.Errorf("Validierungsschuss %v ungueltig", v)
			continue
		}
		wx, wy := v[0]+float64(truth.OffsetXUm)/1000, v[1]+float64(truth.OffsetYUm)/1000
		maxErr = math.Max(maxErr, math.Hypot(float64(r.XUm)/1000-wx, float64(r.YUm)/1000-wy))
	}
	if maxErr > 0.2 {
		t.Errorf("Validierung im Zentrum: max. Fehler %.3f mm, erwartet < 0.2 mm", maxErr)
	}
}

// Ein falscher Standoff laesst sich mit Foto-Wahrheit ebenfalls finden (mit
// der Selbstkonsistenz-Kalibrierung nicht).
func TestPhotoCalibrate_Standoff(t *testing.T) {
	truth := DefaultSimParams()
	truth.UsePiezo = piezoNotRequired()
	truth.StandoffPaperMM = 26
	shots := photoCalSyntheticShots(truth)

	start := DefaultSimParams()
	start.UsePiezo = piezoNotRequired()
	start.StandoffPaperMM = 12
	rep, err := PhotoCalibrate(shots, start, PhotoCalOptions{Standoff: true, Offset: true})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(rep.Params.StandoffPaperMM-26) > 0.5 || rep.RMSAfterMM > 0.15 {
		t.Errorf("Standoff %.2f (want 26), RMS %.3f -> %.3f mm", rep.Params.StandoffPaperMM, rep.RMSBeforeMM, rep.RMSAfterMM)
	}
}

func TestPhotoCalibrate_TooFewShots(t *testing.T) {
	p := DefaultSimParams()
	p.UsePiezo = piezoNotRequired()
	shots := photoCalSyntheticShots(p)[:9]
	// OFS1-5 + OFFSETX/Y + SOUNDSPEED = 8 freie Parameter -> mind. 11 Schuesse
	if _, err := PhotoCalibrate(shots, p, PhotoCalOptions{MicOffsets: true, Offset: true, SoundSpeed: true}); err == nil {
		t.Error("erwartet Fehler bei 9 Schuessen fuer 8 freie Parameter")
	}
}

// Realistischer: Foto-Positionen mit Messrauschen (+-0.3mm) und einem grob
// falsch zugeordneten Loch - die gekappte Kostenfunktion muss den
// Ausreisser verkraften, und die Genauigkeit im Zentrum muss deutlich
// besser als das Einzelrauschen bleiben.
func TestPhotoCalibrate_NoiseAndOutlier(t *testing.T) {
	truth := DefaultSimParams()
	truth.UsePiezo = piezoNotRequired()
	truth.MicOffsetNs = [6]int64{0, 700, -400, 250, -900, 500}
	truth.OffsetXUm, truth.OffsetYUm = -1500, 2200
	shots := photoCalSyntheticShots(truth)
	seed := uint32(12345)
	noise := func() float64 { // deterministisch, gleichverteilt +-0.3mm
		seed = seed*1664525 + 1013904223
		return (float64(seed>>8)/float64(1<<24) - 0.5) * 0.6
	}
	for i := range shots {
		shots[i].TruthX += noise()
		shots[i].TruthY += noise()
	}
	shots[4].TruthX += 25 // Ausreisser (Loch falsch zugeordnet)

	start := DefaultSimParams()
	start.UsePiezo = piezoNotRequired()
	rep, err := PhotoCalibrate(shots, start, PhotoCalOptions{MicOffsets: true, Offset: true})
	if err != nil {
		t.Fatal(err)
	}
	var maxErr float64
	for _, v := range [][2]float64{{0, 0}, {5, -3}, {-10, 8}, {15, 15}, {-22, -12}} {
		r := SolveShot(genCalAirNs(v[0], v[1], truth.MicOffsetNs, truth), nil, rep.Params)
		wx, wy := v[0]+float64(truth.OffsetXUm)/1000, v[1]+float64(truth.OffsetYUm)/1000
		maxErr = math.Max(maxErr, math.Hypot(float64(r.XUm)/1000-wx, float64(r.YUm)/1000-wy))
	}
	t.Logf("RMS %.3f -> %.3f mm, Validierung Zentrum max. %.3f mm", rep.RMSBeforeMM, rep.RMSAfterMM, maxErr)
	if maxErr > 0.3 {
		t.Errorf("Validierung im Zentrum: max. Fehler %.3f mm, erwartet < 0.3 mm", maxErr)
	}
}
