package main

import (
	"encoding/json"
	"math"
	"testing"
)

// piezoNotRequired: *bool(false) fuer SimParams.UsePiezo - die meisten Tests
// hier speisen keine piezoNs ein (testen reine Geometrie/Kalibrier-Logik) und
// sollen vom neuen Piezo-Pflicht-Default (SimParams.UsePiezo, siehe dortigen
// Kommentar) unberuehrt bleiben. Eigener Test dafuer:
// TestSolveShot_RequiresPiezoByDefault.
func piezoNotRequired() *bool {
	f := false
	return &f
}

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
	// tatsaechlich am Geraet aktiven Kalibrierwerte (damals noch flache
	// Anordnung und Timing-Offsets, Firmware vor 4.20.0) zum
	// Aufnahmezeitpunkt sind unbekannt (nicht im Telegramm gespeichert,
	// vermutlich per CAL START individuell kalibriert). Nur grobe
	// Plausibilitaet (gleiche Groessenordnung, gueltige Loesung) pruefen -
	// die eigentliche Korrektheit der Portierung verifiziert
	// TestSolveShot_SyntheticGroundTruth unten mit bekannter Soll-Position.
	p := DefaultSimParams()
	p.MicLayout = micLayoutFlat     // Aufnahme mit Firmware vor 4.20.0
	p.UsePiezo = piezoNotRequired() // Testdaten liefern keine piezoNs
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SolveShot(c.airNs, nil, p)
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
		xMM, yMM       float64
		layout         string
		tempC          float64
		distUm         [6]int64
		bulletShiftPct int
		bulletShiftCap float64
	}{
		{name: "center", xMM: 3, yMM: -2, tempC: 20},
		{name: "offcenter", xMM: 40, yMM: -55, tempC: 20},
		{name: "offcenter2", xMM: -20, yMM: 30, tempC: 20},
		{name: "with_dist", xMM: 10, yMM: 10, tempC: 28,
			distUm: [6]int64{0, -420, 300, 110, -30, 15}},
		{name: "flat_layout", xMM: -8, yMM: 15, layout: micLayoutFlat, tempC: 20},
		// Kugeldurchmesser-Korrektur (50%/3mm) darf bei sauberen,
		// widerspruchsfreien Daten (Rest-Fehler ~0 fuer alle Kombinationen)
		// die Position nicht relevant verschieben.
		{name: "with_bullet_shift", xMM: 5, yMM: -5, tempC: 20,
			bulletShiftPct: 50, bulletShiftCap: 3.0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := SimParams{
				MicLayout:        c.layout,
				TempC:            c.tempC,
				MicDistUm:        c.distUm,
				BulletShiftPct:   c.bulletShiftPct,
				BulletShiftCapMm: c.bulletShiftCap,
				UsePiezo:         piezoNotRequired(), // Testdaten liefern keine piezoNs
			}
			// Laufzeiten exakt aus der Geometrie, die das Geraet mit diesen
			// Entfernungskorrekturen verwendet (genCalAirNs)
			airNs := genCalAirNs(c.xMM, c.yMM, c.distUm, p)

			got := SolveShot(airNs, nil, p)
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

// TestMicGeometry_MatchesDevice: die Geometrie (applyMicGeometry-Port) muss
// die Mikrofon-Positionen ergeben, die das Geraet im raw-Telegramm meldet
// (Beispiel aus protokoll-referenz.md 4.5c, Rev 4.20.0), und die Soll-
// Entfernungen der Anordnung "angled" (136,18 / 117,19 mm, Vermessung vom
// 15.11.2026). Telegramme von Geraeten mit den frueheren Soll-Positionen
// werden weiter als "angled" erkannt.
func TestMicGeometry_MatchesDevice(t *testing.T) {
	r := RawEdges{
		MicDistUm: &[6]int64{0, -420, 300, 110, -30, 15},
		MicPosUm: &[6][3]int64{{-110540, -75460, 25150}, {110199, -75227, 25072}, {-110784, 75626, 25205},
			{110629, 75521, 25170}, {-114301, 0, 25753}, {114345, 0, 25763}},
	}
	if got := r.rawMicLayout(); got != micLayoutAngled {
		t.Fatalf("rawMicLayout = %q, want angled", got)
	}
	legacy := RawEdges{
		MicDistUm: &[6]int64{0, -420, 300, 110, -30, 15},
		MicPosUm: &[6][3]int64{{-109000, -75000, 24000}, {108660, -74766, 23925}, {-109243, 75167, 24054},
			{109089, 75061, 24020}, {-112971, 0, 24994}, {113015, 0, 25003}},
	}
	if got := legacy.rawMicLayout(); got != micLayoutAngled {
		t.Errorf("rawMicLayout (fruehere Soll-Positionen) = %q, want angled", got)
	}
	g := micNominal(micLayoutAngled)
	for i, want := range []float64{136.18, 136.18, 136.18, 136.18, 117.19, 117.19} {
		d := math.Sqrt(float64(g.X[i]*g.X[i] + g.Y[i]*g.Y[i] + g.Z[i]*g.Z[i]))
		if math.Abs(d-want) > 0.01 {
			t.Errorf("Mic%d Entfernung %.3f, want %.2f", i, d, want)
		}
	}
	if v := speedOfSound(20); math.Abs(v-343.2) > 0.05 {
		t.Errorf("speedOfSound(20) = %.2f, want ~343.2", v)
	}
}

// TestSimParams_LegacyJSON: gespeicherte Parameter von vor Firmware 4.20.0
// (sound_mps statt temp_set_c) ergeben die gleichwertige Temperatur, alte
// Zonen-Saetze (Timing-Offsets) werden verworfen.
func TestSimParams_LegacyJSON(t *testing.T) {
	var p SimParams
	if err := json.Unmarshal([]byte(`{"sound_mps":343.2,"mic_offset_ns":[0,1,2,3,4,5],
		"zones":{"enabled":true,"hole_radius_mm":15.25,"a":{"sound_mps":340,"mic_offset_ns":[0,1,2,3,4,5]},
		"b":{"mic_dist_um":[0,10,0,0,0,0]}}}`), &p); err != nil {
		t.Fatal(err)
	}
	if math.Abs(p.TempC-20) > 0.15 {
		t.Errorf("TempC = %.2f, want ~20 (aus sound_mps 343.2)", p.TempC)
	}
	if p.Zones == nil || p.Zones.A != nil || p.Zones.B == nil || p.Zones.B.MicDistUm[1] != 10 {
		t.Errorf("Zonen = %+v, want A verworfen, B behalten", p.Zones)
	}
	var q SimParams
	if err := json.Unmarshal([]byte(`{"offset_x_um":5}`), &q); err != nil || q.TempC != simDefaultTempC {
		t.Errorf("ohne Temperatur: TempC = %.1f, want %.1f", q.TempC, simDefaultTempC)
	}
	var r SimParams
	if err := json.Unmarshal([]byte(`{"temp_set_c":0}`), &r); err != nil || r.TempC != 0 {
		t.Errorf("temp_set_c 0 muss erhalten bleiben, got %.1f", r.TempC)
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
	base.UsePiezo = piezoNotRequired() // Testdaten liefern keine piezoNs

	tiny := base
	tiny.ClusterRadiusUm = 1 // 0.001mm - so gut wie kein Kandidat ausser dem besten selbst
	gotTiny := SolveShot(airNs, nil, tiny)

	huge := base
	huge.ClusterRadiusUm = 500000 // Maximalwert laut SET RADIUS (0-500000)
	gotHuge := SolveShot(airNs, nil, huge)

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
	p.UsePiezo = piezoNotRequired() // Testdaten liefern keine piezoNs

	xMM, yMM := 6.0, -9.0
	airNs := genCalAirNs(xMM, yMM, p.MicDistUm, p)

	t.Run("default_all_enabled", func(t *testing.T) {
		got := SolveShot(airNs, nil, p) // MicEnabled nil -> alle aktiv
		if !got.PosValid {
			t.Fatalf("PosValid=false, erwartet true (keine Deaktivierung gesetzt)")
		}
	})

	t.Run("one_disabled_still_solves", func(t *testing.T) {
		pp := p
		pp.MicEnabled = []bool{true, true, true, true, false, true} // 5 aktiv
		got := SolveShot(airNs, nil, pp)
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
		got := SolveShot(airNs, nil, pp)
		if got.PosValid {
			t.Fatalf("PosValid=true, erwartet false (nur 2 Mics aktiv, mind. 3 noetig)")
		}
	})
}

// TestSolveShot_RequiresPiezoByDefault: Regressionstest fuer den realen
// Vorfall, der SimParams.UsePiezo ausgeloest hat - Luftschall benachbarter
// Staende ohne jeden lokalen Einschlag (piezo_ok durchgehend false) wurde
// trotz stark erhoehtem Rest-Fehler als "gueltige" Trefferposition gewertet.
// Ab jetzt muss ein Schuss ohne (oder mit unplausibler) Piezo-Bestaetigung
// als PosValid=false behandelt werden, sobald UsePiezo aktiv ist (Default) -
// und weiterhin normal durchgehen, wenn es explizit deaktiviert wird (fuer
// Aufbauten ohne Piezo-Hardware).
func TestSolveShot_RequiresPiezoByDefault(t *testing.T) {
	p := DefaultSimParams() // UsePiezo unbelegt -> usePiezo()==true (Default)

	xMM, yMM := 6.0, -9.0
	airNs := genCalAirNs(xMM, yMM, p.MicDistUm, p)

	t.Run("no_piezo_ns_invalid", func(t *testing.T) {
		got := SolveShot(airNs, nil, p)
		if got.PosValid {
			t.Fatalf("PosValid=true, erwartet false (kein piezo_ns, UsePiezo aktiv)")
		}
	})

	t.Run("piezo_outside_window_invalid", func(t *testing.T) {
		// Deutlich ausserhalb PiezoMinUs..PiezoMaxUs (100..1400us) - z.B. ein
		// zeitlich unabhaengiges Ereignis, das zufaellig in die Aufnahme fiel.
		piezoNs := int64(5000) * 1000 // 5000 us
		got := SolveShot(airNs, &piezoNs, p)
		if got.PosValid {
			t.Fatalf("PosValid=true, erwartet false (piezo_ns ausserhalb PIEZOMIN/PIEZOMAX)")
		}
	})

	t.Run("piezo_within_window_valid", func(t *testing.T) {
		piezoNs := int64(p.PiezoMinUs+p.PiezoMaxUs) / 2 * 1000 // Mitte des Fensters
		got := SolveShot(airNs, &piezoNs, p)
		if !got.PosValid {
			t.Fatalf("PosValid=false, erwartet true (piezo_ns innerhalb PIEZOMIN/PIEZOMAX)")
		}
	})

	t.Run("use_piezo_disabled_ignores_missing_piezo", func(t *testing.T) {
		pp := p
		pp.UsePiezo = piezoNotRequired()
		got := SolveShot(airNs, nil, pp)
		if !got.PosValid {
			t.Fatalf("PosValid=false, erwartet true (UsePiezo explizit deaktiviert)")
		}
	})
}

// genCalAirNs erzeugt synthetische Rohdaten (wie im Telegramm) fuer eine
// bekannte Trefferposition: die Mikrofone stehen tatsaechlich an den mit
// trueDistUm verschobenen Positionen der Anordnung aus p (Schallgeschwindigkeit
// aus p) - die Kalibrierung muss trueDistUm wiederfinden, eine Rechnung mit
// p.MicDistUm == trueDistUm muss die Position exakt treffen.
func genCalAirNs(xMM, yMM float64, trueDistUm [6]int64, p SimParams) [6][]int64 {
	q := p
	q.MicDistUm = trueDistUm
	g, soundMmPerNs, _ := solveParams(q)
	raw := make([]int64, 6)
	minT := int64(1<<63 - 1)
	for i := 0; i < 6; i++ {
		dx := xMM - float64(g.X[i])
		dy := yMM - float64(g.Y[i])
		dist := math.Sqrt(dx*dx + dy*dy + float64(g.Z[i])*float64(g.Z[i]))
		raw[i] = int64(math.Round(dist / float64(soundMmPerNs)))
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

// Kalibrier-Tests (Port von calCost()/runCalibration(), Rev 4.20.0): aus
// mehreren Kalibrier-Schuessen mit bekannten (absichtlich falschen)
// Mikrofon-Entfernungen muss die Optimierung diese Fehler wiederfinden.
// Mic0 bleibt Referenz (immer 0). Bei GLEICHZEITIGEN Fehlern auf mehreren
// Mikrofonen kann die gierige Koordinatensuche (1:1 aus der Firmware) in ein
// anderes, aehnlich gutes lokales Optimum laufen - der Mehrfach-Test prueft
// deshalb nur, was der Algorithmus garantiert (Kosten nicht schlechter als
// ohne Kalibrierung).

var calTestPositions = [][2]float64{
	{3, -2}, {40, -55}, {-20, 30}, {10, 10}, {-35, -40}, {25, 40},
}

// TestCalibrateMicDist_SingleRecovered: bei genau EINEM verstellten
// Mikrofon ist das Problem eindeutig loesbar.
func TestCalibrateMicDist_SingleRecovered(t *testing.T) {
	p := DefaultSimParams()
	trueDist := [6]int64{0, 1500, 0, 0, 0, 0}
	var airNs [][6][]int64
	for _, pos := range calTestPositions {
		airNs = append(airNs, genCalAirNs(pos[0], pos[1], trueDist, p))
	}

	got, cost := CalibrateMicDist(airNs, nil, p)
	t.Logf("got=%v cost=%.4f want=%v", got, cost, trueDist)

	const tolUm = 30 // Endschrittweite ~4.9um, Nanosekunden-Rundung der Testdaten
	for i := 0; i < 6; i++ {
		if abs64(got[i]-trueDist[i]) > tolUm {
			t.Errorf("Mic%d DIST = %d, want ~%d", i, got[i], trueDist[i])
		}
	}
}

// TestCalibrateMicDist_ImprovesOverNoCalibration: die gefundene Loesung darf
// nicht schlechter sein als gar keine Kalibrierung (DIST 0).
func TestCalibrateMicDist_ImprovesOverNoCalibration(t *testing.T) {
	p := DefaultSimParams()
	p.UsePiezo = piezoNotRequired() // zeroCost-Baseline unten nutzt SolveShot() ohne piezoNs
	trueDist := [6]int64{0, 600, -800, 300, -200, 1000}
	var airNs [][6][]int64
	for _, pos := range calTestPositions {
		airNs = append(airNs, genCalAirNs(pos[0], pos[1], trueDist, p))
	}

	got, cost := CalibrateMicDist(airNs, nil, p)
	t.Logf("got=%v cost=%.4f want(eingespeist)=%v", got, cost, trueDist)
	if got[0] != 0 {
		t.Errorf("Mic0 (Referenz) = %d, want 0", got[0])
	}

	// Kosten ohne Kalibrierung (CorrResUm wie calCostClassicFn)
	var zeroCost float64
	for _, shot := range airNs {
		r := SolveShot(shot, nil, p) // p.MicDistUm ist hier der Nullwert
		if r.PosValid {
			zeroCost += float64(r.CorrResUm) / 1000.0
		} else {
			zeroCost += 1000.0
		}
	}
	const tolMM = 0.01
	if cost > zeroCost+tolMM {
		t.Errorf("Kalibrierte Kosten (%.4f) schlechter als unkalibriert (%.4f)", cost, zeroCost)
	}
}

// TestCalibrateMicDist_TooFewShots: unloesbare Kalibrier-Schuesse fuehren
// zum Strafwert statt zu einer Panik.
func TestCalibrateMicDist_TooFewShots(t *testing.T) {
	p := DefaultSimParams()
	airNs := [][6][]int64{
		{{0}, {100}, nil, nil, nil, nil}, // nur 2 Mics -> in jedem Kalibrier-Schuss unloesbar
	}
	dist, cost := CalibrateMicDist(airNs, nil, p)
	t.Logf("dist=%v cost=%.3f (Strafwert-Pfad erwartet, kein Crash)", dist, cost)
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
	p.UsePiezo = piezoNotRequired() // Testdaten liefern keine piezoNs
	airNs := genCalAirNs(6, -4, [6]int64{}, p) // DIST=0 -> bereits "kalibrierte" Zeiten

	res, candidates, bulletShift := SolveShotDetail(airNs, nil, p)
	if !res.PosValid {
		t.Fatalf("PosValid=false, erwartet true")
	}
	// Anordnung "angled": alle C(6,3)=20 Kombinationen loesbar. Bei der
	// flachen Anordnung (bis Firmware 4.19) lagen die drei Mics einer Seite
	// auf einer Linie - die beiden kollinearen Kombinationen fielen weg (18).
	if len(candidates) != 20 {
		t.Errorf("len(candidates) = %d, want 20", len(candidates))
	}
	pf := p
	pf.MicLayout = micLayoutFlat
	if _, cf, _ := SolveShotDetail(genCalAirNs(6, -4, [6]int64{}, pf), nil, pf); len(cf) != 18 {
		t.Errorf("flach: len(candidates) = %d, want 18", len(cf))
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

	plain := SolveShot(airNs, nil, p)
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

// TestClusterSum9: nur Schnittpunkte im Cluster-Radius zaehlen, maximal die
// 9 naechsten, fehlende bis 9 mit je 5mm.
func TestClusterSum9(t *testing.T) {
	mk := func(dists ...float32) []airCandidate {
		var c []airCandidate
		for _, d := range dists {
			c = append(c, airCandidate{X: 10 + d, Y: -3})
		}
		return c
	}
	cases := []struct {
		name  string
		cands []airCandidate
		want  float32
		wantN int
	}{
		{"keine", nil, 45, 0},
		{"3 im Radius, 1 ausserhalb", mk(0.5, 1, 1.5, 4.5), 3 + 6*5, 3},
		{"12 im Radius, nur 9 naechste", mk(1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2), 9, 9},
	}
	for _, tc := range cases {
		got, n := clusterSum9(tc.cands, 10, -3, 4, false)
		if math.Abs(float64(got-tc.want)) > 1e-4 || n != tc.wantN {
			t.Errorf("%s: Σ9=%.3f n=%d, want %.3f n=%d", tc.name, got, n, tc.want, tc.wantN)
		}
	}
}

// TestClusterSum9_Corrected: corrected=true misst die korrigierten
// Schnittpunkte (CorrX/CorrY) statt der rohen.
func TestClusterSum9_Corrected(t *testing.T) {
	c := []airCandidate{{X: 3, Y: 0, CorrX: 1, CorrY: 0}}
	if got, n := clusterSum9(c, 0, 0, 4, false); math.Abs(float64(got-(3+8*5))) > 1e-4 || n != 1 {
		t.Errorf("roh: Σ9=%.3f n=%d", got, n)
	}
	if got, n := clusterSum9(c, 0, 0, 4, true); math.Abs(float64(got-(1+8*5))) > 1e-4 || n != 1 {
		t.Errorf("korrigiert: Σ9=%.3f n=%d", got, n)
	}
}
