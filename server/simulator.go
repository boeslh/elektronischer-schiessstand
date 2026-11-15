// ============================================================================
// simulator.go – Server-seitiger Nachbau der ESP32-Firmware-Trilateration
// (Rev 4.20.0, ALGO=CLASSIC-Pfad; ALGO=RIM wird bewusst nicht nachgebildet) fuer
// den Kalibrier-Simulator.
//
// Portiert 1:1 aus standpc/firmware/schiessstand_firmware.ino:
//   - Mikrofon-Anordnung:              board_layout.h MIC_LAYOUT, applyMicGeometry()
//   - Schallgeschwindigkeit:           speedOfSoundMps() (nur Temperatur)
//   - solveAirPair()/solveAirPosition: eigenes z je Mikrofon
//     (inkl. Kugeldurchmesser-Korrektur SET BSHIFTPCT/BSHIFTCAP, Rev 4.7.0)
//   - runCalibration()/calCost():      Entfernungskorrektur DIST1-5
//
// Bewusst mit float32 gerechnet (wie die Firmware "float", nicht "double"),
// um das Rundungsverhalten moeglichst exakt nachzubilden - ein Wechsel auf
// float64 wuerde bei knappen Faellen (z.B. Diskriminante nahe 0) leicht
// abweichende Ergebnisse liefern als das reale Geraet.
//
// Aus den gespeicherten air_ns-Rohdaten (siehe migrations/013) wird damit
// nachgerechnet, wie sich veraenderte Kalibrierparameter (Mikrofon-Anordnung,
// Entfernungskorrektur je Mikrofon, Temperatur, Nachkorrektur-Offset,
// Kugeldurchmesser-Korrektur) auf die berechnete Trefferposition ausgewirkt
// haetten - ohne erneuten Schuss.
//
// Seit Firmware Rev 4.20.0 gibt es keine Timing-Offsets (OFS<i>), keinen
// Standoff (STANDOFFPAPER) und keinen Mic-Halbabstand (MICHALFX) mehr: die
// Mikrofone stehen je Anordnung fest (x/y/z je Mikrofon), die Kalibrierung
// verschiebt jedes entlang seiner Linie zur Scheibenmitte (DIST<i>).
// ============================================================================
package main

import (
	"encoding/json"
	"math"
	"sort"
)

// Fixe Geometrie-Konstanten (nicht konfigurierbar, siehe .ino ~985-993).
const (
	// SET RADIUS Firmware-Default (0.001mm-Einheit -> 200 = 0.2mm) - greift
	// nur als Fallback, wenn SimParams.ClusterRadiusUm nicht gesetzt ist
	// (0, z.B. alte gespeicherte Configs vor Einfuehrung dieses Parameters).
	clusterRadiusDefaultUm int64 = 200

	// Simulator-Defaults (DefaultSimParams) - abweichend von der Firmware:
	// Cluster-Radius 4 mm statt 0,2 mm (beim Wechsel von mm auf 0.001mm
	// verstellt; mit 0,2 mm liegen fast alle Schnittpunkte ausserhalb und
	// Σ9/cluster_hits werden bedeutungslos) und Kugeldurchmesser-Korrektur
	// 30 % (in der Foto-Studie 12.10.2026 bester Wert, calib_study_test.go
	// TestStudyBulletShift).
	simDefaultClusterRadiusUm int64 = 4000
	simDefaultBulletShiftPct        = 30
)

// SimParams: einstellbare Kalibrierparameter, Feldnamen identisch zur
// "SHOW"-Ausgabe der Firmware (sendShowConfig(), .ino ~907-965), damit der
// SHOW-Import ohne Mapping funktioniert.
type SimParams struct {
	// MicLayout: Mikrofon-Anordnung der Firmware (board_layout.h MIC_LAYOUT,
	// "mic_layout" im SHOW) - "angled" (ab Rev 4.20.0, Default) oder "flat"
	// (bis Rev 4.19, fuer aeltere Schuesse), "" = angled.
	MicLayout string `json:"mic_layout,omitempty"`
	// MicDistUm: Entfernungskorrektur je Mikrofon (SET DIST0..DIST5, 0.001mm
	// entlang der Linie zur Scheibenmitte), siehe micGeometry.
	MicDistUm [6]int64 `json:"mic_dist_um"`
	// TempC: Ersatztemperatur (SET TEMP, Grad C) - daraus die Schall-
	// geschwindigkeit, siehe speedOfSound.
	TempC     float64 `json:"temp_set_c"`
	OffsetXUm int64   `json:"offset_x_um"`
	OffsetYUm int64   `json:"offset_y_um"`
	// soundOverride: wirksame Schallgeschwindigkeit eines einzelnen Schusses
	// in m/s (SoundFromShot bzw. je Schuss vorgegeben), 0 = aus TempC. Nur
	// intern, nicht Teil der gespeicherten Parameter.
	soundOverride float64

	// Kugeldurchmesser-Korrektur der Stufe-1-Loesung (SET BSHIFTPCT/
	// BSHIFTCAP, Rev 4.7.0) - siehe airPosition().
	BulletShiftPct   int     `json:"bullet_shift_pct"`    // 0-100, 0=aus
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

	// SET PIEZOMIN/PIEZOMAX: erwartete Piezo-Verzoegerung nach dem Einschlag
	// in us - das Zeitfenster, in dem eine Piezo-Flanke ueberhaupt als
	// Bestaetigung DIESES Einschlags zaehlt (siehe UsePiezo).
	PiezoMinUs int `json:"piezo_min_us"`
	PiezoMaxUs int `json:"piezo_max_us"`
	// SET PIEZO: ein Schuss ohne Piezo-Bestaetigung
	// (kein piezo_ns gespeichert, oder ausserhalb PIEZOMIN..PIEZOMAX) als
	// UNGUELTIG behandelt (PosValid=false), nicht mehr nur als "nicht clean"
	// markiert (siehe airPosition()-Aufrufer in solveShotInternal). Grund:
	// reale Vorfaelle, in denen Luftschall benachbarter Staende ohne jeden
	// lokalen Einschlag (piezo_ok durchgehend false) trotzdem als "gueltige"
	// Trefferposition durchgewunken wurde - der Rest-Fehler war dabei zwar
	// stark erhoeht, aber ohne harte Piezo-Pflicht rutschte der Schuss
	// trotzdem in die Wertung.
	//
	// *bool statt bool: soll wie der Firmware-Default cfg.usePiezo=true
	// verhalten, wenn das Feld fehlt (alte gespeicherte Configs von vor
	// Einfuehrung dieser Option, siehe usePiezo()-Fallback) - ein einfaches
	// "bool" wuerde bei fehlendem JSON-Feld auf false (= Pflicht deaktiviert)
	// zurueckfallen und die neue Pruefung fuer JEDE alte Config lautlos
	// wirkungslos machen (derselbe Fallstrick wie bei MicEnabled, siehe
	// dortigen Kommentar). Bewusst abschaltbar fuer Aufbauten ohne Piezo-
	// Hardware bzw. zum Nachrechnen historischer Schuesse von vor Einfuehrung
	// dieser Pflicht.
	UsePiezo *bool `json:"use_piezo,omitempty"`
	// PosAlgo: Verfahren der Endpositions-Bestimmung (SET CLUSTERCORR/
	// CLUSTERITER/LSQ/LSQPELLETR ab Firmware 4.12.0, siehe sim_posalgo.go) -
	// Nullwert = Verhalten bis Firmware 4.11.5, DefaultSimParams = Firmware-
	// Default ab 4.12.0.
	PosAlgo PosAlgoOptions `json:"pos_algo"`

	// WalkK: Pulsbreiten-Korrektur (SET WALKK, Firmware Rev 4.14.0, 0 = aus) -
	// Feldname wie im SHOW-Telegramm ("walk_k"), siehe walkCorrectedAir.
	WalkK float64 `json:"walk_k"`
	// SoundFromShot: statt der Schallgeschwindigkeit aus TempC die zum
	// Schusszeitpunkt aktive des Geraets verwenden (bei SET DHT=1 aus der
	// gemessenen Temperatur) - fuer Schuesse ohne gespeicherten Wert gilt
	// weiter TempC. Wirkt bei Neuberechnung und Entfernungs-Kalibrierung;
	// Sigma9-Optimierung und Foto-Kalibrierung bestimmen die Temperatur
	// selbst und nutzen daher immer TempC.
	SoundFromShot bool `json:"sound_from_shot,omitempty"`
	// Capture: Erfassungsmodus ("isr"|"mcpwm") der Schuesse, aus denen die
	// Parameter bestimmt wurden - nur Information fuer die Uebernahme an
	// den Stand (die Kalibrierung passt nur zum selben Modus), "" = unbekannt.
	Capture string `json:"capture,omitempty"`

	// Nur fuer Schuesse mit Rohdaten-Telegramm (SET RAWLOG=1, Firmware
	// 4.16.0, siehe sim_raw.go) - Feldnamen wie im SHOW: TDOA-Fenster fuer
	// die Auswahl der ersten Flanke (0 = Firmware-Default 750 us), WALKMAX
	// fuer die Pulsbreite (0 = Wert zum Schusszeitpunkt) und als Variante
	// die Breiten-Regel ("" = Firmware: letzte fallende Flanke, "first" =
	// erste fallende Flanke).
	TdoaUs        int    `json:"tdoa_us,omitempty"`
	WalkMaxUs     int    `json:"walk_max_us,omitempty"`
	WalkWidthRule string `json:"walk_width_rule,omitempty"`

	// Zones: zonenabhaengige Saetze (Loch der Vorsatzscheibe / neben dem
	// Schussband / Band + Vorsatzscheibe), siehe sim_zones.go - nil oder
	// nicht aktiv = ein Satz fuer die ganze Scheibe wie bisher.
	Zones *ZoneParams `json:"zones,omitempty"`
}

// walkCorrectedAir bildet die Pulsbreiten-Korrektur der Firmware nach
// (walkDeltas(), Rev 4.14.0): je Mikrofon t += k*(w - w_mittel), w_mittel =
// Mittel der gueltigen Breiten aller beteiligten (ausgeloesten UND aktiven)
// Mikrofone dieses Schusses. Mics ohne gueltige Breite (null, ISR-Betrieb)
// bleiben unkorrigiert. Korrigiert wird nur die erste Flanke (die fuer die
// Loesung verwendete); air wird nicht veraendert (Kopie der betroffenen
// Kanaele). Die Firmware rechnet t = roh + round(walk) (seit Rev 4.20.0 ohne
// Timing-Offset).
func walkCorrectedAir(air [6][]int64, w [6]*int64, k float64, enabled [6]bool) [6][]int64 {
	if k == 0 {
		return air
	}
	var sum float64
	n := 0
	use := func(i int) bool { return enabled[i] && len(air[i]) > 0 && w[i] != nil && *w[i] >= 0 }
	for i := 0; i < 6; i++ {
		if use(i) {
			sum += float64(*w[i])
			n++
		}
	}
	if n == 0 {
		return air
	}
	mean := float32(sum / float64(n))
	out := air
	for i := 0; i < 6; i++ {
		if use(i) {
			// float32 wie in der Firmware (walkDeltas rechnet in float)
			d := float32(k) * (float32(*w[i]) - mean)
			ch := append([]int64(nil), air[i]...)
			ch[0] += int64(math.Round(float64(d)))
			out[i] = ch
		}
	}
	return out
}

// airFor: Rohzeiten des Schusses mit der Pulsbreiten-Korrektur aus p. Bei
// ALGO=RIM ist die Korrektur laut Firmware schon in air_ns enthalten und
// wird nicht ein zweites Mal angewendet.
func (sh SimShotRaw) airFor(p SimParams) [6][]int64 {
	// Rohdaten-Telegramm (RAWLOG): erste Flanke und Pulsbreite selbst
	// bestimmen (sim_raw.go) - sonst die fertigen Werte aus dem shot.
	if sh.Raw.usable() && sh.Raw.hasEdges() {
		air, w := sh.Raw.airFromRaw(p)
		return walkCorrectedAir(air, w, p.WalkK, p.micEnabled())
	}
	if p.WalkK == 0 || sh.Algo == "rim" {
		return sh.AirNs
	}
	return walkCorrectedAir(sh.AirNs, sh.AirWNs, p.WalkK, p.micEnabled())
}

// soundFor: wirksame Schallgeschwindigkeit fuer diesen Schuss (siehe
// SimParams.SoundFromShot) - 0 = aus p.TempC.
func (sh SimShotRaw) soundFor(p SimParams) float64 {
	if !p.SoundFromShot {
		return 0
	}
	v := sh.SoundActMps
	if v == nil && sh.Raw != nil {
		v = sh.Raw.SoundActMps
	}
	if v != nil && *v >= 300 && *v <= 400 {
		return *v
	}
	return 0
}

// paramsFor: p mit der fuer diesen Schuss wirksamen Schallgeschwindigkeit.
func (sh SimShotRaw) paramsFor(p SimParams) SimParams {
	if v := sh.soundFor(p); v > 0 {
		p.soundOverride = v
	}
	return p
}

// SolveSimShot / SolveSimShotDetail: Neuberechnung eines gespeicherten
// Schusses inkl. Pulsbreiten-Korrektur und Schallgeschwindigkeit je Schuss.
// Mit aktiver Zonen-Kalibrierung (p.Zones) zweistufig, siehe sim_zones.go.
func SolveSimShot(sh SimShotRaw, p SimParams) SimResult {
	res, _, _ := SolveSimShotDetail(sh, p)
	return res
}

func SolveSimShotDetail(sh SimShotRaw, p SimParams) (SimResult, []airCandidate, []bulletShiftMic) {
	pp := sh.paramsFor(p)
	if p.zonedEnabled() {
		res, c, b, eff := solveZoned(sh.airFor(p), sh.PiezoNs, pp)
		res.EffOffsetXUm, res.EffOffsetYUm = eff.OffsetXUm, eff.OffsetYUm
		return res, c, b
	}
	res, c, b := SolveShotDetail(sh.airFor(p), sh.PiezoNs, pp)
	res.EffOffsetXUm, res.EffOffsetYUm = p.OffsetXUm, p.OffsetYUm
	return res, c, b
}

// usePiezo liefert p.UsePiezo mit Fallback auf den Firmware-Default (true) -
// siehe Feld-Kommentar zu UsePiezo.
func (p SimParams) usePiezo() bool {
	return p.UsePiezo == nil || *p.UsePiezo
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
// loadConfig()-Defaults: Anordnung "angled", DIST0-5=0, temp_fb=20.0,
// bshift_cap=3.0) - AUSSER Kugeldurchmesser-Korrektur (30 %) und Cluster-
// Radius (4 mm), siehe simDefaultBulletShiftPct/simDefaultClusterRadiusUm.
func DefaultSimParams() SimParams {
	return SimParams{
		MicLayout:        micLayoutAngled,
		TempC:            simDefaultTempC,
		BulletShiftPct:   simDefaultBulletShiftPct,
		BulletShiftCapMm: 3.0,
		ClusterRadiusUm:  simDefaultClusterRadiusUm,
		PiezoMinUs:       100,
		PiezoMaxUs:       1400,
		// Endpositions-Verfahren wie Firmware-Default ab Rev 4.12.0 (SET
		// CLUSTERCORR=1, CLUSTERITER=5, LSQ=0, LSQPELLETR=2.25).
		PosAlgo: PosAlgoOptions{CorrCluster: true, Iterations: 5, LSQPelletRMm: 2.25},
		// UsePiezo bewusst nil gelassen (statt explizit gesetzt) - der
		// usePiezo()-Fallback liefert dafuer bereits true, siehe Feld-
		// Kommentar bei SimParams.UsePiezo.
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

	// CorrResUm: wie PosResUm, aber MIT bereits angewandter Kugeldurchmesser-
	// Korrektur (SET BSHIFTPCT/BSHIFTCAP) - siehe airPosition()-Kommentar zu
	// corrResMM. Genau dieser (korrigierte) Wert ist das Kalibrier-Kostenmass
	// (calCostClassicFn); PosResUm bleibt bewusst der reine Vorher-Wert.
	CorrResUm int64 `json:"corr_res_um"`

	// InvalidReason: bei PosValid=false der Grund, soweit bekannt (fehlende
	// Piezo-Bestaetigung) - airPosition() selbst unterscheidet nicht, warum
	// ein Fit fehlschlug. Rein diagnostisch fuer die Simulator-UI, kein
	// stabiler API-Vertrag.
	InvalidReason string `json:"invalid_reason,omitempty"`

	// ClusterSumUm/ClusterSumN: Schnittpunkt-Cluster "Σ9" (siehe clusterSum9) - Qualitaetsmass des Schusses, auch als alternatives
	// Kalibrier-Kostenmass waehlbar (CalibrateMicOffsetsMode).
	ClusterSumUm *int64 `json:"cluster_sum_um,omitempty"`
	ClusterSumN  int    `json:"cluster_sum_n,omitempty"`
	// Dasselbe mit den kugeldurchmesser-korrigierten Schnittpunkten
	// (airCandidate.CorrX/CorrY) - fuer die Umschaltung "Rohwerte / nach
	// Kugeldurchm.-Korrektur" im Simulator.
	ClusterSumCorrUm *int64 `json:"cluster_sum_corr_um,omitempty"`
	ClusterSumCorrN  int    `json:"cluster_sum_corr_n,omitempty"`

	// Zone: bei Zonen-Kalibrierung die Zone mit dem groessten Anteil an der
	// Lage ("A"|"B"|"C", siehe sim_zones.go), sonst leer.
	Zone string `json:"zone,omitempty"`
	// EffOffsetXUm/EffOffsetYUm: die fuer die Detailangaben (Schnittpunkte)
	// wirksame Nachkorrektur - bei Zonen die der Zone, sonst p.OffsetX/YUm.
	EffOffsetXUm int64 `json:"-"`
	EffOffsetYUm int64 `json:"-"`
}

// Schnittpunkt-Cluster Σ9: Summe der Abstaende der (bis zu) 9 Schnittpunkte,
// die innerhalb des Cluster-Radius (SET RADIUS) am naechsten am Schusszentrum
// (finale Position, Stufe 2) liegen. Fehlen bis 9 Schnittpunkte im Radius,
// zaehlt jeder fehlende mit cluster9MissingMM. Je kleiner, desto enger liegen
// die Schnittpunkte beieinander - < 10mm gilt als sehr gut. Mit 6 Mikrofonen
// gibt es bis zu 20 Schnittpunkte, mit 5 nur 10 und mit 4 nur 4 (dann
// mindestens 5 x 5mm = 25mm).
const (
	cluster9N         = 9
	cluster9MissingMM = 5.0
)

// clusterSum9 berechnet Σ9 aus den (rohen) Kandidaten relativ zum
// Schusszentrum centerX/centerY (mm, Blechkoordinaten wie die Kandidaten) -
// dieselbe Distanzbasis wie cluster_hits in airPosition(). corrected=true
// nimmt statt der rohen die kugeldurchmesser-korrigierten Schnittpunkte
// (CorrX/CorrY), Bezugspunkt bleibt das Schusszentrum. n = Anzahl der
// tatsaechlich im Radius gezaehlten Schnittpunkte (max. 9).
func clusterSum9(cands []airCandidate, centerX, centerY, radiusMM float32, corrected bool) (sumMM float32, n int) {
	var dists []float32
	for _, c := range cands {
		x, y := c.X, c.Y
		if corrected {
			x, y = c.CorrX, c.CorrY
		}
		dx, dy := x-centerX, y-centerY
		if d := float32(math.Sqrt(float64(dx*dx + dy*dy))); d <= radiusMM {
			dists = append(dists, d)
		}
	}
	sort.Slice(dists, func(i, j int) bool { return dists[i] < dists[j] })
	if len(dists) > cluster9N {
		dists = dists[:cluster9N]
	}
	for _, d := range dists {
		sumMM += d
	}
	n = len(dists)
	sumMM += float32(cluster9N-n) * cluster9MissingMM
	return sumMM, n
}

// Mikrofon-Anordnungen der Firmware (board_layout.h MIC_LAYOUT, Rev 4.20.0):
// Soll-Positionen in mm, Ursprung Scheibenmitte, x rechts, y oben, z vor dem
// Papier, Reihenfolge AIR0..AIR5.
const (
	micLayoutAngled = "angled" // ab Rev 4.20.0, auf die Scheibenmitte ausgerichtet
	micLayoutFlat   = "flat"   // bis Rev 4.19, flach in der Seitenwand

	// micDistMaxUm: SET DIST0..DIST5 Wertebereich (MIC_DIST_MAX_UM)
	micDistMaxUm = 10000.0
	// simDefaultTempC: SET TEMP Firmware-Default
	simDefaultTempC = 20.0
)

var micLayouts = map[string]micGeom{
	// Messelement der Mikrofone laut Vermessung vom 15.11.2026 (board_layout.h)
	micLayoutAngled: {
		X: [6]float32{-110.54, 110.54, -110.54, 110.54, -114.33, 114.33},
		Y: [6]float32{-75.46, -75.46, 75.46, 75.46, 0, 0},
		Z: [6]float32{25.15, 25.15, 25.15, 25.15, 25.76, 25.76},
	},
	micLayoutFlat: {
		X: [6]float32{-115, 115, -115, 115, -115, 115},
		Y: [6]float32{-85, -85, 85, 85, 0, 0},
		Z: [6]float32{28, 28, 28, 28, 28, 28},
	},
}

// micLayoutsLegacy: fruehere Soll-Positionen einer Anordnung, die das Geraet
// in raw-Telegrammen noch melden kann (Firmware vor der Korrektur) - werden
// nur zum Erkennen der Anordnung verwendet (rawMicLayout), gerechnet wird mit
// micLayouts.
var micLayoutsLegacy = map[string][]micGeom{
	// "angled" bis zur Vermessung vom 15.11.2026
	micLayoutAngled: {{
		X: [6]float32{-109, 109, -109, 109, -113, 113},
		Y: [6]float32{-75, -75, 75, 75, 0, 0},
		Z: [6]float32{24, 24, 24, 24, 25, 25},
	}},
}

// micGeom: tatsaechlich verwendete Mikrofon-Positionen in mm (MIC_X/MIC_Y/
// MIC_Z der Firmware).
type micGeom struct {
	X, Y, Z [6]float32
}

// micNominal: Soll-Positionen der Anordnung (unbekannt/leer = angled).
func micNominal(layout string) micGeom {
	if g, ok := micLayouts[layout]; ok {
		return g
	}
	return micLayouts[micLayoutAngled]
}

// micGeometryDist: 1:1-Port von applyMicGeometry() - jedes Mikrofon wird
// entlang seiner Linie zur Scheibenmitte um distUm[i] (0.001mm, positiv =
// weiter weg) verschoben, die Richtung bleibt fest.
func micGeometryDist(layout string, distUm [6]float32) micGeom {
	return micNominal(layout).shifted(distUm)
}

// shifted: Soll-Positionen nom, je Mikrofon um distUm[i] entlang der Linie
// zur Scheibenmitte verschoben.
func (nom micGeom) shifted(distUm [6]float32) micGeom {
	var g micGeom
	for i := 0; i < 6; i++ {
		r := float32(math.Sqrt(float64(nom.X[i]*nom.X[i] + nom.Y[i]*nom.Y[i] + nom.Z[i]*nom.Z[i])))
		f := float32(1)
		if r > 1 {
			f = 1 + distUm[i]/1000/r
		}
		g.X[i], g.Y[i], g.Z[i] = nom.X[i]*f, nom.Y[i]*f, nom.Z[i]*f
	}
	return g
}

// micGeometry: Geometrie mit den Entfernungskorrekturen aus p.
func micGeometry(p SimParams) micGeom {
	var d [6]float32
	for i, v := range p.MicDistUm {
		d[i] = float32(v)
	}
	return micGeometryDist(p.MicLayout, d)
}

// speedOfSound: 1:1-Port von speedOfSoundMps() (Rev 4.20.0, trockene Luft,
// nur Temperatur), float wie in der Firmware.
func speedOfSound(tempC float64) float64 {
	return float64(331.3 * float32(math.Sqrt(float64(1+float32(tempC)/273.15))))
}

// tempForSound: Umkehrung von speedOfSound - fuer alte Parameter mit
// sound_mps statt temp_set_c.
func tempForSound(mps float64) float64 {
	q := mps / 331.3
	return 273.15 * (q*q - 1)
}

// soundMps: wirksame Schallgeschwindigkeit (m/s) dieser Parameter.
func (p SimParams) soundMps() float64 {
	if p.soundOverride > 0 {
		return p.soundOverride
	}
	return speedOfSound(p.TempC)
}

// UnmarshalJSON: Parameter von vor Firmware Rev 4.20.0 (gespeicherte
// Simulator-Einstellungen) tragen sound_mps statt temp_set_c - daraus die
// gleichwertige Temperatur. Fehlt beides, gilt der Firmware-Default.
// Timing-Offsets, Standoff und Mic-Halbabstand alter Parameter gibt es nicht
// mehr und werden ignoriert.
func (p *SimParams) UnmarshalJSON(b []byte) error {
	type plain SimParams
	q := plain(*p)
	if err := json.Unmarshal(b, &q); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err == nil {
		if _, ok := keys["temp_set_c"]; !ok {
			var legacy float64
			if raw, ok := keys["sound_mps"]; ok && json.Unmarshal(raw, &legacy) == nil && legacy >= 300 && legacy <= 400 {
				q.TempC = math.Round(tempForSound(legacy)*10) / 10
			} else {
				q.TempC = simDefaultTempC
			}
		}
	}
	*p = SimParams(q)
	return nil
}

// solveAirPair loest (x,y) aus 2 "Loese"-Mics relativ zu einer Referenz
// per Hyperbel-Trilateration (TDOA). Die Distanz Referenz<->Treffer wird
// als 3. Unbekannte mitgeloest (Linearisierung + quadratische Gleichung).
// 1:1-Port von solveAirPair() - seit Rev 4.20.0 mit eigenem z je Mikrofon
// (geht nur als Konstante ein, das System bleibt linear in x/y/d).
func solveAirPair(ref, a, b int, tNs [6]int64, g micGeom, soundMmPerNs float32) (x, y, d float32, ok bool) {

	Xr, Yr, Zr := g.X[ref], g.Y[ref], g.Z[ref]
	ra := float32(tNs[a]-tNs[ref]) * soundMmPerNs
	rb := float32(tNs[b]-tNs[ref]) * soundMmPerNs

	Kr := Xr*Xr + Yr*Yr + Zr*Zr
	A1, B1, C1 := 2*(g.X[a]-Xr), 2*(g.Y[a]-Yr), 2*ra
	D1 := (g.X[a]*g.X[a] + g.Y[a]*g.Y[a] + g.Z[a]*g.Z[a]) - Kr - ra*ra
	A2, B2, C2 := 2*(g.X[b]-Xr), 2*(g.Y[b]-Yr), 2*rb
	D2 := (g.X[b]*g.X[b] + g.Y[b]*g.Y[b] + g.Z[b]*g.Z[b]) - Kr - rb*rb

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
	qc := -(px*px + py*py + Zr*Zr)

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
	Ref, A, B int
	X, Y      float32 // mm, Blechkoordinaten (vor OffsetXUm/OffsetYUm) - Rohwert, siehe CorrX/CorrY
	D         float32 // mm, Abstand Referenzmikrofon-Loesung (aus solveAirPair) - fuer CorrX/CorrY benoetigt
	CorrX     float32 // mm, wie X/Y aber MIT Kugeldurchmesser-Korrektur (SET BSHIFTPCT/BSHIFTCAP) -
	CorrY     float32 // dieselbe Formel wie fuer die Stufe-1-Loesung (siehe airPosition), hier je
	//                    Kandidat einzeln angewandt (dieser Kandidat als eigene Referenz). Bei
	//                    bulletShiftPct<=0 identisch zu X/Y.
	ResidualMM float32 // mittlerer Rest-Fehler gegen die NICHT beteiligten Mics dieser Kombination
	Best       bool    // true fuer die als Stufe-1-Loesung gewaehlte Kombination
	InCluster  bool    // true wenn dieser Kandidat (Best immer eingeschlossen) innerhalb
	//                    clusterRadiusMM der Stufe-1-Loesung liegt und damit in die Stufe-2-
	//                    Mittelung (finale Position) eingeflossen ist - siehe airPosition()
}

// bulletShiftVector berechnet die Kugeldurchmesser-Korrektur-Verschiebung
// (SET BSHIFTPCT/BSHIFTCAP) fuer eine gegebene geloeste Position (x,y,d aus
// einer 3er-Mikrofon-Kombination ref/a/b) - dieselbe Formel wie fuer die
// Stufe-1-Loesung in airPosition(), hier als eigene Funktion, damit sie
// sowohl fuer die Stufe-1-Loesung als auch (fuer die Detailanzeige "Kandidaten
// nach Korrektur") fuer jeden einzelnen Kandidaten angewandt werden kann.
func bulletShiftVector(x, y, d float32, ref, a, b int, all []int, tNs [6]int64,
	g micGeom, soundMmPerNs float32,
	bulletShiftPct int, bulletShiftCapMM float32) (shiftX, shiftY float32) {
	if bulletShiftPct <= 0 {
		return 0, 0
	}
	for _, m := range all {
		if m == ref || m == a || m == b {
			continue
		}
		mdx, mdy := g.X[m]-x, g.Y[m]-y
		distM := float32(math.Sqrt(float64(mdx*mdx + mdy*mdy)))
		if distM < 1.0e-3 {
			continue
		}
		dc := float32(math.Sqrt(float64(mdx*mdx + mdy*mdy + g.Z[m]*g.Z[m])))
		rc := float32(tNs[m]-tNs[ref]) * soundMmPerNs
		signedResidual := dc - (d + rc)
		shift := signedResidual * (float32(bulletShiftPct) / 100.0)
		if shift > bulletShiftCapMM {
			shift = bulletShiftCapMM
		} else if shift < -bulletShiftCapMM {
			shift = -bulletShiftCapMM
		}
		shiftX += shift * (mdx / distM)
		shiftY += shift * (mdy / distM)
	}
	return shiftX, shiftY
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
func airPosition(tNs [6]int64, seen [6]bool, g micGeom,
	soundMmPerNs, clusterRadiusMM float32,
	bulletShiftPct int, bulletShiftCapMM float32) (xMM, yMM, resMM, precMM float32, clusterHits int,
	candidates []airCandidate, bulletShift []bulletShiftMic, corrResMM float32, ok bool) {

	var all []int
	for i := 0; i < 6; i++ {
		if seen[i] {
			all = append(all, i)
		}
	}
	if len(all) < 3 {
		return 0, 0, 0, 0, 0, nil, nil, 0, false
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

				x, y, d, solved := solveAirPair(ref, a, b, tNs, g, soundMmPerNs)
				if !solved {
					continue
				}

				candIdx := len(cands)
				cands = append(cands, airCandidate{Ref: ref, A: a, B: b, X: x, Y: y, D: d})

				var residualSum float32
				nCheck := 0
				for _, m := range all {
					if m == ref || m == a || m == b {
						continue
					}
					dx, dy := x-g.X[m], y-g.Y[m]
					dc := float32(math.Sqrt(float64(dx*dx + dy*dy + g.Z[m]*g.Z[m])))
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
		return 0, 0, 0, 0, 0, nil, nil, 0, false
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
			mdx, mdy := g.X[m]-bestX, g.Y[m]-bestY
			distM := float32(math.Sqrt(float64(mdx*mdx + mdy*mdy)))
			if distM < 1.0e-3 {
				continue
			}
			dc := float32(math.Sqrt(float64(mdx*mdx + mdy*mdy + g.Z[m]*g.Z[m])))
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

	// corrResMM: derselbe Rest-Fehler wie bestResidual (Mittel des Abstands
	// zu den NICHT an der Stufe-1-Loesung beteiligten Mics), aber MIT der
	// bereits angewandten Kugeldurchmesser-Korrektur (bestX/bestY oben) -
	// bestResidual selbst bleibt bewusst der reine Vorher-Wert (unveraendert
	// fuer PosResUm/die Kandidaten-Tabelle). Fuer die Kalibrierung
	// (calCostClassicFn) ist corrResMM das richtige Kostenmass: nur so wirkt
	// sich BSHIFTPCT/BSHIFTCAP ueberhaupt auf die gefundenen Mic-Offsets aus.
	// dRefCorr: tatsaechlicher (geometrischer) Abstand vom Referenzmikrofon
	// zur KORRIGIERTEN Position - bestD ist der Abstand zur ROHEN Position
	// (aus solveAirPair) und nach der Verschiebung nicht mehr gueltig.
	refDx, refDy := bestX-g.X[bestRef], bestY-g.Y[bestRef]
	dRefCorr := float32(math.Sqrt(float64(refDx*refDx + refDy*refDy + g.Z[bestRef]*g.Z[bestRef])))
	var corrResidualSum float32
	corrNCheck := 0
	for _, m := range all {
		if m == bestRef || m == bestA || m == bestB {
			continue
		}
		dx, dy := bestX-g.X[m], bestY-g.Y[m]
		dc := float32(math.Sqrt(float64(dx*dx + dy*dy + g.Z[m]*g.Z[m])))
		rc := float32(tNs[m]-tNs[bestRef]) * soundMmPerNs
		corrResidualSum += abs32(dc - (dRefCorr + rc))
		corrNCheck++
	}
	if corrNCheck > 0 {
		corrResMM = corrResidualSum / float32(corrNCheck)
	}

	// CorrX/CorrY je Kandidat: dieselbe Kugeldurchmesser-Korrektur wie fuer
	// die Stufe-1-Loesung, hier aber auf JEDEN Kandidaten einzeln angewandt
	// (der jeweilige Kandidat als eigene Referenz) - fuer die Detailanzeige
	// "Schnittpunkte nach Korrektur" als Alternative zu den rohen X/Y-Werten.
	for i := range cands {
		sx, sy := bulletShiftVector(cands[i].X, cands[i].Y, cands[i].D,
			cands[i].Ref, cands[i].A, cands[i].B, all, tNs, g,
			soundMmPerNs, bulletShiftPct, bulletShiftCapMM)
		cands[i].CorrX = cands[i].X + sx
		cands[i].CorrY = cands[i].Y + sy
	}

	// Verifizierungsschritt (Stufe 2): Mittelpunkt aller Kandidaten
	// innerhalb clusterRadiusMM um die Stufe-1-Loesung wird zur finalen
	// Referenz; precision_um/cluster_hits relativ dazu neu berechnet.
	sumX, sumY := bestX, bestY
	nSum := 1
	cands[bestCandIdx].InCluster = true
	for i := range cands {
		if i == bestCandIdx {
			continue
		}
		dx, dy := cands[i].X-bestX, cands[i].Y-bestY
		if float32(math.Sqrt(float64(dx*dx+dy*dy))) <= clusterRadiusMM {
			sumX += cands[i].X
			sumY += cands[i].Y
			nSum++
			cands[i].InCluster = true
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

	return verX, verY, bestResidual, precMM, verInRadius, cands, bulletShift, corrResMM, true
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
func solveParams(p SimParams) (g micGeom, soundMmPerNs, clusterRadiusMM float32) {
	g = micGeometry(p)
	soundMmPerNs = float32(p.soundMps()) * 1.0e-6
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
func solveShotInternal(airNs [6][]int64, piezoNs *int64, p SimParams) (SimResult, []airCandidate, []bulletShiftMic) {

	enabled := p.micEnabled()
	var tNs [6]int64
	var seen [6]bool
	for i := 0; i < 6; i++ {
		if len(airNs[i]) > 0 && enabled[i] {
			seen[i] = true
			tNs[i] = airNs[i][0]
		}
	}

	g, soundMmPerNs, clusterRadiusMM := solveParams(p)

	xMM, yMM, resMM, precMM, clusterHits, candidates, bulletShift, corrResMM, ok := airPosition(
		tNs, seen, g, soundMmPerNs, clusterRadiusMM,
		p.BulletShiftPct, float32(p.BulletShiftCapMm))
	if !ok {
		return SimResult{PosValid: false}, nil, nil
	}

	// SET PIEZO (p.usePiezo(), Default true): ohne Piezo-Bestaetigung ist die
	// Position zwar geometrisch loesbar (ok==true oben), aber NICHT vertrauens-
	// wuerdig - siehe UsePiezo-Feldkommentar zum realen Vorfall, der diese
	// Pruefung ausgeloest hat. piezoNs ist (wie airNs) relativ zur ersten
	// erfassten Flanke gespeichert, daher derselbe PIEZOMIN/PIEZOMAX-Vergleich
	// wie in der Firmware (processShot()::piezoOk).
	if p.usePiezo() {
		piezoOk := piezoNs != nil &&
			*piezoNs >= int64(p.PiezoMinUs)*1000 &&
			*piezoNs <= int64(p.PiezoMaxUs)*1000
		if !piezoOk {
			reason := "keine Piezo-Bestaetigung (SET PIEZO=1, aber kein piezo_ns gespeichert)"
			if piezoNs != nil {
				reason = "Piezo-Verzoegerung außerhalb SET PIEZOMIN/PIEZOMAX"
			}
			return SimResult{PosValid: false, InvalidReason: reason}, nil, nil
		}
	}

	if p.PosAlgo.active() {
		xMM, yMM, clusterHits, precMM = refinePosition(candidates, xMM, yMM, tNs, seen, g,
			soundMmPerNs, clusterRadiusMM, p.PosAlgo)
	}

	xUm := int64(math.Round(float64(xMM)*1000)) + p.OffsetXUm
	yUm := int64(math.Round(float64(yMM)*1000)) + p.OffsetYUm
	sum9, n9 := clusterSum9(candidates, xMM, yMM, clusterRadiusMM, false)
	sum9Um := int64(math.Round(float64(sum9) * 1000))
	sum9c, n9c := clusterSum9(candidates, xMM, yMM, clusterRadiusMM, true)
	sum9cUm := int64(math.Round(float64(sum9c) * 1000))

	return SimResult{
		ClusterSumUm:     &sum9Um,
		ClusterSumN:      n9,
		ClusterSumCorrUm: &sum9cUm,
		ClusterSumCorrN:  n9c,
		XUm:              xUm,
		YUm:              yUm,
		PosResUm:         int64(math.Round(float64(resMM) * 1000)),
		PrecisionUm:      int64(math.Round(float64(precMM) * 1000)),
		CorrResUm:        int64(math.Round(float64(corrResMM) * 1000)),
		ClusterHits:      clusterHits,
		PosValid:         true,
	}, candidates, bulletShift
}

func SolveShot(airNs [6][]int64, piezoNs *int64, p SimParams) SimResult {
	res, _, _ := solveShotInternal(airNs, piezoNs, p)
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
func SolveShotDetail(airNs [6][]int64, piezoNs *int64, p SimParams) (SimResult, []airCandidate, []bulletShiftMic) {
	return solveShotInternal(airNs, piezoNs, p)
}

// CalibrateMicDist berechnet neue Entfernungskorrekturen (DIST0-DIST5) aus
// den rohen Laufzeiten mehrerer ausgewaehlter Kalibrier-Schuesse - 1:1-Port
// von calCost()/runCalibration() (Rev 4.20.0).
//
// Mic0 bleibt fix auf 0 (Eichfreiheitsgrad). Die Optimierung startet IMMER
// bei 0 ("Neukalibrierung", nicht von p.MicDistUm aus) und nutzt dieselben
// Solve-Parameter (Anordnung/Temperatur/BulletShift/ClusterRadius/
// MicEnabled) wie eine normale Neuberechnung.
//
// airNs: je Kalibrier-Schuss die rohen Mikrofon-Flanken wie im Telegramm,
// sounds[k] > 0 die Schallgeschwindigkeit des Schusses (sonst aus p.TempC,
// siehe SimParams.SoundFromShot). Gibt die gefundenen Korrekturen sowie die
// erreichte Kostensumme zurueck (Summe der Rest-Fehler nach Kugelkorrektur
// in mm, Strafwert 1000mm je unloesbarem Schuss - siehe calCostClassicFn()).
func CalibrateMicDist(airNs [][6][]int64, sounds []float64, p SimParams) (distUm [6]int64, cost float64) {
	dist, c := coordinateDescentCalibrate(calCostClassicFn(airNs, sounds, p))
	for i := 0; i < 6; i++ {
		distUm[i] = int64(math.Round(float64(dist[i])))
	}
	return distUm, float64(c)
}

// calCostClassicFn: 1:1-Port von calCost()/calCostClassic() - Kosten =
// Summe der Rest-Fehler nach Kugeldurchmesser-Korrektur ueber alle
// Kalibrier-Schuesse, mit der aus den Probe-Entfernungskorrekturen
// gebildeten Geometrie.
func calCostClassicFn(airNs [][6][]int64, sounds []float64, p SimParams) func([6]float32) float32 {
	_, soundMmPerNs, clusterRadiusMM := solveParams(p)
	enabled := p.micEnabled()

	type calShot struct {
		raw   [6]int64
		seen  [6]bool
		sound float32 // mm/ns
	}
	cal := make([]calShot, 0, len(airNs))
	for k, shot := range airNs {
		cs := calShot{sound: soundMmPerNs}
		if k < len(sounds) && sounds[k] > 0 {
			cs.sound = float32(sounds[k]) * 1.0e-6
		}
		for i := 0; i < 6; i++ {
			if len(shot[i]) > 0 && enabled[i] {
				cs.seen[i] = true
				cs.raw[i] = shot[i][0]
			}
		}
		cal = append(cal, cs)
	}

	return func(dist [6]float32) float32 {
		g := micGeometryDist(p.MicLayout, dist)
		var total float32
		for _, cs := range cal {
			// corrResMM (Rest-Fehler NACH Kugeldurchmesser-Korrektur) wie die
			// Firmware - mit dem reinen Vorher-Rest-Fehler wuerde die
			// Kalibrierung SET BSHIFTPCT/BSHIFTCAP ignorieren.
			_, _, _, _, _, _, _, corrRes, ok := airPosition(cs.raw, cs.seen, g,
				cs.sound, clusterRadiusMM, p.BulletShiftPct, float32(p.BulletShiftCapMm))
			if ok {
				total += corrRes
			} else {
				total += 1000.0 // Strafe: macht Schuss unloesbar
			}
		}
		return total
	}
}

// coordinateDescentCalibrate: Optimierungskern der Kalibrierung - 1:1-Port
// von runCalibration() (Pattern-Search/Koordinatenabstieg: Mic0 fix auf 0
// als Eichfreiheitsgrad, Schrittfolge 5000*0.5^pass um ueber 11 Runden, je
// Mic und Runde der Versuch +-step, der die Kostenfunktion am staerksten
// senkt, Grenze +-MIC_DIST_MAX_UM).
func coordinateDescentCalibrate(calCost func(dist [6]float32) float32) (dist [6]float32, cost float32) {
	clamp := func(v float32) float32 {
		if v > micDistMaxUm {
			return micDistMaxUm
		}
		if v < -micDistMaxUm {
			return -micDistMaxUm
		}
		return v
	}

	const refMic = 0
	step := float32(5000.0)
	for pass := 0; pass < 11; pass++ {
		for i := 0; i < 6; i++ {
			if i == refMic {
				continue
			}
			base := dist[i]
			baseCost := calCost(dist)

			dist[i] = clamp(base + step)
			costPlus := calCost(dist)

			dist[i] = clamp(base - step)
			costMinus := calCost(dist)

			if baseCost <= costPlus && baseCost <= costMinus {
				dist[i] = base
			} else if costPlus < costMinus {
				dist[i] = clamp(base + step)
			} else {
				dist[i] = clamp(base - step)
			}
		}
		step *= 0.5
	}
	cost = calCost(dist)
	return dist, cost
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
