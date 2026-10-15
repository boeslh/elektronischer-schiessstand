// ============================================================================
// sim_export.go – JSON-Export einer Session aus dem Simulator fuer die
// Firmware-Entwicklung.
//
// Eine Exportdatei enthaelt je Schuss die unveraenderten Rohdaten (air_ns/
// piezo_ns, exakt das Eingabeformat von SolveShot()), die daraus mit der
// exportierten Kalibrierung abgeleiteten Eingangszeiten, das damalige
// Geraete-Ergebnis aus dem Telegramm und die Neuberechnung des Servers inkl.
// aller Zwischenwerte (3er-Kandidaten, Kugeldurchmesser-Korrektur) - also
// vollstaendige Testvektoren: eine neue Firmware-Revision kann gegen
// "sim" (Server-Port) bzw. "device" (alte Firmware) verglichen werden, ohne
// erneut schiessen zu muessen.
//
// Bewusst in voller float-Genauigkeit (mm) statt der fuer die Detailanzeige
// gerundeten Werte (siehe solveShotDetail() in api.go) - fuer den Vergleich
// mit der Firmware zaehlt jede Nachkommastelle.
// ============================================================================
package main

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"time"
)

// simExportFormatVersion: bei inkompatiblen Aenderungen der Dateistruktur
// erhoehen, damit Auswerte-Skripte alte Dateien erkennen koennen.
const simExportFormatVersion = 1

// SimExportShotMeta: Telegramm-/Metadaten eines Schusses, die der Simulator
// selbst nicht braucht (siehe SimShotRaw), fuer den Export aber relevant sind.
type SimExportShotMeta struct {
	SeriesNo    *int      `json:"series_no,omitempty"`
	FiredAt     time.Time `json:"fired_at"`
	DeviceSeq   *int      `json:"device_seq,omitempty"`
	DeviceTsMs  *int64    `json:"device_ts_ms,omitempty"`
	XUm         *int64    `json:"x_um"`
	YUm         *int64    `json:"y_um"`
	PosResUm    *int64    `json:"pos_res_um"`
	PrecisionUm *int64    `json:"precision_um"`
	ClusterHits *int      `json:"cluster_hits"`
	PosValid    *bool     `json:"pos_valid"`
	PiezoOk     *bool     `json:"piezo_ok"`
	Clean       *bool     `json:"clean"`
}

type simExportSession struct {
	ID           string     `json:"id"`
	LaneNo       *int       `json:"lane_no"`
	Discipline   string     `json:"discipline"`
	StartedAt    *time.Time `json:"started_at"`
	DeviceSerial string     `json:"device_serial,omitempty"`
	DeviceFw     string     `json:"device_fw_version,omitempty"`
}

// SimExportSessionMeta liefert Stand, Disziplin und (falls zugeordnet) das
// ESP32-Geraet einer Session.
func (s *Store) SimExportSessionMeta(ctx context.Context, sessionID string) (simExportSession, error) {
	var m simExportSession
	m.ID = sessionID
	var laneNo *int16
	err := s.pool.QueryRow(ctx, `
		SELECT l.lane_no, d.name, se.started_at,
		       COALESCE(dv.serial_no,''), COALESCE(dv.fw_version,'')
		FROM sessions se
		JOIN lanes l ON l.id = se.lane_id
		JOIN disciplines d ON d.id = se.discipline_id
		LEFT JOIN devices dv ON dv.id = se.device_id
		WHERE se.id = $1`, sessionID).Scan(&laneNo, &m.Discipline, &m.StartedAt,
		&m.DeviceSerial, &m.DeviceFw)
	if laneNo != nil {
		v := int(*laneNo)
		m.LaneNo = &v
	}
	return m, err
}

// SimExportShotMetas laedt die Telegramm-Werte (Geraete-Ergebnis) aller
// Schuesse einer Session, Schluessel shot_no.
func (s *Store) SimExportShotMetas(ctx context.Context, sessionID string) (map[int]SimExportShotMeta, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT shot_no, series_no, fired_at, device_seq, device_ts_ms,
		       x_um, y_um, pos_res_um, precision_um, cluster_hits,
		       pos_valid, piezo_ok, clean
		FROM shots WHERE session_id=$1`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]SimExportShotMeta{}
	for rows.Next() {
		var no int
		var m SimExportShotMeta
		var seriesNo, clusterHits *int16
		var deviceSeq *int32
		if err := rows.Scan(&no, &seriesNo, &m.FiredAt, &deviceSeq, &m.DeviceTsMs,
			&m.XUm, &m.YUm, &m.PosResUm, &m.PrecisionUm, &clusterHits,
			&m.PosValid, &m.PiezoOk, &m.Clean); err != nil {
			return nil, err
		}
		if seriesNo != nil {
			v := int(*seriesNo)
			m.SeriesNo = &v
		}
		if clusterHits != nil {
			v := int(*clusterHits)
			m.ClusterHits = &v
		}
		if deviceSeq != nil {
			v := int(*deviceSeq)
			m.DeviceSeq = &v
		}
		out[no] = m
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Ausgabeformat
// ---------------------------------------------------------------------------

type simExportCandidate struct {
	Ref        int     `json:"ref"`
	A          int     `json:"a"`
	B          int     `json:"b"`
	XMM        float64 `json:"x_mm"` // Blechkoordinaten, VOR OFFSETX/OFFSETY
	YMM        float64 `json:"y_mm"`
	DMM        float64 `json:"d_mm"` // Abstand Referenzmikrofon (solveAirPair)
	CorrXMM    float64 `json:"corr_x_mm"`
	CorrYMM    float64 `json:"corr_y_mm"`
	ResidualMM float64 `json:"residual_mm"`
	Best       bool    `json:"best"`
	InCluster  bool    `json:"in_cluster"`
}

type simExportBulletShift struct {
	Mic              int     `json:"mic"`
	SignedResidualMM float64 `json:"signed_residual_mm"`
	ShiftMM          float64 `json:"shift_mm"`
}

type simExportScored struct {
	SimResult
	XMM     float64 `json:"x_mm"`
	YMM     float64 `json:"y_mm"`
	Ring    int     `json:"ring"`
	Decimal float64 `json:"decimal"`
}

type simExportDevice struct {
	SimExportShotMeta
	XMM          float64 `json:"x_mm"`
	YMM          float64 `json:"y_mm"`
	Ring         int     `json:"ring"`
	Decimal      float64 `json:"decimal"`
	RejectReason string  `json:"reject_reason,omitempty"`
}

type simExportInput struct {
	// MicUsed: Mikrofon hat eine Flanke UND ist per MICEN aktiviert.
	MicUsed [6]bool `json:"mic_used"`
	// AirNsCorrected: alle Flanken abzueglich OFS0-5, weiterhin relativ zur
	// ersten erfassten Flanke.
	AirNsCorrected [6][]int64 `json:"air_ns_corrected"`
	// TFirstNs: erste Flanke je Mikrofon abzueglich OFS (= tNs[] in
	// solveAirPosition() bei ALGO=CLASSIC), null wenn nicht genutzt.
	TFirstNs [6]*int64 `json:"t_first_ns"`
}

type simExportDelta struct {
	DxUm   int64   `json:"dx_um"` // sim - device
	DyUm   int64   `json:"dy_um"`
	DistUm float64 `json:"dist_um"`
}

type simExportShot struct {
	ShotNo      int                    `json:"shot_no"`
	Kind        string                 `json:"kind"`
	Status      string                 `json:"status"`
	HasRaw      bool                   `json:"has_raw"`
	Raw         *simExportRaw          `json:"raw,omitempty"`
	Input       *simExportInput        `json:"input,omitempty"`
	Device      simExportDevice        `json:"device"`
	Sim         *simExportScored       `json:"sim,omitempty"`
	Candidates  []simExportCandidate   `json:"candidates,omitempty"`
	BulletShift []simExportBulletShift `json:"bullet_shift,omitempty"`
	Delta       *simExportDelta        `json:"delta_sim_vs_device,omitempty"`
}

type simExportRaw struct {
	AirNs   [6][]int64 `json:"air_ns"`
	PiezoNs *int64     `json:"piezo_ns"`
	// Firmware Rev 4.13-4.15 (fehlen bei aelteren Schuessen): Pulsbreiten,
	// Schallgeschwindigkeit/Erfassung/WALKK zum Schusszeitpunkt, Auswertepfad.
	AirWNs      *[6]*int64 `json:"air_w_ns,omitempty"`
	SoundActMps *float64   `json:"sound_act_mps,omitempty"`
	Capture     string     `json:"capture,omitempty"`
	WalkK       *float64   `json:"walk_k,omitempty"`
	Algo        string     `json:"algo,omitempty"`
	Raw         *RawEdges  `json:"raw,omitempty"` // Rohdaten-Telegramm (RAWLOG, Firmware 4.16.0)
}

// simExportEffective: die tatsaechlich wirksamen Werte nach allen Fallbacks
// (leere/0-Felder alter Configs), damit die Firmware-Seite nichts davon
// nachbauen muss.
type simExportEffective struct {
	Algo            string     `json:"algo"`
	ClusterRadiusUm int64      `json:"cluster_radius_um"`
	MicEnabled      [6]bool    `json:"mic_enabled"`
	UsePiezo        bool       `json:"use_piezo"`
	MinMics         int        `json:"min_mics"`
	SoundMmPerNs    float64    `json:"sound_mm_per_ns"`
	MicXMM          [6]float64 `json:"mic_x_mm"`
	MicYMM          [6]float64 `json:"mic_y_mm"`
	StandoffMM      float64    `json:"standoff_mm"`
}

func effectiveSimParams(p SimParams) simExportEffective {
	micX, micY, standoff, sound, clusterMM := solveParams(p)
	e := simExportEffective{
		Algo:            "classic", // der Simulator bildet nur ALGO=CLASSIC nach
		ClusterRadiusUm: int64(math.Round(float64(clusterMM) * 1000)),
		MicEnabled:      p.micEnabled(),
		UsePiezo:        p.usePiezo(),
		MinMics:         3,
		SoundMmPerNs:    float64(sound),
		StandoffMM:      float64(standoff),
	}
	for i := 0; i < 6; i++ {
		e.MicXMM[i] = float64(micX[i])
		e.MicYMM[i] = float64(micY[i])
	}
	return e
}

// exportTarget: TargetDef in snake_case (TargetDef selbst hat keine JSON-Tags).
func exportTarget(t *TargetDef) map[string]any {
	rings := make([]map[string]any, len(t.Rings))
	for i, r := range t.Rings {
		rings[i] = map[string]any{"value": r.Value, "diameter_mm": r.DiameterMM}
	}
	return map[string]any{
		"name": t.Name, "caliber_mm": t.CaliberMM, "edge_scoring": t.EdgeScoring,
		"inner_ten_d_mm": t.InnerTenDMM, "rings": rings,
	}
}

// buildSimExportShot rechnet einen Schuss mit p neu und fuellt alle
// Export-Felder (ohne Wertung/Delta, siehe Aufrufer).
func buildSimExportShot(sh SimShotRaw, p SimParams) simExportShot {
	out := simExportShot{ShotNo: sh.ShotNo, Kind: sh.Kind, Status: sh.Status, HasRaw: sh.HasRaw}
	if !sh.HasRaw {
		return out
	}
	out.Raw = &simExportRaw{AirNs: sh.AirNs, PiezoNs: sh.PiezoNs,
		SoundActMps: sh.SoundActMps, Capture: sh.Capture, WalkK: sh.WalkK, Algo: sh.Algo, Raw: sh.Raw}
	for _, w := range sh.AirWNs {
		if w != nil {
			ws := sh.AirWNs
			out.Raw.AirWNs = &ws
			break
		}
	}

	// Eingabe der Loesung: Rohzeiten inkl. Pulsbreiten-Korrektur (WALKK),
	// abzueglich Mikrofon-Offsets - genau wie in solveShotInternal.
	enabled := p.micEnabled()
	air := sh.airFor(p)
	in := &simExportInput{}
	for i := 0; i < 6; i++ {
		in.AirNsCorrected[i] = make([]int64, len(air[i]))
		for k, t := range air[i] {
			in.AirNsCorrected[i][k] = t - p.MicOffsetNs[i]
		}
		if len(air[i]) > 0 && enabled[i] {
			in.MicUsed[i] = true
			t := in.AirNsCorrected[i][0]
			in.TFirstNs[i] = &t
		}
	}
	out.Input = in

	res, cands, bshift := SolveSimShotDetail(sh, p)
	out.Sim = &simExportScored{SimResult: res}
	for _, c := range cands {
		out.Candidates = append(out.Candidates, simExportCandidate{
			Ref: c.Ref, A: c.A, B: c.B,
			XMM: float64(c.X), YMM: float64(c.Y), DMM: float64(c.D),
			CorrXMM: float64(c.CorrX), CorrYMM: float64(c.CorrY),
			ResidualMM: float64(c.ResidualMM), Best: c.Best, InCluster: c.InCluster,
		})
	}
	for _, b := range bshift {
		out.BulletShift = append(out.BulletShift, simExportBulletShift{
			Mic: b.Mic, SignedResidualMM: float64(b.SignedResidualMM), ShiftMM: float64(b.ShiftMM),
		})
	}
	return out
}

// exportSimulation: POST /api/sessions/{id}/simulator-export - liefert die
// komplette Session als Datei-Download (Content-Disposition), Parameter
// wie bei /simulate. set_commands (optional) kommen aus der Simulator-UI
// (buildSetCommands()), damit die SET-Befehlsliste nicht doppelt gepflegt
// werden muss.
func (a *APIServer) exportSimulation(w http.ResponseWriter, r *http.Request) (any, error) {
	sessionID := r.PathValue("id")
	body, err := decodeBody[struct {
		Params      SimParams `json:"params"`
		SetCommands []string  `json:"set_commands"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body: " + err.Error())
	}
	ctx := r.Context()
	p := body.Params

	meta, err := a.store.SimExportSessionMeta(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	targetID, sightingTargetID, shotsPerSeries, _, err := a.store.SessionTargets(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	matchTarget, err := a.store.LoadTargetDef(ctx, targetID)
	if err != nil {
		return nil, err
	}
	sightingTarget := matchTarget
	if sightingTargetID != "" {
		if sightingTarget, err = a.store.LoadTargetDef(ctx, sightingTargetID); err != nil {
			return nil, err
		}
	}
	scorerMatch, scorerSighting := NewScorer(matchTarget), NewScorer(sightingTarget)

	shots, err := a.store.SessionShotsRaw(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	metas, err := a.store.SimExportShotMetas(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	var esp32Backup *ESP32Calibration
	if meta.LaneNo != nil {
		// Kein Backup vorhanden ist der Normalfall bei Staenden ohne CAL
		// START - Fehler bewusst ignoriert.
		esp32Backup, _ = a.store.GetESP32CalibrationByLane(ctx, *meta.LaneNo)
	}

	var nRaw, nSimValid, nDevValid, nBoth int
	var sumDist, maxDist float64
	outShots := make([]simExportShot, 0, len(shots))
	for _, sh := range shots {
		sc := scorerMatch
		if sh.Kind == "sighting" {
			sc = scorerSighting
		}
		es := buildSimExportShot(sh, p)
		es.Device = simExportDevice{
			SimExportShotMeta: metas[sh.ShotNo],
			XMM:               sh.XMM, YMM: sh.YMM, Ring: sh.Ring, Decimal: sh.Decimal,
			RejectReason: sh.RejectReason,
		}
		if sh.HasRaw {
			nRaw++
		}
		if es.Sim != nil && es.Sim.PosValid {
			nSimValid++
			es.Sim.XMM = float64(es.Sim.XUm) / 1000.0
			es.Sim.YMM = float64(es.Sim.YUm) / 1000.0
			score := sc.Score(es.Sim.XMM, es.Sim.YMM)
			es.Sim.Ring, es.Sim.Decimal = score.Ring, score.Decimal
		}
		d := es.Device
		devValid := d.PosValid != nil && *d.PosValid && d.XUm != nil && d.YUm != nil
		if devValid {
			nDevValid++
		}
		if devValid && es.Sim != nil && es.Sim.PosValid {
			dx, dy := es.Sim.XUm-*d.XUm, es.Sim.YUm-*d.YUm
			dist := math.Round(math.Hypot(float64(dx), float64(dy))*10) / 10
			es.Delta = &simExportDelta{DxUm: dx, DyUm: dy, DistUm: dist}
			nBoth++
			sumDist += dist
			maxDist = math.Max(maxDist, dist)
		}
		outShots = append(outShots, es)
	}
	summary := map[string]any{
		"shots_total":       len(shots),
		"shots_with_raw":    nRaw,
		"sim_valid":         nSimValid,
		"device_valid":      nDevValid,
		"compared":          nBoth,
		"max_delta_dist_um": maxDist,
	}
	if nBoth > 0 {
		summary["mean_delta_dist_um"] = math.Round(sumDist/float64(nBoth)*10) / 10
	}

	laneStr := "x"
	if meta.LaneNo != nil {
		laneStr = fmt.Sprint(*meta.LaneNo)
	}
	stamp := time.Now()
	if meta.StartedAt != nil {
		stamp = *meta.StartedAt
	}
	fname := fmt.Sprintf("sim-export_stand%s_%s.json", laneStr, stamp.Local().Format("2006-01-02_1504"))
	w.Header().Set("Content-Disposition", `attachment; filename="`+fname+`"`)

	return map[string]any{
		"format":         "schiessstand-simulator-export",
		"format_version": simExportFormatVersion,
		"exported_at":    time.Now(),
		"units": map[string]string{
			"*_ns":    "Nanosekunden, relativ zur ersten erfassten Flanke (wie im Telegramm)",
			"*_um":    "0.001 mm (Scheibenkoordinaten inkl. OFFSETX/OFFSETY, wie im Telegramm)",
			"*_mm":    "Millimeter; candidates/bullet_shift in Blechkoordinaten VOR OFFSETX/OFFSETY",
			"mic_*":   "Mikrofonindex 0-5 (0/1 unten, 2/3 oben, 4/5 Mitte; gerade=links, ungerade=rechts)",
			"sim":     "Neuberechnung des Servers (Port der Firmware, float32) mit calibration.params",
			"device":  "Ergebnis des ESP32 aus dem damaligen Telegramm (mit der DAMALS aktiven Konfiguration)",
			"delta_*": "sim - device",
			"coord":   "x nach rechts, y nach oben, Ursprung Scheibenmitte",
		},
		"session": map[string]any{
			"id":                meta.ID,
			"lane_no":           meta.LaneNo,
			"discipline":        meta.Discipline,
			"started_at":        meta.StartedAt,
			"device_serial":     meta.DeviceSerial,
			"device_fw_version": meta.DeviceFw,
			"shots_per_series":  shotsPerSeries,
			"target":            exportTarget(matchTarget),
			"sighting_target":   exportTarget(sightingTarget),
		},
		"calibration": map[string]any{
			"params":       p,
			"effective":    effectiveSimParams(p),
			"set_commands": body.SetCommands,
			"esp32_backup": esp32Backup,
		},
		"summary": summary,
		"shots":   outShots,
	}, nil
}
