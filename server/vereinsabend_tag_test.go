package main

import (
	"bytes"
	"fmt"
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

// TestSaisonwertungFromTage: Punkte aller gueltigen Abende addiert, bester
// Ring (max) und bester Teiler (min) ueber die Saison, Tage ausserhalb des
// Saison-Zeitraums bzw. an anderen Wochentagen zaehlen nicht.
func TestSaisonwertungFromTage(t *testing.T) {
	w := PSWertung{
		AnwesenheitPunkte: 3,
		PunkteRing:        []PSWertungPunktStufe{{Schwelle: 85, Punkte: 6}, {Schwelle: 89, Punkte: 8}},
		PunkteTeiler:      []PSWertungPunktStufe{{Schwelle: 34.4, Punkte: 5}, {Schwelle: 100, Punkte: 2}},
	}
	fr1 := time.Date(2026, 9, 25, 0, 0, 0, 0, time.Local) // Freitag
	fr2 := fr1.AddDate(0, 0, 7)
	sa := fr1.AddDate(0, 0, 1)
	vorher := fr1.AddDate(0, 0, -28) // Freitag vor Saisonbeginn
	f := func(v float64) *float64 { return &v }
	raw := []saisonRawRow{
		{teilnehmerID: "a", nachname: "Alpha", schiesstag: fr1, ringWert: f(86), teilerWert: f(50)},   // 6+2+3 = 11
		{teilnehmerID: "a", nachname: "Alpha", schiesstag: fr2, ringWert: f(90), teilerWert: f(120)},  // 8+0+3 = 11
		{teilnehmerID: "a", nachname: "Alpha", schiesstag: sa, ringWert: f(99), teilerWert: f(1)},     // Samstag: zaehlt nicht
		{teilnehmerID: "a", nachname: "Alpha", schiesstag: vorher, ringWert: f(99), teilerWert: f(1)}, // vor der Saison
		{teilnehmerID: "b", nachname: "Beta", schiesstag: fr1, ringWert: f(89), teilerWert: f(34.4)},  // 8+5+3 = 16
		{teilnehmerID: "c", nachname: "Gamma", schiesstag: fr2},                                       // nur Anwesenheit: 3
	}
	got := saisonwertungFromTage(aggregatePunkteSaisonTage(raw, w, []int{5}), "2026-09-01", "2027-08-30")
	want := []struct {
		id            string
		platz, abende int
		punkte, ring  float64
		teiler        float64
	}{
		{"a", 1, 2, 22, 90, 50},
		{"b", 2, 1, 16, 89, 34.4},
		{"c", 3, 1, 3, 0, 0},
	}
	if len(got) != len(want) {
		t.Fatalf("%d Eintraege, erwartet %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		ring, teiler := 0.0, 0.0
		if g.BesterRing != nil {
			ring = *g.BesterRing
		}
		if g.BesterTeiler != nil {
			teiler = *g.BesterTeiler
		}
		if g.TeilnehmerID != w.id || g.Platz != w.platz || g.Abende != w.abende || g.Punkte != w.punkte || ring != w.ring || teiler != w.teiler {
			t.Errorf("Platz %d: %+v (Ring %v, Teiler %v), erwartet %+v", i+1, g, ring, teiler, w)
		}
	}
}

// TestSaisonSummary: Schiessabende, Durchschnitt/Maximum Anwesende (nur
// Vor-/Nachschuss zaehlt nicht als anwesend), Schnitt je Klasse.
func TestSaisonSummary(t *testing.T) {
	w := PSWertung{AnwesenheitPunkte: 3}
	fr1 := time.Date(2026, 9, 25, 0, 0, 0, 0, time.Local)
	fr2 := fr1.AddDate(0, 0, 7)
	fr3 := fr1.AddDate(0, 0, 14)
	raw := []saisonRawRow{
		{teilnehmerID: "a", klasse: "Herren", schiesstag: fr1},
		{teilnehmerID: "b", klasse: "Damen", schiesstag: fr1},
		{teilnehmerID: "c", klasse: "Herren", schiesstag: fr1},
		{teilnehmerID: "a", klasse: "Herren", schiesstag: fr2},
		{teilnehmerID: "d", klasse: "Jugend", schiesstag: fr2, vorNachschuss: true}, // nicht anwesend
		{teilnehmerID: "a", klasse: "Herren", schiesstag: fr3},
		{teilnehmerID: "b", klasse: "Damen", schiesstag: fr3},
		{teilnehmerID: "c", klasse: "Herren", schiesstag: fr3},
	}
	s := saisonSummary(aggregatePunkteSaisonTage(raw, w, nil), "2026-09-01", "2027-08-30")
	if s.Schiessabende != 3 || s.MaxAnw != 3 || len(s.MaxAnwDaten) != 2 || s.MaxAnwDaten[0] != "2026-09-25" {
		t.Errorf("Abende/Maximum: %+v", s)
	}
	if d := s.DurchschnittAnw; d < 2.33 || d > 2.34 { // (3+1+3)/3
		t.Errorf("Durchschnitt %.3f, erwartet 2,333", d)
	}
	want := map[string]float64{"Damen": 2.0 / 3, "Herren": 5.0 / 3}
	if len(s.DurchschnittKlasse) != 2 {
		t.Fatalf("Klassen: %+v (Jugend nur Vor-/Nachschuss, darf nicht zaehlen)", s.DurchschnittKlasse)
	}
	for _, k := range s.DurchschnittKlasse {
		if d := k.Durchschnitt - want[k.Klasse]; d > 1e-9 || d < -1e-9 {
			t.Errorf("Klasse %s: %.3f, erwartet %.3f", k.Klasse, k.Durchschnitt, want[k.Klasse])
		}
	}
}

// TestBuildVereinsabendPDF: PDF entsteht (auch mehrseitig, Umlaute, quer bei
// vielen Spalten) ohne Fehler.
func TestBuildVereinsabendPDF(t *testing.T) {
	req := vaPDFRequest{Title: "Saison 2026-27 – Tagesergebnisse", Subtitle: "Freitag, 25.09.2026",
		Summary: []vaPDFKV{{"Teilnehmer", "12"}, {"Durchschnitt je Klasse", "Damen 0,7 · Herren 1,7"}}}
	for i := 0; i < 10; i++ {
		req.Columns = append(req.Columns, vaPDFColumn{Label: fmt.Sprintf("Spalte %d", i), Num: i%2 == 0})
	}
	for i := 0; i < 120; i++ {
		row := []string{}
		for j := 0; j < 10; j++ {
			row = append(row, fmt.Sprintf("Größlhuber %d", i*j))
		}
		req.Rows = append(req.Rows, row)
	}
	var buf bytes.Buffer
	pdf := buildVereinsabendPDF(req)
	if err := pdf.Output(&buf); err != nil {
		t.Fatal(err)
	}
	if pdf.PageNo() < 3 || !bytes.HasPrefix(buf.Bytes(), []byte("%PDF")) {
		t.Errorf("Seiten %d, Groesse %d", pdf.PageNo(), buf.Len())
	}
}
