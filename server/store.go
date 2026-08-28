// ============================================================================
// store.go – Datenbankzugriff des Servers
//
// Arbeitet direkt auf dem zentralen Datenmodell (datenmodell.sql).
// Alle Steuerungs-Operationen schreiben in den audit_log.
// ============================================================================
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

// ----------------------------------------------------------------------------
// DTOs (JSON-Form fuer die API)
// ----------------------------------------------------------------------------

type Lane struct {
	ID         string `json:"id"`
	LaneNo     int    `json:"lane_no"`
	Name       string `json:"name,omitempty"`
	Active     bool   `json:"active"`
	StandPCURL string `json:"standpc_url,omitempty"` // HTTP-Basis-URL des StandPC, z.B. http://192.168.1.10:8080
	// Belegung (NULL-Felder leer, wenn frei)
	SessionID    string `json:"session_id,omitempty"`
	SessionState string `json:"session_state,omitempty"`
	ShooterID    string `json:"shooter_id,omitempty"`
	ShooterName  string `json:"shooter_name,omitempty"`
	Discipline   string `json:"discipline,omitempty"`
	ShotCount    int    `json:"shot_count"`
	TotalRings   int    `json:"total_rings"`
	// Veranstaltung (aus events-Tabelle via session.event_id)
	EventName string `json:"event_name,omitempty"`
	EventType string `json:"event_type,omitempty"` // einzel | runde | gruppe
	// Live-Zustand vom StandPC (in-memory, nicht aus DB)
	LiveMode         string  `json:"live_mode,omitempty"`
	LiveWertungCount int     `json:"live_wertung_count,omitempty"`
	LiveTotalRings   int     `json:"live_total_rings,omitempty"`
	LiveTotalDecimal float64 `json:"live_total_decimal,omitempty"`
	LiveLastSeen     int64   `json:"live_last_seen,omitempty"` // Unix-Sekunden; 0 = nie gemeldet
}

// LaneLiveState wird vom StandPC per PUT /api/lanes/{no}/livestate gemeldet.
type LaneLiveState struct {
	Mode         string    `json:"mode"`
	WertungCount int       `json:"wertung_count"`
	TotalRings   int       `json:"total_rings"`
	TotalDecimal float64   `json:"total_decimal"`
	LastSeenAt   time.Time `json:"-"` // serverseitig gesetzt, nicht vom StandPC
	StandPCURL   string    `json:"-"` // zuletzt gemeldete URL (für Change-Detection)
}

type Shooter struct {
	ID        string `json:"id"`
	LastName  string `json:"last_name"`
	FirstName string `json:"first_name"`
	PassNo    string `json:"pass_no,omitempty"`
	Club      string `json:"club,omitempty"`
}

type Discipline struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	RuleNo         string `json:"rule_no,omitempty"`
	MatchShotCount int    `json:"match_shot_count"`
}

type SessionInfo struct {
	ID           string `json:"id"`
	LaneNo       int    `json:"lane_no"`
	Status       string `json:"status"`
	ShooterID    string `json:"shooter_id,omitempty"`
	ShooterName  string `json:"shooter_name,omitempty"`
	DisciplineID string `json:"discipline_id"`
	Discipline   string `json:"discipline"`
	StartedAt    string `json:"started_at,omitempty"`
}

// ----------------------------------------------------------------------------
// Staende
// ----------------------------------------------------------------------------

func (s *Store) ListLanes(ctx context.Context) ([]Lane, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT l.id, l.lane_no, COALESCE(l.name,''), l.active, COALESCE(l.standpc_url,''),
		       COALESCE(se.id::text,''), COALESCE(se.status::text,''),
		       COALESCE(se.shooter_id::text,''),
		       COALESCE(sh.last_name || ', ' || sh.first_name, ''),
		       COALESCE(d.name,''),
		       COALESCE(r.shot_count,0), COALESCE(r.total_rings,0),
		       COALESCE(ev.name,''), COALESCE(ev.type::text,'')
		FROM lanes l
		LEFT JOIN LATERAL (
		    SELECT * FROM sessions
		    WHERE lane_id = l.id
		      AND status IN ('assigned','sighting','match','paused')
		    ORDER BY started_at DESC NULLS LAST LIMIT 1
		) se ON true
		LEFT JOIN shooters sh    ON sh.id  = se.shooter_id
		LEFT JOIN disciplines d  ON d.id   = se.discipline_id
		LEFT JOIN events ev      ON ev.id  = se.event_id
		LEFT JOIN v_session_results r ON r.session_id = se.id
		ORDER BY l.lane_no`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Lane
	for rows.Next() {
		var l Lane
		if err := rows.Scan(&l.ID, &l.LaneNo, &l.Name, &l.Active, &l.StandPCURL,
			&l.SessionID, &l.SessionState, &l.ShooterID, &l.ShooterName, &l.Discipline,
			&l.ShotCount, &l.TotalRings, &l.EventName, &l.EventType); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) SetLaneStandpcURL(ctx context.Context, laneNo int, url string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE lanes SET standpc_url=$1 WHERE lane_no=$2`, url, laneNo)
	return err
}

// TransferSession verschiebt eine Session von ihrer aktuellen Lane auf eine andere.
// Die Ziel-Lane darf keine aktive Session haben.
func (s *Store) TransferSession(ctx context.Context, sessionID string, toLaneNo int) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE sessions SET lane_id = (
			SELECT id FROM lanes WHERE lane_no = $2 AND active
		)
		WHERE id = $1::uuid
		  AND status IN ('assigned','sighting','match','paused')`,
		sessionID, toLaneNo)
	return err
}

// ImportShot beschreibt einen einzelnen importierten Schuss.
type ImportShot struct {
	ShotNo    int     `json:"shot_no"`
	Kind      string  `json:"kind"`      // "probe" | "match"
	FiredAt   string  `json:"fired_at"`  // RFC3339
	XMM       float64 `json:"x_mm"`
	YMM       float64 `json:"y_mm"`
	Ring      int     `json:"ring"`
	Decimal   float64 `json:"decimal"`
	InnerTen  bool    `json:"inner_ten"`
	CenterDistance float64 `json:"center_distance"`
	Seq       int     `json:"seq"`
	RawTNs    []int64 `json:"raw_t_ns"`
	SensorHits int    `json:"sensor_hits"`
	Confidence float64 `json:"confidence"`
}

// ImportShots schreibt Schüsse aus einem lokalen JSONL-Log in die Datenbank.
// sessionID ist die DB-Session (muss 'assigned' oder 'sighting'/'match' sein).
func (s *Store) ImportShots(ctx context.Context, sessionID string, shots []ImportShot) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, sh := range shots {
		kind := "match"
		if sh.Kind == "probe" {
			kind = "sighting"
		}
		rawTNs := sh.RawTNs
		if rawTNs == nil {
			rawTNs = []int64{}
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO shots (
				session_id, shot_no, kind, status, fired_at,
				device_seq, raw_t_ns, sensor_hits, confidence,
				x_mm, y_mm, ring, decimal_value, is_inner_ten, center_distance
			) VALUES (
				$1::uuid, $2, $3::shot_kind, 'valid', $4::timestamptz,
				$5, $6, $7, $8,
				$9, $10, $11, $12, $13, $14
			) ON CONFLICT (session_id, shot_no) DO NOTHING`,
			sessionID, sh.ShotNo, kind, sh.FiredAt,
			sh.Seq, rawTNs, sh.SensorHits, sh.Confidence,
			sh.XMM, sh.YMM, sh.Ring, sh.Decimal, sh.InnerTen, sh.CenterDistance)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) EnsureLanes(ctx context.Context, count int) error {
	for n := 1; n <= count; n++ {
		_, err := s.pool.Exec(ctx, `
			INSERT INTO lanes (lane_no) VALUES ($1)
			ON CONFLICT (lane_no) DO NOTHING`, n)
		if err != nil {
			return err
		}
	}
	return nil
}

// ----------------------------------------------------------------------------
// Stammdaten
// ----------------------------------------------------------------------------

func (s *Store) ListShooters(ctx context.Context, search string) ([]Shooter, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sh.id, sh.last_name, sh.first_name,
		       COALESCE(sh.pass_no,''), COALESCE(c.name,'')
		FROM shooters sh LEFT JOIN clubs c ON c.id = sh.club_id
		WHERE $1 = '' OR sh.last_name ILIKE '%'||$1||'%'
		   OR sh.first_name ILIKE '%'||$1||'%'
		ORDER BY sh.last_name, sh.first_name LIMIT 100`, search)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Shooter
	for rows.Next() {
		var sh Shooter
		if err := rows.Scan(&sh.ID, &sh.LastName, &sh.FirstName,
			&sh.PassNo, &sh.Club); err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

func (s *Store) CreateShooter(ctx context.Context, last, first, passNo string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO shooters (last_name, first_name, pass_no)
		VALUES ($1, $2, NULLIF($3,'')) RETURNING id`,
		last, first, passNo).Scan(&id)
	return id, err
}

func (s *Store) ListDisciplines(ctx context.Context) ([]Discipline, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, COALESCE(rule_no,''), match_shot_count
		FROM disciplines WHERE active ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Discipline
	for rows.Next() {
		var d Discipline
		if err := rows.Scan(&d.ID, &d.Name, &d.RuleNo, &d.MatchShotCount); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ----------------------------------------------------------------------------
// Disziplinen-Verwaltung (CRUD)
// ----------------------------------------------------------------------------

// DisciplineFull enthaelt alle verwaltungsrelevanten Felder einer Disziplin.
type DisciplineFull struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	RuleNo           string  `json:"rule_no"`
	DistanceM        float64 `json:"distance_m"`
	TargetID         string  `json:"target_id"`
	TargetName       string  `json:"target_name"`
	MatchShotCount   int     `json:"match_shot_count"`
	MaxSightingShots *int    `json:"max_sighting_shots"` // nil = unbegrenzt
	ShotsPerSeries   int     `json:"shots_per_series"`
	DecimalScoring   bool    `json:"decimal_scoring"`
	MatchTimeS         *int   `json:"match_time_s"` // nil = keine Begrenzung
	Active             bool   `json:"active"`
	Notes              string `json:"notes"`
	StandPCTargetNo    int    `json:"standpc_target_no"` // Scheibenummer im StandPC (0 = nicht gesetzt)
}

// TargetRef wird fuer Auswahlfelder in der Disziplin-Verwaltung benoetigt.
type TargetRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Store) ListTargets(ctx context.Context) ([]TargetRef, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name FROM targets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TargetRef
	for rows.Next() {
		var t TargetRef
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) ListDisciplinesFull(ctx context.Context) ([]DisciplineFull, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT d.id, d.name, COALESCE(d.rule_no,''),
		       d.distance_m, d.target_id, COALESCE(t.name,''),
		       d.match_shot_count, d.max_sighting_shots,
		       d.shots_per_series, d.decimal_scoring,
		       d.match_time_s, d.active, COALESCE(d.notes,''),
		       d.standpc_target_no
		FROM disciplines d
		LEFT JOIN targets t ON t.id = d.target_id
		ORDER BY d.active DESC, d.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DisciplineFull
	for rows.Next() {
		var d DisciplineFull
		if err := rows.Scan(
			&d.ID, &d.Name, &d.RuleNo,
			&d.DistanceM, &d.TargetID, &d.TargetName,
			&d.MatchShotCount, &d.MaxSightingShots,
			&d.ShotsPerSeries, &d.DecimalScoring,
			&d.MatchTimeS, &d.Active, &d.Notes,
			&d.StandPCTargetNo,
		); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) CreateDiscipline(ctx context.Context, d DisciplineFull) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO disciplines
		  (name, rule_no, distance_m, target_id,
		   match_shot_count, max_sighting_shots, shots_per_series,
		   decimal_scoring, match_time_s, active, notes)
		VALUES ($1, NULLIF($2,''), $3, $4,
		        $5, $6, $7, $8, $9, $10, NULLIF($11,''))
		RETURNING id`,
		d.Name, d.RuleNo, d.DistanceM, d.TargetID,
		d.MatchShotCount, d.MaxSightingShots, d.ShotsPerSeries,
		d.DecimalScoring, d.MatchTimeS, d.Active, d.Notes,
	).Scan(&id)
	return id, err
}

func (s *Store) UpdateDiscipline(ctx context.Context, d DisciplineFull) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE disciplines SET
		  name               = $1,
		  rule_no            = NULLIF($2,''),
		  distance_m         = $3,
		  target_id          = $4,
		  match_shot_count   = $5,
		  max_sighting_shots = $6,
		  shots_per_series   = $7,
		  decimal_scoring    = $8,
		  match_time_s       = $9,
		  active             = $10,
		  notes              = NULLIF($11,''),
		  standpc_target_no  = $13
		WHERE id = $12`,
		d.Name, d.RuleNo, d.DistanceM, d.TargetID,
		d.MatchShotCount, d.MaxSightingShots, d.ShotsPerSeries,
		d.DecimalScoring, d.MatchTimeS, d.Active, d.Notes, d.ID, d.StandPCTargetNo,
	)
	return err
}

func (s *Store) DeleteDiscipline(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM disciplines WHERE id = $1`, id)
	return err
}

// ----------------------------------------------------------------------------
// Sitzungssteuerung – das Kernstueck der Standsteuerung
// ----------------------------------------------------------------------------

var ErrLaneBusy = errors.New("Stand ist bereits belegt")

// AssignLane belegt einen Stand: legt eine Session an (Status 'assigned').
// Nutzt die aktuell gueltige Kalibrierung des Stands; existiert keine,
// wird eine Default-Kalibrierung angelegt (Inbetriebnahme-Komfort).
func (s *Store) AssignLane(ctx context.Context, laneNo int,
	shooterID, disciplineID, eventID string) (string, error) {

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var laneID string
	if err := tx.QueryRow(ctx,
		`SELECT id FROM lanes WHERE lane_no=$1 AND active FOR UPDATE`,
		laneNo).Scan(&laneID); err != nil {
		return "", fmt.Errorf("Stand %d: %w", laneNo, err)
	}

	// Schon belegt?
	var busy int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM sessions
		WHERE lane_id=$1 AND status IN ('assigned','sighting','match','paused')`,
		laneID).Scan(&busy); err != nil {
		return "", err
	}
	if busy > 0 {
		return "", ErrLaneBusy
	}

	// Aktuelle Kalibrierung holen oder Default anlegen
	var calID string
	err = tx.QueryRow(ctx, `
		SELECT id FROM calibrations
		WHERE lane_id=$1 AND valid_to IS NULL
		ORDER BY valid_from DESC LIMIT 1`, laneID).Scan(&calID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
			INSERT INTO calibrations
			  (lane_id, sensor_pos, plate_angle_deg, sound_speed_mps, notes)
			VALUES ($1,
			  '[{"x":0,"y":0},{"x":250,"y":0},{"x":250,"y":250},{"x":0,"y":250}]',
			  30, 3000, 'AUTO-DEFAULT – bitte kalibrieren!')
			RETURNING id`, laneID).Scan(&calID)
	}
	if err != nil {
		return "", fmt.Errorf("Kalibrierung: %w", err)
	}

	var sessionID string
	err = tx.QueryRow(ctx, `
		INSERT INTO sessions
		  (lane_id, calibration_id, discipline_id, shooter_id, event_id, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, NULLIF($4,'')::uuid, NULLIF($5,'')::uuid, 'assigned')
		RETURNING id`,
		laneID, calID, disciplineID, shooterID, eventID).Scan(&sessionID)
	if err != nil {
		return "", err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (action, session_id, lane_id, actor, details)
		VALUES ('session_started', $1::uuid, $2::uuid, 'server',
		        jsonb_build_object('shooter',$3::text,'discipline',$4::text))`,
		sessionID, laneID, shooterID, disciplineID); err != nil {
		return "", err
	}
	return sessionID, tx.Commit(ctx)
}

// SetSessionStatus: assigned -> sighting -> match -> finished etc.
func (s *Store) SetSessionStatus(ctx context.Context, sessionID, status string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sessions SET
		  status     = $2::session_status,
		  started_at = CASE WHEN started_at IS NULL
		                     AND $2 IN ('sighting','match')
		                    THEN now() ELSE started_at END,
		  finished_at = CASE WHEN $2 IN ('finished','aborted')
		                     THEN now() ELSE finished_at END
		WHERE id = $1`, sessionID, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("Session %s nicht gefunden", sessionID)
	}
	if status == "finished" || status == "aborted" {
		_, _ = s.pool.Exec(ctx, `
			INSERT INTO audit_log (action, session_id, actor)
			VALUES ('session_finished', $1, 'server')`, sessionID)
	}
	return nil
}

// ActiveSessionForLane: der Endpunkt, den die STAND-PCs pollen.
// Liefert die aktive Session inkl. Kalibrierung als JSON.
func (s *Store) ActiveSessionForLane(ctx context.Context, laneNo int) (map[string]any, error) {
	var (
		sessionID, status, discipline string
		shooterName                   string
		sensorPos                     []byte
		plateAngle, soundSpeed        float64
		offX, offY                    float64
	)
	var eventName, eventType string
	err := s.pool.QueryRow(ctx, `
		SELECT se.id, se.status, d.name,
		       COALESCE(sh.last_name || ', ' || sh.first_name, ''),
		       c.sensor_pos, c.plate_angle_deg, c.sound_speed_mps,
		       c.plate_offset_x, c.plate_offset_y,
		       COALESCE(ev.name,''), COALESCE(ev.type::text,'')
		FROM sessions se
		JOIN lanes l        ON l.id  = se.lane_id
		JOIN disciplines d  ON d.id  = se.discipline_id
		JOIN calibrations c ON c.id  = se.calibration_id
		LEFT JOIN shooters sh ON sh.id = se.shooter_id
		LEFT JOIN events ev   ON ev.id = se.event_id
		WHERE l.lane_no = $1
		  AND se.status IN ('assigned','sighting','match','paused')
		ORDER BY se.started_at DESC NULLS LAST LIMIT 1`,
		laneNo).Scan(&sessionID, &status, &discipline, &shooterName,
		&sensorPos, &plateAngle, &soundSpeed, &offX, &offY,
		&eventName, &eventType)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // Stand frei -> Stand-PC arbeitet ohne Session
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"session_id":      sessionID,
		"status":          status,
		"discipline":      discipline,
		"shooter":         shooterName,
		"sensor_pos":      jsonRaw(sensorPos),
		"plate_angle_deg": plateAngle,
		"sound_speed_mps": soundSpeed,
		"plate_offset_x":  offX,
		"plate_offset_y":  offY,
		"event_name":      eventName,
		"event_type":      eventType,
	}, nil
}

// SessionShots: alle Schuesse einer Session (fuer Anzeige/Reload)
func (s *Store) SessionShots(ctx context.Context, sessionID string) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT shot_no, kind::text, status::text, x_mm, y_mm, ring, decimal_value,
		       is_inner_ten, center_distance, fired_at,
		       corrected_x_mm, corrected_y_mm, corrected_ring, corrected_decimal_value,
		       corrected_is_inner_ten, corrected_center_distance, corrected_at, corrected_by
		FROM shots WHERE session_id=$1 ORDER BY shot_no`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var (
			no, ring       int
			kind, status   string
			x, y, dec, div float64
			innerTen       bool
			firedAt        time.Time

			cX, cY, cDec, cDiv *float64
			cRing              *int
			cInnerTen          *bool
			cAt                *time.Time
			cBy                *string
		)
		if err := rows.Scan(&no, &kind, &status, &x, &y, &ring, &dec,
			&innerTen, &div, &firedAt,
			&cX, &cY, &cRing, &cDec, &cInnerTen, &cDiv, &cAt, &cBy); err != nil {
			return nil, err
		}
		m := map[string]any{
			"shot_no": no, "kind": kind, "status": status, "x_mm": x, "y_mm": y,
			"ring": ring, "decimal": dec, "inner_ten": innerTen,
			"center_distance": div, "fired_at": firedAt.Format(time.RFC3339),
		}
		// corrected_* nur setzen, wenn eine Korrektur existiert (sonst im JSON
		// schlicht abwesend - Original bleibt die einzige Quelle der Wahrheit).
		if cRing != nil {
			m["corrected_x_mm"] = *cX
			m["corrected_y_mm"] = *cY
			m["corrected_ring"] = *cRing
			m["corrected_decimal"] = *cDec
			m["corrected_inner_ten"] = cInnerTen != nil && *cInnerTen
			m["corrected_center_distance"] = *cDiv
			if cAt != nil {
				m["corrected_at"] = cAt.Format(time.RFC3339)
			}
			if cBy != nil {
				m["corrected_by"] = *cBy
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Result: Ergebniszeile für die Ergebnisübersicht
type Result struct {
	SessionID          string  `json:"session_id"`
	LaneNo             int     `json:"lane_no"` // für Live-Daten-Merge im Handler
	ShooterName        string  `json:"shooter_name"`
	Discipline         string  `json:"discipline"`
	ShotsPerSeries     int     `json:"shots_per_series"`
	DecimalScoring     bool    `json:"decimal_scoring"`
	EventName          string  `json:"event_name,omitempty"`
	EventType          string  `json:"event_type,omitempty"`
	StartedAt          string  `json:"started_at"`
	FinishedAt         string  `json:"finished_at,omitempty"`
	Status             string  `json:"status"`
	ShotCount          int     `json:"shot_count"`
	TotalRings         int     `json:"total_rings"`
	TotalDecimal       float64 `json:"total_decimal"`
	InnerTens          int     `json:"inner_tens"`
	BestCenterDistance float64 `json:"best_center_distance,omitempty"`
	LiveData           bool    `json:"live_data,omitempty"` // true = Daten kommen vom StandPC, nicht DB
}

// ListResults gibt Ergebnisse gefiltert nach Datum, Schützename und Veranstaltung zurück.
// date:    ISO-Datum (2006-01-02); leer = kein Datumsfilter
// name:    Teilstring Nachname oder Vorname; leer = kein Filter
// eventID: UUID einer Veranstaltung; leer = kein Filter
func (s *Store) ListResults(ctx context.Context, date, name, eventID string) ([]Result, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT se.id,
		       l.lane_no,
		       COALESCE(sh.last_name || ', ' || sh.first_name, 'Anonym'),
		       d.name, d.shots_per_series, d.decimal_scoring,
		       COALESCE(ev.name,''), COALESCE(ev.type::text,''),
		       se.started_at,
		       se.finished_at,
		       se.status::text,
		       COALESCE(r.shot_count,0),
		       COALESCE(r.total_rings,0),
		       COALESCE(r.total_decimal,0)::float8,
		       COALESCE(r.inner_tens,0),
		       COALESCE(r.best_center_distance,0)::float8
		FROM sessions se
		JOIN lanes l          ON l.id  = se.lane_id
		JOIN disciplines d    ON d.id  = se.discipline_id
		LEFT JOIN shooters sh ON sh.id = se.shooter_id
		LEFT JOIN events ev   ON ev.id = se.event_id
		LEFT JOIN v_session_results r ON r.session_id = se.id
		WHERE (se.status != 'aborted' OR r.shot_count > 0)
		  AND ($1 = '' OR se.started_at AT TIME ZONE 'Europe/Berlin' >= ($1::date)::timestamptz
		                AND se.started_at AT TIME ZONE 'Europe/Berlin' <  ($1::date + 1)::timestamptz)
		  AND ($2 = '' OR sh.last_name ILIKE '%'||$2||'%' OR sh.first_name ILIKE '%'||$2||'%')
		  AND ($3 = '' OR se.event_id = $3::uuid)
		ORDER BY se.started_at DESC NULLS LAST
		LIMIT 500`,
		date, name, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Result
	for rows.Next() {
		var r Result
		var startedAt *time.Time
		var finishedAt *time.Time
		if err := rows.Scan(&r.SessionID, &r.LaneNo, &r.ShooterName, &r.Discipline,
			&r.ShotsPerSeries, &r.DecimalScoring,
			&r.EventName, &r.EventType,
			&startedAt, &finishedAt, &r.Status,
			&r.ShotCount, &r.TotalRings, &r.TotalDecimal,
			&r.InnerTens, &r.BestCenterDistance); err != nil {
			return nil, err
		}
		if startedAt != nil {
			r.StartedAt = startedAt.Format(time.RFC3339)
		}
		if finishedAt != nil {
			r.FinishedAt = finishedAt.Format(time.RFC3339)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AnnulShot: Kampfrichter-Aktion mit Audit-Log
func (s *Store) AnnulShot(ctx context.Context, sessionID string, shotNo int,
	actor, reason string) error {

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var shotID string
	if err := tx.QueryRow(ctx, `
		UPDATE shots SET status='annulled'
		WHERE session_id=$1 AND shot_no=$2 RETURNING id`,
		sessionID, shotNo).Scan(&shotID); err != nil {
		return fmt.Errorf("Schuss %d: %w", shotNo, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (action, session_id, shot_id, actor, details)
		VALUES ('shot_annulled', $1::uuid, $2::uuid, $3, jsonb_build_object('reason',$4::text))`,
		sessionID, shotID, actor, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// jsonRaw verhindert doppeltes JSON-Encoding von jsonb-Spalten
type jsonRaw []byte

func (j jsonRaw) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return j, nil
}

// ----------------------------------------------------------------------------
// Stammdaten – Gaue
// ----------------------------------------------------------------------------

type Gau struct {
	ID        string `json:"id"`
	GauNo     string `json:"gau_no"`
	Name      string `json:"name"`
	ClubCount int    `json:"club_count"`
}

func (s *Store) ListGaue(ctx context.Context) ([]Gau, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT g.id, g.gau_no, g.name,
		       COUNT(c.id) AS club_count
		FROM gaue g
		LEFT JOIN clubs c ON c.gau_id = g.id
		GROUP BY g.id
		ORDER BY g.gau_no`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Gau
	for rows.Next() {
		var g Gau
		if err := rows.Scan(&g.ID, &g.GauNo, &g.Name, &g.ClubCount); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) CreateGau(ctx context.Context, gauNo, name string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`INSERT INTO gaue (gau_no, name) VALUES ($1, $2) RETURNING id`,
		gauNo, name).Scan(&id)
	return id, err
}

func (s *Store) UpdateGau(ctx context.Context, id, gauNo, name string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE gaue SET gau_no=$1, name=$2 WHERE id=$3`,
		gauNo, name, id)
	return err
}

func (s *Store) DeleteGau(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM gaue WHERE id=$1`, id)
	return err
}

// ----------------------------------------------------------------------------
// Stammdaten – Vereine (Clubs, erweitertes Modell)
// ----------------------------------------------------------------------------

type ClubFull struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ShortName   string `json:"short_name"`
	ExternalNo  string `json:"external_no"`
	GauID       string `json:"gau_id"`
	GauName     string `json:"gau_name"`
	MemberCount *int   `json:"member_count"`
}

type ImportClubEntry struct {
	ExternalNo  string `json:"external_no"`
	Name        string `json:"name"`
	GauNo       string `json:"gau_no"`
	GauName     string `json:"gau_name"`
	MemberCount *int   `json:"member_count"`
}

func (s *Store) ListClubsFull(ctx context.Context) ([]ClubFull, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.name, COALESCE(c.short_name,''),
		       COALESCE(c.external_no,''), COALESCE(c.gau_id::text,''),
		       COALESCE(g.name,''), c.member_count
		FROM clubs c
		LEFT JOIN gaue g ON g.id = c.gau_id
		ORDER BY c.external_no NULLS LAST, c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClubFull
	for rows.Next() {
		var c ClubFull
		if err := rows.Scan(&c.ID, &c.Name, &c.ShortName,
			&c.ExternalNo, &c.GauID, &c.GauName, &c.MemberCount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CreateClub(ctx context.Context, c ClubFull) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO clubs (name, short_name, external_no, gau_id, member_count)
		VALUES ($1, NULLIF($2,''), NULLIF($3,''), NULLIF($4,'')::uuid, $5)
		RETURNING id`,
		c.Name, c.ShortName, c.ExternalNo, c.GauID, c.MemberCount).Scan(&id)
	return id, err
}

func (s *Store) UpdateClub(ctx context.Context, c ClubFull) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE clubs SET
		    name         = $1,
		    short_name   = NULLIF($2,''),
		    external_no  = NULLIF($3,''),
		    gau_id       = NULLIF($4,'')::uuid,
		    member_count = $5
		WHERE id = $6`,
		c.Name, c.ShortName, c.ExternalNo, c.GauID, c.MemberCount, c.ID)
	return err
}

func (s *Store) DeleteClub(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM clubs WHERE id=$1`, id)
	return err
}

// ImportClubs importiert Vereine per Upsert auf external_no.
// Wenn createGaue=true, werden fehlende Gaue automatisch angelegt.
// Gibt (neu angelegt, aktualisiert, Fehler) zurück.
func (s *Store) ImportClubs(ctx context.Context, entries []ImportClubEntry, createGaue bool) (int, int, error) {
	gauRows, err := s.pool.Query(ctx, `SELECT gau_no, id::text FROM gaue`)
	if err != nil {
		return 0, 0, err
	}
	defer gauRows.Close()
	gauMap := make(map[string]string)
	for gauRows.Next() {
		var no, id string
		if err := gauRows.Scan(&no, &id); err != nil {
			return 0, 0, err
		}
		gauMap[no] = id
	}
	if err := gauRows.Err(); err != nil {
		return 0, 0, err
	}

	if createGaue {
		seen := make(map[string]bool)
		for _, e := range entries {
			if e.GauNo == "" || seen[e.GauNo] {
				continue
			}
			seen[e.GauNo] = true
			if _, ok := gauMap[e.GauNo]; !ok {
				id, err := s.CreateGau(ctx, e.GauNo, e.GauName)
				if err != nil {
					return 0, 0, fmt.Errorf("Gau %s anlegen: %w", e.GauNo, err)
				}
				gauMap[e.GauNo] = id
			}
		}
	}

	var created, updated int
	for _, e := range entries {
		gauID := gauMap[e.GauNo]
		var wasInserted bool
		err := s.pool.QueryRow(ctx, `
			INSERT INTO clubs (name, external_no, gau_id, member_count)
			VALUES ($1, NULLIF($2,''), NULLIF($3,'')::uuid, $4)
			ON CONFLICT (external_no) DO UPDATE SET
			    name         = EXCLUDED.name,
			    gau_id       = COALESCE(EXCLUDED.gau_id, clubs.gau_id),
			    member_count = EXCLUDED.member_count
			RETURNING (xmax = 0)`,
			e.Name, e.ExternalNo, gauID, e.MemberCount,
		).Scan(&wasInserted)
		if err != nil {
			return created, updated, fmt.Errorf("Verein %q: %w", e.Name, err)
		}
		if wasInserted {
			created++
		} else {
			updated++
		}
	}
	return created, updated, nil
}

// ----------------------------------------------------------------------------
// Stammdaten – Sportklassen
// ----------------------------------------------------------------------------

// ShooterClass entspricht einer Zeile des CSV-Exports "Sportklassen" aus dem
// Verwaltungstool. type/sex werden als numerische Codes wie im Export
// gespeichert (Anzeige uebersetzt clientseitig):
//   type: 0=Kugel, 1=Bogen, 2=Kugel Auflage
//   sex:  0=weiblich, 1=maennlich, NULL=offen
type ShooterClass struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	ShortName string `json:"short_name"`
	MinAge    *int   `json:"min_age"`
	MaxAge    *int   `json:"max_age"`
	Type      *int   `json:"type"`
	Sex       *int   `json:"sex"`
}

func (s *Store) ListShooterClasses(ctx context.Context) ([]ShooterClass, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, code, name, COALESCE(short_name,''), min_age, max_age, type, sex
		FROM shooter_classes
		ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShooterClass
	for rows.Next() {
		var c ShooterClass
		if err := rows.Scan(&c.ID, &c.Code, &c.Name, &c.ShortName,
			&c.MinAge, &c.MaxAge, &c.Type, &c.Sex); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CreateShooterClass(ctx context.Context, c ShooterClass) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO shooter_classes (code, name, short_name, min_age, max_age, type, sex)
		VALUES ($1, $2, NULLIF($3,''), $4, $5, $6, $7)
		RETURNING id`,
		c.Code, c.Name, c.ShortName, c.MinAge, c.MaxAge, c.Type, c.Sex).Scan(&id)
	return id, err
}

func (s *Store) UpdateShooterClass(ctx context.Context, c ShooterClass) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE shooter_classes SET
		    code       = $1,
		    name       = $2,
		    short_name = NULLIF($3,''),
		    min_age    = $4,
		    max_age    = $5,
		    type       = $6,
		    sex        = $7
		WHERE id = $8`,
		c.Code, c.Name, c.ShortName, c.MinAge, c.MaxAge, c.Type, c.Sex, c.ID)
	return err
}

func (s *Store) DeleteShooterClass(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM shooter_classes WHERE id=$1`, id)
	return err
}

// ImportShooterClasses importiert Sportklassen per Upsert auf (code, type).
// KLASSENNR (code) ist nur innerhalb einer Schießart (type) eindeutig - der
// Export enthält dieselbe Nummer je einmal pro Schießart (siehe Migration
// 020), daher NICHT nur auf code upserten.
// Gibt (neu angelegt, aktualisiert, Fehler) zurück.
func (s *Store) ImportShooterClasses(ctx context.Context, entries []ShooterClass) (int, int, error) {
	var created, updated int
	for _, e := range entries {
		var wasInserted bool
		err := s.pool.QueryRow(ctx, `
			INSERT INTO shooter_classes (code, name, short_name, min_age, max_age, type, sex)
			VALUES ($1, $2, NULLIF($3,''), $4, $5, $6, $7)
			ON CONFLICT (code, type) DO UPDATE SET
			    name       = EXCLUDED.name,
			    short_name = EXCLUDED.short_name,
			    min_age    = EXCLUDED.min_age,
			    max_age    = EXCLUDED.max_age,
			    type       = EXCLUDED.type,
			    sex        = EXCLUDED.sex
			RETURNING (xmax = 0)`,
			e.Code, e.Name, e.ShortName, e.MinAge, e.MaxAge, e.Type, e.Sex,
		).Scan(&wasInserted)
		if err != nil {
			return created, updated, fmt.Errorf("Sportklasse %q: %w", e.Code, err)
		}
		if wasInserted {
			created++
		} else {
			updated++
		}
	}
	return created, updated, nil
}

// RecalculateResult fasst das Ergebnis von RecalculateSportsClasses zusammen.
type RecalculateResult struct {
	Updated       int `json:"updated"`
	SkippedNoData int `json:"skipped_no_data"` // Geschlecht oder Geburtsdatum fehlt
	SkippedNoMatch int `json:"skipped_no_match"` // keine passende Sportklasse gefunden
}

// RecalculateSportsClasses ordnet jedem Schützen mit bekanntem Geschlecht und
// Geburtsdatum die Sportklasse zu, deren Altersbereich das im gegebenen
// Sportjahr erreichte Alter (Jahr - Geburtsjahr, Monat/Tag unerheblich)
// enthält und deren Geschlecht passt (shooter_classes.sex NULL = offen, gilt
// für beide). Betrachtet werden dabei NUR Klassen der Schießart Kugel
// (type=0) - die allgemeine Mitglieder-Sportklasse bezieht sich auf Kugel,
// nicht auf Bogen/Kugel Auflage, deren Altersbereiche sich mit Kugel-Klassen
// überschneiden können. Bei mehreren passenden Klassen gewinnt die mit dem
// engeren Altersbereich (unbegrenzte Grenzen zählen als sehr weit). Schützen
// ohne Geschlecht/Geburtsdatum werden übersprungen (sports_class bleibt
// unverändert), ebenso wenn keine passende Klasse existiert.
func (s *Store) RecalculateSportsClasses(ctx context.Context, year int) (RecalculateResult, error) {
	var res RecalculateResult

	type shooterRow struct {
		id     string
		gender string
		byear  *int
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, COALESCE(gender,''), EXTRACT(YEAR FROM birth_date)::int
		FROM shooters`)
	if err != nil {
		return res, err
	}
	var shooterList []shooterRow
	for rows.Next() {
		var sr shooterRow
		if err := rows.Scan(&sr.id, &sr.gender, &sr.byear); err != nil {
			rows.Close()
			return res, err
		}
		shooterList = append(shooterList, sr)
	}
	if err := rows.Err(); err != nil {
		return res, err
	}
	rows.Close()

	type classRow struct {
		code           string
		minAge, maxAge *int
		sex            *int
	}
	classRows, err := s.pool.Query(ctx, `SELECT code, min_age, max_age, sex FROM shooter_classes WHERE type = 0`)
	if err != nil {
		return res, err
	}
	var classes []classRow
	for classRows.Next() {
		var cr classRow
		if err := classRows.Scan(&cr.code, &cr.minAge, &cr.maxAge, &cr.sex); err != nil {
			classRows.Close()
			return res, err
		}
		classes = append(classes, cr)
	}
	if err := classRows.Err(); err != nil {
		return res, err
	}
	classRows.Close()

	const unboundedLow, unboundedHigh = -32768, 32767
	rangeWidth := func(c classRow) int {
		lo, hi := unboundedLow, unboundedHigh
		if c.minAge != nil {
			lo = *c.minAge
		}
		if c.maxAge != nil {
			hi = *c.maxAge
		}
		return hi - lo
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx)

	for _, sr := range shooterList {
		var sexCode int
		switch sr.gender {
		case "M":
			sexCode = 1
		case "W":
			sexCode = 0
		default:
			res.SkippedNoData++
			continue
		}
		if sr.byear == nil {
			res.SkippedNoData++
			continue
		}
		age := year - *sr.byear

		var best *classRow
		for i := range classes {
			c := &classes[i]
			if c.sex != nil && *c.sex != sexCode {
				continue
			}
			if c.minAge != nil && age < *c.minAge {
				continue
			}
			if c.maxAge != nil && age > *c.maxAge {
				continue
			}
			if best == nil || rangeWidth(*c) < rangeWidth(*best) {
				best = c
			}
		}
		if best == nil {
			res.SkippedNoMatch++
			continue
		}
		codeNum, convErr := strconv.Atoi(best.code)
		if convErr != nil {
			res.SkippedNoMatch++
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE shooters SET sports_class=$1 WHERE id=$2`, codeNum, sr.id); err != nil {
			return res, err
		}
		res.Updated++
	}

	if err := tx.Commit(ctx); err != nil {
		return res, err
	}
	return res, nil
}

// ----------------------------------------------------------------------------
// Stammdaten – Schützen (vollständiges Modell)
// ----------------------------------------------------------------------------

// ShooterFull enthält alle Stammdatenfelder eines Schützen.
type ShooterFull struct {
	ID          string  `json:"id"`
	LastName    string  `json:"last_name"`
	FirstName   string  `json:"first_name"`
	Title       string  `json:"title"`
	Gender      string  `json:"gender"`
	BirthDate   string  `json:"birth_date"`
	PassNo      string  `json:"pass_no"`
	ClubID      string  `json:"club_id"`
	ClubName    string  `json:"club_name"`
	Street      string  `json:"street"`
	Zip         string  `json:"zip"`
	City        string  `json:"city"`
	Phone       string  `json:"phone"`
	Mobile      string  `json:"mobile"`
	Email       string  `json:"email"`
	SportsClass *int    `json:"sports_class"`
	AgeGroup    *int    `json:"age_group"`
	EntryDate   string  `json:"entry_date"`
	Interests   string  `json:"interests"`
	Country     string  `json:"country"`
}

// MemberRow repräsentiert eine Zeile der Mitgliederliste-CSV.
type MemberRow struct {
	VereinsNr    string `json:"vereinnr"`
	PassNr       string `json:"passnr"`
	LastName     string `json:"last_name"`
	FirstName    string `json:"first_name"`
	Title        string `json:"title"`
	BirthDate    string `json:"birth_date"`
	Gender       string `json:"gender"`
	Street       string `json:"street"`
	Zip          string `json:"zip"`
	City         string `json:"city"`
	ErstVereinNr string `json:"erst_vereinnr"`
	EntryDate    string `json:"entry_date"`
	MemberYears  int    `json:"member_years"`
	SportsClass  *int   `json:"sports_class"`
	AgeGroup     *int   `json:"age_group"`
	Interests    string `json:"interests"`
	Phone        string `json:"phone"`
	Mobile       string `json:"mobile"`
	Email        string `json:"email"`
}

func (s *Store) ListShootersFull(ctx context.Context, q, clubID string, limit, offset int) ([]ShooterFull, int, error) {
	const base = `
		FROM shooters sh
		LEFT JOIN clubs c ON c.id = sh.club_id
		WHERE ($1 = '' OR sh.last_name ILIKE '%'||$1||'%'
		                OR sh.first_name ILIKE '%'||$1||'%'
		                OR sh.pass_no   ILIKE '%'||$1||'%')
		  AND ($2 = '' OR sh.club_id::text = $2)`

	var total int
	err := s.pool.QueryRow(ctx,
		"SELECT COUNT(*) "+base, q, clubID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT sh.id, sh.last_name, sh.first_name,
		       COALESCE(sh.title,''),  COALESCE(sh.gender,''),
		       COALESCE(sh.birth_date::text,''), COALESCE(sh.pass_no,''),
		       COALESCE(sh.club_id::text,''),    COALESCE(c.name,''),
		       COALESCE(sh.street,''), COALESCE(sh.zip,''), COALESCE(sh.city,''),
		       COALESCE(sh.phone,''),  COALESCE(sh.mobile,''), COALESCE(sh.email,''),
		       sh.sports_class, sh.age_group,
		       COALESCE(sh.entry_date::text,''), COALESCE(sh.interests,''),
		       COALESCE(sh.country,'GER')
		`+base+`
		ORDER BY sh.last_name, sh.first_name
		LIMIT $3 OFFSET $4`, q, clubID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []ShooterFull
	for rows.Next() {
		var sh ShooterFull
		if err := rows.Scan(
			&sh.ID, &sh.LastName, &sh.FirstName,
			&sh.Title, &sh.Gender, &sh.BirthDate, &sh.PassNo,
			&sh.ClubID, &sh.ClubName,
			&sh.Street, &sh.Zip, &sh.City,
			&sh.Phone, &sh.Mobile, &sh.Email,
			&sh.SportsClass, &sh.AgeGroup,
			&sh.EntryDate, &sh.Interests, &sh.Country,
		); err != nil {
			return nil, 0, err
		}
		out = append(out, sh)
	}
	return out, total, rows.Err()
}

func (s *Store) CreateShooterFull(ctx context.Context, sh ShooterFull) (string, error) {
	bd, _ := time.Parse("2006-01-02", sh.BirthDate)
	ed, _ := time.Parse("2006-01-02", sh.EntryDate)
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO shooters
		  (last_name, first_name, title, gender, birth_date, pass_no, club_id,
		   street, zip, city, phone, mobile, email,
		   sports_class, age_group, entry_date, interests, country)
		VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),$5::date,NULLIF($6,''),NULLIF($7,'')::uuid,
		        NULLIF($8,''),NULLIF($9,''),NULLIF($10,''),
		        NULLIF($11,''),NULLIF($12,''),NULLIF($13,''),
		        $14,$15,$16::date,NULLIF($17,''),COALESCE(NULLIF($18,''),'GER'))
		RETURNING id`,
		sh.LastName, sh.FirstName, sh.Title, sh.Gender, nullTime(bd), sh.PassNo, sh.ClubID,
		sh.Street, sh.Zip, sh.City, sh.Phone, sh.Mobile, sh.Email,
		sh.SportsClass, sh.AgeGroup, nullTime(ed), sh.Interests, sh.Country,
	).Scan(&id)
	return id, err
}

func (s *Store) UpdateShooterFull(ctx context.Context, sh ShooterFull) error {
	bd, _ := time.Parse("2006-01-02", sh.BirthDate)
	ed, _ := time.Parse("2006-01-02", sh.EntryDate)
	_, err := s.pool.Exec(ctx, `
		UPDATE shooters SET
		  last_name    = $1,  first_name   = $2,
		  title        = NULLIF($3,''),  gender = NULLIF($4,''),
		  birth_date   = $5::date,       pass_no = NULLIF($6,''),
		  club_id      = NULLIF($7,'')::uuid,
		  street       = NULLIF($8,''),  zip    = NULLIF($9,''),
		  city         = NULLIF($10,''), phone  = NULLIF($11,''),
		  mobile       = NULLIF($12,''), email  = NULLIF($13,''),
		  sports_class = $14,            age_group  = $15,
		  entry_date   = $16::date,      interests  = NULLIF($17,''),
		  country      = COALESCE(NULLIF($18,''),'GER'),
		  updated_at   = now()
		WHERE id = $19`,
		sh.LastName, sh.FirstName, sh.Title, sh.Gender, nullTime(bd), sh.PassNo, sh.ClubID,
		sh.Street, sh.Zip, sh.City, sh.Phone, sh.Mobile, sh.Email,
		sh.SportsClass, sh.AgeGroup, nullTime(ed), sh.Interests, sh.Country, sh.ID,
	)
	return err
}

func (s *Store) DeleteShooterFull(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM shooters WHERE id=$1`, id)
	return err
}

// nullTime gibt nil zurück wenn t das Zero-Value ist, sonst &t.
// pgx behandelt (*time.Time)(nil) als SQL NULL.
func nullTime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t
}

// ImportMembers wendet die Gau-Auswahllogik an und importiert Schützen per Upsert.
//
// Auswahllogik:
//  1. ERSTVEREINNR liegt im Gau (external_no bekannt) → Erstverein zuordnen
//  2. Sonst → Verein mit höchstem MITGLIEDSJAHREVEREIN wählen
func (s *Store) ImportMembers(ctx context.Context, rows []MemberRow) (created, updated, skipped int, err error) {
	// Bekannte Vereine laden (external_no → club_id)
	cRows, err := s.pool.Query(ctx, `SELECT external_no, id::text FROM clubs WHERE external_no IS NOT NULL`)
	if err != nil {
		return 0, 0, 0, err
	}
	defer cRows.Close()
	clubMap := make(map[string]string) // external_no → uuid
	for cRows.Next() {
		var no, id string
		if err := cRows.Scan(&no, &id); err != nil {
			return 0, 0, 0, err
		}
		clubMap[no] = id
	}
	if err := cRows.Err(); err != nil {
		return 0, 0, 0, err
	}
	if len(clubMap) == 0 {
		return 0, 0, 0, errors.New("keine Vereine importiert – bitte zuerst Vereins-CSV importieren")
	}

	// Zeilen nach PassNr gruppieren
	grouped := make(map[string][]MemberRow)
	for _, r := range rows {
		if r.PassNr == "" {
			skipped++
			continue
		}
		grouped[r.PassNr] = append(grouped[r.PassNr], r)
	}

	for passnr, grp := range grouped {
		selected, clubID := selectMemberRow(grp, clubMap)
		if clubID == "" {
			skipped++
			continue
		}

		bd, _ := parseGermanDate(selected.BirthDate)
		ed, _ := parseGermanDate(selected.EntryDate)

		var wasInserted bool
		qerr := s.pool.QueryRow(ctx, `
			INSERT INTO shooters
			  (last_name, first_name, title, gender, birth_date, pass_no, club_id,
			   street, zip, city, phone, mobile, email,
			   sports_class, age_group, entry_date, interests, country, updated_at)
			VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),$5,NULLIF($6,''),$7::uuid,
			        NULLIF($8,''),NULLIF($9,''),NULLIF($10,''),
			        NULLIF($11,''),NULLIF($12,''),NULLIF($13,''),
			        $14,$15,$16,NULLIF($17,''),'GER',now())
			ON CONFLICT (pass_no) DO UPDATE SET
			  last_name    = EXCLUDED.last_name,
			  first_name   = EXCLUDED.first_name,
			  title        = EXCLUDED.title,
			  gender       = EXCLUDED.gender,
			  birth_date   = EXCLUDED.birth_date,
			  club_id      = EXCLUDED.club_id,
			  street       = EXCLUDED.street,
			  zip          = EXCLUDED.zip,
			  city         = EXCLUDED.city,
			  phone        = EXCLUDED.phone,
			  mobile       = EXCLUDED.mobile,
			  email        = EXCLUDED.email,
			  sports_class = EXCLUDED.sports_class,
			  age_group    = EXCLUDED.age_group,
			  entry_date   = EXCLUDED.entry_date,
			  interests    = EXCLUDED.interests,
			  updated_at   = now()
			RETURNING (xmax = 0)`,
			selected.LastName, selected.FirstName, selected.Title, selected.Gender,
			nullTime(bd), passnr, clubID,
			selected.Street, selected.Zip, selected.City,
			selected.Phone, selected.Mobile, selected.Email,
			selected.SportsClass, selected.AgeGroup, nullTime(ed),
			selected.Interests,
		).Scan(&wasInserted)
		if qerr != nil {
			return created, updated, skipped, fmt.Errorf("Schütze %s %s (Pass %s): %w",
				selected.FirstName, selected.LastName, passnr, qerr)
		}
		if wasInserted {
			created++
		} else {
			updated++
		}
	}
	return created, updated, skipped, nil
}

// selectMemberRow wählt die beste Zeile und gibt den zu verwendenden club_id zurück.
func selectMemberRow(rows []MemberRow, clubMap map[string]string) (MemberRow, string) {
	// Prüfen ob ERSTVEREINNR in unserem Gau liegt
	erstVereinNr := rows[0].ErstVereinNr
	if id, ok := clubMap[erstVereinNr]; ok {
		// Erstverein liegt im Gau: Zeile suchen wo VEREINNR == ERSTVEREINNR
		for _, r := range rows {
			if r.VereinsNr == erstVereinNr {
				return r, id
			}
		}
		// Kein direkter Treffer aber Erstverein bekannt: irgendeine Zeile nehmen
		return rows[0], id
	}

	// Erstverein außerhalb → Verein mit höchstem MITGLIEDSJAHREVEREIN wählen
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].MemberYears != rows[j].MemberYears {
			return rows[i].MemberYears > rows[j].MemberYears
		}
		// Gleichstand: früherer Eintrittsdatum gewinnt
		di, _ := parseGermanDate(rows[i].EntryDate)
		dj, _ := parseGermanDate(rows[j].EntryDate)
		return di.Before(dj)
	})
	best := rows[0]
	return best, clubMap[best.VereinsNr]
}

func parseGermanDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("leeres Datum")
	}
	return time.Parse("02.01.2006", s)
}

// ----------------------------------------------------------------------------
// Stammdaten – Mannschaften
// ----------------------------------------------------------------------------

type Team struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ShortName  string `json:"short_name"`
	Season     string `json:"season"`
	Discipline string `json:"discipline"`
	ClubID     string `json:"club_id"`
	ClubName   string `json:"club_name"`
	GauID      string `json:"gau_id"`
	GauName    string `json:"gau_name"`
	Notes      string `json:"notes"`
	Active     bool   `json:"active"`
	MemberCount int   `json:"member_count"`
}

type TeamMember struct {
	ShooterID  string `json:"shooter_id"`
	LastName   string `json:"last_name"`
	FirstName  string `json:"first_name"`
	PassNo     string `json:"pass_no"`
	ClubName   string `json:"club_name"`
	Position   *int   `json:"position"`
	JoinedAt   string `json:"joined_at"`
	Notes      string `json:"notes"`
}

func (s *Store) ListTeams(ctx context.Context, clubID, gauID string, activeOnly bool) ([]Team, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.name, COALESCE(t.short_name,''), COALESCE(t.season,''),
		       COALESCE(t.discipline,''),
		       COALESCE(t.club_id::text,''), COALESCE(c.name,''),
		       COALESCE(t.gau_id::text,''),  COALESCE(g.name,''),
		       COALESCE(t.notes,''), t.active,
		       (SELECT COUNT(*) FROM team_members tm WHERE tm.team_id = t.id)
		FROM teams t
		LEFT JOIN clubs c ON c.id = t.club_id
		LEFT JOIN gaue  g ON g.id = t.gau_id
		WHERE ($1 = '' OR t.club_id::text = $1)
		  AND ($2 = '' OR t.gau_id::text  = $2)
		  AND (NOT $3   OR t.active)
		ORDER BY t.name`, clubID, gauID, activeOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Team
	for rows.Next() {
		var t Team
		if err := rows.Scan(
			&t.ID, &t.Name, &t.ShortName, &t.Season, &t.Discipline,
			&t.ClubID, &t.ClubName, &t.GauID, &t.GauName,
			&t.Notes, &t.Active, &t.MemberCount,
		); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) CreateTeam(ctx context.Context, t Team) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO teams (name, short_name, season, discipline, club_id, gau_id, notes, active)
		VALUES ($1, NULLIF($2,''), NULLIF($3,''), NULLIF($4,''),
		        NULLIF($5,'')::uuid, NULLIF($6,'')::uuid, NULLIF($7,''), $8)
		RETURNING id`,
		t.Name, t.ShortName, t.Season, t.Discipline,
		t.ClubID, t.GauID, t.Notes, t.Active,
	).Scan(&id)
	return id, err
}

func (s *Store) UpdateTeam(ctx context.Context, t Team) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE teams SET
		  name       = $1, short_name = NULLIF($2,''),
		  season     = NULLIF($3,''), discipline = NULLIF($4,''),
		  club_id    = NULLIF($5,'')::uuid, gau_id = NULLIF($6,'')::uuid,
		  notes      = NULLIF($7,''), active = $8, updated_at = now()
		WHERE id = $9`,
		t.Name, t.ShortName, t.Season, t.Discipline,
		t.ClubID, t.GauID, t.Notes, t.Active, t.ID,
	)
	return err
}

func (s *Store) DeleteTeam(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM teams WHERE id = $1`, id)
	return err
}

func (s *Store) ListTeamMembers(ctx context.Context, teamID string) ([]TeamMember, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sh.id, sh.last_name, sh.first_name,
		       COALESCE(sh.pass_no,''), COALESCE(c.name,''),
		       tm.position, COALESCE(tm.joined_at::text,''), COALESCE(tm.notes,'')
		FROM team_members tm
		JOIN shooters sh ON sh.id = tm.shooter_id
		LEFT JOIN clubs c ON c.id = sh.club_id
		WHERE tm.team_id = $1
		ORDER BY tm.position NULLS LAST, sh.last_name, sh.first_name`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TeamMember
	for rows.Next() {
		var m TeamMember
		if err := rows.Scan(
			&m.ShooterID, &m.LastName, &m.FirstName,
			&m.PassNo, &m.ClubName,
			&m.Position, &m.JoinedAt, &m.Notes,
		); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) AddTeamMember(ctx context.Context, teamID, shooterID string, position *int, joinedAt, notes string) error {
	jd, _ := time.Parse("2006-01-02", joinedAt)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO team_members (team_id, shooter_id, position, joined_at, notes)
		VALUES ($1, $2, $3, $4, NULLIF($5,''))
		ON CONFLICT (team_id, shooter_id) DO UPDATE
		  SET position = EXCLUDED.position,
		      joined_at = EXCLUDED.joined_at,
		      notes = EXCLUDED.notes`,
		teamID, shooterID, position, nullTime(jd), notes,
	)
	return err
}

func (s *Store) RemoveTeamMember(ctx context.Context, teamID, shooterID string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM team_members WHERE team_id=$1 AND shooter_id=$2`,
		teamID, shooterID)
	return err
}

// ----------------------------------------------------------------------------
// Wettkämpfe
// ----------------------------------------------------------------------------

// Competition bildet die events-Tabelle mit Wettkampf-Erweiterungen ab.
type Competition struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"` // einzel | runde | gruppe
	StartsOn       string `json:"starts_on"`
	EndsOn         string `json:"ends_on"`
	Status         string `json:"status"`
	DisciplineID   string `json:"discipline_id"`
	DisciplineName string `json:"discipline_name"`
	Location       string `json:"location"`
	Notes          string `json:"notes"`
	Active         bool   `json:"active"`
	StarterCount   int    `json:"starter_count"`
	ParticipantCount int  `json:"participant_count"`
}

// CompetitionParticipant ist eine antretende Einheit (Mannschaft, Verein oder Gau).
type CompetitionParticipant struct {
	ID        string `json:"id"`
	EventID   string `json:"event_id"`
	TeamID    string `json:"team_id"`
	ClubID    string `json:"club_id"`
	GauID     string `json:"gau_id"`
	Label     string `json:"label"`      // aufgelöster Name
	EntityType string `json:"entity_type"` // team | club | gau
	SortOrder int    `json:"sort_order"`
}

// CompetitionStarter ist ein Schütze, der an einem Wettkampf teilnimmt.
type CompetitionStarter struct {
	ID           string `json:"id"`
	EventID      string `json:"event_id"`
	ShooterID    string `json:"shooter_id"`
	LastName     string `json:"last_name"`
	FirstName    string `json:"first_name"`
	PassNo       string `json:"pass_no"`
	ClubName     string `json:"club_name"`
	TeamID       string `json:"team_id"`
	TeamName     string `json:"team_name"`
	DisciplineID string `json:"discipline_id"`
	StartNo      string `json:"start_no"`
	Role         string `json:"role"` // S=Stammschütze, E=Ersatz, AK=Außer Konkurrenz
}

func (s *Store) ListCompetitions(ctx context.Context, compType, status string, activeOnly, includeArchived bool) ([]Competition, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.id, e.name, COALESCE(e.type,''), COALESCE(e.starts_on::text,''),
		       COALESCE(e.ends_on::text,''), e.status::text,
		       COALESCE(e.discipline_id::text,''), COALESCE(d.name,''),
		       COALESCE(e.location,''), COALESCE(e.notes,''),
		       COALESCE(e.active,TRUE),
		       (SELECT COUNT(*) FROM starters st WHERE st.event_id = e.id),
		       (SELECT COUNT(*) FROM competition_participants cp WHERE cp.event_id = e.id)
		FROM events e
		LEFT JOIN disciplines d ON d.id = e.discipline_id
		WHERE ($1 = '' OR e.type = $1)
		  AND ($2 = '' OR e.status::text = $2)
		  AND (NOT $3 OR COALESCE(e.active,TRUE))
		  AND ($4 OR e.status::text != 'archived')
		ORDER BY e.starts_on DESC NULLS LAST, e.name`,
		compType, status, activeOnly, includeArchived)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Competition
	for rows.Next() {
		var c Competition
		if err := rows.Scan(
			&c.ID, &c.Name, &c.Type, &c.StartsOn, &c.EndsOn, &c.Status,
			&c.DisciplineID, &c.DisciplineName,
			&c.Location, &c.Notes, &c.Active,
			&c.StarterCount, &c.ParticipantCount,
		); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetCompetition(ctx context.Context, id string) (Competition, error) {
	var c Competition
	err := s.pool.QueryRow(ctx, `
		SELECT e.id, e.name, COALESCE(e.type,''), COALESCE(e.starts_on::text,''),
		       COALESCE(e.ends_on::text,''), e.status::text,
		       COALESCE(e.discipline_id::text,''), COALESCE(d.name,''),
		       COALESCE(e.location,''), COALESCE(e.notes,''),
		       COALESCE(e.active,TRUE),
		       (SELECT COUNT(*) FROM starters st WHERE st.event_id = e.id),
		       (SELECT COUNT(*) FROM competition_participants cp WHERE cp.event_id = e.id)
		FROM events e
		LEFT JOIN disciplines d ON d.id = e.discipline_id
		WHERE e.id = $1::uuid`, id,
	).Scan(
		&c.ID, &c.Name, &c.Type, &c.StartsOn, &c.EndsOn, &c.Status,
		&c.DisciplineID, &c.DisciplineName,
		&c.Location, &c.Notes, &c.Active,
		&c.StarterCount, &c.ParticipantCount,
	)
	return c, err
}

func (s *Store) CreateCompetition(ctx context.Context, c Competition) (string, error) {
	sd, _ := time.Parse("2006-01-02", c.StartsOn)
	ed, _ := time.Parse("2006-01-02", c.EndsOn)
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO events (name, type, starts_on, ends_on, discipline_id, location, notes, active)
		VALUES ($1, $2, $3, $4, NULLIF($5,'')::uuid, NULLIF($6,''), NULLIF($7,''), $8)
		RETURNING id`,
		c.Name, c.Type, nullTime(sd), nullTime(ed),
		c.DisciplineID, c.Location, c.Notes, c.Active,
	).Scan(&id)
	return id, err
}

func (s *Store) UpdateCompetition(ctx context.Context, c Competition) error {
	sd, _ := time.Parse("2006-01-02", c.StartsOn)
	ed, _ := time.Parse("2006-01-02", c.EndsOn)
	_, err := s.pool.Exec(ctx, `
		UPDATE events SET
		  name          = $1, type          = $2,
		  starts_on     = $3, ends_on       = $4,
		  discipline_id = NULLIF($5,'')::uuid,
		  location      = NULLIF($6,''),  notes  = NULLIF($7,''),
		  status        = $8::event_status,
		  active        = $9, updated_at    = now()
		WHERE id = $10`,
		c.Name, c.Type, nullTime(sd), nullTime(ed),
		c.DisciplineID, c.Location, c.Notes, c.Status,
		c.Active, c.ID,
	)
	return err
}

func (s *Store) DeleteCompetition(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM events WHERE id = $1`, id)
	return err
}

func (s *Store) SetCompetitionStatus(ctx context.Context, id, status string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE events SET status=$1::event_status, updated_at=now() WHERE id=$2`,
		status, id)
	return err
}

// ── Teilnehmer (Runde / Gruppe) ─────────────────────────────────────────────

func (s *Store) ListParticipants(ctx context.Context, eventID string) ([]CompetitionParticipant, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT cp.id, cp.event_id,
		       COALESCE(cp.team_id::text,''), COALESCE(cp.club_id::text,''), COALESCE(cp.gau_id::text,''),
		       COALESCE(t.name, c.name, g.name,''),
		       CASE WHEN cp.team_id IS NOT NULL THEN 'team'
		            WHEN cp.club_id IS NOT NULL THEN 'club'
		            ELSE 'gau' END,
		       COALESCE(cp.sort_order,0)
		FROM competition_participants cp
		LEFT JOIN teams  t ON t.id = cp.team_id
		LEFT JOIN clubs  c ON c.id = cp.club_id
		LEFT JOIN gaue   g ON g.id = cp.gau_id
		WHERE cp.event_id = $1
		ORDER BY cp.sort_order, cp.created_at`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CompetitionParticipant
	for rows.Next() {
		var p CompetitionParticipant
		if err := rows.Scan(
			&p.ID, &p.EventID,
			&p.TeamID, &p.ClubID, &p.GauID,
			&p.Label, &p.EntityType, &p.SortOrder,
		); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) AddParticipant(ctx context.Context, eventID string, p CompetitionParticipant) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO competition_participants (event_id, team_id, club_id, gau_id, sort_order)
		VALUES ($1, NULLIF($2,'')::uuid, NULLIF($3,'')::uuid, NULLIF($4,'')::uuid, $5)
		RETURNING id`,
		eventID, p.TeamID, p.ClubID, p.GauID, p.SortOrder,
	).Scan(&id)
	return id, err
}

func (s *Store) RemoveParticipant(ctx context.Context, participantID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM competition_participants WHERE id=$1`, participantID)
	return err
}

// ── Starter (Einzel + optional Runde/Gruppe) ─────────────────────────────────

func (s *Store) ListStarters(ctx context.Context, eventID string) ([]CompetitionStarter, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT st.id, st.event_id, st.shooter_id,
		       sh.last_name, sh.first_name,
		       COALESCE(sh.pass_no,''), COALESCE(c.name,''),
		       COALESCE(st.team_id::text,''), COALESCE(t.name,''),
		       COALESCE(st.discipline_id::text,''),
		       COALESCE(st.start_no,''),
		       COALESCE(st.role,'S')
		FROM starters st
		JOIN shooters sh ON sh.id = st.shooter_id
		LEFT JOIN clubs c ON c.id  = sh.club_id
		LEFT JOIN teams t ON t.id  = st.team_id
		WHERE st.event_id = $1
		ORDER BY st.team_id NULLS LAST, st.role, st.start_no NULLS LAST, sh.last_name`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CompetitionStarter
	for rows.Next() {
		var st CompetitionStarter
		if err := rows.Scan(
			&st.ID, &st.EventID, &st.ShooterID,
			&st.LastName, &st.FirstName,
			&st.PassNo, &st.ClubName,
			&st.TeamID, &st.TeamName,
			&st.DisciplineID, &st.StartNo, &st.Role,
		); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Store) AddStarter(ctx context.Context, eventID, shooterID, disciplineID, teamID, startNo, role string) (string, error) {
	if role == "" {
		role = "S"
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO starters (event_id, shooter_id, discipline_id, team_id, start_no, role)
		VALUES ($1, $2, NULLIF($3,'')::uuid, NULLIF($4,'')::uuid, NULLIF($5,''), $6)
		ON CONFLICT (event_id, shooter_id, discipline_id, start_time) DO NOTHING
		RETURNING id`,
		eventID, shooterID, disciplineID, teamID, startNo, role,
	).Scan(&id)
	if err != nil && err.Error() == "no rows in result set" {
		return "", errors.New("Schütze ist bereits eingetragen")
	}
	return id, err
}

func (s *Store) SetStarterRole(ctx context.Context, starterID, role string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE starters SET role=$1 WHERE id=$2`, role, starterID)
	return err
}

// ImportTeamStarters fügt alle Mitglieder einer Mannschaft als Starter ein.
// Schützen, die bereits im Wettkampf eingetragen sind, werden übersprungen.
// Gibt die Anzahl neu hinzugefügter Starter zurück.
func (s *Store) ImportTeamStarters(ctx context.Context, eventID, teamID string) (int, error) {
	// Disziplin des Wettkampfs holen
	var disciplineID *string
	err := s.pool.QueryRow(ctx,
		`SELECT discipline_id::text FROM events WHERE id = $1`, eventID,
	).Scan(&disciplineID)
	if err != nil {
		return 0, err
	}
	if disciplineID == nil || *disciplineID == "" {
		return 0, errors.New("Dem Wettkampf ist keine Disziplin zugeordnet – bitte zuerst im Formular setzen")
	}

	tag, err := s.pool.Exec(ctx, `
		INSERT INTO starters (event_id, shooter_id, discipline_id, team_id, role)
		SELECT $1::uuid, tm.shooter_id, $2::uuid, $3::uuid, 'S'
		FROM team_members tm
		WHERE tm.team_id = $3::uuid
		  AND NOT EXISTS (
		      SELECT 1 FROM starters sx
		      WHERE sx.event_id = $1::uuid
		        AND sx.shooter_id = tm.shooter_id
		  )`,
		eventID, *disciplineID, teamID,
	)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (s *Store) RemoveStarter(ctx context.Context, starterID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM starters WHERE id=$1`, starterID)
	return err
}

// ── Auswertung ──────────────────────────────────────────────────────────────

// SavedAuswertung ist eine gespeicherte Auswertungskonfiguration.
type SavedAuswertung struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	EventID   string         `json:"event_id"`
	EventName string         `json:"event_name"`
	Params    map[string]any `json:"params"`
	CreatedAt string         `json:"created_at"`
}

func (s *Store) ListSavedAuswertungen(ctx context.Context, eventID string) ([]SavedAuswertung, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sa.id::text, sa.name, sa.type,
		       COALESCE(sa.event_id::text,''), COALESCE(e.name,''),
		       sa.params, sa.created_at::text
		FROM saved_auswertungen sa
		LEFT JOIN events e ON e.id = sa.event_id
		WHERE ($1 = '' OR sa.event_id = $1::uuid)
		ORDER BY sa.created_at DESC`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SavedAuswertung
	for rows.Next() {
		var a SavedAuswertung
		var paramsRaw []byte
		if err := rows.Scan(&a.ID, &a.Name, &a.Type,
			&a.EventID, &a.EventName, &paramsRaw, &a.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal(paramsRaw, &a.Params)
		if a.Params == nil {
			a.Params = map[string]any{}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) CreateSavedAuswertung(ctx context.Context, name, typ, eventID string, params map[string]any) (string, error) {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO saved_auswertungen (name, type, event_id, params)
		VALUES ($1, $2, NULLIF($3,'')::uuid, $4)
		RETURNING id::text`, name, typ, eventID, paramsJSON).Scan(&id)
	return id, err
}

func (s *Store) DeleteSavedAuswertung(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM saved_auswertungen WHERE id=$1::uuid`, id)
	return err
}

// GruppenwettkampfStarter ist ein Starter mit Ergebnis für die Gruppenwettkampf-Auswertung.
type GruppenwettkampfStarter struct {
	StarterID      string  `json:"starter_id"`
	ShooterID      string  `json:"shooter_id"`
	LastName       string  `json:"last_name"`
	FirstName      string  `json:"first_name"`
	ClubName       string  `json:"club_name"`
	ClubID         string  `json:"club_id"`
	GauID          string  `json:"gau_id"`
	TeamID         string  `json:"team_id"`
	TeamName       string  `json:"team_name"`
	DisciplineName string  `json:"discipline_name"`
	DecimalScoring bool    `json:"decimal_scoring"`
	ShotCount      int     `json:"shot_count"`
	TotalRings     int     `json:"total_rings"`
	TotalDecimal   float64 `json:"total_decimal"`
	BestCenterDistance float64 `json:"best_center_distance"`
	InnerTens      int     `json:"inner_tens"`
	SessionStatus  string  `json:"session_status"`
	SessionID      string  `json:"session_id"`
}

// GruppenwettkampfData enthält Gruppen (Participants) und Einzelergebnisse.
type GruppenwettkampfData struct {
	Participants []CompetitionParticipant `json:"participants"`
	Starters     []GruppenwettkampfStarter `json:"starters"`
}

func (s *Store) ListGruppenwettkampfData(ctx context.Context, eventID string) (*GruppenwettkampfData, error) {
	parts, err := s.ListParticipants(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if parts == nil {
		parts = []CompetitionParticipant{}
	}

	rows, err := s.pool.Query(ctx, `
		SELECT
			st.id::text,
			st.shooter_id::text,
			sh.last_name, sh.first_name,
			COALESCE(c.name, ''),
			COALESCE(c.id::text, ''),
			COALESCE(c.gau_id::text, ''),
			COALESCE(t.id::text, ''),
			COALESCE(t.name, ''),
			COALESCE(d.name, ''),
			COALESCE(d.decimal_scoring, FALSE),
			COALESCE(best.shot_count,    0),
			COALESCE(best.total_rings,   0),
			COALESCE(best.total_decimal, 0.0),
			COALESCE(best.best_center_distance, 0.0),
			COALESCE(best.inner_tens,    0),
			COALESCE(best.session_status,''),
			COALESCE(best.session_id,    '')
		FROM starters st
		JOIN  shooters sh ON sh.id = st.shooter_id
		LEFT JOIN clubs      c ON c.id = sh.club_id
		LEFT JOIN teams      t ON t.id = st.team_id
		LEFT JOIN disciplines d ON d.id = st.discipline_id
		LEFT JOIN LATERAL (
			SELECT
				se.id::text         AS session_id,
				se.status::text     AS session_status,
				COALESCE(vsr.shot_count,    0)   AS shot_count,
				COALESCE(vsr.total_rings,   0)   AS total_rings,
				COALESCE(vsr.total_decimal, 0.0) AS total_decimal,
				COALESCE(vsr.best_center_distance, 0.0) AS best_center_distance,
				COALESCE(vsr.inner_tens,    0)   AS inner_tens
			FROM sessions se
			LEFT JOIN v_session_results vsr ON vsr.session_id = se.id
			WHERE (se.starter_id = st.id
			    OR (se.event_id = $1::uuid AND se.shooter_id = st.shooter_id
			        AND se.status::text <> 'aborted'))
			ORDER BY COALESCE(vsr.shot_count,0) DESC, se.finished_at DESC NULLS LAST
			LIMIT 1
		) best ON TRUE
		WHERE st.event_id = $1::uuid
		ORDER BY sh.last_name, sh.first_name`,
		eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var starters []GruppenwettkampfStarter
	for rows.Next() {
		var e GruppenwettkampfStarter
		if err := rows.Scan(
			&e.StarterID, &e.ShooterID,
			&e.LastName, &e.FirstName,
			&e.ClubName, &e.ClubID, &e.GauID,
			&e.TeamID, &e.TeamName,
			&e.DisciplineName, &e.DecimalScoring,
			&e.ShotCount, &e.TotalRings, &e.TotalDecimal,
			&e.BestCenterDistance, &e.InnerTens,
			&e.SessionStatus, &e.SessionID,
		); err != nil {
			return nil, err
		}
		starters = append(starters, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if starters == nil {
		starters = []GruppenwettkampfStarter{}
	}
	return &GruppenwettkampfData{Participants: parts, Starters: starters}, nil
}

// RundenwettkampfEntry enthält das Ergebnis eines Starters für die Rundenwettkampf-Auswertung.
type RundenwettkampfEntry struct {
	StarterID      string  `json:"starter_id"`
	ShooterID      string  `json:"shooter_id"`
	LastName       string  `json:"last_name"`
	FirstName      string  `json:"first_name"`
	ClubName       string  `json:"club_name"`
	TeamID         string  `json:"team_id"`
	TeamName       string  `json:"team_name"`
	Role           string  `json:"role"` // S | E | AK
	StartNo        string  `json:"start_no"`
	DisciplineName string  `json:"discipline_name"`
	DecimalScoring bool    `json:"decimal_scoring"`
	ShotCount      int     `json:"shot_count"`
	TotalRings     int     `json:"total_rings"`
	TotalDecimal   float64 `json:"total_decimal"`
	InnerTens      int     `json:"inner_tens"`
	SessionStatus  string  `json:"session_status"`
	SessionID      string  `json:"session_id"`
}

func (s *Store) ListRundenwettkampfResults(ctx context.Context, eventID string) ([]RundenwettkampfEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			st.id::text,
			st.shooter_id::text,
			sh.last_name, sh.first_name,
			COALESCE(c.name, ''),
			COALESCE(t.id::text, ''),
			COALESCE(t.name, ''),
			COALESCE(st.role, 'S'),
			COALESCE(st.start_no, ''),
			COALESCE(d.name, ''),
			COALESCE(d.decimal_scoring, FALSE),
			COALESCE(best.shot_count,     0),
			COALESCE(best.total_rings,    0),
			COALESCE(best.total_decimal,  0.0),
			COALESCE(best.inner_tens,     0),
			COALESCE(best.session_status, ''),
			COALESCE(best.session_id,     '')
		FROM starters st
		JOIN  shooters sh ON sh.id = st.shooter_id
		LEFT JOIN clubs      c ON c.id = sh.club_id
		LEFT JOIN teams      t ON t.id = st.team_id
		LEFT JOIN disciplines d ON d.id = st.discipline_id
		LEFT JOIN LATERAL (
			SELECT
				se.id::text         AS session_id,
				se.status::text     AS session_status,
				COALESCE(vsr.shot_count,    0)   AS shot_count,
				COALESCE(vsr.total_rings,   0)   AS total_rings,
				COALESCE(vsr.total_decimal, 0.0) AS total_decimal,
				COALESCE(vsr.inner_tens,    0)   AS inner_tens
			FROM sessions se
			LEFT JOIN v_session_results vsr ON vsr.session_id = se.id
			WHERE (se.starter_id = st.id
			    OR (se.event_id = $1::uuid AND se.shooter_id = st.shooter_id
			        AND se.status::text <> 'aborted'))
			ORDER BY COALESCE(vsr.shot_count,0) DESC, se.finished_at DESC NULLS LAST
			LIMIT 1
		) best ON TRUE
		WHERE st.event_id = $1::uuid
		ORDER BY
			t.name NULLS LAST,
			CASE WHEN COALESCE(st.role,'S') = 'AK' THEN 1 ELSE 0 END,
			CASE WHEN COALESCE(d.decimal_scoring, FALSE)
			     THEN COALESCE(best.total_decimal, -1)
			     ELSE COALESCE(best.total_rings, -1)::numeric
			END DESC,
			COALESCE(best.inner_tens, -1) DESC`,
		eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RundenwettkampfEntry
	for rows.Next() {
		var e RundenwettkampfEntry
		if err := rows.Scan(
			&e.StarterID, &e.ShooterID,
			&e.LastName, &e.FirstName,
			&e.ClubName, &e.TeamID, &e.TeamName,
			&e.Role, &e.StartNo, &e.DisciplineName, &e.DecimalScoring,
			&e.ShotCount, &e.TotalRings, &e.TotalDecimal, &e.InnerTens,
			&e.SessionStatus, &e.SessionID,
		); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ----------------------------------------------------------------------------
// Simulator (Kalibrier-Neuberechnung aus gespeicherten air_ns-Rohdaten,
// siehe simulator.go)
// ----------------------------------------------------------------------------

// SimShotRaw: ein Schuss mit allen fuer die Neuberechnung noetigen Rohdaten.
type SimShotRaw struct {
	ShotNo       int        `json:"shot_no"`
	Kind         string     `json:"kind"`
	Status       string     `json:"status"`
	XMM          float64    `json:"x_mm"`
	YMM          float64    `json:"y_mm"`
	Ring         int        `json:"ring"`
	Decimal      float64    `json:"decimal"`
	AirNs        [6][]int64 `json:"air_ns"`
	HasRaw       bool       `json:"has_raw"`
	RejectReason string     `json:"reject_reason,omitempty"`
}

// SessionShotsRaw laedt alle Schuesse einer Session inkl. air_ns-Rohdaten
// fuer den Simulator. reject-Telegramme haben kein air_ns (HasRaw=false) -
// die Firmware sendet dafuer keine Mikrofon-Flanken, sie koennen daher nicht
// neu berechnet werden.
func (s *Store) SessionShotsRaw(ctx context.Context, sessionID string) ([]SimShotRaw, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT shot_no, kind::text, status::text, x_mm, y_mm, ring, decimal_value,
		       air_ns, COALESCE(reject_reason,'')
		FROM shots WHERE session_id=$1 ORDER BY shot_no`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SimShotRaw
	for rows.Next() {
		var r SimShotRaw
		var airNsRaw []byte
		if err := rows.Scan(&r.ShotNo, &r.Kind, &r.Status, &r.XMM, &r.YMM,
			&r.Ring, &r.Decimal, &airNsRaw, &r.RejectReason); err != nil {
			return nil, err
		}
		if len(airNsRaw) > 0 {
			var nested [][]int64
			if err := json.Unmarshal(airNsRaw, &nested); err == nil && len(nested) == 6 {
				for i := 0; i < 6; i++ {
					r.AirNs[i] = nested[i]
				}
				r.HasRaw = true
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SessionTargets liefert die Ziel-IDs (Wertung/Probe, falls abweichend), die
// Serienlaenge und die StandPC-Scheibennummer (fuer die visuelle Referenz-
// scheibe im Simulator, siehe target_geometry.go) der Disziplin einer
// Session - fuer die Wertung im Simulator (analog zur Stand-PC-Logik: Probe
// nutzt sighting_target_id, falls gesetzt, sonst dieselbe Scheibe wie die
// Wertung).
func (s *Store) SessionTargets(ctx context.Context, sessionID string) (targetID, sightingTargetID string, shotsPerSeries, standpcTargetNo int, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT d.target_id::text, COALESCE(d.sighting_target_id::text,''),
		       d.shots_per_series, d.standpc_target_no
		FROM sessions se JOIN disciplines d ON d.id = se.discipline_id
		WHERE se.id = $1`, sessionID).Scan(&targetID, &sightingTargetID, &shotsPerSeries, &standpcTargetNo)
	return
}

// LoadTargetDef laedt die Scheibengeometrie (targets + target_rings) fuer
// die Wertung im Simulator (simulator.go: TargetDef/Scorer).
func (s *Store) LoadTargetDef(ctx context.Context, targetID string) (*TargetDef, error) {
	var t TargetDef
	if err := s.pool.QueryRow(ctx, `
		SELECT name, caliber_mm, edge_scoring, COALESCE(inner_ten_d_mm,0)
		FROM targets WHERE id=$1::uuid`, targetID).Scan(
		&t.Name, &t.CaliberMM, &t.EdgeScoring, &t.InnerTenDMM); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT ring_value, diameter_mm FROM target_rings WHERE target_id=$1::uuid`, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var rd RingDef
		if err := rows.Scan(&rd.Value, &rd.DiameterMM); err != nil {
			return nil, err
		}
		t.Rings = append(t.Rings, rd)
	}
	return &t, rows.Err()
}

// SimulatorConfig: benannter, wiederverwendbarer Parametersatz.
type SimulatorConfig struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Params    SimParams `json:"params"`
	CreatedAt string    `json:"created_at"`
}

func (s *Store) ListSimulatorConfigs(ctx context.Context) ([]SimulatorConfig, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, name, params, created_at::text
		FROM simulator_configs ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SimulatorConfig
	for rows.Next() {
		var c SimulatorConfig
		var paramsRaw []byte
		if err := rows.Scan(&c.ID, &c.Name, &paramsRaw, &c.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(paramsRaw, &c.Params)
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveSimulatorConfig legt einen Parametersatz an oder ueberschreibt ihn,
// falls der Name bereits existiert ("Speichern unter existierendem Namen").
func (s *Store) SaveSimulatorConfig(ctx context.Context, name string, params SimParams) (string, error) {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO simulator_configs (name, params) VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET params = EXCLUDED.params
		RETURNING id::text`, name, paramsJSON).Scan(&id)
	return id, err
}

func (s *Store) DeleteSimulatorConfig(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM simulator_configs WHERE id=$1::uuid`, id)
	return err
}
