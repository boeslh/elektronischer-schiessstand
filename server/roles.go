// ============================================================================
// roles.go – rollenbasierte Benutzer-/Rechteverwaltung.
//
// Kein individuelles Login: 4 feste Rollen (admin/developer/anwender/revisor)
// mit je einem optionalen gemeinsamen Passwort. Ohne Cookie ist man
// automatisch "anwender" (sofern dafür kein Passwort gesetzt ist). Ein
// Rollenwechsel verlangt das Passwort der Zielrolle, falls eines gesetzt ist.
//
// Rechte werden serverseitig durchgesetzt: serveHTMLGated prüft pro Seite
// die Kachel-Zugehörigkeit (ui_role_tiles), requireCorrection prüft das
// Revisor-Sonderrecht "Ergebnisse korrigieren" (Annullieren, Trefferwert-
// Korrektur, Session-Neuberechnung übernehmen). "benutzerverwaltung" ist
// bewusst nicht Teil der Kachel-Matrix, sondern hart auf role_key='admin'
// verdrahtet (requireAdmin), siehe migrations/016_ui_roles.sql.
// ============================================================================
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"log"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

const roleCookieName = "ss_role"

var ErrWrongPassword = errors.New("falsches Passwort")
var ErrUnknownRole = errors.New("unbekannte Rolle")

var knownTileKeys = []string{
	"lanes", "stammdaten", "disciplines", "wettkampf", "standaktion",
	"ergebnisse", "simulator", "auswertung", "settings", "archiv",
}

func isKnownTile(t string) bool { return containsStr(knownTileKeys, t) }

func containsStr(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// ----------------------------------------------------------------------------
// Store: Rollen, Kacheln, Sessions
// ----------------------------------------------------------------------------

type RoleInfo struct {
	RoleKey           string   `json:"role_key"`
	DisplayName       string   `json:"display_name"`
	Tiles             []string `json:"tiles"`
	CanCorrectResults bool     `json:"can_correct_results"`
}

type RoleAdmin struct {
	RoleKey           string   `json:"role_key"`
	DisplayName       string   `json:"display_name"`
	HasPassword       bool     `json:"has_password"`
	CanCorrectResults bool     `json:"can_correct_results"`
	Tiles             []string `json:"tiles"`
	SortOrder         int      `json:"-"`
}

func (s *Store) roleTiles(ctx context.Context, roleKey string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT tile_key FROM ui_role_tiles WHERE role_key=$1 ORDER BY tile_key`, roleKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tiles []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tiles = append(tiles, t)
	}
	return tiles, rows.Err()
}

func (s *Store) RoleInfo(ctx context.Context, roleKey string) (RoleInfo, error) {
	var displayName string
	var canCorrect bool
	if err := s.pool.QueryRow(ctx,
		`SELECT display_name, can_correct_results FROM ui_roles WHERE role_key=$1`,
		roleKey).Scan(&displayName, &canCorrect); err != nil {
		return RoleInfo{}, err
	}
	tiles, err := s.roleTiles(ctx, roleKey)
	if err != nil {
		return RoleInfo{}, err
	}
	return RoleInfo{RoleKey: roleKey, DisplayName: displayName, Tiles: tiles, CanCorrectResults: canCorrect}, nil
}

func (s *Store) ListRoles(ctx context.Context) ([]RoleAdmin, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT role_key, display_name, password_hash IS NOT NULL, can_correct_results, sort_order
		FROM ui_roles ORDER BY sort_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoleAdmin
	for rows.Next() {
		var ra RoleAdmin
		if err := rows.Scan(&ra.RoleKey, &ra.DisplayName, &ra.HasPassword,
			&ra.CanCorrectResults, &ra.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, ra)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		tiles, err := s.roleTiles(ctx, out[i].RoleKey)
		if err != nil {
			return nil, err
		}
		out[i].Tiles = tiles
	}
	return out, nil
}

func (s *Store) SetRoleTiles(ctx context.Context, roleKey string, tiles []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM ui_role_tiles WHERE role_key=$1`, roleKey); err != nil {
		return err
	}
	for _, t := range tiles {
		if _, err := tx.Exec(ctx,
			`INSERT INTO ui_role_tiles (role_key, tile_key) VALUES ($1,$2)`, roleKey, t); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) SetRolePassword(ctx context.Context, roleKey, password string) error {
	var hash *string
	if password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		hs := string(h)
		hash = &hs
	}
	ct, err := s.pool.Exec(ctx, `UPDATE ui_roles SET password_hash=$2 WHERE role_key=$1`, roleKey, hash)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrUnknownRole
	}
	return nil
}

func (s *Store) SetRoleCorrectionRight(ctx context.Context, roleKey string, can bool) error {
	ct, err := s.pool.Exec(ctx,
		`UPDATE ui_roles SET can_correct_results=$2 WHERE role_key=$1`, roleKey, can)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrUnknownRole
	}
	return nil
}

func (s *Store) createSession(ctx context.Context, roleKey string) (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO ui_role_sessions (token, role_key) VALUES ($1,$2)`, token, roleKey); err != nil {
		return "", err
	}
	return token, nil
}

// ResolveOrCreateSession liest den Cookie-Token. Ohne (gültigen) Token wird
// automatisch eine Anwender-Session angelegt, SOFERN für 'anwender' kein
// Passwort gesetzt ist - newToken ist dann != "" (Aufrufer muss den Cookie
// setzen). ok=false bedeutet: Login noetig (Anwender-Passwort gesetzt, aber
// kein/kein gültiger Cookie vorhanden).
func (s *Store) ResolveOrCreateSession(ctx context.Context, token string) (roleKey, newToken string, ok bool, err error) {
	if token != "" {
		var rk string
		scanErr := s.pool.QueryRow(ctx,
			`SELECT role_key FROM ui_role_sessions WHERE token=$1`, token).Scan(&rk)
		if scanErr == nil {
			_, _ = s.pool.Exec(ctx,
				`UPDATE ui_role_sessions SET last_seen_at=now() WHERE token=$1`, token)
			return rk, "", true, nil
		}
		if !errors.Is(scanErr, pgx.ErrNoRows) {
			return "", "", false, scanErr
		}
	}

	var anwenderHasPassword bool
	if err := s.pool.QueryRow(ctx,
		`SELECT password_hash IS NOT NULL FROM ui_roles WHERE role_key='anwender'`,
	).Scan(&anwenderHasPassword); err != nil {
		return "", "", false, err
	}
	if anwenderHasPassword {
		return "", "", false, nil
	}
	newTok, err := s.createSession(ctx, "anwender")
	if err != nil {
		return "", "", false, err
	}
	return "anwender", newTok, true, nil
}

// SwitchRole prueft das Passwort der Zielrolle (kein Passwort gesetzt = immer
// erlaubt) und legt bei Erfolg eine neue Session an.
func (s *Store) SwitchRole(ctx context.Context, targetRoleKey, password string) (string, error) {
	var hash *string
	if err := s.pool.QueryRow(ctx,
		`SELECT password_hash FROM ui_roles WHERE role_key=$1`, targetRoleKey,
	).Scan(&hash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrUnknownRole
		}
		return "", err
	}
	if hash != nil {
		if err := bcrypt.CompareHashAndPassword([]byte(*hash), []byte(password)); err != nil {
			return "", ErrWrongPassword
		}
	}
	token, err := s.createSession(ctx, targetRoleKey)
	if err != nil {
		return "", err
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO audit_log (action, actor, details)
		 VALUES ('role_switched', $1, jsonb_build_object('role',$1::text))`,
		targetRoleKey); err != nil {
		log.Printf("audit_log role_switched: %v", err)
	}
	return token, nil
}

// CorrectShotValue: Revisor-Korrektur eines Trefferwerts (x/y manuell neu
// gesetzt, Ring/Zehntel/Innenzehner/Teiler ueber denselben Scorer wie beim
// Simulator neu berechnet). Schreibt in die corrected_*-Spalten - die
// Originalmessung (x_mm/y_mm/ring/...) bleibt unveraendert erhalten, alle
// Auswertungen verwenden ab jetzt automatisch den korrigierten Wert (siehe
// v_scoring_shots). Analog zu AnnulShot mit Audit-Log.
func (s *Store) CorrectShotValue(ctx context.Context, sessionID string, shotNo int,
	xmm, ymm float64, actor string) (map[string]any, error) {

	var kind string
	var oldX, oldY, oldDecimal float64
	var oldRing int
	var prevCX, prevCY, prevCDecimal *float64
	var prevCRing *int
	if err := s.pool.QueryRow(ctx,
		`SELECT kind::text, x_mm, y_mm, ring, decimal_value,
		        corrected_x_mm, corrected_y_mm, corrected_ring, corrected_decimal_value
		 FROM shots WHERE session_id=$1 AND shot_no=$2`, sessionID, shotNo,
	).Scan(&kind, &oldX, &oldY, &oldRing, &oldDecimal,
		&prevCX, &prevCY, &prevCRing, &prevCDecimal); err != nil {
		return nil, fmt.Errorf("Schuss %d: %w", shotNo, err)
	}
	// Vorheriger EFFEKTIVER Wert fuers Audit-Log (bereits korrigiert, falls vorhanden).
	effX, effY, effDecimal, effRing := oldX, oldY, oldDecimal, oldRing
	if prevCRing != nil {
		effX, effY, effDecimal, effRing = *prevCX, *prevCY, *prevCDecimal, *prevCRing
	}

	targetID, sightingTargetID, _, _, err := s.SessionTargets(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	useTargetID := targetID
	if kind == "sighting" && sightingTargetID != "" {
		useTargetID = sightingTargetID
	}
	target, err := s.LoadTargetDef(ctx, useTargetID)
	if err != nil {
		return nil, err
	}
	res := NewScorer(target).Score(xmm, ymm)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var shotID string
	if err := tx.QueryRow(ctx, `
		UPDATE shots SET corrected_x_mm=$3, corrected_y_mm=$4, corrected_ring=$5,
		  corrected_decimal_value=$6, corrected_is_inner_ten=$7, corrected_center_distance=$8,
		  corrected_at=now(), corrected_by=$9
		WHERE session_id=$1 AND shot_no=$2 RETURNING id`,
		sessionID, shotNo, xmm, ymm, res.Ring, res.Decimal, res.InnerTen, res.CenterDistance, actor,
	).Scan(&shotID); err != nil {
		return nil, fmt.Errorf("Schuss %d: %w", shotNo, err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (action, session_id, shot_id, actor, details)
		VALUES ('shot_value_corrected', $1::uuid, $2::uuid, $3, jsonb_build_object(
			'old_x_mm',$4::float8,'old_y_mm',$5::float8,'old_ring',$6::int,'old_decimal',$7::float8,
			'new_x_mm',$8::float8,'new_y_mm',$9::float8,'new_ring',$10::int,'new_decimal',$11::float8))`,
		sessionID, shotID, actor, effX, effY, effRing, effDecimal,
		xmm, ymm, res.Ring, res.Decimal,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return map[string]any{
		"shot_no": shotNo, "x_mm": oldX, "y_mm": oldY, "ring": oldRing, "decimal": oldDecimal,
		"corrected_x_mm": xmm, "corrected_y_mm": ymm, "corrected_ring": res.Ring,
		"corrected_decimal": res.Decimal, "corrected_inner_ten": res.InnerTen,
		"corrected_center_distance": res.CenterDistance,
	}, nil
}

// RevertShotCorrection: entfernt eine Korrektur wieder, der Originalwert
// (x_mm/y_mm/ring/...) greift danach wieder ueberall.
func (s *Store) RevertShotCorrection(ctx context.Context, sessionID string, shotNo int, actor string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var shotID string
	var hadCorrection bool
	if err := tx.QueryRow(ctx,
		`SELECT id, corrected_ring IS NOT NULL FROM shots WHERE session_id=$1 AND shot_no=$2`,
		sessionID, shotNo,
	).Scan(&shotID, &hadCorrection); err != nil {
		return fmt.Errorf("Schuss %d: %w", shotNo, err)
	}
	if !hadCorrection {
		return nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE shots SET corrected_x_mm=NULL, corrected_y_mm=NULL, corrected_ring=NULL,
		  corrected_decimal_value=NULL, corrected_is_inner_ten=NULL, corrected_center_distance=NULL,
		  corrected_at=NULL, corrected_by=NULL
		WHERE id=$1`, shotID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (action, session_id, shot_id, actor)
		VALUES ('shot_correction_reverted', $1::uuid, $2::uuid, $3)`,
		sessionID, shotID, actor); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ApplySessionRecalibration: wendet dieselbe Neuberechnung wie simulateSession
// (aus den gespeicherten air_ns-Rohdaten) auf die Session an und schreibt die
// Ergebnisse als Korrektur (corrected_*-Spalten) zurueck - die Original-
// messung bleibt erhalten. Betrifft NUR die gespeicherten Ergebnisse dieser
// Session, nicht die laufende Stand-Kalibrierung/ESP32.
func (s *Store) ApplySessionRecalibration(ctx context.Context, sessionID string,
	params SimParams, actor string) (int, error) {

	targetID, sightingTargetID, _, _, err := s.SessionTargets(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	matchTarget, err := s.LoadTargetDef(ctx, targetID)
	if err != nil {
		return 0, err
	}
	scorerMatch := NewScorer(matchTarget)
	scorerSighting := scorerMatch
	if sightingTargetID != "" {
		sightingTarget, err := s.LoadTargetDef(ctx, sightingTargetID)
		if err != nil {
			return 0, err
		}
		scorerSighting = NewScorer(sightingTarget)
	}

	shots, err := s.SessionShotsRaw(ctx, sessionID)
	if err != nil {
		return 0, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	updated := 0
	for _, sh := range shots {
		if !sh.HasRaw {
			continue
		}
		sim := SolveShot(sh.AirNs, params)
		if !sim.PosValid {
			continue
		}
		xmm := float64(sim.XUm) / 1000.0
		ymm := float64(sim.YUm) / 1000.0
		sc := scorerMatch
		if sh.Kind == "sighting" {
			sc = scorerSighting
		}
		res := sc.Score(xmm, ymm)
		if _, err := tx.Exec(ctx, `
			UPDATE shots SET corrected_x_mm=$3, corrected_y_mm=$4, corrected_ring=$5,
			  corrected_decimal_value=$6, corrected_is_inner_ten=$7, corrected_center_distance=$8,
			  corrected_at=now(), corrected_by=$9
			WHERE session_id=$1 AND shot_no=$2`,
			sessionID, sh.ShotNo, xmm, ymm, res.Ring, res.Decimal, res.InnerTen, res.CenterDistance, actor,
		); err != nil {
			return 0, fmt.Errorf("Schuss %d: %w", sh.ShotNo, err)
		}
		updated++
	}

	if updated > 0 {
		paramsJSON, _ := json.Marshal(params)
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_log (action, session_id, actor, details)
			VALUES ('session_recalibrated', $1::uuid, $2,
			        jsonb_build_object('params',$3::jsonb,'updated_count',$4::int))`,
			sessionID, actor, string(paramsJSON), updated,
		); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return updated, nil
}

// ----------------------------------------------------------------------------
// HTTP-Ebene: Rollenaufloesung + Zugriffsprüfungen
// ----------------------------------------------------------------------------

// resolveRole liest/erzeugt den Rollen-Cookie und liefert die aktuelle Rolle.
// ok=false bedeutet: Login noetig (kein Zugriff moeglich, bis eine gueltige
// Rolle per /api/role/switch gesetzt wird).
func (a *APIServer) resolveRole(w http.ResponseWriter, r *http.Request) (RoleInfo, bool, error) {
	token := ""
	if c, err := r.Cookie(roleCookieName); err == nil {
		token = c.Value
	}
	roleKey, newToken, ok, err := a.store.ResolveOrCreateSession(r.Context(), token)
	if err != nil {
		return RoleInfo{}, false, err
	}
	if !ok {
		return RoleInfo{}, false, nil
	}
	if newToken != "" {
		http.SetCookie(w, &http.Cookie{
			Name: roleCookieName, Value: newToken, Path: "/",
			HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 60 * 60 * 24 * 365,
		})
	}
	info, err := a.store.RoleInfo(r.Context(), roleKey)
	if err != nil {
		return RoleInfo{}, false, err
	}
	return info, true, nil
}

func (a *APIServer) requireTile(w http.ResponseWriter, r *http.Request, tileKey string) (RoleInfo, error) {
	role, ok, err := a.resolveRole(w, r)
	if err != nil {
		return RoleInfo{}, err
	}
	if !ok || !containsStr(role.Tiles, tileKey) {
		return RoleInfo{}, errForbidden("Diese Seite ist fuer deine aktuelle Rolle nicht freigegeben.")
	}
	return role, nil
}

func (a *APIServer) requireCorrection(w http.ResponseWriter, r *http.Request) (RoleInfo, error) {
	role, ok, err := a.resolveRole(w, r)
	if err != nil {
		return RoleInfo{}, err
	}
	if !ok || !role.CanCorrectResults {
		return RoleInfo{}, errForbidden("Keine Berechtigung fuer Ergebniskorrekturen.")
	}
	return role, nil
}

func (a *APIServer) requireAdmin(w http.ResponseWriter, r *http.Request) (RoleInfo, error) {
	role, ok, err := a.resolveRole(w, r)
	if err != nil {
		return RoleInfo{}, err
	}
	if !ok || role.RoleKey != "admin" {
		return RoleInfo{}, errForbidden("Nur fuer Admin.")
	}
	return role, nil
}

// serveHTMLGated wie serveHTML, liefert bei fehlendem Kachel-Recht eine
// minimale Hinweisseite statt des eigentlichen Inhalts.
func (a *APIServer) serveHTMLGated(fsys fs.FS, name, tileKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := a.requireTile(w, r, tileKey); err != nil {
			writeAccessDeniedPage(w, err)
			return
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	}
}

// writeAccessDeniedPage rendert eine minimale Hinweisseite fuer gesperrte
// Routen (verwendet von serveHTMLGated und der /benutzerverwaltung-Route).
func writeAccessDeniedPage(w http.ResponseWriter, err error) {
	code := http.StatusForbidden
	msg := "Kein Zugriff."
	var he *httpError
	if errors.As(err, &he) {
		code, msg = he.code, he.msg
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, `<!DOCTYPE html><html lang="de"><head><meta charset="UTF-8">
<title>Kein Zugriff</title><style>body{background:#12161b;color:#dce4ec;font-family:system-ui,sans-serif;
display:flex;align-items:center;justify-content:center;height:100vh;margin:0}
.box{text-align:center}.box a{color:#4ab8a0}</style></head><body>
<div class="box"><h1>🔒 Kein Zugriff</h1><p>%s</p><p><a href="/">← Zur Startseite</a></p></div>
</body></html>`, html.EscapeString(msg))
}

// ----------------------------------------------------------------------------
// HTTP-Handler
// ----------------------------------------------------------------------------

func (a *APIServer) getRole(w http.ResponseWriter, r *http.Request) (any, error) {
	role, ok, err := a.resolveRole(w, r)
	if err != nil {
		return nil, err
	}
	if !ok {
		return map[string]any{
			"role_key": nil, "display_name": nil,
			"tiles": []string{}, "can_correct_results": false,
		}, nil
	}
	return role, nil
}

func (a *APIServer) switchRole(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		RoleKey  string `json:"role_key"`
		Password string `json:"password"`
	}](r)
	if err != nil || body.RoleKey == "" {
		return nil, errBadRequest("role_key erforderlich")
	}
	token, err := a.store.SwitchRole(r.Context(), body.RoleKey, body.Password)
	if err != nil {
		return nil, err
	}
	http.SetCookie(w, &http.Cookie{
		Name: roleCookieName, Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 60 * 60 * 24 * 365,
	})
	return a.store.RoleInfo(r.Context(), body.RoleKey)
}

func (a *APIServer) listRoles(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	roles, err := a.store.ListRoles(r.Context())
	if err != nil {
		return nil, err
	}
	return map[string]any{"roles": roles, "known_tiles": knownTileKeys}, nil
}

func (a *APIServer) setRoleTiles(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		TileKeys []string `json:"tile_keys"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body")
	}
	for _, t := range body.TileKeys {
		if !isKnownTile(t) {
			return nil, errBadRequest("unbekannte Kachel: " + t)
		}
	}
	if err := a.store.SetRoleTiles(r.Context(), r.PathValue("key"), body.TileKeys); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) setRolePassword(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		Password string `json:"password"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body")
	}
	if err := a.store.SetRolePassword(r.Context(), r.PathValue("key"), body.Password); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) setRoleCorrection(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		CanCorrectResults bool `json:"can_correct_results"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body")
	}
	if err := a.store.SetRoleCorrectionRight(r.Context(), r.PathValue("key"), body.CanCorrectResults); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) correctShot(w http.ResponseWriter, r *http.Request) (any, error) {
	role, err := a.requireCorrection(w, r)
	if err != nil {
		return nil, err
	}
	no, err := strconv.Atoi(r.PathValue("no"))
	if err != nil {
		return nil, errBadRequest("ungueltige Schussnummer")
	}
	body, err := decodeBody[struct {
		XMM float64 `json:"x_mm"`
		YMM float64 `json:"y_mm"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body: " + err.Error())
	}
	return a.store.CorrectShotValue(r.Context(), r.PathValue("id"), no, body.XMM, body.YMM, role.RoleKey)
}

func (a *APIServer) revertShotCorrection(w http.ResponseWriter, r *http.Request) (any, error) {
	role, err := a.requireCorrection(w, r)
	if err != nil {
		return nil, err
	}
	no, err := strconv.Atoi(r.PathValue("no"))
	if err != nil {
		return nil, errBadRequest("ungueltige Schussnummer")
	}
	if err := a.store.RevertShotCorrection(r.Context(), r.PathValue("id"), no, role.RoleKey); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) applySessionSimulation(w http.ResponseWriter, r *http.Request) (any, error) {
	role, err := a.requireCorrection(w, r)
	if err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		Params SimParams `json:"params"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body: " + err.Error())
	}
	updated, err := a.store.ApplySessionRecalibration(r.Context(), r.PathValue("id"), body.Params, role.RoleKey)
	if err != nil {
		return nil, err
	}
	return map[string]any{"updated": updated}, nil
}

func errForbidden(msg string) error {
	return &httpError{code: http.StatusForbidden, msg: msg}
}
