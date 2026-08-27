package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
)

// TargetGeometry: visuelle Scheibenbeschreibung fuer den Browser.
// Abgeleitet aus Scheibengroessen.csv; wird via GET /targets bereitgestellt.
type TargetGeometry struct {
	Name           string     `json:"name"`
	InnerTenD      float64    `json:"inner10_d"`      // 0 = kein separater Innenzehner-Kreis
	InnerTenDashed bool       `json:"inner10_dashed"` // gestrichelte Linie (Scheibe 4 + 7)
	Rings          []RingGeom `json:"rings"`
}

// RingGeom: ein einzelner Ring, aus der CSV-Spalte "Gefüllt" abgeleitet.
type RingGeom struct {
	V      int     `json:"v"`      // Ringwert 1–10
	D      float64 `json:"d"`      // Aussendurchmesser mm
	Filled bool    `json:"filled"` // true = schwarzer Spiegel
}

// builtinTargets: eingebettete Standardwerte, falls targets.json fehlt.
// Entspricht exakt der Datei targets.json.
var builtinTargets = map[string]TargetGeometry{
	"1": {
		Name: "LG",
		Rings: []RingGeom{
			{V: 10, D: 0.5, Filled: true}, {V: 9, D: 5.5, Filled: true},
			{V: 8, D: 10.5, Filled: true}, {V: 7, D: 15.5, Filled: true},
			{V: 6, D: 20.5, Filled: true}, {V: 5, D: 25.5, Filled: true},
			{V: 4, D: 30.5, Filled: true}, {V: 3, D: 35.5, Filled: false},
			{V: 2, D: 40.5, Filled: false}, {V: 1, D: 45.5, Filled: false},
		},
	},
	"2": {
		Name: "ZS",
		Rings: []RingGeom{
			{V: 10, D: 4.5, Filled: true}, {V: 9, D: 13.5, Filled: true},
			{V: 8, D: 22.5, Filled: true}, {V: 7, D: 31.5, Filled: true},
			{V: 6, D: 40.5, Filled: true}, {V: 5, D: 49.5, Filled: false},
			{V: 4, D: 58.5, Filled: false}, {V: 3, D: 67.5, Filled: false},
			{V: 2, D: 76.5, Filled: false}, {V: 1, D: 85.5, Filled: false},
		},
	},
	"4": {
		Name: "SP", InnerTenD: 25, InnerTenDashed: true,
		Rings: []RingGeom{
			{V: 10, D: 50, Filled: true}, {V: 9, D: 100, Filled: true},
			{V: 8, D: 150, Filled: true}, {V: 7, D: 200, Filled: true},
			{V: 6, D: 250, Filled: false}, {V: 5, D: 300, Filled: false},
			{V: 4, D: 350, Filled: false}, {V: 3, D: 400, Filled: false},
			{V: 2, D: 450, Filled: false}, {V: 1, D: 500, Filled: false},
		},
	},
	"7": {
		Name: "LP", InnerTenD: 5, InnerTenDashed: true,
		Rings: []RingGeom{
			{V: 10, D: 11.5, Filled: true}, {V: 9, D: 27.5, Filled: true},
			{V: 8, D: 43.5, Filled: true}, {V: 7, D: 59.5, Filled: true},
			{V: 6, D: 75.5, Filled: false}, {V: 5, D: 91.5, Filled: false},
			{V: 4, D: 107.5, Filled: false}, {V: 3, D: 123.5, Filled: false},
			{V: 2, D: 139.5, Filled: false}, {V: 1, D: 155.5, Filled: false},
		},
	},
}

// TargetGeomToTargetDef konvertiert eine visuelle Scheibenbeschreibung in eine
// Scorer-taugliche TargetDef. Kaliber und Randwertung kommen aus config.json,
// da sie scheibenunabhaengig sind (gleiche Waffe fuer alle Disziplinen).
func TargetGeomToTargetDef(tg TargetGeometry, caliberMM float64, edgeScoring bool) TargetDef {
	rings := make([]RingDef, len(tg.Rings))
	for i, r := range tg.Rings {
		rings[i] = RingDef{Value: r.V, DiameterMM: r.D}
	}
	return TargetDef{
		Name:        tg.Name,
		CaliberMM:   caliberMM,
		EdgeScoring: edgeScoring,
		InnerTenDMM: tg.InnerTenD,
		Rings:       rings,
	}
}

func loadTargets(path string) (map[string]TargetGeometry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Erst als RawMessage einlesen, damit Kommentar-Schluessel (_kommentar)
	// und kuenftige Metadatenfelder das Parsen nicht blockieren.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("targets.json parsen: %w", err)
	}
	m := make(map[string]TargetGeometry, len(raw))
	for k, v := range raw {
		if len(k) > 0 && k[0] == '_' {
			continue // Kommentar-/Metadaten-Schluessel ueberspringen
		}
		var tg TargetGeometry
		if err := json.Unmarshal(v, &tg); err != nil {
			log.Printf("Scheibe %q: %v – uebersprungen", k, err)
			continue
		}
		m[k] = tg
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("targets.json enthaelt keine gueltigen Scheiben")
	}
	log.Printf("Scheiben: %d aus %s geladen", len(m), path)
	return m, nil
}
