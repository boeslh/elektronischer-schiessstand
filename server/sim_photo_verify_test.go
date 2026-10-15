package main

import (
	"math"
	"testing"
)

// Synthetische Schuesse mit einem bekannten Laufzeitfehler an Mic 2: Die
// Pruefung muss ihn als mittleren Rest (Punktquellen-Modell, wie erzeugt)
// relativ zu Mic 0 wiederfinden, die Neuberechnung mit korrekten Parametern
// muss auf den Fotopositionen liegen und Varianten werden ausgewertet.
func TestPhotoVerify_MicResidualsAndStats(t *testing.T) {
	p := DefaultSimParams()
	p.UsePiezo = new(bool)
	p.BulletShiftPct = 0
	trueOfs := [6]int64{0, 0, 400, 0, 0, 0}
	byNo := map[int]SimShotRaw{}
	var pairs []photoVerifyPair
	for i, xy := range [][2]float64{{-10, 8}, {12, -6}, {3, 15}, {-14, -12}, {7, 2}, {-2, -9}, {0.5, 0.5}} {
		no := i + 1
		byNo[no] = SimShotRaw{ShotNo: no, HasRaw: true, AirNs: genCalAirNs(xy[0], xy[1], trueOfs, p),
			XMM: xy[0] + 0.3, YMM: xy[1] - 0.2} // "Geraet" mit konstantem Versatz
		pairs = append(pairs, photoVerifyPair{ShotNo: no, XMM: xy[0], YMM: xy[1]})
	}
	good := p
	good.MicOffsetNs = trueOfs
	res := photoVerify(byNo, p, pairs, []photoVerifyVariant{{"richtige Offsets", good}})

	ms := res["mic_stats"].([]verifyMicStat)
	if d := ms[2].MeanCtrNs - ms[0].MeanCtrNs; math.Abs(d-400) > 5 {
		t.Errorf("Mic 2 relativ zu Mic 0: %.1f ns, erwartet 400", d)
	}
	for _, i := range []int{1, 3, 4, 5} {
		if d := ms[i].MeanCtrNs - ms[0].MeanCtrNs; math.Abs(d) > 5 {
			t.Errorf("Mic %d: %.1f ns, erwartet 0", i, d)
		}
	}
	dev := res["device_stats"].(PosErrStats)
	if dev.N != 7 || math.Abs(dev.MeanDxMM-0.3) > 1e-9 || math.Abs(dev.MeanDyMM+0.2) > 1e-9 || dev.RmsCenteredMM > 1e-9 {
		t.Errorf("Geraete-Statistik: %+v", dev)
	}
	vr := res["variants"].([]verifyVariantResult)
	sim := res["sim_stats"].(PosErrStats)
	if len(vr) != 1 || vr[0].Stats.N != 7 || vr[0].Stats.RmsMM > 0.02 || vr[0].Stats.RmsMM >= sim.RmsMM {
		t.Errorf("Variante %+v sollte genauer sein als aktuelle Parameter %+v", vr, sim)
	}
	shots := res["shots"].([]verifyShot)
	if len(shots) != 7 || len(shots[0].Candidates) == 0 || len(shots[0].VariantErr) != 1 {
		t.Errorf("Schussdetails unvollstaendig: %d Schuesse", len(shots))
	}
}
