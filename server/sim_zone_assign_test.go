package main

import "testing"

func TestHungarianAssign(t *testing.T) {
	// vier Schuesse, systematisch um 6 mm nach unten verschoben, Reihe mit
	// 10-15 mm Abstand (wie rechts neben dem Band am 12.10.2026) plus ein
	// fremdes Loch weit weg
	holes := [][2]float64{{66.8, 39.6}, {59.3, 25.0}, {64.9, 15.0}, {65.1, 0.3}, {-30, -60}}
	pos := [][2]float64{{66.4, 33.0}, {59.3, 18.6}, {65.1, 8.6}, {65.5, -6.0}}
	valid := []bool{true, true, true, true}
	a := assignHolesOptimal(pos, valid, holes, 10)
	for i := 0; i < 4; i++ {
		if a[i] != i {
			t.Fatalf("Zuordnung %v, erwartet Schuss i -> Loch i", a)
		}
	}
	// ueber dem Gate: keine Zuordnung
	if a := assignHolesOptimal([][2]float64{{0, 0}}, []bool{true}, [][2]float64{{20, 0}}, 10); len(a) != 0 {
		t.Fatalf("Paar ueber dem Gate zugeordnet: %v", a)
	}
}
