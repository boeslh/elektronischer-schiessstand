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
	micX, micY, so := micGeometry(p)
	var air [6][]int64
	for i := 0; i < 6; i++ {
		d := math.Sqrt(math.Pow(x-float64(micX[i]), 2) + math.Pow(y-float64(micY[i]), 2) + float64(so*so))
		air[i] = []int64{int64(math.Round(d/(p.SoundMps*1e-6))) + 50000}
	}
	return SimShotRaw{HasRaw: true, AirNs: air}
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
	p.Zones.A = &ZoneSet{SoundMps: p.SoundMps, MicOffsetNs: p.MicOffsetNs, OffsetXUm: 500, OffsetYUm: -300}
	zc, zo := SolveSimShot(center, p), SolveSimShot(outer, p)
	if zc.XUm != plainC.XUm+500 || zc.YUm != plainC.YUm-300 {
		t.Errorf("Zone A: %d/%d, erwartet %d/%d", zc.XUm, zc.YUm, plainC.XUm+500, plainC.YUm-300)
	}
	if zo.XUm != plainO.XUm || zo.YUm != plainO.YUm || zo.Zone != "C" {
		t.Errorf("Zone C darf sich nicht aendern: %+v gegen %+v", zo, plainO)
	}
}
