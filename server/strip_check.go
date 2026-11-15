// ============================================================================
// strip_check.go – Bandkontrolle: Foto des Papierbands mit dem gespeicherten
// Ergebnis vergleichen (Zuordnung siehe strip_match.go, Bilderkennung und
// Anzeige in web/bandkontrolle.html).
//
//	GET    /bandkontrolle?session_id=…               Seite
//	GET    /api/sessions/{id}/strip-info             Schüsse mit Vorschub-Zählung, Band-Einstellungen des Stands
//	POST   /api/sessions/{id}/strip-match            Löcher (mm auf dem Band) den Schüssen zuordnen
//	GET    /api/sessions/{id}/strip-photos               alle Fotos (Nummer, Dateiname, Markierungen)
//	POST   /api/sessions/{id}/strip-photos               weiteres Foto hochladen (Body = Bilddatei)
//	GET    /api/sessions/{id}/strip-photos/{no}          Bilddaten
//	PUT    /api/sessions/{id}/strip-photos/{no}/markers  Markierungen speichern (JSON)
//	DELETE /api/sessions/{id}/strip-photos/{no}          Foto löschen
//
// Mehrere Fotos je Scheibe (z.B. bei langen Bändern); sie dürfen sich
// überschneiden oder Lücken lassen. Jedes Foto wird für sich zugeordnet
// (eigene Lage, Perspektive, Orientierung), die Seite führt die Ergebnisse
// je Schuss zusammen.
//
// Vorschub-Zählung: die Firmware schiebt nach einer Auslösung vor, wenn
// PAPERAUTO=1 und PAPERTRIGGER passt (ANY = immer, PIEZO = Piezo hat
// ausgelöst, CLEAN = "clean"-Schuss). Maßgeblich ist die Konfiguration des
// Stands zu Beginn der Scheibe (esp32_config_history). Von Hand oder durch
// einen Scheibenwechsel ausgelöste Vorschübe sind nicht gespeichert - die
// Zuordnung erkennt solche Sprünge selbst (strip_match.go).
//
// Alles nur mit Kachel "simulator" (technische Kontrolle).
// ============================================================================
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const stripTile = "simulator"

type stripInfo struct {
	SessionID   string      `json:"session_id"`
	LaneNo      int         `json:"lane_no"`
	FeedMM      float64     `json:"feed_mm"`
	Trigger     string      `json:"trigger"`
	Auto        bool        `json:"auto"`
	ConfigKnown bool        `json:"config_known"`
	Shots       []stripShot `json:"shots"`
	Feeds       int         `json:"feeds"` // Vorschübe insgesamt
}

func (a *APIServer) loadStripInfo(r *http.Request, sessionID string) (*stripInfo, error) {
	ctx := r.Context()
	info := &stripInfo{SessionID: sessionID, FeedMM: 50, Trigger: "PIEZO", Auto: true}
	var start *time.Time
	err := a.store.pool.QueryRow(ctx, `
		SELECT l.lane_no, COALESCE((SELECT min(fired_at) FROM shots WHERE session_id=s.id), s.started_at)
		FROM sessions s JOIN lanes l ON l.id=s.lane_id WHERE s.id=$1::uuid`, sessionID).Scan(&info.LaneNo, &start)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &httpError{code: 404, msg: "Scheibe nicht gefunden"}
	}
	if err != nil {
		return nil, err
	}
	// Band-Einstellungen des Stands zu Beginn der Scheibe
	if start != nil {
		rows, err := a.store.pool.Query(ctx, `
			SELECT changes FROM esp32_config_history WHERE lane_no=$1 AND created_at <= $2 ORDER BY id`, info.LaneNo, *start)
		if err != nil {
			return nil, err
		}
		state := map[string]string{}
		for rows.Next() {
			var ch map[string]string
			if err := rows.Scan(&ch); err != nil {
				rows.Close()
				return nil, err
			}
			for k, v := range ch {
				state[k] = v
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if v, ok := state["PAPERFEED"]; ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
				info.FeedMM = f
				info.ConfigKnown = true
			}
		}
		if v, ok := state["PAPERTRIGGER"]; ok && v != "" {
			info.Trigger = strings.ToUpper(v)
		}
		if v, ok := state["PAPERAUTO"]; ok {
			info.Auto = v != "0"
		}
	}

	rows, err := a.store.pool.Query(ctx, `
		SELECT shot_no, kind::text, status::text, entry_source,
		       COALESCE(corrected_x_mm, x_mm)::float8, COALESCE(corrected_y_mm, y_mm)::float8,
		       corrected_ring IS NOT NULL, piezo_ok, clean
		FROM shots WHERE session_id=$1::uuid ORDER BY shot_no`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	feeds, nSight, nMatch := 0, 0, 0
	for rows.Next() {
		var s stripShot
		var src string
		var x, y *float64
		var piezo, clean *bool
		if err := rows.Scan(&s.ShotNo, &s.Kind, &s.Status, &src, &x, &y, &s.Corrected, &piezo, &clean); err != nil {
			return nil, err
		}
		// Nummerierung wie in der grafischen Ansicht (nur angezeigte Schüsse)
		shown := s.Status == "valid" || s.Status == "annulled" || s.Status == "cross_shot_in"
		if shown {
			if s.Kind == "sighting" {
				nSight++
				s.Label = "P" + strconv.Itoa(nSight)
			} else {
				nMatch++
				s.Label = strconv.Itoa(nMatch)
			}
		} else {
			s.Label = "#" + strconv.Itoa(s.ShotNo)
		}
		sensor := src == "sensor"
		if x != nil && y != nil && sensor && shown {
			s.X, s.Y, s.HasPos = *x, *y, true
		}
		s.Feed = feeds
		if info.Auto && sensor && s.Status != "cross_shot_out" {
			switch info.Trigger {
			case "ANY":
				s.FeedAfter = true
			case "CLEAN":
				s.FeedAfter = clean == nil || *clean
			default: // PIEZO
				s.FeedAfter = piezo == nil || *piezo
			}
		}
		if s.FeedAfter {
			feeds++
		}
		info.Shots = append(info.Shots, s)
	}
	info.Feeds = feeds
	return info, rows.Err()
}

func (a *APIServer) getStripInfo(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, stripTile); err != nil {
		return nil, err
	}
	return a.loadStripInfo(r, r.PathValue("id"))
}

func (a *APIServer) postStripMatch(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, stripTile); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		Holes   []stripHole   `json:"holes"`
		VMin    float64       `json:"v_min"`
		VMax    float64       `json:"v_max"`
		FeedMM  float64       `json:"feed_mm"`
		Anchors []stripAnchor `json:"anchors"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungültige Anfrage: " + err.Error())
	}
	info, err := a.loadStripInfo(r, r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	if info.Feeds == 0 {
		return nil, errBadRequest("Laut Einstellungen des Stands gab es bei dieser Scheibe keinen automatischen Vorschub - eine Bandkontrolle ist nicht möglich.")
	}
	feed := body.FeedMM
	if feed <= 0 {
		feed = info.FeedMM
	}
	if len(body.Holes) > 500 {
		return nil, errBadRequest("zu viele Löcher")
	}
	res, err := runStripMatch(stripMatchInput{Holes: body.Holes, Shots: info.Shots, VMin: body.VMin, VMax: body.VMax,
		FeedMM: feed, Anchors: body.Anchors})
	if err != nil {
		return nil, errBadRequest(err.Error())
	}
	return res, nil
}

// ── Fotos (mehrere je Scheibe) ──────────────────────────────────────────────

const stripMaxPhotos = 10

type stripPhotoMeta struct {
	PhotoNo int `json:"photo_no"`
	sessionPhotoMeta
}

func stripPhotoNo(r *http.Request) (int, error) {
	n, err := strconv.Atoi(r.PathValue("no"))
	if err != nil || n < 1 {
		return 0, errBadRequest("ungültige Fotonummer")
	}
	return n, nil
}

func (a *APIServer) listStripPhotos(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, stripTile); err != nil {
		return nil, err
	}
	rows, err := a.store.pool.Query(r.Context(), `
		SELECT photo_no, session_id::text, content_type, filename, length(data), uploaded_at, uploaded_by, markers, markers_at
		FROM session_strip_photos WHERE session_id=$1::uuid ORDER BY photo_no`, r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []stripPhotoMeta{}
	for rows.Next() {
		var m stripPhotoMeta
		if err := rows.Scan(&m.PhotoNo, &m.SessionID, &m.ContentType, &m.Filename, &m.SizeBytes, &m.UploadedAt,
			&m.UploadedBy, &m.Markers, &m.MarkersAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (a *APIServer) getStripPhoto(w http.ResponseWriter, r *http.Request) {
	if _, err := a.requireTile(w, r, stripTile); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	no, err := stripPhotoNo(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var ct string
	var data []byte
	err = a.store.pool.QueryRow(r.Context(),
		`SELECT content_type, data FROM session_strip_photos WHERE session_id=$1::uuid AND photo_no=$2`,
		r.PathValue("id"), no).Scan(&ct, &data)
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

// postStripPhoto: weiteres Foto anlegen (nächste freie Nummer).
func (a *APIServer) postStripPhoto(w http.ResponseWriter, r *http.Request) (any, error) {
	role, err := a.requireTile(w, r, stripTile)
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
	sid := r.PathValue("id")
	tx, err := a.store.pool.Begin(r.Context())
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(r.Context())
	// je Scheibe serialisieren, damit zwei gleichzeitige Uploads nicht dieselbe Nummer bekommen
	if _, err := tx.Exec(r.Context(), `SELECT 1 FROM sessions WHERE id=$1::uuid FOR UPDATE`, sid); err != nil {
		return nil, err
	}
	var count, next int
	if err := tx.QueryRow(r.Context(), `SELECT count(*), COALESCE(max(photo_no),0)+1 FROM session_strip_photos WHERE session_id=$1::uuid`,
		sid).Scan(&count, &next); err != nil {
		return nil, err
	}
	if count >= stripMaxPhotos {
		return nil, errBadRequest(fmt.Sprintf("höchstens %d Bandfotos je Scheibe", stripMaxPhotos))
	}
	tag, err := tx.Exec(r.Context(), `
		INSERT INTO session_strip_photos (session_id, photo_no, content_type, filename, data, uploaded_by)
		SELECT id, $2, $3, $4, $5, $6 FROM sessions WHERE id=$1::uuid`, sid, next, ct, name, data, role.RoleKey)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, &httpError{code: 404, msg: "Scheibe nicht gefunden"}
	}
	if err := tx.Commit(r.Context()); err != nil {
		return nil, err
	}
	return map[string]any{"photo_no": next, "filename": name}, nil
}

func (a *APIServer) putStripPhotoMarkers(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, stripTile); err != nil {
		return nil, err
	}
	no, err := stripPhotoNo(r)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if !json.Valid(body) {
		return nil, errBadRequest("Markierungen: ungültiges JSON")
	}
	tag, err := a.store.pool.Exec(r.Context(),
		`UPDATE session_strip_photos SET markers=$3::jsonb, markers_at=now() WHERE session_id=$1::uuid AND photo_no=$2`,
		r.PathValue("id"), no, string(body))
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, &httpError{code: 404, msg: "Bandfoto nicht gefunden"}
	}
	return map[string]bool{"ok": true}, nil
}

func (a *APIServer) deleteStripPhoto(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, stripTile); err != nil {
		return nil, err
	}
	no, err := stripPhotoNo(r)
	if err != nil {
		return nil, err
	}
	if _, err := a.store.pool.Exec(r.Context(), `DELETE FROM session_strip_photos WHERE session_id=$1::uuid AND photo_no=$2`,
		r.PathValue("id"), no); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}
