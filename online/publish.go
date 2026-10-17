// ============================================================================
// publish.go – Empfang der Momentaufnahmen vom Schiessstand-Server.
//
//	GET /api/v1/ping                         Schluessel pruefen -> Verein
//	PUT /api/v1/publish/{kind}/{source_id}   Momentaufnahme speichern
//
// kind = "preisschiessen" | "rwk", source_id = UUID des Preisschiessens bzw.
// Wettkampfs auf dem Schiessstand-Server. Jede Momentaufnahme ersetzt die
// vorherige derselben Veroeffentlichung vollstaendig (aeltere werden anhand
// generated_at verworfen). Veroeffentlichungen, die ein Schiessstand nicht
// (mehr) schickt, bleiben unveraendert stehen (Historie).
//
// Die Formate (PSSnapshot, RWKSnapshot) sind das Gegenstueck zu
// server/online_snapshot.go - dort werden sie gebaut.
// ============================================================================
package main

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	snapshotFormat   = 1
	maxSnapshotBytes = 16 << 20 // nach dem Entpacken
)

// ── Preisschiessen ───────────────────────────────────────────────────────

type PSSnapshot struct {
	Format      int           `json:"format"`
	GeneratedAt time.Time     `json:"generated_at"`
	Name        string        `json:"name"`
	Art         string        `json:"art"` // preisschiessen | vereinsabend_saison
	StartsOn    string        `json:"starts_on"`
	EndsOn      string        `json:"ends_on"`
	Finished    bool          `json:"finished"`
	Berechnet   string        `json:"berechnet"` // Zeitpunkt der letzten Auswertung (Text)
	Stats       PSStats       `json:"stats"`
	Wertungen   []PSListe     `json:"wertungen"`
	Vereine     []PSVereinLst `json:"vereine"`
}

type PSStats struct {
	Vereine    int `json:"vereine"`
	Teilnehmer int `json:"teilnehmer"`
	Scheiben   int `json:"scheiben"`
	Schuss     int `json:"schuss"`
}

type PSListe struct {
	Key          string    `json:"key"`
	Name         string    `json:"name"`
	Beschreibung string    `json:"beschreibung"`
	Feld         string    `json:"feld"` // ring | ring_decimal | teiler
	AnzSumme     int       `json:"anz_summe"`
	Zeilen       []PSZeile `json:"zeilen"`
}

type PSZeile struct {
	Platz  int       `json:"platz"`
	Nr     int       `json:"nr"`
	Name   string    `json:"name"`
	Verein string    `json:"verein"`
	Werte  []float64 `json:"werte"`
	Summe  float64   `json:"summe"`
	Preis  string    `json:"preis"`
}

type PSVereinLst struct {
	Typ       string        `json:"typ"`
	Name      string        `json:"name"`
	WertTitel string        `json:"wert_titel"`
	Zeilen    []PSVereinRow `json:"zeilen"`
}

type PSVereinRow struct {
	Platz      int    `json:"platz"`
	Verein     string `json:"verein"`
	Gastgeber  bool   `json:"gastgeber"`
	Teilnehmer int    `json:"teilnehmer"`
	Wert       string `json:"wert"`
	Preis      string `json:"preis"`
}

// ── Rundenwettkampf ──────────────────────────────────────────────────────

type RWKSnapshot struct {
	Format      int                    `json:"format"`
	GeneratedAt time.Time              `json:"generated_at"`
	Name        string                 `json:"name"`
	Liga        string                 `json:"liga"`
	Ort         string                 `json:"ort"`
	Datum       string                 `json:"datum"`
	Disziplin   string                 `json:"disziplin"`
	Status      string                 `json:"status"` // planned | running | finished | archived
	Modus       string                 `json:"modus"`  // mannschaft | paarung
	BestOf      int                    `json:"best_of"`
	Decimal     bool                   `json:"decimal"`
	Teams       [2]RWKTeam             `json:"teams"` // 0 Heim, 1 Gast
	Pairs       []RWKPair              `json:"pairs"`
	Starter     []RWKStarter           `json:"starter"`
	Geos        map[string]TargetGeo   `json:"geos"`
	Extra       map[string]interface{} `json:"extra,omitempty"`
}

type RWKTeam struct {
	Name   string  `json:"name"`
	Verein string  `json:"verein"`
	Total  float64 `json:"total"`
	HR     float64 `json:"hr"`
	Points int     `json:"points"`
}

type RWKPair struct {
	No     int  `json:"no"`
	Heim   int  `json:"heim"` // Index in Starter, -1 = nicht besetzt
	Gast   int  `json:"gast"`
	Leader int  `json:"leader"` // 0 Heim, 1 Gast, -1 offen
	Stech  bool `json:"stechen"`
}

type RWKStarter struct {
	Team       int       `json:"team"`
	Name       string    `json:"name"`
	Role       string    `json:"role"` // S | E | AK
	PairNo     int       `json:"pair_no"`
	Shots      int       `json:"shots"`
	MatchShots int       `json:"match_shots"`
	Total      float64   `json:"total"`
	HR         float64   `json:"hr"`
	Counts     bool      `json:"counts"` // zaehlt fuer die Mannschaft
	Series     []float64 `json:"series"`
	Lane       int       `json:"lane"`
	Stechen    int       `json:"stechen"` // laufender/letzter Stechschuss, 0 = keiner
	Tile       *RWKTile  `json:"tile,omitempty"`
}

type RWKTile struct {
	Geo   string     `json:"geo"`
	Shots []TileShot `json:"shots"`
	Last  string     `json:"last"`
	Probe bool       `json:"probe"`
}

type TileShot struct {
	No      int     `json:"no"`
	X       float64 `json:"x_mm"`
	Y       float64 `json:"y_mm"`
	Ring    int     `json:"ring"`
	Decimal float64 `json:"decimal"`
}

type TargetGeo struct {
	InnerTenD      float64    `json:"inner10_d"`
	InnerTenDashed bool       `json:"inner10_dashed"`
	Rings          []RingGeom `json:"rings"`
}

type RingGeom struct {
	V      int     `json:"v"`
	D      float64 `json:"d"`
	Filled bool    `json:"filled"`
}

// ── Empfang ──────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (a *App) handlePing(w http.ResponseWriter, r *http.Request) {
	vid, err := a.authAPIKey(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	var name, slug string
	a.pool.QueryRow(r.Context(), `SELECT name, slug FROM vereine WHERE id=$1::uuid`, vid).Scan(&name, &slug)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "verein": name, "url": "/v/" + slug + "/", "format": snapshotFormat})
}

func readBody(r *http.Request) ([]byte, error) {
	var rd io.Reader = http.MaxBytesReader(nil, r.Body, maxSnapshotBytes)
	if r.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(rd)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		rd = io.LimitReader(gz, maxSnapshotBytes+1)
	}
	b, err := io.ReadAll(rd)
	if err == nil && len(b) > maxSnapshotBytes {
		err = errors.New("Daten zu groß")
	}
	return b, err
}

func (a *App) handlePublish(w http.ResponseWriter, r *http.Request) {
	vid, err := a.authAPIKey(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	kind, source := r.PathValue("kind"), r.PathValue("source")
	if kind != "preisschiessen" && kind != "rwk" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unbekannte Art"})
		return
	}
	body, err := readBody(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var title, startsOn, status string
	var gen time.Time
	switch kind {
	case "preisschiessen":
		var s PSSnapshot
		if err := json.Unmarshal(body, &s); err != nil || s.Format != snapshotFormat || s.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("ungültige Daten (Format %d erwartet)", snapshotFormat)})
			return
		}
		title, startsOn, gen = s.Name, s.StartsOn, s.GeneratedAt
		status = "laufend"
		if s.Finished {
			status = "beendet"
		}
	case "rwk":
		var s RWKSnapshot
		if err := json.Unmarshal(body, &s); err != nil || s.Format != snapshotFormat || s.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("ungültige Daten (Format %d erwartet)", snapshotFormat)})
			return
		}
		title, startsOn, gen, status = s.Name, s.Datum, s.GeneratedAt, s.Status
	}
	if gen.IsZero() {
		gen = time.Now()
	}
	var start *string
	if len(startsOn) >= 10 {
		v := startsOn[:10]
		start = &v
	}
	var id string
	err = a.pool.QueryRow(r.Context(), `
		INSERT INTO publications (verein_id, kind, source_id, title, starts_on, status, data, generated_at)
		VALUES ($1::uuid, $2, $3::uuid, $4, $5::date, $6, $7::jsonb, $8)
		ON CONFLICT (verein_id, kind, source_id) DO UPDATE
		   SET title = EXCLUDED.title, starts_on = EXCLUDED.starts_on, status = EXCLUDED.status,
		       data = EXCLUDED.data, generated_at = EXCLUDED.generated_at, updated_at = now()
		 WHERE publications.generated_at <= EXCLUDED.generated_at
		RETURNING id::text`, vid, kind, source, title, start, status, string(body), gen).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": "älterer Stand"})
		return
	}
	if err != nil {
		log.Printf("publish %s/%s: %v", kind, source, err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "speichern fehlgeschlagen"})
		return
	}
	var slug string
	a.pool.QueryRow(r.Context(), `SELECT slug FROM vereine WHERE id=$1::uuid`, vid).Scan(&slug)
	path := "/v/" + slug + "/ps/" + id + "/"
	if kind == "rwk" {
		path = "/v/" + slug + "/rwk/" + id + "/"
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "url": path})
}
