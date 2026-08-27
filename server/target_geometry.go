// ============================================================================
// target_geometry.go – Visuelle Scheibengeometrie fuer die Referenzscheibe
// im Simulator (SVG-Darstellung, siehe web/simulator.html).
//
// Die DB (targets/target_rings) kennt nur Ring-Durchmesser, keine visuelle
// "gefuellt/nicht gefuellt"-Information (schwarzer Spiegel vs. nur Kontur).
// Diese Daten sind 1:1 aus standpc/targets.json uebernommen (identische
// Werte, siehe standpc/targets.go builtinTargets) und werden ueber die
// Bruecke disciplines.standpc_target_no (Migration 010) einer Session
// zugeordnet.
// ============================================================================
package main

import "strings"

// TargetGeometry: visuelle Scheibenbeschreibung fuer den Browser - Feldnamen
// identisch zu standpc/targets.go, damit dasselbe Frontend-Rendering
// (SVG-Aufbau) wiederverwendet werden kann.
type TargetGeometry struct {
	Name           string     `json:"name"`
	InnerTenD      float64    `json:"inner10_d"`
	InnerTenDashed bool       `json:"inner10_dashed"`
	Rings          []RingGeom `json:"rings"`
}

type RingGeom struct {
	V      int     `json:"v"`
	D      float64 `json:"d"`
	Filled bool    `json:"filled"`
}

// targetGeometries: Kopie von standpc/targets.json (standpc_target_no -> Geometrie).
var targetGeometries = map[int]TargetGeometry{
	1: {
		Name: "LG",
		Rings: []RingGeom{
			{V: 10, D: 0.5, Filled: true}, {V: 9, D: 5.5, Filled: true},
			{V: 8, D: 10.5, Filled: true}, {V: 7, D: 15.5, Filled: true},
			{V: 6, D: 20.5, Filled: true}, {V: 5, D: 25.5, Filled: true},
			{V: 4, D: 30.5, Filled: true}, {V: 3, D: 35.5, Filled: false},
			{V: 2, D: 40.5, Filled: false}, {V: 1, D: 45.5, Filled: false},
		},
	},
	2: {
		Name: "ZS",
		Rings: []RingGeom{
			{V: 10, D: 4.5, Filled: true}, {V: 9, D: 13.5, Filled: true},
			{V: 8, D: 22.5, Filled: true}, {V: 7, D: 31.5, Filled: true},
			{V: 6, D: 40.5, Filled: true}, {V: 5, D: 49.5, Filled: false},
			{V: 4, D: 58.5, Filled: false}, {V: 3, D: 67.5, Filled: false},
			{V: 2, D: 76.5, Filled: false}, {V: 1, D: 85.5, Filled: false},
		},
	},
	4: {
		Name: "SP", InnerTenD: 25, InnerTenDashed: true,
		Rings: []RingGeom{
			{V: 10, D: 50, Filled: true}, {V: 9, D: 100, Filled: true},
			{V: 8, D: 150, Filled: true}, {V: 7, D: 200, Filled: true},
			{V: 6, D: 250, Filled: false}, {V: 5, D: 300, Filled: false},
			{V: 4, D: 350, Filled: false}, {V: 3, D: 400, Filled: false},
			{V: 2, D: 450, Filled: false}, {V: 1, D: 500, Filled: false},
		},
	},
	7: {
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

// nameToTargetNo bildet die kurzen StandPC-Scheibenkuerzel (wie sie oft als
// Wortbestandteil im DB-Namen auftauchen, z.B. "LP 10m ISSF") auf die
// jeweilige Nummer in targetGeometries ab.
var nameToTargetNo = map[string]int{"LG": 1, "ZS": 2, "SP": 4, "LP": 7}

// matchTargetGeometryByName sucht eines der bekannten Scheibenkuerzel als
// eigenstaendiges Wort im Scheibennamen (Gross-/Kleinschreibung egal).
func matchTargetGeometryByName(name string) (TargetGeometry, bool) {
	for _, word := range strings.Fields(strings.ToUpper(name)) {
		if no, ok := nameToTargetNo[word]; ok {
			return targetGeometries[no], true
		}
	}
	return TargetGeometry{}, false
}

// targetGeometryFromRings baut eine (unstilisierte) Geometrie direkt aus den
// DB-Ringdurchmessern (target_rings), falls standpc_target_no nicht gesetzt
// ist bzw. keiner der bekannten Nummern entspricht - Fallback ohne
// "gefuellt"-Unterscheidung, damit trotzdem eine Referenzscheibe angezeigt
// werden kann.
func targetGeometryFromRings(t *TargetDef) TargetGeometry {
	rings := make([]RingGeom, len(t.Rings))
	for i, r := range t.Rings {
		rings[i] = RingGeom{V: r.Value, D: r.DiameterMM, Filled: r.Value >= 4}
	}
	return TargetGeometry{Name: t.Name, InnerTenD: t.InnerTenDMM, Rings: rings}
}
