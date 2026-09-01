// ============================================================================
// preisschiessen_vereine.go – Vereins-Auswertungen je Preisschießen
//
// Drei feste Auswertungsarten (nicht frei konfigurierbar wie
// ps_wertungen/preisschiessen_wertungen.go, dort geht es um einzelne
// Teilnehmer):
//   - Anzahl:  Anzahl angemeldeter Teilnehmer je Verein
//   - Prozent: Teilnehmer im Verhältnis zur Vereins-Mitgliederzahl (clubs.
//     member_count, siehe migrations/003_stammdaten.sql)
//   - Punkte:  je Teilnehmer nach dem Datum seines ersten Schusses in diesem
//     Preisschießen (erste Scheibe begonnen/beendet) einer von bis
//     zu 5 konfigurierten Zeiträumen zugeordnet und aufsummiert je
//     Verein
//
// Konfigurierbar ist nur, welche Vereine überhaupt in der Auswertung
// auftauchen (ps_verein_teilnahme) und die Punkte-Zeiträume
// (ps_verein_punkte_zeitraum). Ein als Gastgeber markierter Verein belegt in
// allen drei Auswertungen unabhängig von seinen Werten immer den letzten
// Platz (assignVereinsPlatz).
//
// Die Berechnung ist im Gegensatz zu preisschiessen_wertungen.go billig
// (keine Zehntel-/Teiler-Aggregation über tausende Schüsse, nur Zählungen
// und ein MIN() je Teilnehmer) und läuft deshalb live pro Anfrage.
// ============================================================================
package main

import (
	"context"
	"net/http"
	"sort"
	"time"
)

type PSVereinTeilnahme struct {
	ClubID    string `json:"club_id"`
	ClubName  string `json:"club_name"`
	Gastgeber bool   `json:"gastgeber"`
}

type PSVereinZeitraum struct {
	ID        string  `json:"id"`
	Von       string  `json:"von"`
	Bis       string  `json:"bis"`
	Punkte    float64 `json:"punkte"`
	SortOrder int     `json:"sort_order"`
}

type PSVereinErgebnisZeile struct {
	ClubID       string   `json:"club_id"`
	ClubName     string   `json:"club_name"`
	Gastgeber    bool     `json:"gastgeber"`
	Anzahl       int      `json:"anzahl"`
	Mitglieder   *int     `json:"mitglieder"`
	Prozent      *float64 `json:"prozent"`
	Punkte       float64  `json:"punkte"`
	PlatzAnzahl  int      `json:"platz_anzahl"`
	PlatzProzent int      `json:"platz_prozent"`
	PlatzPunkte  int      `json:"platz_punkte"`
}

const maxVereinZeitraeume = 5

// ----------------------------------------------------------------------------
// Store – Konfiguration
// ----------------------------------------------------------------------------

func (s *Store) ListVereinTeilnahme(ctx context.Context, preisschiessenID string) ([]PSVereinTeilnahme, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT vt.club_id, c.name, vt.gastgeber
		FROM ps_verein_teilnahme vt JOIN clubs c ON c.id = vt.club_id
		WHERE vt.preisschiessen_id=$1 ORDER BY c.name`, preisschiessenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PSVereinTeilnahme{}
	for rows.Next() {
		var v PSVereinTeilnahme
		if err := rows.Scan(&v.ClubID, &v.ClubName, &v.Gastgeber); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SetVereinTeilnahme ersetzt die komplette Liste der an der Vereins-
// Auswertung teilnehmenden Vereine (DELETE+INSERT, wie z.B.
// Store.SetScheibeClasses in preisschiessen.go). gastgeberClubID="" = kein
// Gastgeber gesetzt.
func (s *Store) SetVereinTeilnahme(ctx context.Context, preisschiessenID string, clubIDs []string, gastgeberClubID string) error {
	if gastgeberClubID != "" && !containsStr(clubIDs, gastgeberClubID) {
		return errBadRequest("Gastgeber-Verein muss auch in der Teilnehmerliste ausgewählt sein")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM ps_verein_teilnahme WHERE preisschiessen_id=$1`, preisschiessenID); err != nil {
		return err
	}
	for _, cid := range clubIDs {
		if cid == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ps_verein_teilnahme (preisschiessen_id, club_id, gastgeber)
			VALUES ($1,$2,$3)`, preisschiessenID, cid, cid == gastgeberClubID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) ListVereinZeitraeume(ctx context.Context, preisschiessenID string) ([]PSVereinZeitraum, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, von::text, bis::text, punkte, sort_order
		FROM ps_verein_punkte_zeitraum WHERE preisschiessen_id=$1
		ORDER BY sort_order, von`, preisschiessenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PSVereinZeitraum{}
	for rows.Next() {
		var z PSVereinZeitraum
		if err := rows.Scan(&z.ID, &z.Von, &z.Bis, &z.Punkte, &z.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

// SetVereinZeitraeume ersetzt die komplette Zeitraum-Staffelung (max. 5,
// siehe maxVereinZeitraeume - hier per Anwendungslogik geprüft statt per
// DB-Constraint, analog zur Prüfung in Store.purchaseItem für Kauflimits).
func (s *Store) SetVereinZeitraeume(ctx context.Context, preisschiessenID string, items []PSVereinZeitraum) error {
	if len(items) > maxVereinZeitraeume {
		return errBadRequest("höchstens 5 Zeiträume möglich")
	}
	for _, it := range items {
		von, errV := time.Parse("2006-01-02", it.Von)
		bis, errB := time.Parse("2006-01-02", it.Bis)
		if errV != nil || errB != nil {
			return errBadRequest("Von/Bis müssen gültige Daten sein")
		}
		if bis.Before(von) {
			return errBadRequest("Bis darf nicht vor Von liegen")
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM ps_verein_punkte_zeitraum WHERE preisschiessen_id=$1`, preisschiessenID); err != nil {
		return err
	}
	for i, it := range items {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ps_verein_punkte_zeitraum (preisschiessen_id, von, bis, punkte, sort_order)
			VALUES ($1,$2,$3,$4,$5)`, preisschiessenID, it.Von, it.Bis, it.Punkte, i); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ----------------------------------------------------------------------------
// Store – Berechnung
// ----------------------------------------------------------------------------

func (s *Store) ComputeVereinsAuswertung(ctx context.Context, preisschiessenID string) ([]PSVereinErgebnisZeile, error) {
	clubRows, err := s.pool.Query(ctx, `
		SELECT vt.club_id, c.name, vt.gastgeber, c.member_count
		FROM ps_verein_teilnahme vt JOIN clubs c ON c.id = vt.club_id
		WHERE vt.preisschiessen_id=$1`, preisschiessenID)
	if err != nil {
		return nil, err
	}
	var rows []PSVereinErgebnisZeile
	var clubIDs []string
	for clubRows.Next() {
		var r PSVereinErgebnisZeile
		if err := clubRows.Scan(&r.ClubID, &r.ClubName, &r.Gastgeber, &r.Mitglieder); err != nil {
			clubRows.Close()
			return nil, err
		}
		rows = append(rows, r)
		clubIDs = append(clubIDs, r.ClubID)
	}
	if err := clubRows.Err(); err != nil {
		return nil, err
	}
	clubRows.Close()
	if len(rows) == 0 {
		return []PSVereinErgebnisZeile{}, nil
	}

	// ---- Anzahl Teilnehmer je Verein ----
	countByClub := map[string]int{}
	cRows, err := s.pool.Query(ctx, `
		SELECT sh.club_id, COUNT(*) FROM ps_teilnehmer t
		JOIN shooters sh ON sh.id = t.shooter_id
		WHERE t.preisschiessen_id=$1 AND sh.club_id = ANY($2::uuid[])
		GROUP BY sh.club_id`, preisschiessenID, clubIDs)
	if err != nil {
		return nil, err
	}
	for cRows.Next() {
		var cid string
		var n int
		if err := cRows.Scan(&cid, &n); err != nil {
			cRows.Close()
			return nil, err
		}
		countByClub[cid] = n
	}
	if err := cRows.Err(); err != nil {
		return nil, err
	}
	cRows.Close()

	// ---- Punkte je Verein: erster Schuss-Tag je Teilnehmer -> Zeitraum ----
	zeitraeume, err := s.ListVereinZeitraeume(ctx, preisschiessenID)
	if err != nil {
		return nil, err
	}
	type zSpan struct {
		von, bis time.Time
		punkte   float64
	}
	spans := make([]zSpan, 0, len(zeitraeume))
	for _, z := range zeitraeume {
		von, errV := time.Parse("2006-01-02", z.Von)
		bis, errB := time.Parse("2006-01-02", z.Bis)
		if errV != nil || errB != nil {
			continue
		}
		spans = append(spans, zSpan{von, bis, z.Punkte})
	}

	punkteByClub := map[string]float64{}
	fRows, err := s.pool.Query(ctx, `
		SELECT sh.club_id, MIN((s.fired_at AT TIME ZONE 'Europe/Berlin')::date)
		FROM ps_teilnehmer t
		JOIN shooters sh ON sh.id = t.shooter_id
		JOIN ps_kaeufe k ON k.teilnehmer_id = t.id
		JOIN ps_kauf_scheiben ks ON ks.kauf_id = k.id
		JOIN shots s ON s.session_id = ks.session_id AND s.status <> 'annulled'
		WHERE t.preisschiessen_id=$1 AND sh.club_id = ANY($2::uuid[])
		GROUP BY t.id, sh.club_id`, preisschiessenID, clubIDs)
	if err != nil {
		return nil, err
	}
	for fRows.Next() {
		var cid string
		var d time.Time
		if err := fRows.Scan(&cid, &d); err != nil {
			fRows.Close()
			return nil, err
		}
		for _, sp := range spans {
			if !d.Before(sp.von) && !d.After(sp.bis) {
				punkteByClub[cid] += sp.punkte
				break
			}
		}
	}
	if err := fRows.Err(); err != nil {
		return nil, err
	}
	fRows.Close()

	for i := range rows {
		rows[i].Anzahl = countByClub[rows[i].ClubID]
		if rows[i].Mitglieder != nil && *rows[i].Mitglieder > 0 {
			p := float64(rows[i].Anzahl) / float64(*rows[i].Mitglieder) * 100
			rows[i].Prozent = &p
		}
		rows[i].Punkte = punkteByClub[rows[i].ClubID]
	}

	platzAnzahl := assignVereinsPlatz(rows, func(r PSVereinErgebnisZeile) *float64 {
		v := float64(r.Anzahl)
		return &v
	})
	platzProzent := assignVereinsPlatz(rows, func(r PSVereinErgebnisZeile) *float64 { return r.Prozent })
	platzPunkte := assignVereinsPlatz(rows, func(r PSVereinErgebnisZeile) *float64 {
		v := r.Punkte
		return &v
	})
	for i := range rows {
		rows[i].PlatzAnzahl = platzAnzahl[rows[i].ClubID]
		rows[i].PlatzProzent = platzProzent[rows[i].ClubID]
		rows[i].PlatzPunkte = platzPunkte[rows[i].ClubID]
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].ClubName < rows[j].ClubName })
	return rows, nil
}

// assignVereinsPlatz sortiert nach get() absteigend (nil sortiert nach allen
// vorhandenen Werten ein, Namensgleichstand alphabetisch) und vergibt Plätze
// 1..n - der Gastgeber-Verein (falls einer gesetzt ist) wird dabei IMMER auf
// den letzten Platz gesetzt, unabhängig von seinem Wert.
func assignVereinsPlatz(rows []PSVereinErgebnisZeile, get func(PSVereinErgebnisZeile) *float64) map[string]int {
	type item struct {
		clubID    string
		name      string
		val       *float64
		gastgeber bool
	}
	items := make([]item, len(rows))
	for i, r := range rows {
		items[i] = item{r.ClubID, r.ClubName, get(r), r.Gastgeber}
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.gastgeber != b.gastgeber {
			return !a.gastgeber // Gastgeber immer zuletzt
		}
		if a.gastgeber && b.gastgeber {
			return a.name < b.name
		}
		if (a.val == nil) != (b.val == nil) {
			return a.val != nil // vorhandene Werte vor "-"
		}
		if a.val != nil && b.val != nil && *a.val != *b.val {
			return *a.val > *b.val // absteigend
		}
		return a.name < b.name
	})
	out := make(map[string]int, len(items))
	for i, it := range items {
		out[it.clubID] = i + 1
	}
	return out
}

// ----------------------------------------------------------------------------
// HTTP-Handler
// ----------------------------------------------------------------------------

func (a *APIServer) listVereinTeilnahme(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ListVereinTeilnahme(r.Context(), r.PathValue("id"))
}

func (a *APIServer) setVereinTeilnahme(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		ClubIDs         []string `json:"club_ids"`
		GastgeberClubID string   `json:"gastgeber_club_id"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungültiger Body")
	}
	if err := a.store.SetVereinTeilnahme(r.Context(), r.PathValue("id"), body.ClubIDs, body.GastgeberClubID); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) listVereinZeitraeume(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ListVereinZeitraeume(r.Context(), r.PathValue("id"))
}

func (a *APIServer) setVereinZeitraeume(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		Zeitraeume []PSVereinZeitraum `json:"zeitraeume"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungültiger Body")
	}
	if err := a.store.SetVereinZeitraeume(r.Context(), r.PathValue("id"), body.Zeitraeume); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) getVereinsAuswertung(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ComputeVereinsAuswertung(r.Context(), r.PathValue("id"))
}
