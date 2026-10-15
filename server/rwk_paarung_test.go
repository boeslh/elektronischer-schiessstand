package main

import "testing"

func paarE(team string, side, pair int, rings int) RundenwettkampfEntry {
	return RundenwettkampfEntry{TeamID: team, TeamName: team, TeamSide: side, PairNo: pair,
		Role: "S", ShotCount: 40, TotalRings: rings, MatchShotCount: 40}
}

func TestComputePaarung(t *testing.T) {
	// 5 Paarungen: Heim gewinnt 3:2
	var es []RundenwettkampfEntry
	h := []int{380, 370, 360, 350, 340}
	g := []int{379, 371, 359, 351, 339}
	for i := range h {
		es = append(es, paarE("H", 0, i+1, h[i]), paarE("G", 1, i+1, g[i]))
	}
	r := computePaarung(es, false)
	if r.HeimPoints != 3 || r.GastPoints != 2 || r.Winner != 0 || r.TotalPoint != -1 {
		t.Fatalf("5er: %+v", r)
	}
	// 4 Paarungen 2:2 -> Mannschaftsergebnis entscheidet (Gast 1440+... )
	es = nil
	h = []int{380, 370, 360, 340}
	g = []int{379, 371, 359, 345}
	for i := range h {
		es = append(es, paarE("H", 0, i+1, h[i]), paarE("G", 1, i+1, g[i]))
	}
	r = computePaarung(es, false)
	if r.HeimPoints != 2 || r.GastPoints != 3 || r.TotalPoint != 1 || r.Winner != 1 {
		t.Fatalf("4er: %+v", r)
	}
	// Gleichstand in Paarung -> Stechen, kein Punkt
	es = []RundenwettkampfEntry{paarE("H", 0, 1, 370), paarE("G", 1, 1, 370)}
	r = computePaarung(es, false)
	if !r.Pairs[0].Stechen || r.HeimPoints+r.GastPoints != 0 {
		t.Fatalf("Stechen: %+v", r)
	}
	// Hochrechnung: 20 von 40 Schuss, 190 -> 380 schlaegt 375
	a := paarE("H", 0, 1, 190)
	a.ShotCount = 20
	r = computePaarung([]RundenwettkampfEntry{a, paarE("G", 1, 1, 375)}, true)
	if r.Pairs[0].Winner != 0 {
		t.Fatalf("HR: %+v", r.Pairs[0])
	}
}

func TestDecideStechen(t *testing.T) {
	sh := func(no, ring int, dec float64) StechenShot { return StechenShot{No: no, Shot: true, Ring: ring, Decimal: dec} }
	// Ganzring: 10 (10.1) gegen 10 (10.9) ist im 1. Durchgang gleich, 2. Durchgang entscheidet
	w, d := decideStechen([]StechenShot{sh(1, 10, 10.1), sh(2, 9, 9.5)}, []StechenShot{sh(1, 10, 10.9), sh(2, 10, 10.0)}, false)
	if w != 1 || d != 2 {
		t.Fatalf("ring: %d %d", w, d)
	}
	// ab dem 4. Durchgang Zehntel
	h := []StechenShot{sh(1, 10, 10.1), sh(2, 10, 10.2), sh(3, 9, 9.9), sh(4, 10, 10.5)}
	g := []StechenShot{sh(1, 10, 10.4), sh(2, 10, 10.0), sh(3, 9, 9.1), sh(4, 10, 10.3)}
	if w, d = decideStechen(h, g, false); w != 0 || d != 4 {
		t.Fatalf("zehntel ab 4: %d %d", w, d)
	}
	// Zehntelwertung: schon der 1. Durchgang in Zehntel
	if w, _ = decideStechen(h[:1], g[:1], true); w != 1 {
		t.Fatalf("zehntel: %d", w)
	}
	// laufender Durchgang ohne Gegner-Schuss -> offen
	if w, d = decideStechen([]StechenShot{sh(1, 9, 9.0)}, []StechenShot{{No: 1}}, false); w != -1 || d != 0 {
		t.Fatalf("offen: %d %d", w, d)
	}
	// computePaarung: Gleichstand durch Stechen entschieden
	a, b := paarE("H", 0, 1, 370), paarE("G", 1, 1, 370)
	a.Stechen = []StechenShot{sh(1, 10, 10.2)}
	b.Stechen = []StechenShot{sh(1, 9, 9.8)}
	r := computePaarung([]RundenwettkampfEntry{a, b}, false)
	if r.Pairs[0].Winner != 0 || !r.Pairs[0].ByStechen || r.HeimPoints != 1 {
		t.Fatalf("paarung: %+v", r.Pairs[0])
	}
}
