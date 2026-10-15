package main

import (
	"testing"
	"time"
)

func TestPairRawTelegrams(t *testing.T) {
	in := make(chan RawShot, 10)
	out := make(chan RawShot, 10)
	active := true
	go pairRawTelegrams(in, out, func() bool { return active })

	// shot + raw mit gleicher seq -> ein Schuss mit Rohdaten (ueber dispatchLine)
	st := NewDeviceState()
	dispatchLine([]byte(`{"type":"config","mac":"AA","raw_log":1}`), in, nil, st)
	dispatchLine([]byte(`{"type":"shot","seq":5,"air_ns":[[0],[1],[2],[3],[4],[5]],"pos_valid":1,"sound_act_mps":344.1}`), in, nil, st)
	dispatchLine([]byte(`{"type":"raw","seq":5,"ref":"first","rise_ns":[[0],[1],[2],[3],[4],[5]],"fall_ns":[[],[],[],[],[],[]]}`), in, nil, st)
	got := <-out
	if got.Seq != 5 || len(got.RawEdges) == 0 || got.SoundActMps == nil || *got.SoundActMps != 344.1 {
		t.Fatalf("Schuss 5: seq=%d raw=%q sound=%v", got.Seq, got.RawEdges, got.SoundActMps)
	}
	if !st.RawLogActive() {
		t.Error("raw_log=1 nicht erkannt")
	}

	// reject laeuft sofort durch, auch bei aktivem RAWLOG
	in <- RawShot{Type: "reject", Seq: 6}
	select {
	case r := <-out:
		if r.Type != "reject" {
			t.Fatalf("erwartet reject, bekam %+v", r)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("reject wurde zurueckgehalten")
	}

	// shot ohne raw -> nach dem Timeout ohne Rohdaten
	start := time.Now()
	in <- RawShot{Type: "shot", Seq: 7, PosValid: 1}
	r := <-out
	if r.Seq != 7 || len(r.RawEdges) != 0 || time.Since(start) < rawPairTimeout-20*time.Millisecond {
		t.Fatalf("Schuss 7: %+v nach %s", r, time.Since(start))
	}

	// naechster Schuss kommt vor dem raw des vorherigen -> beide in Reihenfolge
	in <- RawShot{Type: "shot", Seq: 8, PosValid: 1}
	in <- RawShot{Type: "shot", Seq: 9, PosValid: 1}
	in <- RawShot{Type: "raw", Seq: 9, RawEdges: []byte(`{"seq":9}`)}
	a, b := <-out, <-out
	if a.Seq != 8 || len(a.RawEdges) != 0 || b.Seq != 9 || len(b.RawEdges) == 0 {
		t.Fatalf("Reihenfolge/Zuordnung: %d(%d) %d(%d)", a.Seq, len(a.RawEdges), b.Seq, len(b.RawEdges))
	}

	// RAWLOG aus: kein Zurueckhalten
	active = false
	in <- RawShot{Type: "shot", Seq: 10, PosValid: 1}
	select {
	case r := <-out:
		if r.Seq != 10 {
			t.Fatalf("%+v", r)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Schuss trotz RAWLOG=0 zurueckgehalten")
	}
	close(in)
	if _, ok := <-out; ok {
		t.Error("out nicht geschlossen")
	}
}

// processShot uebernimmt die Rohdaten und bevorzugt die Schallgeschwindigkeit
// aus dem shot-Telegramm (4.16) vor dem Wert aus dem letzten status.
func TestProcessShot_RawEdgesAndSound(t *testing.T) {
	ctxSound, shotSound := 342.5, 344.1
	raw := RawShot{Type: "shot", Seq: 1, PosValid: 1, AirNs: [][]int64{{0}, {1}, {2}, {3}, {4}, {5}},
		SoundActMps: &shotSound, RawEdges: []byte(`{"type":"raw","seq":1}`),
		Ctx: ShotContext{SoundActMps: &ctxSound}}
	s := processShot(raw, testScorer(t))
	if string(s.RawEdges) != `{"type":"raw","seq":1}` || s.SoundActMps == nil || *s.SoundActMps != 344.1 {
		t.Errorf("RawEdges=%s Sound=%v", s.RawEdges, s.SoundActMps)
	}
}

// raw_follows (Rev 4.16.0) steuert das Warten unabhaengig von der config:
// 0 = sofort weiter (z.B. aus dem Offline-Puffer), 1 = auf die naechste
// Zeile warten, ohne raw hoechstens rawFollowsTimeout.
func TestPairRawTelegrams_RawFollows(t *testing.T) {
	in := make(chan RawShot, 10)
	out := make(chan RawShot, 10)
	go pairRawTelegrams(in, out, func() bool { return true }) // config sagt RAWLOG=1
	zero, one := 0, 1

	in <- RawShot{Type: "shot", Seq: 1, RawFollows: &zero}
	select {
	case r := <-out:
		if r.Seq != 1 {
			t.Fatalf("%+v", r)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("raw_follows=0 wurde trotz RAWLOG=1 zurueckgehalten")
	}

	in <- RawShot{Type: "shot", Seq: 2, RawFollows: &one}
	in <- RawShot{Type: "raw", Seq: 2, RawEdges: []byte(`{"seq":2}`)}
	if r := <-out; r.Seq != 2 || len(r.RawEdges) == 0 {
		t.Fatalf("raw_follows=1: %+v", r)
	}

	start := time.Now()
	in <- RawShot{Type: "shot", Seq: 3, RawFollows: &one}
	r := <-out
	if el := time.Since(start); r.Seq != 3 || el < rawFollowsTimeout-20*time.Millisecond || el > rawFollowsTimeout+200*time.Millisecond {
		t.Fatalf("Timeout bei raw_follows=1: seq=%d nach %s", r.Seq, el)
	}
	close(in)
}
