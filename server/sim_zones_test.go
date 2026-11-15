package main

import (
	"math"
	"testing"
)

func TestZoneWeights(t *testing.T) {
	z := &ZoneParams{Enabled: true, HoleRadiusMM: 15.25}
	cases := []struct {
		x, y float64
		want string
	}{{0, 0, "A"}, {10, 5, "A"}, {0, 30, "C"}, {30, -20, "C"}, {60, 0, "B"}, {-70, 30, "B"}}
	for _, c := range cases {
		if got := z.ZoneOf(c.x, c.y); got != c.want {
			t.Errorf("ZoneOf(%v,%v) = %s, erwartet %s", c.x, c.y, got, c.want)
		}
		w := z.zoneWeights(c.x, c.y)
		if s := w[0] + w[1] + w[2]; math.Abs(s-1) > 1e-12 {
			t.Errorf("Gewichte (%v,%v) Summe %v", c.x, c.y, s)
		}
	}
	// Ueberblendung genau auf der Lochgrenze: halb A, halb C
	if w := z.zoneWeights(15.25, 0); math.Abs(w[0]-0.5) > 1e-9 || math.Abs(w[2]-0.5) > 1e-9 {
		t.Errorf("Grenze Loch: %v", w)
	}
	// ausserhalb der Ueberblendung rein
	if w := z.zoneWeights(15.25+2.3, 0); w[0] != 0 {
		t.Errorf("ausserhalb der Ueberblendung noch A-Anteil: %v", w)
	}
}

// synthShot: Laufzeiten fuer einen Treffer bei x,y (mm) mit Parametern p.
func synthShot(p SimParams, x, y float64) SimShotRaw {
	return SimShotRaw{HasRaw: true, AirNs: genCalAirNs(x, y, p.MicDistUm, p)}
}

// Ohne Zonen-Saetze (alle nil) muss die zweistufige Rechnung exakt die
// einstufige ergeben; ein Satz mit anderer Nachkorrektur wirkt nur in
// seiner Zone.
func TestSolveZoned(t *testing.T) {
	p := DefaultSimParams()
	off := false
	p.UsePiezo = &off
	p.BulletShiftPct = 0
	center, outer := synthShot(p, 3, -2), synthShot(p, 30, 25)
	plainC, plainO := SolveSimShot(center, p), SolveSimShot(outer, p)
	if !plainC.PosValid || !plainO.PosValid {
		t.Fatalf("synthetische Schuesse ungueltig: %+v %+v", plainC, plainO)
	}
	p.Zones = &ZoneParams{Enabled: true, HoleRadiusMM: 15.25}
	if z := SolveSimShot(center, p); z.XUm != plainC.XUm || z.YUm != plainC.YUm || z.Zone != "A" {
		t.Fatalf("ohne Saetze: %+v gegen %+v", z, plainC)
	}
	p.Zones.A = &ZoneSet{MicDistUm: p.MicDistUm, OffsetXUm: 500, OffsetYUm: -300}
	zc, zo := SolveSimShot(center, p), SolveSimShot(outer, p)
	if zc.XUm != plainC.XUm+500 || zc.YUm != plainC.YUm-300 {
		t.Errorf("Zone A: %d/%d, erwartet %d/%d", zc.XUm, zc.YUm, plainC.XUm+500, plainC.YUm-300)
	}
	if zo.XUm != plainO.XUm || zo.YUm != plainO.YUm || zo.Zone != "C" {
		t.Errorf("Zone C darf sich nicht aendern: %+v gegen %+v", zo, plainO)
	}
}

// One-Click ohne Zonen: nur Basissatz, Zonen ausgeschaltet (Grenzen bleiben).
func TestZoneCalibrate_NoZones(t *testing.T) {
	truth := DefaultSimParams()
	truth.UsePiezo = piezoNotRequired()
	truth.MicDistUm = [6]int64{0, 900, -600, 400, -1200, 300}
	truth.OffsetXUm, truth.OffsetYUm = 2500, -1800
	var in []zoneCalInput
	for _, s := range photoCalSyntheticShots(truth) {
		in = append(in, zoneCalInput{Raw: SimShotRaw{ShotNo: s.ShotNo, AirNs: s.AirNs, HasRaw: true}, TruthX: s.TruthX, TruthY: s.TruthY})
	}
	start := DefaultSimParams()
	start.UsePiezo = piezoNotRequired()
	zones := ZoneParams{HoleRadiusMM: 7.625, BandHalfMM: 45}
	rep, err := zoneCalibrate(in, start, PhotoCalOptions{MicDist: true, Offset: true}, zones, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Zones) != 0 {
		t.Errorf("Zonen-Saetze %d, erwartet keine", len(rep.Zones))
	}
	z := rep.Params.Zones
	if z == nil || z.Enabled || z.A != nil || z.B != nil || z.C != nil || z.HoleRadiusMM != 7.625 {
		t.Errorf("Zonen = %+v, erwartet ausgeschaltet mit Grenzen", z)
	}
	if rep.After.RMSMM > 0.15 || rep.After.RMSMM >= rep.Before.RMSMM {
		t.Errorf("RMS %.3f -> %.3f mm", rep.Before.RMSMM, rep.After.RMSMM)
	}
}
