// ============================================================================
// calibration_transfer.go – Kalibrierung vom Simulator auf einen Stand
// uebertragen, ohne serielle Verbindung zum ESP32.
//
// Ablauf: Der Simulator gibt einen Parametersatz fuer einen Stand frei
// (POST /api/lanes/{no}/calibration-transfer). Die Admin-Oberflaeche des
// Stand-PCs holt ihn ab (GET ..., unauthentifiziert wie die uebrigen Stand-
// PC-Endpunkte, z.B. /api/esp32-calibrations), zeigt die Unterschiede zur
// aktuellen Geraetekonfiguration und schreibt sie auf Knopfdruck per
// CAL IMPORT (OFS0-5 + SOUNDSPEED atomar) und SET auf den ESP32 (siehe
// standpc/calibration_transfer.go). Danach meldet er das Ergebnis zurueck
// (POST .../applied).
//
// Der Server liefert dem Stand-PC bereits die fertige Liste aus Firmware-
// Schluessel und SET-Wert (firmwareSettings): nur hier sind die Fallbacks
// der Simulator-Parameter bekannt (z.B. RADIUS 0 -> 200, siehe SimParams), und die Formatierung entspricht exakt der
// SET-Befehls-Ausgabe des Simulators (buildSetCommands in simulator.html).
// ============================================================================
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// FirmwareSetting: ein Firmware-Parameter mit dem Wert, wie er per
// "SET <Key>=<Value>" bzw. im CAL IMPORT gesendet wird.
type FirmwareSetting struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// firmwareSettings uebersetzt SimParams in Firmware-Einstellungen - Reihen-
// folge und Formate wie buildSetCommands() im Simulator.
func firmwareSettings(p SimParams) []FirmwareSetting {
	f2 := func(v float64) string { return strconv.FormatFloat(math.Round(v*100)/100, 'f', 2, 64) }
	i := func(v int64) string { return strconv.FormatInt(v, 10) }
	_, _, _, _, clusterMM := solveParams(p) // RADIUS inkl. Fallback 0 -> Firmware-Default
	s := []FirmwareSetting{
		{"STANDOFFPAPER", f2(p.StandoffPaperMM)},
		{"MICHALFX", f2(p.MicHalfXMm)},
		{"OFFSETX", i(p.OffsetXUm)},
		{"OFFSETY", i(p.OffsetYUm)},
		// eine Nachkommastelle (Firmware ab 4.15.0) - fuer aeltere Firmware
		// rundet der Stand-PC ganzzahlig (standpc targetForDevice)
		{"SOUNDSPEED", strconv.FormatFloat(math.Round(p.SoundMps*10)/10, 'f', 1, 64)},
	}
	for k := 0; k < 6; k++ {
		s = append(s, FirmwareSetting{fmt.Sprintf("OFS%d", k), i(p.MicOffsetNs[k])})
	}
	s = append(s,
		FirmwareSetting{"BSHIFTPCT", i(int64(p.BulletShiftPct))},
		FirmwareSetting{"BSHIFTCAP", f2(p.BulletShiftCapMm)},
		FirmwareSetting{"RADIUS", i(int64(math.Round(float64(clusterMM) * 1000)))},
	)
	// Endpositions-Verfahren (Firmware Rev 4.12.0) - "nach Rest-Fehler
	// gewichten" (PosAlgo.Weighted) gibt es nur im Simulator. Aeltere
	// Firmware kennt die Schluessel nicht; der Stand-PC ueberspringt sie
	// dann (standpc/calibration_transfer.go firmwareOptionalKeys).
	b01 := func(v bool) string {
		if v {
			return "1"
		}
		return "0"
	}
	iter := p.PosAlgo.Iterations
	if iter < 0 {
		iter = 0
	} else if iter > 10 {
		iter = 10
	}
	s = append(s,
		FirmwareSetting{"CLUSTERCORR", b01(p.PosAlgo.CorrCluster)},
		FirmwareSetting{"CLUSTERITER", i(int64(iter))},
		FirmwareSetting{"LSQ", b01(p.PosAlgo.LSQ)},
		FirmwareSetting{"LSQPELLETR", f2(p.PosAlgo.LSQPelletRMm)},
	)
	en := p.micEnabled()
	for k := 0; k < 6; k++ {
		v := "0"
		if en[k] {
			v = "1"
		}
		s = append(s, FirmwareSetting{fmt.Sprintf("MICEN%d", k), v})
	}
	piezo := "0"
	if p.usePiezo() {
		piezo = "1"
	}
	s = append(s,
		// Der Simulator bildet nur ALGO=CLASSIC nach - das Geraet wird daher
		// immer auf CLASSIC gestellt (die RIM-Parameter PELLETR/MAXSIGMA/
		// MINMICS bleiben unberuehrt).
		FirmwareSetting{"ALGO", "CLASSIC"},
		FirmwareSetting{"PIEZOMIN", i(int64(p.PiezoMinUs))},
		FirmwareSetting{"PIEZOMAX", i(int64(p.PiezoMaxUs))},
		FirmwareSetting{"PIEZO", piezo},
	)
	// Pulsbreiten-Korrektur (Firmware Rev 4.14.0) nur, wenn im Simulator
	// gesetzt: ein Parametersatz ohne WALKK (z.B. aus der Zeit vor 4.14)
	// soll ein am Geraet per WALKCAL bestimmtes WALKK nicht auf 0 setzen.
	if p.WalkK > 0 {
		s = append(s, FirmwareSetting{"WALKK", strconv.FormatFloat(math.Round(p.WalkK*1000)/1000, 'f', 3, 64)})
	}
	return append(s, zoneFirmwareSettings(p)...)
}

// zoneFirmwareSettings: Zonen-Kalibrierung (sim_zones.go, Firmware Rev
// 4.17.0). Den Lochradius (ZHOLE) setzt der Stand-PC je Scheibentyp selbst;
// uebertragen werden Bandbreite, Ueberblendung und die Saetze A/B/C (als
// Schluessel ZCALA/ZCALB/ZCALC, der Stand-PC macht daraus SET ZCAL=<Zone>,..)
// und zuletzt ZONES. Ein Parametersatz ohne Zonen schaltet sie ab - die
// Saetze am Geraet passen dann nicht mehr zu den neuen Basiswerten. Aeltere
// Firmware kennt die Schluessel nicht (Stand-PC: firmwareOptionalKeys).
func zoneFirmwareSettings(p SimParams) []FirmwareSetting {
	if !p.zonedEnabled() {
		return []FirmwareSetting{{"ZONES", "0"}}
	}
	z := p.Zones
	um := func(mm float64) string { return strconv.FormatInt(int64(math.Round(mm*1000)), 10) }
	return []FirmwareSetting{
		{"ZBAND", um(z.bandHalf())},
		{"ZBLEND", um(z.blend())},
		{"ZCALA", zcalValue(z.A)},
		{"ZCALB", zcalValue(z.B)},
		{"ZCALC", zcalValue(z.C)},
		{"ZONES", "1"},
	}
}

// zcalValue: Wert fuer SET ZCAL=<Zone>,<Wert> (Format wie standpc
// ZoneSetValue.zcalValue, damit der Vergleich am Stand-PC greift).
func zcalValue(s *ZoneSet) string {
	if s == nil {
		return "OFF"
	}
	parts := []string{strconv.FormatFloat(math.Round(s.SoundMps*10)/10, 'f', 1, 64)}
	for _, o := range s.MicOffsetNs {
		parts = append(parts, strconv.FormatInt(o, 10))
	}
	parts = append(parts, strconv.FormatInt(s.OffsetXUm, 10), strconv.FormatInt(s.OffsetYUm, 10))
	return strings.Join(parts, ",")
}

type CalibrationTransfer struct {
	LaneNo   int               `json:"lane_no"`
	Params   SimParams         `json:"params"`
	Settings []FirmwareSetting `json:"settings"`
	// Capture: Erfassungsmodus der Schuesse, aus denen die Parameter bestimmt
	// wurden (Params.Capture) - der Stand-PC uebernimmt nur auf ein Geraet
	// im selben Modus (standpc/calibration_transfer.go).
	Capture       string          `json:"capture"`
	Note          string          `json:"note"`
	CreatedBy     string          `json:"created_by"`
	CreatedAt     time.Time       `json:"created_at"`
	AppliedAt     *time.Time      `json:"applied_at"`
	AppliedMAC    *string         `json:"applied_mac"`
	AppliedOK     *bool           `json:"applied_ok"`
	AppliedResult json.RawMessage `json:"applied_result,omitempty"`
}

func (s *Store) PutCalibrationTransfer(ctx context.Context, laneNo int, p SimParams, note, actor string) (CalibrationTransfer, error) {
	return s.putCalibrationTransfer(ctx, laneNo, p, note, actor, true)
}

// putCalibrationTransfer: saveCopy=false, wenn die Parameter bereits eine
// gespeicherte Kalibrierung sind (Auswahl am Stand-PC).
func (s *Store) putCalibrationTransfer(ctx context.Context, laneNo int, p SimParams, note, actor string, saveCopy bool) (CalibrationTransfer, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return CalibrationTransfer{}, err
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO lane_calibration_transfers (lane_no, params, note, created_by, created_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (lane_no) DO UPDATE SET
		  params=EXCLUDED.params, note=EXCLUDED.note, created_by=EXCLUDED.created_by, created_at=now(),
		  applied_at=NULL, applied_mac=NULL, applied_ok=NULL, applied_result=NULL`,
		laneNo, raw, note, actor); err != nil {
		return CalibrationTransfer{}, err
	}
	// Zusaetzlich als gespeicherte Simulator-Kalibrierung ablegen: die
	// Freigabe selbst existiert je Stand nur einmal (wird bei der naechsten
	// Freigabe ersetzt) und war im Simulator bislang nicht wieder ladbar.
	if saveCopy {
		if _, err := s.SaveSimulatorConfig(ctx, transferConfigName(laneNo, note, time.Now()), &laneNo, p); err != nil {
			return CalibrationTransfer{}, fmt.Errorf("als gespeicherte Kalibrierung ablegen: %w", err)
		}
	}
	return s.GetCalibrationTransfer(ctx, laneNo)
}

// laneConfigInfo: gespeicherte Kalibrierung in der Auswahlliste des
// Stand-PCs (ohne die vollstaendigen Parameter).
type laneConfigInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	Capture   string `json:"capture"` // "isr" | "mcpwm" | "" (unbekannt)
	Zones     bool   `json:"zones"`
}

// listLaneSimulatorConfigs: GET /api/lanes/{no}/simulator-configs - die fuer
// diesen Stand gespeicherten Kalibrierungen, neueste zuerst (fuer die Auswahl
// in der Admin-Oberflaeche des Stand-PCs; wie die Freigabe selbst ohne
// Rollenpruefung lesbar).
func (a *APIServer) listLaneSimulatorConfigs(w http.ResponseWriter, r *http.Request) (any, error) {
	laneNo, err := laneNoParam(r)
	if err != nil {
		return nil, err
	}
	all, err := a.store.ListSimulatorConfigs(r.Context())
	if err != nil {
		return nil, err
	}
	out := []laneConfigInfo{}
	for _, c := range all {
		if c.LaneNo == nil || *c.LaneNo != laneNo {
			continue
		}
		out = append(out, laneConfigInfo{ID: c.ID, Name: c.Name, CreatedAt: c.CreatedAt, Capture: c.Params.Capture, Zones: c.Params.zonedEnabled()})
	}
	return out, nil
}

// postCalibrationTransferFromConfig: POST /api/lanes/{no}/calibration-
// transfer/from-config {id} - am Stand-PC gewaehlte gespeicherte
// Kalibrierung dieses Stands als Freigabe setzen (ersetzt die bisherige
// Freigabe; keine weitere Kopie in den gespeicherten Kalibrierungen). Nur
// Kalibrierungen DIESES Stands sind waehlbar.
func (a *APIServer) postCalibrationTransferFromConfig(w http.ResponseWriter, r *http.Request) (any, error) {
	laneNo, err := laneNoParam(r)
	if err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		ID string `json:"id"`
	}](r)
	if err != nil || body.ID == "" {
		return nil, errBadRequest("id erforderlich")
	}
	c, err := a.store.GetSimulatorConfig(r.Context(), body.ID)
	if err != nil {
		return nil, err
	}
	if c == nil || c.LaneNo == nil || *c.LaneNo != laneNo {
		return nil, &httpError{code: 404, msg: "Kalibrierung fuer diesen Stand nicht gefunden"}
	}
	return a.store.putCalibrationTransfer(r.Context(), laneNo, c.Params, c.Name, "Stand-PC", false)
}

// transferConfigName: Name der zur Freigabe mitgespeicherten Kalibrierung
// (simulator_configs.name ist eindeutig - daher sekundengenauer Zeitstempel).
func transferConfigName(laneNo int, note string, t time.Time) string {
	name := fmt.Sprintf("Freigabe Stand %d %s", laneNo, t.Format("02.01.2006 15:04:05"))
	if note != "" {
		name += " – " + note
	}
	return name
}

func (s *Store) GetCalibrationTransfer(ctx context.Context, laneNo int) (CalibrationTransfer, error) {
	t := CalibrationTransfer{LaneNo: laneNo}
	var raw []byte
	err := s.pool.QueryRow(ctx, `
		SELECT params, note, created_by, created_at, applied_at, applied_mac, applied_ok, applied_result
		FROM lane_calibration_transfers WHERE lane_no=$1`, laneNo).Scan(
		&raw, &t.Note, &t.CreatedBy, &t.CreatedAt, &t.AppliedAt, &t.AppliedMAC, &t.AppliedOK, &t.AppliedResult)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, &httpError{code: 404, msg: fmt.Sprintf("keine Kalibrierung fuer Stand %d freigegeben", laneNo)}
	}
	if err != nil {
		return t, err
	}
	if err := json.Unmarshal(raw, &t.Params); err != nil {
		return t, err
	}
	t.Settings = firmwareSettings(t.Params)
	t.Capture = t.Params.Capture
	return t, nil
}

// MarkCalibrationTransferApplied haelt das Ergebnis der Uebernahme fest -
// nur fuer genau die Freigabe, die der Stand-PC angezeigt bekam
// (createdAt), damit eine inzwischen neuere Freigabe nicht faelschlich als
// uebernommen gilt.
func (s *Store) MarkCalibrationTransferApplied(ctx context.Context, laneNo int, createdAt time.Time, mac string, ok bool, result json.RawMessage) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE lane_calibration_transfers
		SET applied_at=now(), applied_mac=$3, applied_ok=$4, applied_result=$5
		WHERE lane_no=$1 AND created_at=$2`, laneNo, createdAt, mac, ok, result)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return &httpError{code: 409, msg: "Freigabe wurde inzwischen ersetzt"}
	}
	return nil
}

func laneNoParam(r *http.Request) (int, error) {
	n, err := strconv.Atoi(r.PathValue("no"))
	if err != nil || n < 1 {
		return 0, errBadRequest("ungueltige Standnummer")
	}
	return n, nil
}

func (a *APIServer) putCalibrationTransfer(w http.ResponseWriter, r *http.Request) (any, error) {
	role, err := a.requireTile(w, r, "simulator")
	if err != nil {
		return nil, err
	}
	laneNo, err := laneNoParam(r)
	if err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		Params SimParams `json:"params"`
		Note   string    `json:"note"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body: " + err.Error())
	}
	return a.store.PutCalibrationTransfer(r.Context(), laneNo, body.Params, body.Note, role.RoleKey)
}

func (a *APIServer) getCalibrationTransfer(w http.ResponseWriter, r *http.Request) (any, error) {
	laneNo, err := laneNoParam(r)
	if err != nil {
		return nil, err
	}
	return a.store.GetCalibrationTransfer(r.Context(), laneNo)
}

func (a *APIServer) postCalibrationTransferApplied(w http.ResponseWriter, r *http.Request) (any, error) {
	laneNo, err := laneNoParam(r)
	if err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		CreatedAt time.Time       `json:"created_at"`
		MAC       string          `json:"mac"`
		OK        bool            `json:"ok"`
		Result    json.RawMessage `json:"result"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body: " + err.Error())
	}
	if len(body.Result) == 0 {
		body.Result = json.RawMessage("null")
	}
	if err := a.store.MarkCalibrationTransferApplied(r.Context(), laneNo, body.CreatedAt, body.MAC, body.OK, body.Result); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}
