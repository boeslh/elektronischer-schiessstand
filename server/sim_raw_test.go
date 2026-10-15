package main

import (
	"math"
	"testing"
)

// Pulsbreite nach Firmware-Regel: letzte fallende Flanke vor der naechsten
// steigenden (bzw. WALKMAX), unter 300 ns ungueltig; Variante "first".
func TestPulseWidthNs(t *testing.T) {
	rise := []int64{1000, 60000}
	fall := []int64{1150, 30000, 45000, 70000}
	w := func(idx int, maxNs int64, rule string) int64 {
		v := pulseWidthNs(rise, fall, idx, maxNs, rule)
		if v == nil {
			return -1
		}
		return *v
	}
	if got := w(0, 150000, ""); got != 44000 { // letzte fallende vor naechster steigender (60000)
		t.Errorf("last: %d, erwartet 44000", got)
	}
	if got := w(0, 40000, ""); got != 29000 { // WALKMAX 40us begrenzt
		t.Errorf("walkmax: %d, erwartet 29000", got)
	}
	if got := w(0, 150000, "first"); got != 29000 { // 150ns-Preller zaehlt nicht, erste ab 300ns
		t.Errorf("first: %d, erwartet 29000", got)
	}
	if got := w(1, 150000, ""); got != 10000 {
		t.Errorf("zweite Flanke: %d, erwartet 10000", got)
	}
	if got := pulseWidthNs([]int64{0}, []int64{200}, 0, 150000, ""); got != nil {
		t.Errorf("200ns muss ungueltig sein, bekam %d", *got)
	}
}

// Aus den Rohflanken (inkl. Flanke vor dem Nullpunkt und ausserhalb TDOA)
// bestimmt der Simulator dieselbe Eingabe wie die Firmware und findet mit
// WALKK die wahre Position wieder.
func TestSolveSimShot_FromRawEdges(t *testing.T) {
	p := DefaultSimParams()
	p.UsePiezo = new(bool)
	truth := genCalAirNs(-7.2, 5.5, [6]int64{}, p)
	widths := [6]int64{42000, 31000, 38500, 27000, 35000, 45500}
	const k = 0.42
	var wp [6]*int64
	for i := range widths {
		v := widths[i]
		wp[i] = &v
	}
	delayed := walkDelayed(truth, wp, k)

	raw := &RawEdges{Algo: "classic", Ref: "first", Capture: "mcpwm"}
	for i := 0; i < 6; i++ {
		r0 := delayed[i][0]
		// Stoerflanke vor dem Nullpunkt, Nutzflanke, Nachschwinger nach dem Puls
		raw.RiseNs[i] = []int64{-38200, r0, r0 + 200000}
		raw.FallNs[i] = []int64{-30000, r0 + 150, r0 + widths[i], r0 + 250000}
	}
	sh := SimShotRaw{AirNs: delayed, HasRaw: true, Raw: raw}

	q := p
	q.WalkK = k
	air, w := raw.airFromRaw(q)
	for i := 0; i < 6; i++ {
		if len(air[i]) != 1 || air[i][0] != delayed[i][0] || w[i] == nil || *w[i] != widths[i] {
			t.Fatalf("Mic %d: air=%v w=%v, erwartet %d / %d", i, air[i], w[i], delayed[i][0], widths[i])
		}
	}
	want := SolveShot(truth, nil, p)
	got := SolveSimShot(sh, q)
	if d := math.Hypot(float64(got.XUm-want.XUm), float64(got.YUm-want.YUm)); !got.PosValid || d > 20 {
		t.Errorf("aus Rohdaten: Abweichung %.0f um (valid=%v)", d, got.PosValid)
	}
	// TDOA zu klein: ein Mic mit spaeter Flanke faellt heraus
	var late int
	for i := 0; i < 6; i++ {
		if delayed[i][0] > delayed[late][0] {
			late = i
		}
	}
	q.TdoaUs = int(delayed[late][0]/1000) - 1
	if air, _ := raw.airFromRaw(q); len(air[late]) != 0 {
		t.Errorf("Mic %d mit Flanke %d ns sollte bei TDOA %d us fehlen", late, delayed[late][0], q.TdoaUs)
	}
	// RIM-Rohdaten werden nicht verwendet
	if (&RawEdges{Algo: "rim", Ref: "piezo"}).usable() {
		t.Error("RIM-Rohdaten duerfen nicht nachgerechnet werden")
	}
}

// Leeres raw-Telegramm (kommt bei Firmware 4.16.0 vor): Simulator rechnet
// mit den Laufzeiten aus dem shot weiter.
func TestSolveSimShot_EmptyRawFallsBack(t *testing.T) {
	p := DefaultSimParams()
	p.UsePiezo = new(bool)
	air := genCalAirNs(4, -3, [6]int64{}, p)
	sh := SimShotRaw{AirNs: air, HasRaw: true, Raw: &RawEdges{Algo: "classic", Ref: "first"}}
	if r := SolveSimShot(sh, p); !r.PosValid {
		t.Fatal("Schuss mit leerem raw muss aus air_ns berechnet werden")
	}
}
