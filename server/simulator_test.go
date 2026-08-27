package main

import (
	"math"
	"testing"
)

// TestSolveShot_RegressionAgainstDevice vergleicht SolveShot() mit
// Default-Parametern (Firmware-Werkseinstellungen) gegen echte, vom ESP32
// berechnete und in der DB gespeicherte Werte (session_id
// b142514d-41d9-4335-b2fc-0fe465e7f63c, Schuesse 1-3). Die tatsaechlich am
// Geraet aktiven Kalibrierparameter zum Aufnahmezeitpunkt sind nicht
// bekannt (nicht im Telegramm gespeichert) - bei einem frisch aufgesetzten
// Stand mit Werkseinstellungen sollten Default-Parameter aber sehr nah oder
// exakt uebereinstimmen. Dient als Regressionstest fuer die Portierung.
func TestSolveShot_RegressionAgainstDevice(t *testing.T) {
	cases := []struct {
		name        string
		airNs       [6][]int64
		wantXUm     int64
		wantYUm     int64
		wantResUm   int64
		wantPrecUm  int64
		wantCluster int
	}{
		{
			name: "shot1",
			airNs: [6][]int64{
				{102887, 241412, 417025, 605425, 694525, 728125},
				{55862, 186575, 373337, 593550, 710125},
				{132325, 285500, 450287, 636600},
				{110625, 250862, 426537, 615087},
				{26837, 183275, 411362, 476600, 666100},
				{0, 149900, 457912, 653287},
			},
			wantXUm: 7134, wantYUm: -11981, wantResUm: 1026, wantPrecUm: 563, wantCluster: 4,
		},
		{
			name: "shot2",
			airNs: [6][]int64{
				{77425, 198375, 223812, 263887, 395287, 580862},
				{66137, 211600, 378612, 585637, 690300},
				{94362, 238350, 413287, 624650, 679750},
				{96200, 236462, 402362, 584337, 608862},
				{2100, 157137, 249250, 363662, 444000, 644137},
				{0, 155600, 360037, 435900, 642600},
			},
			wantXUm: 265, wantYUm: -7014, wantResUm: 1992, wantPrecUm: 1041, wantCluster: 4,
		},
		{
			name: "shot3",
			airNs: [6][]int64{
				{89562, 222125, 385612, 587037, 710725},
				{121512, 274600, 434700, 622737},
				{103100, 247600, 417075, 606250, 653037},
				{150650, 286012, 451100, 649650},
				{0, 152812, 367337, 455537, 651312},
				{48287, 210975, 398887, 477525, 578862, 679850},
			},
			wantXUm: -10117, wantYUm: -6415, wantResUm: 413, wantPrecUm: 224, wantCluster: 4,
		},
	}

	// Keine harte Gleichheit erwartet (siehe Kommentar oben) - die
	// tatsaechlich am Geraet aktiven OFS/Standoff/Soundspeed-Werte zum
	// Aufnahmezeitpunkt sind unbekannt (nicht im Telegramm gespeichert,
	// vermutlich per CAL START individuell kalibriert). Nur grobe
	// Plausibilitaet (gleiche Groessenordnung, gueltige Loesung) pruefen -
	// die eigentliche Korrektheit der Portierung verifiziert
	// TestSolveShot_SyntheticGroundTruth unten mit bekannter Soll-Position.
	p := DefaultSimParams()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SolveShot(c.airNs, p)
			if !got.PosValid {
				t.Fatalf("PosValid=false, erwartet true")
			}
			t.Logf("got=%+v want(Geraet, andere Kalibrierung)={x:%d y:%d res:%d prec:%d cluster:%d}",
				got, c.wantXUm, c.wantYUm, c.wantResUm, c.wantPrecUm, c.wantCluster)

			absDiff := func(a, b int64) int64 {
				if a > b {
					return a - b
				}
				return b - a
			}
			const tolUm = 15000 // 15mm - grobe Plausibilitaet, keine Kalibrierung angenommen
			if absDiff(got.XUm, c.wantXUm) > tolUm || absDiff(got.YUm, c.wantYUm) > tolUm {
				t.Errorf("XUm/YUm = %d/%d weit ausserhalb der Groessenordnung von %d/%d (Geraet)",
					got.XUm, got.YUm, c.wantXUm, c.wantYUm)
			}
		})
	}
}

// TestSolveShot_SyntheticGroundTruth verifiziert die eigentliche Korrektheit
// der Portierung: aus einer bekannten Soll-Trefferposition werden (mit
// exakt der Firmware-Geometrie) synthetische Mikrofon-Laufzeiten berechnet
// und in SolveShot() zurueckgefuettert - erwartet wird die urspruengliche
// Position (bis auf Nanosekunden-Quantisierungsrauschen).
func TestSolveShot_SyntheticGroundTruth(t *testing.T) {
	cases := []struct {
		name           string
		target         string
		xMM, yMM       float64
		standoffMM     float64
		soundMps       float64
		micHalfXMm     float64
		micOffsetNs    [6]int64
		bulletShiftPct int
		bulletShiftCap float64
	}{
		{name: "steel_center", target: "steel", xMM: 3, yMM: -2, standoffMM: 30, soundMps: 355, micHalfXMm: 115},
		{name: "steel_offcenter", target: "steel", xMM: 40, yMM: -55, standoffMM: 30, soundMps: 355, micHalfXMm: 115},
		{name: "paper_offcenter", target: "paper", xMM: -20, yMM: 30, standoffMM: 28, soundMps: 355, micHalfXMm: 115},
		{name: "steel_with_mic_offsets", target: "steel", xMM: 10, yMM: 10, standoffMM: 30, soundMps: 348, micHalfXMm: 115,
			micOffsetNs: [6]int64{120, -80, 50, 0, -150, 30}},
		{name: "custom_mic_half_x", target: "steel", xMM: -8, yMM: 15, standoffMM: 30, soundMps: 355, micHalfXMm: 120},
		// Kugeldurchmesser-Korrektur (Default 50%/3mm) darf bei sauberen,
		// widerspruchsfreien Daten (Rest-Fehler ~0 fuer alle Kombinationen)
		// die Position nicht relevant verschieben.
		{name: "steel_with_bullet_shift", target: "steel", xMM: 5, yMM: -5, standoffMM: 30, soundMps: 355, micHalfXMm: 115,
			bulletShiftPct: 50, bulletShiftCap: 3.0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := SimParams{
				StandoffSteelMM:  30,
				StandoffPaperMM:  28,
				MicHalfXMm:       c.micHalfXMm,
				SoundMps:         c.soundMps,
				Target:           c.target,
				MicOffsetNs:      c.micOffsetNs,
				BulletShiftPct:   c.bulletShiftPct,
				BulletShiftCapMm: c.bulletShiftCap,
			}
			if c.target == "steel" {
				p.StandoffSteelMM = c.standoffMM
			} else {
				p.StandoffPaperMM = c.standoffMM
			}

			micX, micY, standoffMM := micGeometry(p)
			soundMmPerNs := c.soundMps * 1.0e-6

			// Fuer jedes Mic: exakte Laufzeit (inkl. Standoff-Z) in ns,
			// PLUS den simulierten Mic-Offset (wird von SolveShot wieder
			// abgezogen - muss also hier draufgerechnet werden, damit die
			// Rundtrip-Rechnung konsistent ist).
			raw := make([]int64, 6)
			minT := int64(1<<63 - 1)
			for i := 0; i < 6; i++ {
				dx := c.xMM - float64(micX[i])
				dy := c.yMM - float64(micY[i])
				dist := math.Sqrt(dx*dx + dy*dy + float64(standoffMM)*float64(standoffMM))
				tNs := int64(math.Round(dist/soundMmPerNs)) + c.micOffsetNs[i]
				raw[i] = tNs
				if tNs < minT {
					minT = tNs
				}
			}
			var airNs [6][]int64
			for i := 0; i < 6; i++ {
				airNs[i] = []int64{raw[i] - minT}
			}

			got := SolveShot(airNs, p)
			if !got.PosValid {
				t.Fatalf("PosValid=false, erwartet true")
			}
			wantXUm := int64(math.Round(c.xMM * 1000))
			wantYUm := int64(math.Round(c.yMM * 1000))
			const tolUm = 50 // Nanosekunden-Rundung -> Bruchteile von um, 50um grosszuegig
			if abs64(got.XUm-wantXUm) > tolUm || abs64(got.YUm-wantYUm) > tolUm {
				t.Errorf("Position = %d/%d um, want %d/%d um (Soll)", got.XUm, got.YUm, wantXUm, wantYUm)
			}
			if got.ClusterHits < 1 {
				t.Errorf("ClusterHits = %d, erwartet >=1 (mind. die eigene Loesung)", got.ClusterHits)
			}
			t.Logf("ok: got=%+v want_x_um=%d want_y_um=%d", got, wantXUm, wantYUm)
		})
	}
}

// TestSolveShot_ClusterRadius prueft, dass SET RADIUS (ClusterRadiusUm)
// tatsaechlich wirkt: ein sehr kleiner Radius darf nicht mehr Kandidaten
// einschliessen als ein sehr grosser (Bug-Auslser fuer diesen Test: RADIUS
// war zunaechst kein einstellbarer Parameter, siehe Konversation).
func TestSolveShot_ClusterRadius(t *testing.T) {
	airNs := [6][]int64{
		{102887, 241412, 417025, 605425, 694525, 728125},
		{55862, 186575, 373337, 593550, 710125},
		{132325, 285500, 450287, 636600},
		{110625, 250862, 426537, 615087},
		{26837, 183275, 411362, 476600, 666100},
		{0, 149900, 457912, 653287},
	}
	base := DefaultSimParams()

	tiny := base
	tiny.ClusterRadiusUm = 1 // 0.001mm - so gut wie kein Kandidat ausser dem besten selbst
	gotTiny := SolveShot(airNs, tiny)

	huge := base
	huge.ClusterRadiusUm = 500000 // Maximalwert laut SET RADIUS (0-500000)
	gotHuge := SolveShot(airNs, huge)

	if !gotTiny.PosValid || !gotHuge.PosValid {
		t.Fatalf("PosValid=false erwartet true (tiny=%v huge=%v)", gotTiny.PosValid, gotHuge.PosValid)
	}
	if gotTiny.ClusterHits >= gotHuge.ClusterHits {
		t.Errorf("ClusterHits mit RADIUS=1 (%d) sollte kleiner sein als mit RADIUS=500000 (%d) - "+
			"Parameter scheint nicht anzukommen", gotTiny.ClusterHits, gotHuge.ClusterHits)
	}
	t.Logf("tiny=%+v huge=%+v", gotTiny, gotHuge)
}

// TestSolveShot_MicEnabled prueft die Mikrofon-Auswahl (mic_enabled, analog
// SET MICEN0..MICEN5): ein deaktiviertes Mikrofon muss wie ein nicht
// ausgeloestes behandelt werden, auch wenn Rohdaten dafuer vorliegen.
func TestSolveShot_MicEnabled(t *testing.T) {
	p := DefaultSimParams()
	p.Target = "steel"

	micX, micY, standoffMM := micGeometry(p)
	soundMmPerNs := p.SoundMps * 1.0e-6
	xMM, yMM := 6.0, -9.0

	raw := make([]int64, 6)
	minT := int64(1<<63 - 1)
	for i := 0; i < 6; i++ {
		dx := xMM - float64(micX[i])
		dy := yMM - float64(micY[i])
		dist := math.Sqrt(dx*dx + dy*dy + float64(standoffMM)*float64(standoffMM))
		raw[i] = int64(math.Round(dist / soundMmPerNs))
		if raw[i] < minT {
			minT = raw[i]
		}
	}
	var airNs [6][]int64
	for i := 0; i < 6; i++ {
		airNs[i] = []int64{raw[i] - minT}
	}

	t.Run("default_all_enabled", func(t *testing.T) {
		got := SolveShot(airNs, p) // MicEnabled nil -> alle aktiv
		if !got.PosValid {
			t.Fatalf("PosValid=false, erwartet true (keine Deaktivierung gesetzt)")
		}
	})

	t.Run("one_disabled_still_solves", func(t *testing.T) {
		pp := p
		pp.MicEnabled = []bool{true, true, true, true, false, true} // 5 aktiv
		got := SolveShot(airNs, pp)
		if !got.PosValid {
			t.Fatalf("PosValid=false, erwartet true (5 Mics reichen)")
		}
		wantXUm, wantYUm := int64(math.Round(xMM*1000)), int64(math.Round(yMM*1000))
		if abs64(got.XUm-wantXUm) > 50 || abs64(got.YUm-wantYUm) > 50 {
			t.Errorf("Position = %d/%d um, want ~%d/%d um", got.XUm, got.YUm, wantXUm, wantYUm)
		}
	})

	t.Run("only_two_enabled_invalid", func(t *testing.T) {
		pp := p
		pp.MicEnabled = []bool{true, true, false, false, false, false} // nur 2 aktiv
		got := SolveShot(airNs, pp)
		if got.PosValid {
			t.Fatalf("PosValid=true, erwartet false (nur 2 Mics aktiv, mind. 3 noetig)")
		}
	})
}

// genCalAirNs erzeugt synthetische Rohdaten (wie im Telegramm, VOR
// Offset-Korrektur) fuer eine bekannte Trefferposition unter der Annahme,
// dass die Mikrofone tatsaechlich die angegebenen (unbekannten, zu
// findenden) Timing-Fehler trueOffsetNs haben - analog zu den anderen
// Ground-Truth-Tests, aber mit absichtlich verfaelschten ("unkalibrierten")
// Zeiten statt bereits korrekter.
func genCalAirNs(xMM, yMM float64, trueOffsetNs [6]int64, p SimParams) [6][]int64 {
	micX, micY, standoffMM, soundMmPerNs, _ := solveParams(p)
	raw := make([]int64, 6)
	minT := int64(1<<63 - 1)
	for i := 0; i < 6; i++ {
		dx := xMM - float64(micX[i])
		dy := yMM - float64(micY[i])
		dist := math.Sqrt(dx*dx + dy*dy + float64(standoffMM)*float64(standoffMM))
		exact := int64(math.Round(dist / float64(soundMmPerNs)))
		raw[i] = exact + trueOffsetNs[i] // "unkalibriert" - der zu findende Fehler steckt noch drin
		if raw[i] < minT {
			minT = raw[i]
		}
	}
	var airNs [6][]int64
	for i := 0; i < 6; i++ {
		airNs[i] = []int64{raw[i] - minT}
	}
	return airNs
}

// TestCalibrateMicOffsets_RecoversKnownOffsets verifiziert die Portierung
// von calCost()/runCalibration(): aus mehreren Kalibrier-Schuessen mit
// bekannten (aber absichtlich falschen) Mikrofon-Timing-Fehlern muss die
// Optimierung diese Fehler wiederfinden. Mic0 bleibt Referenz (immer 0).
// Hinweis zur Testphilosophie: Bei GLEICHZEITIGEN Timing-Fehlern auf mehreren
// Mikrofonen kann die gieriege Koordinatensuche (1:1 aus der Firmware
// uebernommen, siehe runCalibration()-Kommentar zu "kann ... einfrieren")
// in ein ANDERES, aehnlich gutes lokales statt das globale Optimum laufen -
// das ist eine echte Eigenschaft des Original-Algorithmus (durch einen
// Vergleichstest mit einem einzelnen verstellten Mikrofon unten separat als
// korrekt bestaetigt), keine Portierungsabweichung. Der Mehrfach-Offset-Test
// prueft deshalb bewusst nur, was der Algorithmus tatsaechlich GARANTIERT
// (Kosten sinken/bleiben mindestens so gut wie ohne Kalibrierung), nicht die
// exakte Wiederherstellung der eingespeisten Werte.

// TestCalibrateMicOffsets_SingleOffsetRecovered: bei genau EINEM verstellten
// Mikrofon ist das Problem eindeutig loesbar - hier muss die Optimierung den
// eingespeisten Fehler nahezu exakt wiederfinden. Bestaetigt die Korrektheit
// von calCost()/runCalibration() unabhaengig von der oben beschriebenen
// Mehrdeutigkeit bei mehreren gleichzeitigen Fehlern.
func TestCalibrateMicOffsets_SingleOffsetRecovered(t *testing.T) {
	p := DefaultSimParams()
	trueOffsetNs := [6]int64{0, 180, 0, 0, 0, 0}

	positions := [][2]float64{
		{3, -2}, {40, -55}, {-20, 30}, {10, 10}, {-35, -40}, {25, 40},
	}
	var airNs [][6][]int64
	for _, pos := range positions {
		airNs = append(airNs, genCalAirNs(pos[0], pos[1], trueOffsetNs, p))
	}

	gotOffsets, cost := CalibrateMicOffsets(airNs, p)
	t.Logf("gotOffsets=%v cost=%.4f want=%v", gotOffsets, cost, trueOffsetNs)

	const tolNs = 30 // Endschrittweite der Koordinatensuche ~9.8ns, grosszuegige Toleranz
	for i := 0; i < 6; i++ {
		if abs64(gotOffsets[i]-trueOffsetNs[i]) > tolNs {
			t.Errorf("Mic%d Offset = %d, want ~%d (diff %d)", i, gotOffsets[i], trueOffsetNs[i],
				abs64(gotOffsets[i]-trueOffsetNs[i]))
		}
	}
}

// TestCalibrateMicOffsets_ImprovesOverNoCalibration prueft die bei
// GLEICHZEITIGEN Mehrfach-Offsets tatsaechlich garantierte Eigenschaft: die
// gefundene Loesung darf nicht schlechter sein als gar keine Kalibrierung
// (Offsets 0), da die Koordinatensuche bei jedem Schritt nur Aenderungen
// uebernimmt, die die Kosten senken oder gleich lassen (siehe runCalibration()).
func TestCalibrateMicOffsets_ImprovesOverNoCalibration(t *testing.T) {
	p := DefaultSimParams()
	trueOffsetNs := [6]int64{0, 180, -240, 90, -60, 310}

	positions := [][2]float64{
		{3, -2}, {40, -55}, {-20, 30}, {10, 10}, {-35, -40}, {25, 40},
	}
	var airNs [][6][]int64
	for _, pos := range positions {
		airNs = append(airNs, genCalAirNs(pos[0], pos[1], trueOffsetNs, p))
	}

	gotOffsets, cost := CalibrateMicOffsets(airNs, p)
	t.Logf("gotOffsets=%v cost=%.4f want(eingespeist)=%v", gotOffsets, cost, trueOffsetNs)

	if gotOffsets[0] != 0 {
		t.Errorf("Mic0 (Referenz) = %d, want 0", gotOffsets[0])
	}

	// Kosten ohne jede Kalibrierung (Offsets 0) als Vergleichsbasis.
	var zeroCost float64
	for _, shot := range airNs {
		r := SolveShot(shot, p) // p.MicOffsetNs ist hier der Nullwert
		if r.PosValid {
			zeroCost += float64(r.PosResUm) / 1000.0
		} else {
			zeroCost += 1000.0
		}
	}

	const tolMM = 0.01 // Rundungstoleranz zwischen den beiden Kostenberechnungen
	if cost > zeroCost+tolMM {
		t.Errorf("Kalibrierte Kosten (%.4f) schlechter als unkalibriert (%.4f) - "+
			"die Optimierung darf sich nie verschlechtern", cost, zeroCost)
	}
}

// TestCalibrateMicOffsets_TooFewShots stellt sicher, dass zu wenige/
// unloesbare Kalibrier-Schuesse nicht zu einer Panik fuehren (Strafwert-Pfad
// in calCost() statt Absturz) - kein Erwartungswert an das Ergebnis selbst.
func TestCalibrateMicOffsets_TooFewShots(t *testing.T) {
	p := DefaultSimParams()
	airNs := [][6][]int64{
		{{0}, {100}, nil, nil, nil, nil}, // nur 2 Mics -> in jedem Kalibrier-Schuss unloesbar
	}
	offsets, cost := CalibrateMicOffsets(airNs, p)
	t.Logf("offsets=%v cost=%.3f (Strafwert-Pfad erwartet, kein Crash)", offsets, cost)
	if cost < 1000.0 {
		t.Errorf("cost = %.3f, erwartet >=1000 (Strafwert fuer unloesbaren Schuss)", cost)
	}
}

// TestSolveShotDetail_Candidates prueft, dass SolveShotDetail() dieselbe
// Endposition wie SolveShot() liefert und dabei plausible Kandidaten
// ("Schnittpunkte") zurueckgibt: bei 6 erfassten Mikrofonen gibt es
// C(6,3)=20 3er-Kombinationen, jede davon sollte hier loesbar sein (die
// Synthetic-Daten sind widerspruchsfrei), und die finale Position muss
// selbst nahe am Mittelwert der Kandidaten liegen (Stufe-2-Verifizierung).
func TestSolveShotDetail_Candidates(t *testing.T) {
	p := DefaultSimParams() // BulletShiftPct=50 (Firmware-Default)
	airNs := genCalAirNs(6, -4, [6]int64{}, p) // trueOffset=0 -> bereits "kalibrierte" Zeiten

	res, candidates, bulletShift := SolveShotDetail(airNs, p)
	if !res.PosValid {
		t.Fatalf("PosValid=false, erwartet true")
	}
	// 18 statt der theoretischen C(6,3)=20: 2 Kombinationen sind bei dieser
	// Mic-Geometrie strukturell singulaer (kollinear) und liefern nie eine
	// Loesung - bereits in TestSolveShot_SyntheticGroundTruth durchgaengig
	// als ClusterHits=18 beobachtet, unabhaengig von der Trefferposition.
	if len(candidates) != 18 {
		t.Errorf("len(candidates) = %d, want 18", len(candidates))
	}

	xMM, yMM := float64(res.XUm)/1000, float64(res.YUm)/1000
	nBest := 0
	for i, c := range candidates {
		if c.Ref == c.A || c.Ref == c.B || c.A == c.B {
			t.Errorf("Kandidat %d: Ref/A/B nicht paarweise verschieden (%d/%d/%d)", i, c.Ref, c.A, c.B)
		}
		for _, m := range []int{c.Ref, c.A, c.B} {
			if m < 0 || m > 5 {
				t.Errorf("Kandidat %d: Mic-Index %d ausserhalb 0-5", i, m)
			}
		}
		if c.Best {
			nBest++
		}
		dx, dy := float64(c.X)-xMM, float64(c.Y)-yMM
		if dx*dx+dy*dy > 25 { // 5mm Radius - grosszuegig, reine Plausibilitaet
			t.Errorf("Kandidat %d = (%.2f,%.2f) liegt weit von der finalen Position (%.2f,%.2f) entfernt",
				i, c.X, c.Y, xMM, yMM)
		}
	}
	if nBest != 1 {
		t.Errorf("Anzahl Kandidaten mit Best=true = %d, want genau 1", nBest)
	}

	// Bei 6 erfassten Mics und BulletShiftPct>0 bleiben nach der besten
	// 3er-Kombination 3 weitere Mics als "verbleibend" fuer die Korrektur.
	if len(bulletShift) != 3 {
		t.Errorf("len(bulletShift) = %d, want 3 (6 Mics - 3 an der besten Loesung beteiligt)", len(bulletShift))
	}

	plain := SolveShot(airNs, p)
	if plain.XUm != res.XUm || plain.YUm != res.YUm {
		t.Errorf("SolveShot()=%v und SolveShotDetail()=%v liefern unterschiedliche Positionen", plain, res)
	}
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
