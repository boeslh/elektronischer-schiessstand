package main

import (
	"testing"
	"time"
)

// TestTageswertungFromTage: Beispielwerte aus dem Konzept (89 Ringe = 8
// Punkte, Teiler 34,4 = 5 Punkte, Anwesenheit = 3 Punkte) - mehrere
// Scheiben am selben Tag zaehlen nur mit dem besten Wert, Anwesenheit nur
// einmal, Vor-/Nachschuss ohne Anwesenheitsbonus, gleiche Punkte = gleicher
// Platz, andere Tage/Wochentage bleiben aussen vor.
func TestTageswertungFromTage(t *testing.T) {
	w := PSWertung{
		AnwesenheitPunkte: 3,
		PunkteRing:        []PSWertungPunktStufe{{Schwelle: 85, Punkte: 6}, {Schwelle: 89, Punkte: 8}},
		PunkteTeiler:      []PSWertungPunktStufe{{Schwelle: 34.4, Punkte: 5}, {Schwelle: 100, Punkte: 2}},
	}
	fr := time.Date(2026, 9, 25, 0, 0, 0, 0, time.Local) // Freitag
	sa := fr.AddDate(0, 0, 1)
	f := func(v float64) *float64 { return &v }
	raw := []saisonRawRow{
		{teilnehmerID: "a", nachname: "Alpha", schiesstag: fr, ringWert: f(86), teilerWert: f(50)},
		{teilnehmerID: "a", nachname: "Alpha", schiesstag: fr, ringWert: f(90), teilerWert: f(120)},
		{teilnehmerID: "b", nachname: "Beta", schiesstag: fr, vorNachschuss: true, ringWert: f(90), teilerWert: f(30)},
		{teilnehmerID: "c", nachname: "Gamma", schiesstag: fr}, // reine Teilnahme
		{teilnehmerID: "d", nachname: "Delta", schiesstag: fr, ringWert: f(89), teilerWert: f(34.4)},
		{teilnehmerID: "e", nachname: "Epsilon", schiesstag: sa, ringWert: f(99)}, // Samstag: kein Saison-Wochentag
	}
	got := tageswertungFromTage(aggregatePunkteSaisonTage(raw, w, []int{5}), "2026-09-25")

	want := []struct {
		id     string
		platz  int
		punkte float64
	}{
		{"d", 1, 8 + 5 + 3}, // 89 Ringe / Teiler 34,4 genau auf der Schwelle
		{"a", 2, 8 + 2 + 3}, // bester Ring 90 und bester Teiler 50 aus zwei Scheiben
		{"b", 2, 8 + 5},     // Vor-/Nachschuss: kein Anwesenheitsbonus, punktgleich mit a
		{"c", 4, 3},         // reine Teilnahme
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].TeilnehmerID != w.id || got[i].Platz != w.platz || got[i].Punkte != w.punkte {
			t.Errorf("[%d] = %s Platz %d %.1f Punkte, want %s Platz %d %.1f Punkte",
				i, got[i].TeilnehmerID, got[i].Platz, got[i].Punkte, w.id, w.platz, w.punkte)
		}
	}
	if !got[2].VorNachschuss || got[1].VorNachschuss {
		t.Errorf("VorNachschuss-Flags falsch: %+v", got)
	}
}
