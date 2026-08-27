package main

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

func testScorer(t *testing.T) *Scorer {
	t.Helper()
	td := TargetDef{
		Name:        "LG-40",
		CaliberMM:   4.5,
		EdgeScoring: true,
		InnerTenDMM: 0.5,
		Rings: []RingDef{
			{Value: 10, DiameterMM: 0.5},
			{Value: 9, DiameterMM: 26.5},
			{Value: 8, DiameterMM: 51.5},
			{Value: 7, DiameterMM: 76.5},
			{Value: 6, DiameterMM: 101.5},
			{Value: 5, DiameterMM: 126.5},
			{Value: 4, DiameterMM: 151.5},
			{Value: 3, DiameterMM: 176.5},
			{Value: 2, DiameterMM: 201.5},
			{Value: 1, DiameterMM: 226.5},
		},
	}
	return NewScorer(&td)
}

// TestProcessShot_ExampleTelegrams verarbeitet die realen Firmware-Telegramme
// aus firmware/example_telegrams.txt (Rev 4.6.1, type=shot) und prueft, dass
// x_um/y_um korrekt nach x_mm/y_mm uebernommen und gewertet werden.
func TestProcessShot_ExampleTelegrams(t *testing.T) {
	f, err := os.Open("firmware/example_telegrams.txt")
	if err != nil {
		t.Fatalf("example_telegrams.txt: %v", err)
	}
	defer f.Close()

	sc := testScorer(t)
	n := 0
	sc2 := bufio.NewScanner(f)
	for sc2.Scan() {
		line := sc2.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var raw RawShot
		if err := json.Unmarshal(line, &raw); err != nil {
			t.Fatalf("Telegramm unparsbar: %s: %v", line, err)
		}
		if raw.Type != "shot" {
			t.Fatalf("unerwarteter Typ %q", raw.Type)
		}
		shot := processShot(raw, sc)
		if shot.Rejected {
			t.Fatalf("seq %d: unerwartet Rejected (%s)", raw.Seq, shot.RejectMsg)
		}
		wantX := float64(raw.XUm) / 1000.0
		wantY := float64(raw.YUm) / 1000.0
		if shot.XMM != wantX || shot.YMM != wantY {
			t.Errorf("seq %d: x/y = %v/%v, want %v/%v", raw.Seq, shot.XMM, shot.YMM, wantX, wantY)
		}
		if shot.AirNs == nil {
			t.Errorf("seq %d: air_ns nicht uebernommen", raw.Seq)
		}
		if !shot.Clean {
			t.Errorf("seq %d: clean sollte gesetzt sein", raw.Seq)
		}
		n++
	}
	if n == 0 {
		t.Fatal("keine shot-Telegramme in example_telegrams.txt gefunden")
	}
}

// TestProcessShot_Reject stellt sicher, dass reject-Telegramme als Rejected
// markiert werden (fuer Anzeige/Wertung ignoriert, aber mit allen Rohwerten
// fuer Log/DB erhalten) und pos_valid=0-Schuesse ebenso behandelt werden.
func TestProcessShot_Reject(t *testing.T) {
	sc := testScorer(t)

	rejLine := `{"type":"reject","seq":9,"reason":"only 2 mic(s)","hits":2,"piezo_ns":null}`
	var raw RawShot
	if err := json.Unmarshal([]byte(rejLine), &raw); err != nil {
		t.Fatalf("reject unparsbar: %v", err)
	}
	shot := processShot(raw, sc)
	if !shot.Rejected {
		t.Fatal("reject-Telegramm sollte Rejected sein")
	}
	if shot.RejectMsg != "only 2 mic(s)" {
		t.Errorf("RejectMsg = %q", shot.RejectMsg)
	}
	if shot.Hits != 2 {
		t.Errorf("Hits = %d, want 2", shot.Hits)
	}
	if shot.PiezoNs != nil {
		t.Errorf("PiezoNs sollte nil sein bei piezo_ns:null")
	}

	invalidLine := `{"type":"shot","seq":10,"air_ns":[[],[],[]],"x_um":0,"y_um":0,` +
		`"pos_res_um":0,"precision_um":0,"cluster_hits":0,"pos_valid":0,"clean":0,"hits":2,"ts":1}`
	if err := json.Unmarshal([]byte(invalidLine), &raw); err != nil {
		t.Fatalf("shot(pos_valid=0) unparsbar: %v", err)
	}
	shot = processShot(raw, sc)
	if !shot.Rejected {
		t.Fatal("shot mit pos_valid=0 sollte Rejected sein")
	}
}
