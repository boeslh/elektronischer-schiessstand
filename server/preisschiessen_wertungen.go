// ============================================================================
// preisschiessen_wertungen.go – Auswerte-Logik je Preisschiessen (Phase 1
// der gs26-Ablösung, siehe .claude/plans/hidden-booping-duckling.md)
//
// Portiert die Ranking-Logik aus gs26/backend/gen_SerienP.py (Meister:
// Ringsumme absteigend), gen_TeilerP.py (Punkt: Teiler aufsteigend) und
// gen_Adler.py (Adler: alternierende Mischung einer Punkt- und einer
// Meister-Wertung), konfiguriert je preisschiessen_id statt über
// jahresspezifische MySQL-Tabellen (gs26_Listen). Quelle ist dieselbe
// Join-Kette wie in gs26/backend/copy_Scheiben_pg.py.
//
// Zwei-Ebenen-Modell (bewusst wie im Vorbild, siehe Plan-Kontext): die
// Berechnung ist bei grossen Preisschiessen rechenintensiv (bis zu 5 Minuten)
// und läuft deshalb NICHT live pro Anfrage, sondern periodisch im
// Hintergrund (RunAuswertungScheduler) bzw. per manuellem Trigger,
// materialisiert in ps_wertung_ergebnisse. Der separate Anzeige-Prozess
// (preisanzeige/) und die Preisschiessen-Seite lesen nur diesen Cache.
//
// Job-Vergabe ist rein DB-basiert und atomar (claimAuswertungJob* nutzen
// "FOR UPDATE SKIP LOCKED"), damit mehrere Server-Prozesse (auch auf
// verschiedenen Rechnern, siehe main.go -worker-only) sich die Arbeit ohne
// zusätzliche Koordination sicher teilen können.
// ============================================================================
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ----------------------------------------------------------------------------
// Typen
// ----------------------------------------------------------------------------

// PSWertungScheibe verknüpft eine Wertung mit einer im Preisschiessen
// angelegten Scheibe (echte FK statt fehleranfälligem Namensvergleich) und
// trägt den für DIESE Scheibe geltenden Faktor (z.B. LG vs. LP in einer
// kombinierten Wertung brauchen unterschiedliche Normierungsfaktoren, analog
// TEILER_FAKTOR/RING_FAKTOR je Disziplin in gs26/backend/gsConfig.py).
type PSWertungScheibe struct {
	ScheibeID   string  `json:"scheibe_id"`
	ScheibeName string  `json:"scheibe_name"`
	Faktor      float64 `json:"faktor"`
}

type PSWertung struct {
	ID               string             `json:"id"`
	PreisschiessenID string             `json:"preisschiessen_id"`
	DisziplinKey     string             `json:"disziplin_key"`
	Typ              string             `json:"typ"` // meister | punkt | adler
	ShortDesc        string             `json:"short_desc"`
	LongDesc         string             `json:"long_desc"`
	Wertungsfeld     string             `json:"wertungsfeld"` // ring | ring_decimal | teiler
	Scheiben         []PSWertungScheibe `json:"scheiben"`
	KlassenIDs       []string           `json:"klassen_ids"`
	// AnzSumme: Anzahl der besten Werte, die zur Platzierung aufsummiert
	// werden (Summenwertung). AnzSumme=1 ergibt automatisch eine
	// Einzelwertung (nur die beste Scheibe/der beste Schuss zählt) - dafür
	// ist kein eigenes Konzept nötig, siehe computeMeisterPunkt.
	AnzSumme       int     `json:"anz_summe"`
	AdlerTeilerID  *string `json:"adler_teiler_id"`
	AdlerMeisterID *string `json:"adler_meister_id"`
	SortOrder      int     `json:"sort_order"`
	Visible        bool    `json:"visible"`
}

type PSWertungErgebnis struct {
	TeilnehmerID string    `json:"teilnehmer_id"`
	StartNr      int       `json:"start_nr"`
	Nachname     string    `json:"nachname"`
	Vorname      string    `json:"vorname"`
	Verein       string    `json:"verein"`
	Klasse       string    `json:"klasse"`
	Werte        []float64 `json:"werte"`
	Summe        float64   `json:"summe"`
	Platz        int       `json:"platz"`
}

type PSAuswertungStatus struct {
	PreisschiessenID string `json:"preisschiessen_id"`
	// nil = keine automatische Hintergrundberechnung (Default), siehe
	// migrations/034_preisschiessen_auswertung_interval_optional.sql.
	IntervalSeconds *int       `json:"interval_seconds"`
	Status          string     `json:"status"`
	LastStartedAt   *time.Time `json:"last_started_at"`
	LastFinishedAt  *time.Time `json:"last_finished_at"`
	LastDurationMs  *int       `json:"last_duration_ms"`
	LastError       *string    `json:"last_error"`
}

type PSAnzeigeConfig struct {
	PreisschiessenID string   `json:"preisschiessen_id"`
	ReloadSeconds    int      `json:"reload_seconds"`
	TitleFontSize    int      `json:"title_font_size"`
	WertungIDs       []string `json:"wertung_ids"`
}

// ----------------------------------------------------------------------------
// Store – Wertungen (Konfiguration)
// ----------------------------------------------------------------------------

func (s *Store) ListWertungen(ctx context.Context, preisschiessenID string) ([]PSWertung, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, preisschiessen_id, disziplin_key, typ, short_desc, COALESCE(long_desc,''),
		       COALESCE(wertungsfeld,''), klassen_ids::text[],
		       anz_summe, adler_teiler_id::text, adler_meister_id::text,
		       sort_order, visible
		FROM ps_wertungen WHERE preisschiessen_id=$1 ORDER BY sort_order, short_desc`, preisschiessenID)
	if err != nil {
		return nil, err
	}
	var out []PSWertung
	for rows.Next() {
		var x PSWertung
		if err := rows.Scan(&x.ID, &x.PreisschiessenID, &x.DisziplinKey, &x.Typ, &x.ShortDesc,
			&x.LongDesc, &x.Wertungsfeld, &x.KlassenIDs,
			&x.AnzSumme, &x.AdlerTeilerID, &x.AdlerMeisterID,
			&x.SortOrder, &x.Visible); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range out {
		if out[i].Scheiben, err = s.listWertungScheiben(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) GetWertung(ctx context.Context, id string) (PSWertung, error) {
	var x PSWertung
	err := s.pool.QueryRow(ctx, `
		SELECT id, preisschiessen_id, disziplin_key, typ, short_desc, COALESCE(long_desc,''),
		       COALESCE(wertungsfeld,''), klassen_ids::text[],
		       anz_summe, adler_teiler_id::text, adler_meister_id::text,
		       sort_order, visible
		FROM ps_wertungen WHERE id=$1`, id).Scan(
		&x.ID, &x.PreisschiessenID, &x.DisziplinKey, &x.Typ, &x.ShortDesc,
		&x.LongDesc, &x.Wertungsfeld, &x.KlassenIDs,
		&x.AnzSumme, &x.AdlerTeilerID, &x.AdlerMeisterID,
		&x.SortOrder, &x.Visible)
	if err != nil {
		return x, err
	}
	x.Scheiben, err = s.listWertungScheiben(ctx, x.ID)
	return x, err
}

// listWertungScheiben liest die einer Wertung zugeordneten Scheiben inkl.
// ihres jeweiligen Faktors (JOIN gegen ps_scheiben fuer den Anzeigenamen).
func (s *Store) listWertungScheiben(ctx context.Context, wertungID string) ([]PSWertungScheibe, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ws.scheibe_id, sc.name, ws.faktor
		FROM ps_wertung_scheiben ws
		JOIN ps_scheiben sc ON sc.id = ws.scheibe_id
		WHERE ws.wertung_id=$1
		ORDER BY sc.sort_order, sc.name`, wertungID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PSWertungScheibe{}
	for rows.Next() {
		var x PSWertungScheibe
		if err := rows.Scan(&x.ScheibeID, &x.ScheibeName, &x.Faktor); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// setWertungScheiben ersetzt die komplette Scheiben-Zuordnung einer Wertung
// (DELETE+INSERT, wie das bestehende Store.SetScheibeClasses-Muster in
// preisschiessen.go).
func setWertungScheiben(ctx context.Context, tx pgx.Tx, wertungID string, scheiben []PSWertungScheibe) error {
	if _, err := tx.Exec(ctx, `DELETE FROM ps_wertung_scheiben WHERE wertung_id=$1`, wertungID); err != nil {
		return err
	}
	for _, sc := range scheiben {
		faktor := sc.Faktor
		if faktor == 0 {
			faktor = 1
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO ps_wertung_scheiben (wertung_id, scheibe_id, faktor) VALUES ($1,$2,$3)`,
			wertungID, sc.ScheibeID, faktor); err != nil {
			return err
		}
	}
	return nil
}

// orEmptyStrs verhindert, dass ein nil-Slice (z.B. bei einer Adler-Wertung
// ohne ScheibenNamen/KlassenIDs) als SQL NULL statt als leeres Array
// gebunden wird - beide Spalten sind NOT NULL.
func orEmptyStrs(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (s *Store) CreateWertung(ctx context.Context, x PSWertung) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO ps_wertungen
		  (preisschiessen_id, disziplin_key, typ, short_desc, long_desc, wertungsfeld,
		   klassen_ids, anz_summe, adler_teiler_id, adler_meister_id, sort_order, visible)
		VALUES ($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),$7::uuid[],$8,$9,$10,$11,$12)
		RETURNING id`,
		x.PreisschiessenID, x.DisziplinKey, x.Typ, x.ShortDesc, x.LongDesc, x.Wertungsfeld,
		orEmptyStrs(x.KlassenIDs), x.AnzSumme, x.AdlerTeilerID,
		x.AdlerMeisterID, x.SortOrder, x.Visible,
	).Scan(&id); err != nil {
		return "", err
	}
	if err := setWertungScheiben(ctx, tx, id, x.Scheiben); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func (s *Store) UpdateWertung(ctx context.Context, x PSWertung) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE ps_wertungen SET
		  disziplin_key = $1, typ = $2, short_desc = $3, long_desc = NULLIF($4,''),
		  wertungsfeld = NULLIF($5,''), klassen_ids = $6::uuid[], anz_summe = $7,
		  adler_teiler_id = $8, adler_meister_id = $9, sort_order = $10, visible = $11
		WHERE id = $12`,
		x.DisziplinKey, x.Typ, x.ShortDesc, x.LongDesc, x.Wertungsfeld,
		orEmptyStrs(x.KlassenIDs), x.AnzSumme, x.AdlerTeilerID,
		x.AdlerMeisterID, x.SortOrder, x.Visible, x.ID,
	); err != nil {
		return err
	}
	if err := setWertungScheiben(ctx, tx, x.ID, x.Scheiben); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteWertung(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM ps_wertungen WHERE id=$1`, id)
	return err
}

// ----------------------------------------------------------------------------
// Store – Ergebnis-Cache
// ----------------------------------------------------------------------------

func (s *Store) ListWertungErgebnisse(ctx context.Context, wertungID string) ([]PSWertungErgebnis, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT teilnehmer_id, start_nr, nachname, vorname, COALESCE(verein,''),
		       COALESCE(klasse,''), werte, summe, COALESCE(platz,0)
		FROM ps_wertung_ergebnisse WHERE ps_wertung_id=$1 ORDER BY platz`, wertungID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PSWertungErgebnis
	for rows.Next() {
		var x PSWertungErgebnis
		if err := rows.Scan(&x.TeilnehmerID, &x.StartNr, &x.Nachname, &x.Vorname, &x.Verein,
			&x.Klasse, &x.Werte, &x.Summe, &x.Platz); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// replaceWertungErgebnisse ersetzt den kompletten Ergebnis-Cache einer
// Wertung (DELETE+INSERT in einer Transaktion, wie im Vorbild
// gen_SerienP.py/TRUNCATE, nur je Wertung statt der ganzen Tabelle).
func replaceWertungErgebnisse(ctx context.Context, tx pgx.Tx, wertungID string, rows []PSWertungErgebnis) error {
	if _, err := tx.Exec(ctx, `DELETE FROM ps_wertung_ergebnisse WHERE ps_wertung_id=$1`, wertungID); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ps_wertung_ergebnisse
			  (ps_wertung_id, teilnehmer_id, start_nr, nachname, vorname, verein, klasse,
			   werte, summe, platz)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			wertungID, r.TeilnehmerID, r.StartNr, r.Nachname, r.Vorname, r.Verein, r.Klasse,
			r.Werte, r.Summe, r.Platz); err != nil {
			return err
		}
	}
	return nil
}

// ----------------------------------------------------------------------------
// Store – Batch-Status/Scheduler
// ----------------------------------------------------------------------------

func (s *Store) GetAuswertungStatus(ctx context.Context, preisschiessenID string) (PSAuswertungStatus, error) {
	var x PSAuswertungStatus
	x.PreisschiessenID = preisschiessenID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO ps_auswertung_status (preisschiessen_id) VALUES ($1)
		ON CONFLICT (preisschiessen_id) DO UPDATE SET preisschiessen_id = EXCLUDED.preisschiessen_id
		RETURNING interval_seconds, status, last_started_at, last_finished_at, last_duration_ms, last_error`,
		preisschiessenID,
	).Scan(&x.IntervalSeconds, &x.Status, &x.LastStartedAt, &x.LastFinishedAt,
		&x.LastDurationMs, &x.LastError)
	return x, err
}

// SetAuswertungInterval setzt das Berechnungsintervall - seconds=nil schaltet
// die automatische Hintergrundberechnung für dieses Preisschießen ab
// (claimAuswertungJob greift dann nie mehr zu, "Jetzt neu berechnen" bleibt
// unabhängig davon weiter manuell möglich).
func (s *Store) SetAuswertungInterval(ctx context.Context, preisschiessenID string, seconds *int) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ps_auswertung_status (preisschiessen_id, interval_seconds) VALUES ($1,$2)
		ON CONFLICT (preisschiessen_id) DO UPDATE SET interval_seconds = EXCLUDED.interval_seconds`,
		preisschiessenID, seconds)
	return err
}

// claimAuswertungJob sucht EIN fälliges Preisschiessen (Intervall
// abgelaufen) und markiert es atomar als "running". "FOR UPDATE SKIP LOCKED"
// sorgt dafür, dass mehrere gleichzeitig laufende Scheduler (auch auf
// verschiedenen Rechnern) sich nie denselben Job doppelt greifen.
func (s *Store) claimAuswertungJob(ctx context.Context) (string, bool, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		UPDATE ps_auswertung_status SET status='running', last_started_at=now()
		WHERE preisschiessen_id = (
			SELECT preisschiessen_id FROM ps_auswertung_status
			WHERE status <> 'running'
			  AND interval_seconds IS NOT NULL
			  AND (last_finished_at IS NULL
			       OR last_finished_at < now() - make_interval(secs => interval_seconds))
			ORDER BY last_finished_at NULLS FIRST
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING preisschiessen_id`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// claimAuswertungJobNow greift ein bestimmtes Preisschiessen sofort,
// unabhängig vom Intervall (manueller "Jetzt neu berechnen"-Trigger).
// Schlägt fehl (ok=false), wenn bereits eine Berechnung läuft.
func (s *Store) claimAuswertungJobNow(ctx context.Context, preisschiessenID string) (bool, error) {
	ct, err := s.pool.Exec(ctx, `
		INSERT INTO ps_auswertung_status (preisschiessen_id, status, last_started_at)
		VALUES ($1, 'running', now())
		ON CONFLICT (preisschiessen_id) DO UPDATE
		  SET status='running', last_started_at=now()
		  WHERE ps_auswertung_status.status <> 'running'`,
		preisschiessenID)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

func (s *Store) finishAuswertungJob(ctx context.Context, preisschiessenID string, jobErr error, duration time.Duration) error {
	status := "idle"
	var errMsg *string
	if jobErr != nil {
		status = "error"
		m := jobErr.Error()
		errMsg = &m
	}
	ms := int(duration.Milliseconds())
	_, err := s.pool.Exec(ctx, `
		UPDATE ps_auswertung_status
		SET status=$2, last_finished_at=now(), last_duration_ms=$3, last_error=$4
		WHERE preisschiessen_id=$1`,
		preisschiessenID, status, ms, errMsg)
	return err
}

// ----------------------------------------------------------------------------
// Store – Anzeige-Konfiguration
// ----------------------------------------------------------------------------

func (s *Store) GetAnzeigeConfig(ctx context.Context, preisschiessenID string) (PSAnzeigeConfig, error) {
	var x PSAnzeigeConfig
	x.PreisschiessenID = preisschiessenID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO ps_anzeige_config (preisschiessen_id) VALUES ($1)
		ON CONFLICT (preisschiessen_id) DO UPDATE SET preisschiessen_id = EXCLUDED.preisschiessen_id
		RETURNING reload_seconds, title_font_size, wertung_ids::text[]`,
		preisschiessenID,
	).Scan(&x.ReloadSeconds, &x.TitleFontSize, &x.WertungIDs)
	return x, err
}

func (s *Store) SetAnzeigeConfig(ctx context.Context, x PSAnzeigeConfig) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ps_anzeige_config (preisschiessen_id, reload_seconds, title_font_size, wertung_ids)
		VALUES ($1,$2,$3,$4::uuid[])
		ON CONFLICT (preisschiessen_id) DO UPDATE SET
		  reload_seconds = EXCLUDED.reload_seconds,
		  title_font_size = EXCLUDED.title_font_size,
		  wertung_ids = EXCLUDED.wertung_ids`,
		x.PreisschiessenID, x.ReloadSeconds, x.TitleFontSize, orEmptyStrs(x.WertungIDs))
	return err
}

// ----------------------------------------------------------------------------
// Berechnung – Meister/Punkt
// ----------------------------------------------------------------------------

// wertungRow ist ein einzelner Wert (eine Serie bzw. ein Schuss) eines
// Teilnehmers, wie er aus der Datenbank gelesen wird.
type wertungRow struct {
	teilnehmerID string
	startNr      int
	nachname     string
	vorname      string
	verein       string
	klasse       string
	wert         float64
}

// loadWertungRows liest die Rohwerte für eine Meister-/Punkt-Wertung.
//   - "ring"/"ring_decimal": eine Zeile je Serie (v_series_results), analog
//     gs26_Serien - Quelle für gen_SerienP.py.
//   - "teiler": eine Zeile je Einzelschuss (v_scoring_shots), analog
//     gs26_Treffer - Quelle für gen_TeilerP.py.
//
// Die Zuordnung Wertung -> Scheibe läuft über die echte FK-Tabelle
// ps_wertung_scheiben (nicht mehr über Namensvergleich) - der dort
// hinterlegte Faktor je Scheibe (z.B. LG vs. LP in einer kombinierten
// Wertung) wird direkt in der SQL-Abfrage angewandt. Optional zusätzlich
// nach Klasse gefiltert (klassen_ids, leer = alle), und nur abgeschlossene
// Scheiben (Wertungsschüsse erreicht) zählen, wie in copy_Scheiben_pg.py/
// gen_*.py.
func loadWertungRows(ctx context.Context, pool *pgxpool.Pool, w PSWertung) ([]wertungRow, error) {
	var valueExpr, order string
	switch w.Wertungsfeld {
	case "ring":
		valueExpr, order = "vser.rings", "DESC"
	case "ring_decimal":
		valueExpr, order = "vser.decimal_total", "DESC"
	case "teiler":
		valueExpr, order = "vss.eff_center_distance", "ASC"
	default:
		return nil, fmt.Errorf("unbekanntes Wertungsfeld %q", w.Wertungsfeld)
	}

	joinTable := "JOIN v_series_results vser ON vser.session_id = ks.session_id"
	if w.Wertungsfeld == "teiler" {
		joinTable = "JOIN v_scoring_shots vss ON vss.effective_session_id = ks.session_id"
	}

	sql := fmt.Sprintf(`
		SELECT pt.id, pt.teilnehmer_nr, sh.last_name, sh.first_name,
		       COALESCE(cl.name,''), COALESCE(sc.name,''), (%s) * ws.faktor AS wert
		FROM ps_kauf_scheiben ks
		JOIN ps_wertung_scheiben ws ON ws.scheibe_id = ks.scheibe_id AND ws.wertung_id = $1
		JOIN ps_kaeufe k          ON k.id = ks.kauf_id
		JOIN ps_teilnehmer pt     ON pt.id = k.teilnehmer_id
		JOIN ps_scheiben psc      ON psc.id = ks.scheibe_id
		JOIN disciplines d        ON d.id = psc.discipline_id
		JOIN shooters sh          ON sh.id = pt.shooter_id
		LEFT JOIN clubs cl        ON cl.id = sh.club_id
		LEFT JOIN shooter_classes sc ON sc.id = pt.class_id
		JOIN v_session_results vsr ON vsr.session_id = ks.session_id
		%s
		WHERE ks.preisschiessen_id = $2
		  AND (array_length($3::uuid[],1) IS NULL OR pt.class_id = ANY($3::uuid[]))
		  AND vsr.shot_count >= d.match_shot_count
		ORDER BY pt.id, wert %s`, valueExpr, joinTable, order)

	rows, err := pool.Query(ctx, sql, w.ID, w.PreisschiessenID, w.KlassenIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []wertungRow
	for rows.Next() {
		var x wertungRow
		if err := rows.Scan(&x.teilnehmerID, &x.startNr, &x.nachname, &x.vorname,
			&x.verein, &x.klasse, &x.wert); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// computeMeisterPunkt berechnet die Platzierung für eine Meister- oder
// Punkt-Wertung: je Teilnehmer die Rohwerte auf 10 auffüllen (0 bei Ring,
// "schlechtester Wert" bei Teiler), Summe der besten AnzSumme Werte bilden
// (bereits sortiert) und danach nach dieser Summe platzieren, mit den
// Einzelwerten als Tiebreak bei Gleichstand.
//
// Fachlich identisch zu gen_SerienP.py/gen_TeilerP.py, aber vereinfacht auf
// EINE Platzierung statt der beiden dort parallel gepflegten Felder
// (Platz nach Einzelwert, PlatzP nach Summe): mit AnzSumme=1 entspricht diese
// eine Platzierung automatisch einer reinen Einzelwertung (nur der beste
// Wert zählt), AnzSumme>1 einer Summenwertung (mehrere Scheiben/Schüsse
// zählen zusammen) - ein separates Konzept für "Einzelwertung" ist dafür
// nicht nötig.
func computeMeisterPunkt(ctx context.Context, pool *pgxpool.Pool, w PSWertung) ([]PSWertungErgebnis, error) {
	rows, err := loadWertungRows(ctx, pool, w)
	if err != nil {
		return nil, err
	}

	fillValue := 0.0
	desc := true
	if w.Wertungsfeld == "teiler" {
		fillValue = 99999
		desc = false
	}

	// Nach Teilnehmer gruppieren (SQL liefert bereits nach pt.id sortiert).
	type group struct {
		meta  wertungRow
		werte []float64
	}
	order := []string{}
	groups := map[string]*group{}
	for _, r := range rows {
		g, ok := groups[r.teilnehmerID]
		if !ok {
			g = &group{meta: r}
			groups[r.teilnehmerID] = g
			order = append(order, r.teilnehmerID)
		}
		g.werte = append(g.werte, r.wert) // Faktor bereits in loadWertungRows (je Scheibe) angewandt
	}

	out := make([]PSWertungErgebnis, 0, len(order))
	for _, tid := range order {
		g := groups[tid]
		werte := append([]float64(nil), g.werte...)
		for len(werte) < 10 {
			werte = append(werte, fillValue)
		}
		werte = werte[:10]
		n := w.AnzSumme
		if n < 1 {
			n = 1
		}
		if n > len(werte) {
			n = len(werte)
		}
		sum := 0.0
		for _, v := range werte[:n] {
			sum += v
		}
		out = append(out, PSWertungErgebnis{
			TeilnehmerID: g.meta.teilnehmerID,
			StartNr:      g.meta.startNr,
			Nachname:     g.meta.nachname,
			Vorname:      g.meta.vorname,
			Verein:       g.meta.verein,
			Klasse:       g.meta.klasse,
			Werte:        werte,
			Summe:        sum,
		})
	}

	cmpWerte := func(a, b []float64) int {
		for i := range a {
			if a[i] != b[i] {
				if (a[i] < b[i]) == desc {
					return 1
				}
				return -1
			}
		}
		return 0
	}

	// Platz: sortiert nach Summe, bei Gleichstand die naechstbeste
	// Scheibe/der naechstbeste Schuss als Tiebreak (Werte ist immer nach
	// bestem Wert zuerst sortiert). Sind auch dort alle 10 Werte gleich,
	// entscheidet die kleinere Teilnehmernummer (deterministisch, statt
	// von der zufaelligen SQL-Reihenfolge abzuhaengen).
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Summe != out[j].Summe {
			if desc {
				return out[i].Summe > out[j].Summe
			}
			return out[i].Summe < out[j].Summe
		}
		if c := cmpWerte(out[i].Werte, out[j].Werte); c != 0 {
			return c < 0
		}
		return out[i].StartNr < out[j].StartNr
	})
	for i := range out {
		out[i].Platz = i + 1
	}
	return out, nil
}

// ----------------------------------------------------------------------------
// Berechnung – Adler
// ----------------------------------------------------------------------------

// computeAdler mischt zwei bereits berechnete Wertungen (eine Punkt-, eine
// Meister-Wertung) alternierend zu einer gemeinsamen Rangliste, analog
// gen_Adler.py (Zeile 90-134): beginnend mit der Meister-Wertung, abwechselnd
// je einen noch nicht vergebenen Teilnehmer aus Teiler- bzw. Meister-Liste
// (jeweils in deren eigener Platz-Reihenfolge) übernehmen, bis eine der
// beiden Listen erschöpft ist.
func computeAdler(teilerErgebnisse, meisterErgebnisse []PSWertungErgebnis) []PSWertungErgebnis {
	byPlatz := func(list []PSWertungErgebnis) []PSWertungErgebnis {
		out := append([]PSWertungErgebnis(nil), list...)
		sort.SliceStable(out, func(i, j int) bool { return out[i].Platz < out[j].Platz })
		return out
	}
	teiler := byPlatz(teilerErgebnisse)
	meister := byPlatz(meisterErgebnisse)

	seen := map[string]bool{}
	var out []PSWertungErgebnis
	platz := 1
	ti, mi := 0, 0
	fromMeister := true
	for ti < len(teiler) || mi < len(meister) {
		if fromMeister {
			for mi < len(meister) && seen[meister[mi].TeilnehmerID] {
				mi++
			}
			if mi < len(meister) {
				e := meister[mi]
				mi++
				seen[e.TeilnehmerID] = true
				e.Platz = platz
				out = append(out, e)
				platz++
			}
		} else {
			for ti < len(teiler) && seen[teiler[ti].TeilnehmerID] {
				ti++
			}
			if ti < len(teiler) {
				e := teiler[ti]
				ti++
				seen[e.TeilnehmerID] = true
				e.Platz = platz
				out = append(out, e)
				platz++
			}
		}
		fromMeister = !fromMeister
		if ti >= len(teiler) && mi >= len(meister) {
			break
		}
	}
	return out
}

// ----------------------------------------------------------------------------
// Orchestrierung + Scheduler
// ----------------------------------------------------------------------------

// recomputeAuswertung berechnet alle Wertungen eines Preisschiessens neu.
// Meister/Punkt zuerst (werden sofort im Cache gespeichert), danach Adler
// (liest die soeben gespeicherten Meister-/Punkt-Ergebnisse aus dem Cache) -
// exakt die Reihenfolge des gs26-Batch-Jobs (gen_SerienP.py/gen_TeilerP.py
// vor gen_Adler.py).
func recomputeAuswertung(ctx context.Context, pool *pgxpool.Pool, preisschiessenID string) error {
	store := &Store{pool: pool}
	wertungen, err := store.ListWertungen(ctx, preisschiessenID)
	if err != nil {
		return err
	}

	ergebnisseByID := map[string][]PSWertungErgebnis{}

	for _, w := range wertungen {
		if w.Typ == "adler" {
			continue
		}
		ergebnisse, err := computeMeisterPunkt(ctx, pool, w)
		if err != nil {
			return fmt.Errorf("Wertung %s (%s): %w", w.ShortDesc, w.DisziplinKey, err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		if err := replaceWertungErgebnisse(ctx, tx, w.ID, ergebnisse); err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		ergebnisseByID[w.ID] = ergebnisse
	}

	for _, w := range wertungen {
		if w.Typ != "adler" {
			continue
		}
		if w.AdlerTeilerID == nil || w.AdlerMeisterID == nil {
			return fmt.Errorf("Adler-Wertung %s: Teiler-/Meister-Referenz fehlt", w.ShortDesc)
		}
		teiler, ok := ergebnisseByID[*w.AdlerTeilerID]
		if !ok {
			return fmt.Errorf("Adler-Wertung %s: referenzierte Teiler-Wertung nicht gefunden", w.ShortDesc)
		}
		meister, ok := ergebnisseByID[*w.AdlerMeisterID]
		if !ok {
			return fmt.Errorf("Adler-Wertung %s: referenzierte Meister-Wertung nicht gefunden", w.ShortDesc)
		}
		ergebnisse := computeAdler(teiler, meister)
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		if err := replaceWertungErgebnisse(ctx, tx, w.ID, ergebnisse); err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}

	return nil
}

// RunAuswertungScheduler läuft als Hintergrund-Goroutine (gestartet sowohl
// im Normalbetrieb als auch im -worker-only-Modus, siehe main.go) und
// arbeitet fällige Preisschiessen-Auswertungen ab. Mehrere Instanzen dieser
// Funktion (auf demselben oder verschiedenen Rechnern) können gefahrlos
// gegen dieselbe DB laufen, siehe Store.claimAuswertungJob.
func RunAuswertungScheduler(ctx context.Context, pool *pgxpool.Pool) {
	store := &Store{pool: pool}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for {
			id, ok, err := store.claimAuswertungJob(ctx)
			if err != nil {
				log.Printf("Auswertung-Scheduler: Claim fehlgeschlagen: %v", err)
				break
			}
			if !ok {
				break
			}
			runAuswertungJob(ctx, store, pool, id)
		}
	}
}

func runAuswertungJob(ctx context.Context, store *Store, pool *pgxpool.Pool, preisschiessenID string) {
	start := time.Now()
	log.Printf("Auswertung: Berechnung für Preisschiessen %s gestartet", preisschiessenID)
	err := recomputeAuswertung(ctx, pool, preisschiessenID)
	dur := time.Since(start)
	if err != nil {
		log.Printf("Auswertung: Preisschiessen %s fehlgeschlagen nach %s: %v", preisschiessenID, dur, err)
	} else {
		log.Printf("Auswertung: Preisschiessen %s fertig nach %s", preisschiessenID, dur)
	}
	if ferr := store.finishAuswertungJob(context.Background(), preisschiessenID, err, dur); ferr != nil {
		log.Printf("Auswertung: Status-Update für %s fehlgeschlagen: %v", preisschiessenID, ferr)
	}
}

// ----------------------------------------------------------------------------
// API-Handler
// ----------------------------------------------------------------------------

func (a *APIServer) listWertungen(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ListWertungen(r.Context(), r.PathValue("id"))
}

func (a *APIServer) createWertung(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[PSWertung](r)
	if err != nil || body.DisziplinKey == "" || body.ShortDesc == "" {
		return nil, errors.New("disziplin_key und short_desc erforderlich")
	}
	body.PreisschiessenID = r.PathValue("id")
	id, err := a.store.CreateWertung(r.Context(), body)
	if err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusCreated)
	return map[string]string{"id": id}, nil
}

func (a *APIServer) updateWertung(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[PSWertung](r)
	if err != nil || body.DisziplinKey == "" || body.ShortDesc == "" {
		return nil, errors.New("disziplin_key und short_desc erforderlich")
	}
	body.ID = r.PathValue("wid")
	if err := a.store.UpdateWertung(r.Context(), body); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) deleteWertung(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	if err := a.store.DeleteWertung(r.Context(), r.PathValue("wid")); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) getWertungErgebnis(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ListWertungErgebnisse(r.Context(), r.PathValue("wid"))
}

func (a *APIServer) getAuswertungStatus(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.GetAuswertungStatus(r.Context(), r.PathValue("id"))
}

func (a *APIServer) putAuswertungSettings(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		// nil/fehlend = automatische Berechnung abschalten.
		IntervalSeconds *int `json:"interval_seconds"`
	}](r)
	if err != nil {
		return nil, errors.New("ungültiger Body")
	}
	if body.IntervalSeconds != nil && (*body.IntervalSeconds < 300 || *body.IntervalSeconds > 900) {
		return nil, errors.New("interval_seconds muss zwischen 300 und 900 liegen (oder leer für 'aus')")
	}
	if err := a.store.SetAuswertungInterval(r.Context(), r.PathValue("id"), body.IntervalSeconds); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) postRecomputeAuswertung(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	id := r.PathValue("id")
	ok, err := a.store.claimAuswertungJobNow(r.Context(), id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return map[string]any{"status": "running"}, nil // bereits am Laufen
	}
	go runAuswertungJob(context.Background(), a.store, a.store.pool, id)
	return map[string]any{"status": "running"}, nil
}

func (a *APIServer) getAnzeigeConfig(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.GetAnzeigeConfig(r.Context(), r.PathValue("id"))
}

func (a *APIServer) putAnzeigeConfig(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[PSAnzeigeConfig](r)
	if err != nil {
		return nil, err
	}
	body.PreisschiessenID = r.PathValue("id")
	if err := a.store.SetAnzeigeConfig(r.Context(), body); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}
