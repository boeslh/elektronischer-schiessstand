package main

import (
	"reflect"
	"testing"
)

// TestBuildSimExportShot: Export muss exakt dieselbe Loesung wie
// SolveShotDetail() liefern und die Eingangszeiten korrekt um OFS0-5
// verschieben bzw. deaktivierte Mikrofone (MICEN) ausblenden.
func TestBuildSimExportShot(t *testing.T) {
	p := DefaultSimParams()
	p.UsePiezo = piezoNotRequired()
	p.MicOffsetNs = [6]int64{0, 100, -200, 300, 0, 50}
	airNs := genCalAirNs(6, -4, p.MicOffsetNs, p)
	sh := SimShotRaw{ShotNo: 7, Kind: "match", Status: "valid", AirNs: airNs, HasRaw: true}

	es := buildSimExportShot(sh, p)
	want, cands, _ := SolveShotDetail(airNs, nil, p)
	if es.Sim == nil || !reflect.DeepEqual(es.Sim.SimResult, want) {
		t.Fatalf("Sim = %+v, want %+v", es.Sim, want)
	}
	if len(es.Candidates) != len(cands) {
		t.Errorf("len(Candidates) = %d, want %d", len(es.Candidates), len(cands))
	}
	for i := 0; i < 6; i++ {
		if got := *es.Input.TFirstNs[i]; got != airNs[i][0]-p.MicOffsetNs[i] {
			t.Errorf("TFirstNs[%d] = %d, want %d", i, got, airNs[i][0]-p.MicOffsetNs[i])
		}
	}

	p.MicEnabled = []bool{true, true, true, true, true, false}
	es = buildSimExportShot(sh, p)
	if es.Input.MicUsed[5] || es.Input.TFirstNs[5] != nil {
		t.Errorf("Mic5 deaktiviert, aber MicUsed=%v TFirstNs=%v", es.Input.MicUsed[5], es.Input.TFirstNs[5])
	}

	if es := buildSimExportShot(SimShotRaw{ShotNo: 8}, p); es.Sim != nil || es.Raw != nil {
		t.Errorf("Schuss ohne Rohdaten darf keine Sim/Raw-Daten enthalten")
	}
}
