package main

import (
	"math"
	"testing"
)

// rimTestGeometry: dieselbe Geometrie wie test/host_sim.cpp (Firmware-Repo,
// siehe shot_locator.h-Portierung) - 6 Mics, Standoff 28mm, Schallgeschw.
// 0.000355 mm/ns, Diabolo-Lochrand 2.25mm.
func rimTestGeometry() rimGeometry {
	const hx, hy float32 = 115, 85
	return rimGeometry{
		micX:           [6]float32{-hx, hx, -hx, hx, -hx, hx},
		micY:           [6]float32{-hy, -hy, hy, hy, 0, 0},
		standoffMm:     28,
		soundMmPerNs:   0.000355,
		pelletRadiusMm: 2.25,
		maxDistMm:      320,
	}
}

// TestRimLocate_SyntheticGroundTruth generiert (ohne Rauschen) fuer eine
// bekannte Trefferposition + bekannten Einschlagzeitpunkt t0 (relativ zum
// Piezo-Anker) die direkten Laufzeiten je Mikrofon exakt wie processShot-
// Anchored()/host_sim.cpp es taeten, und prueft, dass rimLocate() daraus
// wieder (x,y) mit sehr kleiner Abweichung liefert - Regressionstest fuer
// die Go-Portierung von shot_locator.h.
func TestRimLocate_SyntheticGroundTruth(t *testing.T) {
	g := rimTestGeometry()
	gate := rimGate{active: true, t0MinNs: -1400000, t0MaxNs: -100000}
	p := defaultRimParams(4)

	cases := []struct {
		name   string
		x, y   float32
		t0Ns   float32 // relativ zum Piezo, muss im Gate liegen
	}{
		{"zentrum", 0, 0, -900000},
		{"ausser_zentrum", 30, -20, -700000},
		{"randnah", 60, 55, -1000000},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var e rimEdges
			for i := 0; i < 6; i++ {
				e.enabled[i] = true
				d, _, _ := rimPathMm(g, i, c.x, c.y)
				tDirect := c.t0Ns + d/g.soundMmPerNs
				e.t[i][0] = tDirect
				e.n[i] = 1
			}

			res, ok := rimLocate(g, e, gate, p)
			if !ok || !res.valid {
				t.Fatalf("rimLocate: ok=%v valid=%v (erwartet gueltiges Ergebnis)", ok, res.valid)
			}
			dx, dy := float64(res.xMm-c.x), float64(res.yMm-c.y)
			errMM := math.Hypot(dx, dy)
			t.Logf("got x=%.3f y=%.3f t0=%.0f sigmaX=%.4f sigmaY=%.4f nUsed=%d (soll x=%.1f y=%.1f)",
				res.xMm, res.yMm, res.t0Ns, res.sigmaXMm, res.sigmaYMm, res.nUsed, c.x, c.y)
			if errMM > 0.05 {
				t.Errorf("Abweichung %.4f mm zu gross (erwartet < 0.05mm ohne Rauschen)", errMM)
			}
			if res.nUsed != 6 {
				t.Errorf("nUsed=%d, erwartet 6 (alle Mics rauschfrei innerhalb Toleranz)", res.nUsed)
			}
		})
	}
}

// TestRimLocate_MuzzleBlastIgnored bildet nach, wofuer ALGO=RIM eingefuehrt
// wurde (siehe Rev-4.11.0-Hinweis im .ino): ein Muendungsknall-Ereignis vor
// dem eigentlichen Einschlag (ausserhalb des Piezo-Gates) darf die Loesung
// nicht verfaelschen - das Gate filtert es heraus.
func TestRimLocate_MuzzleBlastIgnored(t *testing.T) {
	g := rimTestGeometry()
	gate := rimGate{active: true, t0MinNs: -1400000, t0MaxNs: -100000}
	p := defaultRimParams(4)

	const x, y float32 = 15, -10
	const t0 float32 = -900000
	var e rimEdges
	for i := 0; i < 6; i++ {
		e.enabled[i] = true
		// Muendungsknall: alle Mics gleichzeitig, lange VOR dem Gate.
		e.t[i][0] = -38000000
		d, _, _ := rimPathMm(g, i, x, y)
		e.t[i][1] = t0 + d/g.soundMmPerNs
		e.n[i] = 2
	}

	res, ok := rimLocate(g, e, gate, p)
	if !ok || !res.valid {
		t.Fatalf("rimLocate: ok=%v valid=%v", ok, res.valid)
	}
	errMM := math.Hypot(float64(res.xMm-x), float64(res.yMm-y))
	if errMM > 0.05 {
		t.Errorf("Muendungsknall hat Loesung verfaelscht: Abweichung %.4f mm (x=%.2f y=%.2f statt %.1f/%.1f)",
			errMM, res.xMm, res.yMm, x, y)
	}
}

// TestSolveShotRIM_NoPiezo: ohne piezo_ns (z.B. SET PIEZO=0 zum
// Aufnahmezeitpunkt) kann ALGO=RIM nicht ausgewertet werden - siehe
// rimTimesFromPiezo()/solveShotRIM().
func TestSolveShotRIM_NoPiezo(t *testing.T) {
	p := DefaultSimParams()
	p.Algo = "rim"
	var airNs [6][]int64
	got := SolveShot(airNs, nil, p)
	if got.PosValid {
		t.Errorf("PosValid=true ohne piezo_ns erwartet false")
	}
}

// genRimCalAirNs erzeugt fuer eine bekannte Trefferposition + Einschlagzeit-
// punkt t0 (relativ zum Piezo) je Mikrofon EINE synthetische Kandidatenflanke
// inkl. eines absichtlich falschen Timing-Fehlers (trueOffsetNs) - Piezo-
// Anker willkuerlich auf 0 gesetzt (rimTimesFromPiezo() zieht ihn ohnehin
// nur wieder ab), airNs[i][0] daher direkt = piezo-relative Rohzeit.
//
// jitterNs (deterministisch aus i/x/y abgeleitet, kein echter Zufall - der
// Test soll reproduzierbar bleiben): calCostRim() erlaubt waehrend der Suche
// bewusst nur minMics=3 (RANSAC verwirft "Ausreisser"), siehe Kommentar in
// calCostRimFn(). Bei EXAKT rauschfreien Daten ist das Verwerfen von zwei
// eigentlich einwandfreien Mikrofonen "kostenlos" (ein 4er-Fit ist dann
// ebenso perfekt rauschfrei wie ein 6er-Fit) - die Koordinatensuche findet
// dann einen ebenso guten, aber falschen Alternativpunkt (verifiziert:
// keine Portierungsluecke, siehe Session-Notiz). Echte Kalibrierdaten haben
// nie exakt null Rauschen - das minimale Jitter hier bildet das nach und
// macht "Mikrofone verwerfen" wieder unattraktiv gegenueber der echten
// Loesung, wie es reale Aufnahmen taeten.
func genRimCalAirNs(g rimGeometry, x, y, t0Ns float32, trueOffsetNs [6]int64) ([6][]int64, *int64) {
	var airNs [6][]int64
	for i := 0; i < 6; i++ {
		d, _, _ := rimPathMm(g, i, x, y)
		jitterNs := float32(((i*7+int(x)*3+int(y)*5)%21)-10) * 2.0 // ~-20..20ns
		raw := int64(math.Round(float64(t0Ns+d/g.soundMmPerNs+jitterNs))) + trueOffsetNs[i]
		airNs[i] = []int64{raw}
	}
	zero := int64(0)
	return airNs, &zero
}

// rimCalTestParams: gemeinsame Test-Parameter fuer die RIM-Kalibrierungs-
// Tests - Schallgeschwindigkeit MUSS zu rimTestGeometry() passen, sonst baut
// calCostRimFn() intern eine ANDERE Geometrie als die hier zur
// Datenerzeugung genutzte rimTestGeometry() (Diskrepanz fuehrt zu einem
// systematischen Restfehler, der wie ein Portierungsfehler aussieht, aber
// keiner ist - siehe Session-Notiz): SoundMps=355 wie
// rimTestGeometry()/host_sim.cpp (DefaultSimParams() liefert seit Rev
// 4.10.3 den neuen Firmware-Default 343).
func rimCalTestParams() SimParams {
	p := DefaultSimParams()
	p.Algo = "rim"
	p.SoundMps = 355
	return p
}

// TestCalibrateMicOffsetsRIM_ImprovesOverNoCalibration: RIM-Gegenstueck zu
// TestCalibrateMicOffsets_ImprovesOverNoCalibration - prueft bewusst nur die
// tatsaechlich GARANTIERTE Eigenschaft der Koordinatensuche (jeder Schritt
// wird nur uebernommen, wenn er die Kosten senkt oder gleich laesst, siehe
// coordinateDescentCalibrate()), nicht die exakte Wiederherstellung eines
// eingespeisten Fehlers.
//
// Anders als bei ALGO=CLASSIC ist "exakte Wiederherstellung selbst bei nur
// EINEM verstellten Mikrofon" fuer ALGO=RIM NICHT zuverlaessig testbar:
// calCostRim() laesst waehrend der Suche bewusst nur minMics=3 zu (RANSAC
// verwirft "Ausreisser"-Mikrofone, siehe Kommentar in calCostRimFn()). Bei
// (nahezu) rauschfreien Kalibrierdaten ist das Verwerfen von 1-2 eigentlich
// einwandfreien Mikrofonen dadurch "kostenlos" (ein 4er-Fit ist dann ebenso
// rauschfrei perfekt wie ein 6er-Fit) - die Koordinatensuche kann dann einen
// ebenso guten, aber falschen Alternativpunkt finden. Verifiziert (siehe
// Session-Notiz): calCostRimFn([0,-10000,-10000,0,0,0]) liefert dabei sogar
// einen tatsaechlich NIEDRIGEREN Kostenwert als calCostRimFn([0,180,0,0,0,0])
// - eine echte, firmware-treue Eigenschaft des Algorithmus (dieselbe
// calCostRim()-Funktion liefe auf echter Hardware identisch in dieselbe
// Falle), keine Portierungsluecke. Die eigentliche Korrektheit von
// rimLocate()/rimPathMm() (dem tatsaechlich portierten Kern) verifizieren
// stattdessen TestRimLocate_SyntheticGroundTruth/_MuzzleBlastIgnored oben.
func TestCalibrateMicOffsetsRIM_ImprovesOverNoCalibration(t *testing.T) {
	g := rimTestGeometry()
	p := rimCalTestParams()
	trueOffsetNs := [6]int64{0, 180, -240, 90, -60, 310}

	type calPos struct{ x, y, t0 float32 }
	positions := []calPos{
		{3, -2, -900000}, {40, -55, -700000}, {-20, 30, -1100000},
		{10, 10, -450000}, {-35, -40, -1300000}, {25, 40, -600000},
	}
	var airNs [][6][]int64
	var piezoNs []*int64
	for _, pos := range positions {
		a, pz := genRimCalAirNs(g, pos.x, pos.y, pos.t0, trueOffsetNs)
		airNs = append(airNs, a)
		piezoNs = append(piezoNs, pz)
	}

	costFn := calCostRimFn(airNs, piezoNs, p)
	costNoCalibration := costFn([6]float32{})
	gotOffsets, costAfter := CalibrateMicOffsets(airNs, piezoNs, p)
	t.Logf("gotOffsets=%v costAfter=%.1f costNoCalibration=%.1f want=%v",
		gotOffsets, costAfter, costNoCalibration, trueOffsetNs)

	if float32(costAfter) > costNoCalibration+1e-3 {
		t.Errorf("Kalibrierung verschlechtert die Kosten: %.3f > %.3f (unkalibriert)",
			costAfter, costNoCalibration)
	}
}

// TestSolveShotRIM_AppliesMicOffsetNs ist ein End-zu-Ende-Regressionstest
// fuer einen realen Fund (siehe Session-Notiz): solveShotRIM() wandte
// p.MicOffsetNs frueher NIRGENDS an - eine per CalibrateMicOffsets(algo=rim)
// gefundene (oder manuell gesetzte, z.B. per SHOW-Import uebernommene)
// Kalibrierung hatte dadurch beim eigentlichen "Neu berechnen" ueberhaupt
// keine Wirkung, Positionen blieben auf den unkorrigierten Rohzeiten (bei
// echten Mikrofon-Timingfehlern im zweistelligen us-Bereich: Verschiebungen
// von mehreren Zentimetern). rimLocate()/calCostRimFn() selbst waren davon
// NICHT betroffen (isoliert in TestRimLocate_*/TestCalibrateMicOffsetsRIM_*
// oben bereits verifiziert) - dieser Test deckt gezielt die Anwendung der
// gefundenen Offsets im operativen SolveShot()-Pfad ab, die zuvor kein Test
// abdeckte.
func TestSolveShotRIM_AppliesMicOffsetNs(t *testing.T) {
	g := rimTestGeometry()
	p := rimCalTestParams()
	const x, y, t0 float32 = 5, -3, -900000
	trueOffsetNs := [6]int64{0, 9500, -8200, 11000, -6300, 7800}

	air, piezoNs := genRimCalAirNs(g, x, y, t0, trueOffsetNs)

	// Ohne Korrektur (Offsets=0): die eingespeisten Mic-Timingfehler muessen
	// eine deutliche Abweichung von der wahren Position verursachen -
	// andernfalls waere dieser Test nicht aussagekraeftig.
	rUncorrected := SolveShot(air, piezoNs, p)
	if rUncorrected.PosValid {
		dx := float64(rUncorrected.XUm)/1000 - float64(x)
		dy := float64(rUncorrected.YUm)/1000 - float64(y)
		if math.Hypot(dx, dy) < 2.0 {
			t.Fatalf("Testaufbau ungeeignet: unkorrigierte Abweichung nur %.2fmm, erwartet >2mm", math.Hypot(dx, dy))
		}
	}

	// Mit den EXAKT eingespeisten Offsets muss SolveShot() die wahre
	// Position (nahezu) exakt zurueckliefern - das ist der eigentliche Kern
	// des Regressionstests: schlaegt fehl, falls MicOffsetNs im RIM-Pfad
	// wieder nicht angewandt wird.
	p.MicOffsetNs = trueOffsetNs
	rCorrected := SolveShot(air, piezoNs, p)
	if !rCorrected.PosValid {
		t.Fatalf("PosValid=false mit korrekten Offsets, reason=%q", rCorrected.InvalidReason)
	}
	dx := float64(rCorrected.XUm)/1000 - float64(x)
	dy := float64(rCorrected.YUm)/1000 - float64(y)
	if errMM := math.Hypot(dx, dy); errMM > 0.5 {
		t.Errorf("Abweichung mit korrekten Offsets = %.3fmm, erwartet < 0.5mm - "+
			"wird p.MicOffsetNs im RIM-Pfad angewandt?", errMM)
	}
}
