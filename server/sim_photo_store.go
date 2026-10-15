// ============================================================================
// sim_photo_store.go – Foto der beschossenen Scheibe zur Foto-Kalibrierung
// dauerhaft speichern (Tabelle session_photos, migrations/080).
//
// Das Foto gehoert zur Session (= Scheibe). Laedt der Simulator die Scheibe
// erneut, holt er Foto und Markierungen (Schrauben, Innenkreis, Loecher,
// Einstellungen) automatisch - Auswertungen lassen sich so jederzeit
// wiederholen. Ein neu hochgeladenes Foto ersetzt das vorhandene; seine
// Markierungen werden dabei verworfen (sie gehoeren zum alten Bild).
//
//	GET    /api/sessions/{id}/photo          Bilddaten (404 = keins)
//	GET    /api/sessions/{id}/photo/meta     Dateiname, Groesse, Zeitpunkt, Markierungen
//	PUT    /api/sessions/{id}/photo          Bild hochladen (Body = Bilddatei)
//	PUT    /api/sessions/{id}/photo/markers  Markierungen speichern (JSON)
//	DELETE /api/sessions/{id}/photo          Foto loeschen
//
// Alles nur mit Kachel "simulator".
// ============================================================================
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const sessionPhotoMaxBytes = 30 << 20 // 30 MB (Handyfotos sind typ. 3-12 MB)

type sessionPhotoMeta struct {
	SessionID   string          `json:"session_id"`
	ContentType string          `json:"content_type"`
	Filename    string          `json:"filename"`
	SizeBytes   int             `json:"size_bytes"`
	UploadedAt  time.Time       `json:"uploaded_at"`
	UploadedBy  string          `json:"uploaded_by"`
	Markers     json.RawMessage `json:"markers,omitempty"`
	MarkersAt   *time.Time      `json:"markers_at,omitempty"`
}

func (a *APIServer) getSessionPhotoMeta(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, "simulator"); err != nil {
		return nil, err
	}
	var m sessionPhotoMeta
	err := a.store.pool.QueryRow(r.Context(), `
		SELECT session_id::text, content_type, filename, length(data), uploaded_at, uploaded_by, markers, markers_at
		FROM session_photos WHERE session_id=$1::uuid`, r.PathValue("id")).Scan(
		&m.SessionID, &m.ContentType, &m.Filename, &m.SizeBytes, &m.UploadedAt, &m.UploadedBy, &m.Markers, &m.MarkersAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &httpError{code: 404, msg: "kein Foto zu dieser Scheibe"}
	}
	return m, err
}

// getSessionPhoto liefert die Bilddaten (eigener Handler, kein JSON).
func (a *APIServer) getSessionPhoto(w http.ResponseWriter, r *http.Request) {
	if _, err := a.requireTile(w, r, "simulator"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	var ct string
	var data []byte
	err := a.store.pool.QueryRow(r.Context(),
		`SELECT content_type, data FROM session_photos WHERE session_id=$1::uuid`, r.PathValue("id")).Scan(&ct, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "kein Foto", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

func (a *APIServer) putSessionPhoto(w http.ResponseWriter, r *http.Request) (any, error) {
	role, err := a.requireTile(w, r, "simulator")
	if err != nil {
		return nil, err
	}
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") {
		return nil, errBadRequest("nur Bilddateien (image/*)")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, sessionPhotoMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errBadRequest("leere Datei")
	}
	if len(data) > sessionPhotoMaxBytes {
		return nil, errBadRequest(fmt.Sprintf("Foto zu groß (max. %d MB)", sessionPhotoMaxBytes>>20))
	}
	name, _ := url.QueryUnescape(r.Header.Get("X-Filename"))
	tag, err := a.store.pool.Exec(r.Context(), `
		INSERT INTO session_photos (session_id, content_type, filename, data, markers, uploaded_at, uploaded_by, markers_at)
		SELECT id, $2, $3, $4, NULL, now(), $5, NULL FROM sessions WHERE id=$1::uuid
		ON CONFLICT (session_id) DO UPDATE SET content_type=EXCLUDED.content_type, filename=EXCLUDED.filename,
		  data=EXCLUDED.data, markers=NULL, uploaded_at=now(), uploaded_by=EXCLUDED.uploaded_by, markers_at=NULL`,
		r.PathValue("id"), ct, name, data, role.RoleKey)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, &httpError{code: 404, msg: "Session nicht gefunden"}
	}
	return a.getSessionPhotoMeta(w, r)
}

func (a *APIServer) putSessionPhotoMarkers(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, "simulator"); err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if !json.Valid(body) {
		return nil, errBadRequest("Markierungen: ungueltiges JSON")
	}
	tag, err := a.store.pool.Exec(r.Context(),
		`UPDATE session_photos SET markers=$2::jsonb, markers_at=now() WHERE session_id=$1::uuid`,
		r.PathValue("id"), string(body))
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, &httpError{code: 404, msg: "kein Foto zu dieser Scheibe"}
	}
	return map[string]bool{"ok": true}, nil
}

func (a *APIServer) deleteSessionPhoto(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, "simulator"); err != nil {
		return nil, err
	}
	if _, err := a.store.pool.Exec(r.Context(), `DELETE FROM session_photos WHERE session_id=$1::uuid`, r.PathValue("id")); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}
