// ============================================================================
// sim_zones.go – Zonen-Kalibrierung (Simulator und Firmware ab Rev 4.17.0,
// dort zoneSolve(); Herleitung siehe study/ und calib_study_test.go
// TestStudyZoneCal).
//
// Hintergrund: Der Treffer erzeugt je nach getroffenen Papierlagen einen
// unterschiedlich lauten bzw. langen Knall - eine Schwelle loest bei leisem
// Signal und grossem Abstand spaeter aus. Mit einem einzigen Parametersatz
// bleiben deshalb je Bereich systematische Abweichungen:
//
//	A  im Loch der Vorsatzscheibe (nur das Schussband)   r < HoleRadiusMM
//	B  neben dem Schussband (nur die Vorsatzscheibe)     |x| > BandHalfMM
//	C  Schussband + Vorsatzscheibe                       sonst
//
// Je Zone gibt es einen eigenen Satz aus Schallgeschwindigkeit, Mikrofon-
// Offsets und Nachkorrektur (die Mikrofon-Geometrie ist fuer alle Zonen
// gleich - die Mikrofone haengen ja fest). Fehlt der Satz einer Zone, gelten
// die Basiswerte.
//
// Ablauf je Schuss (so auch fuer die Firmware vorgesehen):
//  1. Lage mit den Basiswerten bestimmen.
//  2. Zonen-Gewichte aus der Lage: innerhalb +-BlendMM um eine Zonengrenze
//     wird linear zwischen den Saetzen ueberblendet (ein Diabolo am Lochrand
//     trifft teils Band, teils Vorsatzscheibe - ohne Ueberblendung sprunge
//     die Lage an der Grenze).
//  3. Mit den Saetzen der beteiligten Zonen rechnen, Lage gewichtet mitteln.
//     Aendern sich dadurch die Gewichte, wird (hoechstens zweimal)
//     wiederholt.
//
// ============================================================================
package main

import "math"

// ZoneSet: zonenabhaengige Werte (wie die gleichnamigen SimParams-Felder).
type ZoneSet struct {
	SoundMps    float64  `json:"sound_mps"`
	MicOffsetNs [6]int64 `json:"mic_offset_ns"`
	OffsetXUm   int64    `json:"offset_x_um"`
	OffsetYUm   int64    `json:"offset_y_um"`
}

// ZoneParams: Zonengrenzen und je Zone der Parametersatz (nil = Basiswerte).
type ZoneParams struct {
	Enabled      bool     `json:"enabled"`
	HoleRadiusMM float64  `json:"hole_radius_mm"` // Loch der Vorsatzscheibe (LG 15,25)
	BandHalfMM   float64  `json:"band_half_mm"`   // halbe Breite des Schussbands
	BlendMM      float64  `json:"blend_mm"`       // Ueberblendung +- um jede Grenze
	A            *ZoneSet `json:"a,omitempty"`
	B            *ZoneSet `json:"b,omitempty"`
	C            *ZoneSet `json:"c,omitempty"`
}

const (
	zoneDefaultBandHalfMM = 45.0
	zoneDefaultBlendMM    = 2.25 // Diabolo-Radius
)

func (z *ZoneParams) active() bool {
	return z != nil && z.Enabled && z.HoleRadiusMM > 0
}

func (z *ZoneParams) bandHalf() float64 {
	if z.BandHalfMM > 0 {
		return z.BandHalfMM
	}
	return zoneDefaultBandHalfMM
}

func (z *ZoneParams) blend() float64 {
	if z.BlendMM > 0 && z.BlendMM <= 10 {
		return z.BlendMM
	}
	return zoneDefaultBlendMM
}

func (z *ZoneParams) set(name string) *ZoneSet {
	switch name {
	case "A":
		return z.A
	case "B":
		return z.B
	}
	return z.C
}

// zoneNames: Reihenfolge der Zonen (Index wie in zoneWeights).
var zoneNames = [3]string{"A", "B", "C"}

// ramp: 1 fuer v <= edge-w, 0 fuer v >= edge+w, linear dazwischen.
func ramp(v, edge, w float64) float64 {
	if w <= 0 {
		if v < edge {
			return 1
		}
		return 0
	}
	return math.Max(0, math.Min(1, (edge+w-v)/(2*w)))
}

// zoneWeights: Anteile der Zonen A, B, C an der Lage (Scheibenkoordinaten,
// mm); Summe 1.
func (z *ZoneParams) zoneWeights(x, y float64) [3]float64 {
	bl := z.blend()
	wA := ramp(math.Hypot(x, y), z.HoleRadiusMM, bl)
	wB := 1 - ramp(math.Abs(x), z.bandHalf(), bl)
	return [3]float64{wA, (1 - wA) * wB, (1 - wA) * (1 - wB)}
}

// ZoneOf: Zone mit dem groessten Anteil an der Lage.
func (z *ZoneParams) ZoneOf(x, y float64) string {
	w := z.zoneWeights(x, y)
	k := 0
	for i := 1; i < 3; i++ {
		if w[i] > w[k] {
			k = i
		}
	}
	return zoneNames[k]
}

// zoneParamsFor: p mit den Werten der Zone (nil-Satz = p unveraendert).
// Die Schallgeschwindigkeit wird als Differenz zur Basis uebernommen, damit
// eine je Schuss gemessene Schallgeschwindigkeit (SoundFromShot) erhalten
// bleibt: pp traegt bereits die wirksame Schallgeschwindigkeit des Schusses,
// base.SoundMps ist der eingestellte Basiswert.
func zoneParamsFor(pp SimParams, baseSound float64, s *ZoneSet) SimParams {
	if s == nil {
		return pp
	}
	if s.SoundMps > 0 {
		pp.SoundMps += s.SoundMps - baseSound
	}
	pp.MicOffsetNs = s.MicOffsetNs
	pp.OffsetXUm, pp.OffsetYUm = s.OffsetXUm, s.OffsetYUm
	return pp
}

// solveZoned: Ablauf siehe Dateikopf. air/piezo wie SolveShotDetail, pp =
// Parameter mit der wirksamen Schallgeschwindigkeit des Schusses, baseSound
// = eingestellte Basis-Schallgeschwindigkeit.
func solveZoned(air [6][]int64, piezo *int64, pp SimParams, baseSound float64) (SimResult, []airCandidate, []bulletShiftMic, SimParams) {
	z := pp.Zones
	res, cands, bs := SolveShotDetail(air, piezo, pp)
	eff := pp
	if !res.PosValid {
		return res, cands, bs, eff
	}
	x, y := float64(res.XUm)/1000, float64(res.YUm)/1000
	var lastW [3]float64
	for iter := 0; iter < 3; iter++ {
		w := z.zoneWeights(x, y)
		if iter > 0 && w == lastW {
			break
		}
		lastW = w
		var sx, sy, sw float64
		best := -1.0
		var bestRes SimResult
		var bestC []airCandidate
		var bestB []bulletShiftMic
		var bestP SimParams
		for k := 0; k < 3; k++ {
			if w[k] <= 0 {
				continue
			}
			zp := zoneParamsFor(pp, baseSound, z.set(zoneNames[k]))
			r, c, b := SolveShotDetail(air, piezo, zp)
			if !r.PosValid {
				continue
			}
			sx += w[k] * float64(r.XUm)
			sy += w[k] * float64(r.YUm)
			sw += w[k]
			if w[k] > best {
				best, bestRes, bestC, bestB, bestP = w[k], r, c, b, zp
				bestRes.Zone = zoneNames[k]
			}
		}
		if sw == 0 {
			return res, cands, bs, eff
		}
		res, cands, bs, eff = bestRes, bestC, bestB, bestP
		res.XUm = int64(math.Round(sx / sw))
		res.YUm = int64(math.Round(sy / sw))
		x, y = float64(res.XUm)/1000, float64(res.YUm)/1000
	}
	return res, cands, bs, eff
}

// zonedEnabled: Zonen fuer diese Parameter wirksam?
func (p SimParams) zonedEnabled() bool {
	return p.Zones.active()
}
