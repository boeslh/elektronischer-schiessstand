// ============================================================================
// sim_raw.go – Nachrechnen aus dem "raw"-Telegramm (Firmware Rev 4.16.0,
// SET RAWLOG=1, siehe standpc/firmware/protokoll-referenz.md 4.5c).
//
// Das Telegramm enthaelt je Mikrofon ALLE steigenden und fallenden Flanken
// (unkorrigiert) plus die Parameter zum Schusszeitpunkt. Daraus bestimmt der
// Simulator - statt aus den fertigen air_ns/air_w_ns des shot-Telegramms -
// selbst wie die Firmware:
//   - die fuer die Loesung verwendete Flanke je Mic (CLASSIC: erste steigende
//     Flanke mit 0 <= t <= TDOA),
//   - die Pulsbreite (letzte fallende Flanke vor der naechsten steigenden,
//     hoechstens WALKMAX, mindestens 300 ns - edgeWidthNs() der Firmware).
//
// TDOA, WALKMAX und die Breiten-Regel sind dabei als Varianten einstellbar
// (SimParams.TdoaUs/WalkMaxUs/WalkWidthRule). Nur ALGO=CLASSIC - den RIM-Pfad
// bildet der Simulator nicht nach.
// ============================================================================
package main

import (
	"encoding/json"
	"math"
)

// RawEdges: Inhalt des raw-Telegramms (Feldnamen wie im Telegramm).
type RawEdges struct {
	Seq         int      `json:"seq"`
	Algo        string   `json:"algo"`
	Ref         string   `json:"ref"` // "first" (CLASSIC) | "piezo" (RIM)
	Capture     string   `json:"capture"`
	SoundActMps *float64 `json:"sound_act_mps"`
	WalkK       *float64 `json:"walk_k"`
	WalkMaxUs   *int     `json:"walk_max_us"`
	WinPreNs    int64    `json:"win_pre_ns"`
	WinPostNs   int64    `json:"win_post_ns"`
	// bis Firmware 4.19: Timing-Offsets; ab Rev 4.20.0 Entfernungs-
	// korrekturen und die damit tatsaechlich gerechneten Mikrofon-Positionen
	// [x,y,z] (0.001mm), siehe rawMicLayout
	OfsNs     *[6]int64    `json:"ofs_ns,omitempty"`
	MicDistUm *[6]int64    `json:"mic_dist_um,omitempty"`
	MicPosUm  *[6][3]int64 `json:"mic_pos_um,omitempty"`
	// MicLayout: daraus erkannte Anordnung (rawMicLayout), nur fuer die
	// Simulator-Oberflaeche - kein Telegrammfeld
	MicLayout string     `json:"mic_layout,omitempty"`
	MicEn     [6]int     `json:"mic_en"`
	PiezoNs   *int64     `json:"piezo_ns"`
	RiseNs    [6][]int64 `json:"rise_ns"`
	FallNs    [6][]int64 `json:"fall_ns"`
	Truncated int        `json:"truncated"`
}

// rawMicLayout: Mikrofon-Anordnung, mit der das Geraet laut raw-Telegramm
// gerechnet hat (Firmware ab Rev 4.20.0, mic_pos_um + mic_dist_um) - die
// Anordnung, deren Soll-Positionen mit den Korrekturen auf hoechstens 5 um
// genau die gemeldeten Positionen ergeben; "" = unbekannt (aeltere Firmware).
func (r *RawEdges) rawMicLayout() string {
	if r == nil || r.MicPosUm == nil || r.MicDistUm == nil {
		return ""
	}
	var d [6]float32
	for i, v := range r.MicDistUm {
		d[i] = float32(v)
	}
	for _, name := range []string{micLayoutAngled, micLayoutFlat} {
		g := micGeometryDist(name, d)
		ok := true
		for i := 0; i < 6 && ok; i++ {
			for k, v := range [3]float32{g.X[i], g.Y[i], g.Z[i]} {
				if math.Abs(float64(v)*1000-float64(r.MicPosUm[i][k])) > 5 {
					ok = false
				}
			}
		}
		if ok {
			return name
		}
	}
	return ""
}

func parseRawEdges(b []byte) *RawEdges {
	if len(b) == 0 {
		return nil
	}
	var r RawEdges
	if json.Unmarshal(b, &r) != nil {
		return nil
	}
	r.MicLayout = r.rawMicLayout()
	return &r
}

// usable: nur CLASSIC-Rohdaten (Nullpunkt = erste Flanke) kann der Simulator
// nachrechnen.
func (r *RawEdges) usable() bool {
	return r != nil && r.Ref == "first" && r.Algo != "rim"
}

// hasEdges: das raw-Telegramm enthaelt ueberhaupt steigende Flanken. Es
// kommt vor, dass die Firmware ein leeres raw sendet, obwohl der shot
// Laufzeiten hat - dann rechnet der Simulator mit air_ns/air_w_ns aus dem
// shot weiter, statt den Schuss als ungueltig zu verwerfen.
func (r *RawEdges) hasEdges() bool {
	for i := 0; i < 6; i++ {
		if len(r.RiseNs[i]) > 0 {
			return true
		}
	}
	return false
}

const (
	tdoaDefaultUs    = 750 // SET TDOA Firmware-Default
	walkMaxDefaultUs = 150 // SET WALKMAX Firmware-Default
	walkMinNs        = 300 // WALK_MIN_NS der Firmware: kuerzere "Pulse" sind Preller
)

// firstEdgeIdx: Index der ersten steigenden Flanke mit 0 <= t <= tdoaNs
// (CLASSIC-Auswahl der Firmware), -1 = keine.
func firstEdgeIdx(rise []int64, tdoaNs int64) int {
	for k, t := range rise {
		if t >= 0 && t <= tdoaNs {
			return k
		}
	}
	return -1
}

// pulseWidthNs: Pulsbreite zur steigenden Flanke rise[idx] - Firmware-Regel
// (edgeWidthNs): Grenze = min(WALKMAX, Abstand zur naechsten steigenden
// Flanke), davon die LETZTE fallende Flanke davor; rule "first" waehlt als
// Variante die ERSTE fallende Flanke ab 300 ns. nil = keine gueltige Breite.
func pulseWidthNs(rise, fall []int64, idx int, walkMaxNs int64, rule string) *int64 {
	r := rise[idx]
	limit := walkMaxNs
	for _, t := range rise {
		if d := t - r; d > 0 && d < limit {
			limit = d
		}
	}
	var best int64 = -1
	for _, f := range fall {
		d := f - r
		if d <= 0 || d >= limit {
			continue
		}
		if rule == "first" {
			if d >= walkMinNs && (best < 0 || d < best) {
				best = d
			}
		} else if d > best {
			best = d
		}
	}
	if best < walkMinNs {
		return nil
	}
	return &best
}

// airFromRaw bestimmt aus den Rohflanken die Eingabe der Loesung: je Mic die
// ausgewaehlte erste Flanke (als einziges Element, weitere Flanken spielen
// fuer die Loesung keine Rolle) und deren Pulsbreite.
func (r *RawEdges) airFromRaw(p SimParams) (air [6][]int64, w [6]*int64) {
	tdoa := int64(p.TdoaUs)
	if tdoa <= 0 {
		tdoa = tdoaDefaultUs
	}
	walkMax := int64(p.WalkMaxUs)
	if walkMax <= 0 {
		walkMax = walkMaxDefaultUs
		if r.WalkMaxUs != nil && *r.WalkMaxUs > 0 {
			walkMax = int64(*r.WalkMaxUs)
		}
	}
	for i := 0; i < 6; i++ {
		k := firstEdgeIdx(r.RiseNs[i], tdoa*1000)
		if k < 0 {
			continue
		}
		air[i] = []int64{r.RiseNs[i][k]}
		w[i] = pulseWidthNs(r.RiseNs[i], r.FallNs[i], k, walkMax*1000, p.WalkWidthRule)
	}
	return air, w
}
