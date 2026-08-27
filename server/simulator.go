// ============================================================================
// simulator.go – Server-seitiger Nachbau der ESP32-Firmware-Trilateration
// (Rev 4.7.4) fuer den Kalibrier-Simulator.
//
// Portiert 1:1 aus standpc/firmware/schiessstand_firmware.ino:
//   - Geometrie-Konstanten:            Zeilen ~985-995
//   - solveAirPair()/solveAirPosition: Zeilen ~1080-1330
//     (inkl. Kugeldurchmesser-Korrektur SET BSHIFTPCT/BSHIFTCAP, Rev 4.7.0)
//
// Bewusst mit float32 gerechnet (wie die Firmware "float", nicht "double"),
// um das Rundungsverhalten moeglichst exakt nachzubilden - ein Wechsel auf
// float64 wuerde bei knappen Faellen (z.B. Diskriminante nahe 0) leicht
// abweichende Ergebnisse liefern als das reale Geraet.
//
// Aus den gespeicherten air_ns-Rohdaten (siehe migrations/013) wird damit
// nachgerechnet, wie sich veraenderte Kalibrierparameter (Standoff, Mic-
// Halbabstand X, Mic-Timing-Offsets, Schallgeschwindigkeit, Nachkorrektur-
// Offset, Kugeldurchmesser-Korrektur) auf die berechnete Trefferposition
// ausgewirkt haetten - ohne erneuten Schuss.
//
// Nicht Teil des Ports: runCalibration() (CAL START, Rev 4.7.2 auf 11 statt
// 9 Runden erhoeht) - der Simulator laesst OFS0-5 manuell einstellen bzw.
// per SHOW-Import uebernehmen, reproduziert aber nicht den automatischen
// Kalibrier-ABLAUF selbst (aendert nichts an solveAirPosition()).
// ============================================================================
package main

import (
	"math"
	"sort"
)

// Fixe Geometrie-Konstanten (nicht konfigurierbar, siehe .ino ~985-993).
const (
	micHalfYSteel float32 = 100.0 // mm, TARGET=STEEL
	micHalfYPaper float32 = 85.0  // mm, TARGET=PAPER

	// SET RADIUS Firmware-Default (0.001mm-Einheit -> 200 = 0.2mm) - greift
	// nur als Fallback, wenn SimParams.ClusterRadiusUm nicht gesetzt ist
	// (0, z.B. alte gespeicherte Configs vor Einfuehrung dieses Parameters).
	clusterRadiusDefaultUm int64 = 200
)

// SimParams: einstellbare Kalibrierparameter, Feldnamen identisch zur
// "SHOW"-Ausgabe der Firmware (sendShowConfig(), .ino ~907-965), damit der
// SHOW-Import ohne Mapping funktioniert.
type SimParams struct {
	StandoffSteelMM float64  `json:"standoff_steel_mm"`
	StandoffPaperMM float64  `json:"standoff_paper_mm"`
	MicHalfXMm      float64  `json:"mic_half_x_mm"` // SET MICHALFX, Rev 4.7.3 (vorher fix 115.0)
	OffsetXUm       int64    `json:"offset_x_um"`
	OffsetYUm       int64    `json:"offset_y_um"`
	SoundMps        float64  `json:"sound_mps"`
	MicOffsetNs     [6]int64 `json:"mic_offset_ns"`
	Target          string   `json:"target"` // "steel" | "paper"

	// Kugeldurchmesser-Korrektur der Stufe-1-Loesung (SET BSHIFTPCT/
	// BSHIFTCAP, Rev 4.7.0) - siehe airPosition().
	BulletShiftPct   int     `json:"bullet_shift_pct"`   // 0-100, 0=aus
	BulletShiftCapMm float64 `json:"bullet_shift_cap_mm"` // mm, Kappung je Mikrofon

	// SET RADIUS: Umkreis (0.001mm) fuer cluster_hits UND den Verifizierungs-
	// Mittelpunkt (Stufe 2, siehe airPosition()). 0 (unbelegt, z.B. alte
	// gespeicherte Configs) faellt auf den Firmware-Default (200) zurueck -
	// ein Nutzer wuerde RADIUS praktisch nie bewusst auf 0 setzen (macht
	// cluster_hits/precision_um bedeutungslos), daher risikoarm.
	ClusterRadiusUm int64 `json:"cluster_radius_um"`

	// MicEnabled (SET MICEN0..MICEN5): welche der 6 Mikrofone in die
	// Positionsloesung eingehen - ein deaktiviertes Mikrofon wird wie ein
	// nicht ausgeloestes behandelt (airSeen=false), unabhaengig von
	// vorhandenen Rohdaten. Bewusst als Slice statt [6]bool: eine leere/
	// fehlende Angabe (z.B. alte gespeicherte Configs vor dieser Funktion,
	// oder ein API-Aufruf ohne dieses Feld) bedeutet "alle aktiv" - ein
	// Go-Bool-Array wuerde bei fehlendem JSON-Feld sonst auf lauter false
	// (= alle Mikrofone aus) zurueckfallen und jede Loesung verhindern.
	MicEnabled []bool `json:"mic_enabled,omitempty"`
}

// micEnabled liefert die tatsaechlich wirksame Mikrofon-Auswahl: p.MicEnabled
// falls vollstaendig angegeben (6 Werte), sonst alle 6 aktiv (Default).
func (p SimParams) micEnabled() [6]bool {
	en := [6]bool{true, true, true, true, true, true}
	if len(p.MicEnabled) == 6 {
		copy(en[:], p.MicEnabled)
	}
	return en
}

// DefaultSimParams liefert die Firmware-Werkseinstellungen (siehe .ino
// loadConfig()-Defaults: standoff_st=30.0, standoff_pa=28.0, mic_half_x=115.0,
// sound_mps=355, bshift_pct=50, bshift_cap=3.0, cluster_r=200).
func DefaultSimParams() SimParams {
	return SimParams{
		StandoffSteelMM:  30.0,
		StandoffPaperMM:  28.0,
		MicHalfXMm:       115.0,
		SoundMps:         355,
		Target:           "steel",
		BulletShiftPct:   50,
		BulletShiftCapMm: 3.0,
		ClusterRadiusUm:  clusterRadiusDefaultUm,
	}
}

// SimResult: Ergebnis einer Neuberechnung, Feldnamen/Einheiten identisch
// zum ESP32-Telegramm (x_um/y_um usw., 0.001mm).
type SimResult struct {
	XUm         int64 `json:"x_um"`
	YUm         int64 `json:"y_um"`
	PosResUm    int64 `json:"pos_res_um"`
	PrecisionUm int64 `json:"precision_um"`
	ClusterHits int   `json:"cluster_hits"`
	PosValid    bool  `json:"pos_valid"`
}

// micGeometry liefert MIC_X/MIC_Y/Standoff fuer den gewaehlten Target-Modus
// (applyTargetGeometry(), .ino ~1000-1010) und MicHalfXMm (applyMicHalfX(),
// Rev 4.7.3, .ino ~1035-1045 - seit dieser Revision laufzeitkonfigurierbar
// statt fest 115.0, fuer STEEL UND PAPER gleich). Alles andere als "paper"
// -> steel (Firmware-Default, SET TARGET akzeptiert nur STEEL|PAPER).
func micGeometry(p SimParams) (micX, micY [6]float32, standoffMM float32) {
	halfY := micHalfYSteel
	standoffMM = float32(p.StandoffSteelMM)
	if p.Target == "paper" {
		halfY = micHalfYPaper
		standoffMM = float32(p.StandoffPaperMM)
	}
	halfX := float32(p.MicHalfXMm)
	micX = [6]float32{-halfX, +halfX, -halfX, +halfX, -halfX, +halfX}
	micY = [6]float32{-halfY, -halfY, +halfY, +halfY, 0, 0}
	return
}

// solveAirPair loest (x,y) aus 2 "Loese"-Mics relativ zu einer Referenz
// per Hyperbel-Trilateration (TDOA). Die Distanz Referenz<->Treffer wird
// als 3. Unbekannte mitgeloest (Linearisierung + quadratische Gleichung).
// 1:1-Port von solveAirPair(), .ino:953-1005.
func solveAirPair(ref, a, b int, tNs [6]int64, micX, micY [6]float32,
	standoffMM, soundMmPerNs float32) (x, y, d float32, ok bool) {

	Xr, Yr := micX[ref], micY[ref]
	ra := float32(tNs[a]-tNs[ref]) * soundMmPerNs
	rb := float32(tNs[b]-tNs[ref]) * soundMmPerNs

	A1, B1, C1 := 2*(micX[a]-Xr), 2*(micY[a]-Yr), 2*ra
	D1 := (micX[a]*micX[a] + micY[a]*micY[a]) - (Xr*Xr + Yr*Yr) - ra*ra
	A2, B2, C2 := 2*(micX[b]-Xr), 2*(micY[b]-Yr), 2*rb
	D2 := (micX[b]*micX[b] + micY[b]*micY[b]) - (Xr*Xr + Yr*Yr) - rb*rb

	det := A1*B2 - A2*B1
	if abs32(det) < 1e-6 {
		return 0, 0, 0, false // a, b kollinear mit ref
	}

	x0 := (D1*B2 - D2*B1) / det
	x1 := (C2*B1 - C1*B2) / det
	y0 := (A1*D2 - A2*D1) / det
	y1 := (A2*C1 - A1*C2) / det

	px, py := x0-Xr, y0-Yr
	qa := 1 - x1*x1 - y1*y1
	qb := -2 * (px*x1 + py*y1)
	qc := -(px*px + py*py + standoffMM*standoffMM)

	if abs32(qa) < 1e-6 {
		if abs32(qb) < 1e-6 {
			return 0, 0, 0, false
		}
		d = -qc / qb
	} else {
		disc := qb*qb - 4*qa*qc
		if disc < 0 {
			return 0, 0, 0, false
		}
		sq := float32(math.Sqrt(float64(disc)))
		d1 := (-qb + sq) / (2 * qa)
		d2 := (-qb - sq) / (2 * qa)
		if d1 > 0 && (d2 <= 0 || d1 < d2) {
			d = d1
		} else if d2 > 0 {
			d = d2
		} else {
			return 0, 0, 0, false
		}
	}
	if d <= 0 {
		return 0, 0, 0, false
	}

	x = x0 + x1*d
	y = y0 + y1*d
	return x, y, d, true
}

// airCandidate: eine geloeste 3er-Mikrofon-Kombination ("Schnittpunkt") -
// dieselben Angaben, die die Firmware bei SET DEBUG=3 als "type":"cand"
// ausgibt (Ref/A/B-Mikrofone, x/y, Rest-Fehler gegen die Kontroll-Mics),
// hier immer verfuegbar fuer die Detailanzeige im Simulator.
type airCandidate struct {
	Ref, A, B  int
	X, Y       float32 // mm, Blechkoordinaten (vor OffsetXUm/OffsetYUm)
	ResidualMM float32 // mittlerer Rest-Fehler gegen die NICHT beteiligten Mics dieser Kombination
	Best       bool    // true fuer die als Stufe-1-Loesung gewaehlte Kombination
}

// bulletShiftMic: Beitrag eines an der Stufe-1-Loesung NICHT beteiligten
// ("verbleibenden") Mikrofons zur Kugeldurchmesser-Korrektur (SET BSHIFTPCT/
// BSHIFTCAP) - SignedResidualMM ist der Messwert, aus dem die Verschiebung
// abgeleitet wird (siehe airPosition()-Kommentar zur Korrektur).
type bulletShiftMic struct {
	Mic              int
	SignedResidualMM float32
	ShiftMM          float32 // nach BSHIFTPCT-Gewichtung und BSHIFTCAP-Kappung
}

// airPosition bestimmt die Trefferposition aus den ersten Flanken der
// erfassten Luftmikrofone (3 bis 6). 1:1-Port von solveAirPosition(),
// .ino ~1080-1330 (ohne die DEBUG=3-"_pre"-Zwischenwerte, die im Simulator
// nicht benoetigt werden). Inkl. Kugeldurchmesser-Korrektur (Rev 4.7.0,
// bulletShiftPct/bulletShiftCapMM).
func airPosition(tNs [6]int64, seen [6]bool, micX, micY [6]float32,
	standoffMM, soundMmPerNs, clusterRadiusMM float32,
	bulletShiftPct int, bulletShiftCapMM float32) (xMM, yMM, resMM, precMM float32, clusterHits int,
	candidates []airCandidate, bulletShift []bulletShiftMic, ok bool) {

	var all []int
	for i := 0; i < 6; i++ {
		if seen[i] {
			all = append(all, i)
		}
	}
	if len(all) < 3 {
		return 0, 0, 0, 0, 0, nil, nil, false
	}

	var cands []airCandidate
	found := false
	bestCandIdx := -1
	var bestResidual, bestX, bestY, bestD float32
	bestRef, bestA, bestB := -1, -1, -1

	for i0 := 0; i0 < len(all); i0++ {
		for i1 := i0 + 1; i1 < len(all); i1++ {
			for i2 := i1 + 1; i2 < len(all); i2++ {
				trio := [3]int{all[i0], all[i1], all[i2]}
				ref := trio[0]
				for k := 1; k < 3; k++ {
					if tNs[trio[k]] < tNs[ref] {
						ref = trio[k]
					}
				}
				a, b := -1, -1
				for k := 0; k < 3; k++ {
					if trio[k] == ref {
						continue
					}
					if a < 0 {
						a = trio[k]
					} else {
						b = trio[k]
					}
				}

				x, y, d, solved := solveAirPair(ref, a, b, tNs, micX, micY, standoffMM, soundMmPerNs)
				if !solved {
					continue
				}

				candIdx := len(cands)
				cands = append(cands, airCandidate{Ref: ref, A: a, B: b, X: x, Y: y})

				var residualSum float32
				nCheck := 0
				for _, m := range all {
					if m == ref || m == a || m == b {
						continue
					}
					dx, dy := x-micX[m], y-micY[m]
					dc := float32(math.Sqrt(float64(dx*dx + dy*dy + standoffMM*standoffMM)))
					rc := float32(tNs[m]-tNs[ref]) * soundMmPerNs
					residualSum += abs32(dc - (d + rc))
					nCheck++
				}
				residual := float32(0)
				if nCheck > 0 {
					residual = residualSum / float32(nCheck)
				}
				cands[candIdx].ResidualMM = residual

				if !found || residual < bestResidual {
					found = true
					bestResidual = residual
					bestX, bestY = x, y
					bestD = d
					bestRef, bestA, bestB = ref, a, b
					bestCandIdx = candIdx
				}
			}
		}
	}
	if !found {
		return 0, 0, 0, 0, 0, nil, nil, false
	}
	cands[bestCandIdx].Best = true

	// Kugeldurchmesser-Korrektur (SET BSHIFTPCT/BSHIFTCAP, Rev 4.7.0): das
	// Projektil (ca. 4,5mm Durchmesser) strahlt den Einschlagsschall nicht
	// exakt aus dem Lochmittelpunkt ab, sondern eher von der dem jeweiligen
	// Mikrofon zugewandten Kugeloberflaeche. Fuer jedes an der Stufe-1-
	// Loesung NICHT beteiligte (verbleibende) Mikrofon zeigt der signierte
	// Rest-Fehler eine Verschiebung Richtung (bzw. weg von) diesem Mikrofon
	// an - gewichtet mit bulletShiftPct, je Mikrofon gedeckelt auf
	// bulletShiftCapMM. Wirkt NUR auf die Stufe-1-Loesung (bestX/bestY),
	// die dadurch automatisch auch als korrigierte Referenz in Stufe 2
	// (Verifizierung, unten) einfliesst.
	if bulletShiftPct > 0 {
		var shiftX, shiftY float32
		for _, m := range all {
			if m == bestRef || m == bestA || m == bestB {
				continue
			}
			mdx, mdy := micX[m]-bestX, micY[m]-bestY
			distM := float32(math.Sqrt(float64(mdx*mdx + mdy*mdy)))
			if distM < 1.0e-3 {
				continue
			}
			dc := float32(math.Sqrt(float64(mdx*mdx + mdy*mdy + standoffMM*standoffMM)))
			rc := float32(tNs[m]-tNs[bestRef]) * soundMmPerNs
			signedResidual := dc - (bestD + rc)
			shift := signedResidual * (float32(bulletShiftPct) / 100.0)
			if shift > bulletShiftCapMM {
				shift = bulletShiftCapMM
			} else if shift < -bulletShiftCapMM {
				shift = -bulletShiftCapMM
			}
			bulletShift = append(bulletShift, bulletShiftMic{Mic: m, SignedResidualMM: signedResidual, ShiftMM: shift})
			shiftX += shift * (mdx / distM)
			shiftY += shift * (mdy / distM)
		}
		bestX += shiftX
		bestY += shiftY
	}

	// Verifizierungsschritt (Stufe 2): Mittelpunkt aller Kandidaten
	// innerhalb clusterRadiusMM um die Stufe-1-Loesung wird zur finalen
	// Referenz; precision_um/cluster_hits relativ dazu neu berechnet.
	sumX, sumY := bestX, bestY
	nSum := 1
	for i := range cands {
		if i == bestCandIdx {
			continue
		}
		dx, dy := cands[i].X-bestX, cands[i].Y-bestY
		if float32(math.Sqrt(float64(dx*dx+dy*dy))) <= clusterRadiusMM {
			sumX += cands[i].X
			sumY += cands[i].Y
			nSum++
		}
	}
	verX, verY := sumX/float32(nSum), sumY/float32(nSum)

	var vd1, vd2 float32 = -1, -1
	verInRadius := 0
	for i := range cands {
		dx, dy := cands[i].X-verX, cands[i].Y-verY
		dist := float32(math.Sqrt(float64(dx*dx + dy*dy)))
		if dist <= clusterRadiusMM {
			verInRadius++
		}
		if vd1 < 0 || dist < vd1 {
			vd2 = vd1
			vd1 = dist
		} else if vd2 < 0 || dist < vd2 {
			vd2 = dist
		}
	}
	verNNear := 0
	var verSumSq float32
	if vd1 >= 0 {
		verNNear++
		verSumSq += vd1 * vd1
	}
	if vd2 >= 0 {
		verNNear++
		verSumSq += vd2 * vd2
	}
	precMM = 0
	if verNNear > 0 {
		precMM = float32(math.Sqrt(float64(verSumSq / float32(verNNear))))
	}

	return verX, verY, bestResidual, precMM, verInRadius, cands, bulletShift, true
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// SolveShot berechnet die Trefferposition eines Schusses aus den
// gespeicherten Rohdaten (air_ns je Mikrofon, wie im ESP32-Telegramm) unter
// den gegebenen (ggf. veraenderten) Kalibrierparametern neu - der
// eigentliche Simulator-Einstiegspunkt.
//
// airNs[i][0] ist die erste erfasste Flanke von Mikrofon i in ns, relativ
// zum insgesamt ersten erfassten Mikrofon (wie im Telegramm dokumentiert).
// Ein leeres/fehlendes airNs[i] bedeutet "Mikrofon nicht ausgeloest".
// solveParams buendelt die aus SimParams abgeleiteten, fuer airPosition()
// benoetigten Groessen (Geometrie, Schallgeschwindigkeit, Cluster-Radius mit
// Default-Fallback) - gemeinsam genutzt von SolveShot() und
// CalibrateMicOffsets(), damit beide garantiert dieselbe Solve-Konfiguration
// verwenden (die Kalibrierung optimiert gegen genau die Parameter, mit denen
// auch tatsaechlich neu gerechnet wird).
func solveParams(p SimParams) (micX, micY [6]float32, standoffMM, soundMmPerNs, clusterRadiusMM float32) {
	micX, micY, standoffMM = micGeometry(p)
	soundMmPerNs = float32(p.SoundMps) * 1.0e-6
	clusterRadiusUm := p.ClusterRadiusUm
	if clusterRadiusUm == 0 {
		clusterRadiusUm = clusterRadiusDefaultUm
	}
	clusterRadiusMM = float32(clusterRadiusUm) / 1000.0
	return
}

// solveShotInternal ist der gemeinsame Kern von SolveShot() und
// SolveShotDetail() - liefert zusaetzlich zum SimResult die rohen
// Kandidaten-Schnittpunkte und Kugelkorrektur-Messwerte (Blechkoordinaten/
// mm, VOR OffsetXUm/OffsetYUm), die SolveShot() verwirft und
// SolveShotDetail() fuer die Detailanzeige weiterreicht.
func solveShotInternal(airNs [6][]int64, p SimParams) (SimResult, []airCandidate, []bulletShiftMic) {
	enabled := p.micEnabled()
	var tNs [6]int64
	var seen [6]bool
	for i := 0; i < 6; i++ {
		if len(airNs[i]) > 0 && enabled[i] {
			seen[i] = true
			tNs[i] = airNs[i][0] - p.MicOffsetNs[i]
		}
	}

	micX, micY, standoffMM, soundMmPerNs, clusterRadiusMM := solveParams(p)

	xMM, yMM, resMM, precMM, clusterHits, candidates, bulletShift, ok := airPosition(
		tNs, seen, micX, micY, standoffMM, soundMmPerNs, clusterRadiusMM,
		p.BulletShiftPct, float32(p.BulletShiftCapMm))
	if !ok {
		return SimResult{PosValid: false}, nil, nil
	}

	xUm := int64(math.Round(float64(xMM)*1000)) + p.OffsetXUm
	yUm := int64(math.Round(float64(yMM)*1000)) + p.OffsetYUm

	return SimResult{
		XUm:         xUm,
		YUm:         yUm,
		PosResUm:    int64(math.Round(float64(resMM) * 1000)),
		PrecisionUm: int64(math.Round(float64(precMM) * 1000)),
		ClusterHits: clusterHits,
		PosValid:    true,
	}, candidates, bulletShift
}

func SolveShot(airNs [6][]int64, p SimParams) SimResult {
	res, _, _ := solveShotInternal(airNs, p)
	return res
}

// SolveShotDetail wie SolveShot(), liefert zusaetzlich alle geloesten 3er-
// Mikrofon-Kombinationen ("Schnittpunkte", X/Y in mm, Blechkoordinaten VOR
// OffsetXUm/OffsetYUm) und die Kugeldurchmesser-Korrektur-Messwerte je
// verbleibendem Mikrofon fuer die optionale Detailanzeige im Simulator -
// die Firmware gibt dieselben Angaben nur bei SET DEBUG=3 aus
// ("type":"cand"), hier stehen sie immer bereit. Die Umrechnung nach
// Scheibenkoordinaten (0.001mm inkl. Nachkorrektur-Offset) fuer die API-
// Antwort erfolgt im Aufrufer (api.go), da die rohen mm-Werte fuer
// Nachkommastellen-Rundung (2 Stellen wie gewuenscht) dort gebraucht werden.
func SolveShotDetail(airNs [6][]int64, p SimParams) (SimResult, []airCandidate, []bulletShiftMic) {
	return solveShotInternal(airNs, p)
}

// micOfsMaxNs: SET OFS0..OFS5 Wertebereich (.ino ~1030, MIC_OFS_MAX_NS).
const micOfsMaxNs = 20000.0

// CalibrateMicOffsets berechnet neue Mikrofon-Timing-Offsets (OFS0-OFS5) aus
// den rohen Laufzeiten mehrerer ausgewaehlter Kalibrier-Schuesse - 1:1-Port
// von calCost()/runCalibration(), .ino ~1380-1459.
//
// Mic0 bleibt fix auf 0 (Eichfreiheitsgrad: eine gemeinsame Konstante auf
// alle Offsets aendert keine TDOA-Differenz, siehe Firmware-Kommentar). Die
// Optimierung startet IMMER bei 0 ("Neukalibrierung", nicht inkrementell von
// p.MicOffsetNs aus) und nutzt dieselben Solve-Parameter (Standoff/
// Soundspeed/Target/MicHalfX/BulletShift/ClusterRadius/MicEnabled) wie eine
// normale Neuberechnung - p.MicOffsetNs selbst wird ignoriert.
//
// airNs: je Kalibrier-Schuss die rohen (unkorrigierten) Mikrofon-Flanken,
// wie im Telegramm gespeichert (identisch zum Eingabeformat von SolveShot).
// Gibt die gefundenen Offsets sowie die erreichte Kostensumme (Summe der
// Stufe-1-Rest-Fehler in mm ueber alle Kalibrier-Schuesse, inkl. Strafwert
// 1000mm je unloesbarem Schuss) zurueck.
func CalibrateMicOffsets(airNs [][6][]int64, p SimParams) (offsetsNs [6]int64, cost float64) {
	micX, micY, standoffMM, soundMmPerNs, clusterRadiusMM := solveParams(p)
	enabled := p.micEnabled()

	type calShot struct {
		raw  [6]int64
		seen [6]bool
	}
	cal := make([]calShot, 0, len(airNs))
	for _, shot := range airNs {
		var cs calShot
		for i := 0; i < 6; i++ {
			if len(shot[i]) > 0 && enabled[i] {
				cs.seen[i] = true
				cs.raw[i] = shot[i][0]
			}
		}
		cal = append(cal, cs)
	}

	calCost := func(offsets [6]float32) float32 {
		var total float32
		for _, cs := range cal {
			var corrected [6]int64
			for i := 0; i < 6; i++ {
				if cs.seen[i] {
					corrected[i] = cs.raw[i] - int64(math.Round(float64(offsets[i])))
				}
			}
			_, _, res, _, _, _, _, ok := airPosition(corrected, cs.seen, micX, micY, standoffMM,
				soundMmPerNs, clusterRadiusMM, p.BulletShiftPct, float32(p.BulletShiftCapMm))
			if ok {
				total += res
			} else {
				total += 1000.0 // Strafe: macht Schuss unloesbar
			}
		}
		return total
	}

	clamp := func(v float32) float32 {
		if v > micOfsMaxNs {
			return micOfsMaxNs
		}
		if v < -micOfsMaxNs {
			return -micOfsMaxNs
		}
		return v
	}

	var offsets [6]float32
	const refMic = 0
	stepNs := float32(10000.0)
	for pass := 0; pass < 11; pass++ {
		for i := 0; i < 6; i++ {
			if i == refMic {
				continue
			}
			base := offsets[i]
			baseCost := calCost(offsets)

			offsets[i] = clamp(base + stepNs)
			costPlus := calCost(offsets)

			offsets[i] = clamp(base - stepNs)
			costMinus := calCost(offsets)

			if baseCost <= costPlus && baseCost <= costMinus {
				offsets[i] = base
			} else if costPlus < costMinus {
				offsets[i] = clamp(base + stepNs)
			} else {
				offsets[i] = clamp(base - stepNs)
			}
		}
		stepNs *= 0.5
	}

	for i := 0; i < 6; i++ {
		offsetsNs[i] = int64(math.Round(float64(offsets[i])))
	}
	cost = float64(calCost(offsets))
	return offsetsNs, cost
}

// ============================================================================
// Wertung – woertlich portiert aus standpc/score.go (reine Mathematik ohne
// Firmware-/DB-Abhaengigkeiten, 1:1 uebernehmbar). Wird genutzt, um die
// simulierte (und die Original-)Position in Ring/Zehntel/Teiler zu uebersetzen.
// ============================================================================

// TargetDef: Scheibengeometrie fuer die Wertung (aus targets/target_rings).
type TargetDef struct {
	Name        string
	CaliberMM   float64
	EdgeScoring bool
	InnerTenDMM float64
	Rings       []RingDef
}

type RingDef struct {
	Value      int
	DiameterMM float64
}

type Scorer struct {
	target     *TargetDef
	rings      []RingDef // nach Durchmesser aufsteigend sortiert (10 zuerst)
	ringStepMM float64
}

type ScoreResult struct {
	Ring           int
	Decimal        float64
	InnerTen       bool
	CenterDistance float64
}

func NewScorer(t *TargetDef) *Scorer {
	rings := make([]RingDef, len(t.Rings))
	copy(rings, t.Rings)
	sort.Slice(rings, func(i, j int) bool {
		return rings[i].DiameterMM < rings[j].DiameterMM
	})
	s := &Scorer{target: t, rings: rings}
	if len(rings) >= 2 {
		s.ringStepMM = (rings[1].DiameterMM - rings[0].DiameterMM) / 2.0
	}
	return s
}

func (s *Scorer) Score(xMM, yMM float64) ScoreResult {
	r := math.Hypot(xMM, yMM)

	res := ScoreResult{CenterDistance: math.Round(r*100*10) / 10}

	rEff := r
	if s.target.EdgeScoring {
		rEff = math.Max(0, r-s.target.CaliberMM/2.0)
	}

	if s.target.InnerTenDMM > 0 && rEff <= s.target.InnerTenDMM/2.0 {
		res.InnerTen = true
	}

	res.Ring = 0
	for _, ring := range s.rings {
		if rEff <= ring.DiameterMM/2.0 {
			res.Ring = ring.Value
			break
		}
	}

	res.Decimal = s.decimalValue(r)
	return res
}

func (s *Scorer) decimalValue(r float64) float64 {
	if len(s.rings) == 0 || s.target.CaliberMM <= 0 {
		return 0
	}
	r10 := s.rings[0].DiameterMM / 2.0
	denom := (r10 + s.target.CaliberMM/2.0) * 100
	if denom <= 0 {
		return 0
	}
	teiler := r * 100
	dec := 11.0 - teiler/denom
	if dec > 10.9 {
		dec = 10.9
	}
	if dec < 0 {
		dec = 0
	}
	return math.Floor(dec*10) / 10
}
