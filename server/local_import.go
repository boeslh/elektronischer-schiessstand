// ============================================================================
// local_import.go – Import lokal geschossener Durchgaenge eines Stands.
//
// Ein Stand-PC ohne Serververbindung (insbesondere der Standalone-Betrieb in
// der Android-App, standpc Config.Standalone) speichert jeden Durchgang mit
// eigener lokaler Session-ID, Disziplin und Zeitpunkten im Schussprotokoll.
// Der Server holt diese Durchgaenge ueber die StandPC-URL des Stands
// (GET /api/local-sessions) und uebernimmt einen davon als neue Session -
// zugeordnet einem Schuetzen (Training), einem Wettkampf-Starter oder einer
// gekauften Preisschiessen-Scheibe. Die Session liegt wie bei der manuellen
// Erfassung auf einer virtuellen Bahn (siehe manual_result.go).
//
// sessions.imported_local_sid (migrations/090) merkt sich die Herkunft:
// derselbe Durchgang wird nur nach Bestaetigung ein zweites Mal uebernommen
// (die fruehere Session wird dann abgebrochen).
// ============================================================================
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrAlreadyImported = errors.New("dieser Durchgang wurde bereits importiert")

// standpcLogShot: ein Schuss im Format des Stand-PC-Schussprotokolls
// (standpc/main.go Shot) - nur die fuer die Datenbank relevanten Felder.
type standpcLogShot struct {
	SessionID      string   `json:"session_id"`
	Seq            int      `json:"seq"`
	FiredAt        string   `json:"fired_at"`
	Hits           int      `json:"hits"`
	XUm            *int64   `json:"x_um"`
	YUm            *int64   `json:"y_um"`
	PosValid       bool     `json:"pos_valid"`
	Clean          bool     `json:"clean"`
	DeviceTsMs     *int64   `json:"device_ts_ms"`
	Zone           string   `json:"zone"`
	Algo           string   `json:"algo"`
	XMM            float64  `json:"x_mm"`
	YMM            float64  `json:"y_mm"`
	Ring           int      `json:"ring"`
	Decimal        float64  `json:"decimal"`
	InnerTen       bool     `json:"inner_ten"`
	CenterDistance float64  `json:"center_distance"`
	Mode           string   `json:"mode"`
	Discipline     string   `json:"discipline"`
	Rejected       bool     `json:"rejected"`
	Overcount      bool     `json:"overcount"`
	SeriesNo       *int     `json:"series_no"`
	SoundActMps    *float64 `json:"sound_act_mps"`
}

// LocalImportTarget: wohin ein lokaler Durchgang uebernommen wird.
type LocalImportTarget struct {
	Kind          string `json:"kind"` // training | wettkampf | preisschiessen
	ShooterID     string `json:"shooter_id"`
	DisciplineID  string `json:"discipline_id"`
	EventID       string `json:"event_id"`
	StarterID     string `json:"starter_id"`
	KaufScheibeID string `json:"kauf_scheibe_id"`
}

// ImportLocalSession legt die Ziel-Session an und schreibt die Schuesse des
// lokalen Durchgangs hinein. Verworfene und Ueberzahl-Schuesse werden nicht
// uebernommen.
func (s *Store) ImportLocalSession(ctx context.Context, localSID string, t LocalImportTarget,
	shots []standpcLogShot, confirmOverwrite bool) (string, int, error) {

	var valid []standpcLogShot
	for _, sh := range shots {
		if sh.SessionID == localSID && !sh.Rejected && !sh.Overcount {
			valid = append(valid, sh)
		}
	}
	if len(valid) == 0 {
		return "", 0, errBadRequest("der Durchgang enthält keine gültigen Schüsse")
	}

	// bereits importiert?
	var previous string
	err := s.pool.QueryRow(ctx, `
		SELECT id FROM sessions WHERE imported_local_sid=$1 AND status <> 'aborted'
		ORDER BY started_at DESC NULLS LAST LIMIT 1`, localSID).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", 0, err
	}
	if previous != "" && !confirmOverwrite {
		return "", 0, ErrAlreadyImported
	}

	var sessionID string
	switch t.Kind {
	case "training":
		if t.ShooterID == "" || t.DisciplineID == "" {
			return "", 0, errBadRequest("Schütze und Disziplin erforderlich")
		}
		sessionID, err = s.AssignVirtualLane(ctx, t.DisciplineID, t.EventID, t.ShooterID)
	case "wettkampf":
		if t.StarterID == "" {
			return "", 0, errBadRequest("Starter erforderlich")
		}
		sessionID, err = s.newStarterSession(ctx, t.StarterID, confirmOverwrite)
	case "preisschiessen":
		if t.KaufScheibeID == "" {
			return "", 0, errBadRequest("Scheibe erforderlich")
		}
		sessionID, err = s.newKaufScheibeSession(ctx, t.KaufScheibeID, confirmOverwrite)
	default:
		return "", 0, errBadRequest(`Ziel muss "training", "wettkampf" oder "preisschiessen" sein`)
	}
	if err != nil {
		return "", 0, err
	}

	if err := s.writeImportedShots(ctx, sessionID, localSID, valid); err != nil {
		s.abortOnFailure(ctx, sessionID)
		return "", 0, err
	}
	if t.Kind == "preisschiessen" {
		if err := s.linkKaufScheibe(ctx, t.KaufScheibeID, sessionID); err != nil {
			return "", 0, err
		}
	}
	if previous != "" {
		if err := s.SetSessionStatus(ctx, previous, "aborted"); err != nil {
			return "", 0, err
		}
	}
	return sessionID, len(valid), nil
}

func (s *Store) writeImportedShots(ctx context.Context, sessionID, localSID string, shots []standpcLogShot) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Serien je Probe/Wertung durchzaehlen, falls das Protokoll (aeltere
	// Stand-PC-Versionen) keine Seriennummer enthaelt.
	shotsPerSeries := 10
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(NULLIF(d.shots_per_series,0),10) FROM sessions se
		JOIN disciplines d ON d.id = se.discipline_id WHERE se.id=$1`, sessionID,
	).Scan(&shotsPerSeries); err != nil {
		return err
	}
	counter := map[string]int{}

	var first, last time.Time
	for i, sh := range shots {
		kind := "match"
		if sh.Mode == "probe" {
			kind = "sighting"
		}
		counter[kind]++
		seriesNo := (counter[kind]-1)/shotsPerSeries + 1
		if sh.SeriesNo != nil {
			seriesNo = *sh.SeriesNo
		}
		firedAt, err := time.Parse(time.RFC3339Nano, sh.FiredAt)
		if err != nil {
			return fmt.Errorf("Schuss %d: ungültiger Zeitpunkt %q", i+1, sh.FiredAt)
		}
		if first.IsZero() || firedAt.Before(first) {
			first = firedAt
		}
		if firedAt.After(last) {
			last = firedAt
		}
		var zone, algo *string
		if sh.Zone != "" {
			zone = &sh.Zone
		}
		if sh.Algo != "" {
			algo = &sh.Algo
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO shots (
				session_id, shot_no, kind, status, series_no, fired_at,
				device_seq, sensor_hits, x_um, y_um, pos_valid, clean, device_ts_ms,
				zone, algo, sound_act_mps,
				x_mm, y_mm, ring, decimal_value, is_inner_ten, center_distance
			) VALUES (
				$1::uuid, $2, $3::shot_kind, 'valid', $4, $5,
				$6, $7, $8, $9, $10, $11, $12,
				$13, $14, $15,
				$16, $17, $18, $19, $20, $21
			)`,
			sessionID, i+1, kind, seriesNo, firedAt,
			sh.Seq, sh.Hits, sh.XUm, sh.YUm, sh.PosValid, sh.Clean, sh.DeviceTsMs,
			zone, algo, sh.SoundActMps,
			sh.XMM, sh.YMM, sh.Ring, sh.Decimal, sh.InnerTen, sh.CenterDistance,
		); err != nil {
			return fmt.Errorf("Schuss %d: %w", i+1, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE sessions SET status='finished', started_at=$2, finished_at=$3, imported_local_sid=$4
		WHERE id=$1`, sessionID, first, last, localSID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// importedLocalSessions liefert zu lokalen Session-IDs die bereits daraus
// importierte (nicht abgebrochene) Session mit Schuetzenname.
func (s *Store) importedLocalSessions(ctx context.Context, sids []string) (map[string]map[string]any, error) {
	out := map[string]map[string]any{}
	if len(sids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT se.imported_local_sid, se.id::text,
		       COALESCE(sh.first_name || ' ' || sh.last_name, ''), COALESCE(d.name, ''),
		       COALESCE(ev.name, ''), se.finished_at
		FROM sessions se
		LEFT JOIN shooters sh ON sh.id = se.shooter_id
		LEFT JOIN disciplines d ON d.id = se.discipline_id
		LEFT JOIN events ev ON ev.id = se.event_id
		WHERE se.imported_local_sid = ANY($1) AND se.status <> 'aborted'`, sids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sid, id, shooter, disc, event string
		var at *time.Time
		if err := rows.Scan(&sid, &id, &shooter, &disc, &event, &at); err != nil {
			return nil, err
		}
		out[sid] = map[string]any{"session_id": id, "shooter": shooter, "discipline": disc, "event": event}
	}
	return out, rows.Err()
}

// ── HTTP ─────────────────────────────────────────────────────────────────────

func (a *APIServer) laneStandpcURL(ctx context.Context, noStr string) (string, error) {
	no, err := strconv.Atoi(noStr)
	if err != nil {
		return "", errBadRequest("ungültige Lane-Nummer")
	}
	lanes, err := a.store.ListLanes(ctx)
	if err != nil {
		return "", err
	}
	return standpcURL(lanes, no)
}

func fetchStandpcJSON(u string, out any) error {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		return fmt.Errorf("StandPC nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("StandPC: %s %s", resp.Status, b)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("StandPC-Antwort ungültig: %w", err)
	}
	return nil
}

// GET /api/lanes/{no}/local-sessions - lokale Durchgaenge des Stands, ergaenzt
// um bereits erfolgte Importe.
func (a *APIServer) proxyLocalSessions(w http.ResponseWriter, r *http.Request) (any, error) {
	base, err := a.laneStandpcURL(r.Context(), r.PathValue("no"))
	if err != nil {
		return nil, err
	}
	var list []map[string]any
	if err := fetchStandpcJSON(base+"/api/local-sessions", &list); err != nil {
		return nil, err
	}
	var sids []string
	for _, ls := range list {
		if sid, _ := ls["session_id"].(string); sid != "" {
			sids = append(sids, sid)
		}
	}
	imported, err := a.store.importedLocalSessions(r.Context(), sids)
	if err != nil {
		return nil, err
	}
	for _, ls := range list {
		sid, _ := ls["session_id"].(string)
		if imp, ok := imported[sid]; ok {
			ls["imported"] = imp
		}
	}
	if list == nil {
		list = []map[string]any{}
	}
	return list, nil
}

// POST /api/lanes/{no}/local-sessions/{sid}/import
func (a *APIServer) importLocalSession(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		LocalImportTarget
		ConfirmOverwrite bool `json:"confirm_overwrite"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungültiger Body: " + err.Error())
	}
	base, err := a.laneStandpcURL(r.Context(), r.PathValue("no"))
	if err != nil {
		return nil, err
	}
	sid := r.PathValue("sid")
	var shots []standpcLogShot
	if err := fetchStandpcJSON(base+"/api/local-sessions/"+url.PathEscape(sid)+"/shots", &shots); err != nil {
		return nil, err
	}
	sessionID, n, err := a.store.ImportLocalSession(r.Context(), sid, body.LocalImportTarget, shots, body.ConfirmOverwrite)
	if err != nil {
		if errors.Is(err, ErrAlreadyImported) {
			return nil, &httpError{code: http.StatusConflict, msg: err.Error()}
		}
		return nil, err
	}
	res := map[string]any{"session_id": sessionID, "imported": n}
	// Hinweis, wenn der Durchgang mit einer anderen Disziplin geschossen
	// wurde, als die Ziel-Session hat
	var localDisc string
	for _, sh := range shots {
		if sh.Discipline != "" {
			localDisc = sh.Discipline
			break
		}
	}
	var targetDisc string
	_ = a.store.pool.QueryRow(r.Context(), `
		SELECT d.name FROM sessions se JOIN disciplines d ON d.id=se.discipline_id WHERE se.id=$1`,
		sessionID).Scan(&targetDisc)
	if localDisc != "" && targetDisc != "" && localDisc != targetDisc {
		res["warning"] = fmt.Sprintf("Am Stand wurde %s geschossen, übernommen als %s.", localDisc, targetDisc)
	}
	return res, nil
}
