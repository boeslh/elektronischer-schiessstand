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
	"regexp"
	"sort"
	"strings"
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
	// SerienModus/SerienAnzahl (Migration 065, urspruenglich je Scheibe in
	// Migration 063 - siehe buildSeriesJoin-Kommentar) steuern EINHEITLICH
	// FUER DIE GANZE WERTUNG, welche Serien einer Scheibe eingehen. Nutzer-
	// Feedback: je Scheibe war das bei mehreren Scheiben in einer
	// Kombi-Wertung unuebersichtlich und wird in der Praxis ohnehin nie
	// unterschiedlich gesetzt. Nur der Faktor (PSWertungScheibe) bleibt je
	// Scheibe, da unterschiedliche Zielgroessen/Kaliber weiterhin
	// unterschiedliche Normierungsfaktoren brauchen.
	SerienModus  string `json:"serien_modus"`
	SerienAnzahl *int   `json:"serien_anzahl"`
	// Nur typ='punkte_saison' (Migration 062, siehe vereinsabend_wertungen.go):
	// Punkte-Lookup-Tabellen fuer Ring/Teiler je effektivem Schiesstag plus
	// ein fester Anwesenheits-Bonus (nur wenn an diesem Tag nicht vor-/
	// nachgeschossen wurde). Wertungsfeld waehlt dabei die Ring-Basis
	// ("ring" oder "ring_decimal"); Teiler wird immer unabhaengig davon
	// aus dem besten Teiler des Tages ermittelt.
	AnwesenheitPunkte float64               `json:"anwesenheit_punkte"`
	PunkteRing        []PSWertungPunktStufe `json:"punkte_ring"`
	PunkteTeiler      []PSWertungPunktStufe `json:"punkte_teiler"`
}

// PSWertungPunktStufe ist eine Schwelle einer Punkte-Lookup-Tabelle
// (ps_wertung_punkte_ring/ps_wertung_punkte_teiler): Schwelle bedeutet je
// nach Tabelle "ab_wert" (Ring, untere Schranke) oder "bis_wert" (Teiler,
// obere Schranke) - siehe lookupPunkteAb/lookupPunkteBis.
type PSWertungPunktStufe struct {
	Schwelle float64 `json:"schwelle"`
	Punkte   float64 `json:"punkte"`
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
	// Scheiben: Namen der Scheibe(n), die der Teilnehmer für diese Wertung
	// tatsächlich geschossen hat (z.B. "LG Kombi" vs. "LP Kombi" in einer
	// kombinierten Wertung) - siehe computeMeisterPunkt.
	Scheiben []string `json:"scheiben"`
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
	PreisschiessenID string `json:"preisschiessen_id"`
	ReloadSeconds    int    `json:"reload_seconds"`
	TitleFontSize    int    `json:"title_font_size"`
	ListFontSize     int    `json:"list_font_size"`
	// AnzeigeItems: für den Kiosk-Modus ausgewählte Teilnehmer-Wertungen
	// und/oder Vereins-Auswertungen, im selben Schlüsselschema wie die
	// Listen-Navigation der browsbaren Ergebnis-Website (site.go
	// loadSidebar): "wertung:<uuid>" bzw. "verein:anzahl"/"verein:prozent"/
	// "verein:punkte" - siehe validAnzeigeItem und preisanzeige/display.go.
	AnzeigeItems []string `json:"anzeige_items"`
	// AnzeigeItems2: dieselbe Auswahl, aber für die zweite, unabhängige
	// Kiosk-Anzeige ("/ps/{id}/kiosk2") - alle übrigen Einstellungen dieser
	// Struct gelten für beide Kiosk-Anzeigen gemeinsam.
	AnzeigeItems2 []string `json:"anzeige_items_2"`
	// WerbungIntervall: Werbebild alle N Teilnehmer-Zeilen in einer
	// Ergebnisliste (Wertung/Vereins-Auswertung) im Display-Server - die
	// Bilder selbst liegen als Dateien auf dessen Rechner, siehe
	// preisanzeige/werbung.go.
	WerbungIntervall int `json:"werbung_intervall"`
	// Farben gelten sowohl für die browsbare Ergebnis-Website als auch für
	// den Kiosk-Modus des Display-Servers, siehe preisanzeige/farben.go -
	// darüber lassen sich beide Ansichten aneinander angleichen.
	BgColor      string `json:"bg_color"`
	TextColor    string `json:"text_color"`
	RowEvenColor string `json:"row_even_color"`
	RowOddColor  string `json:"row_odd_color"`
	// Spaltensteuerung im Kiosk-Modus (preisanzeige/display.go): Verein/
	// Klasse einzeln aus-/einblendbar, dafür eine konfigurierbare Anzahl an
	// Einzelergebnis-Spalten (0-10, bisher fest "Beste 5").
	KioskShowVerein             bool `json:"kiosk_show_verein"`
	KioskShowKlasse             bool `json:"kiosk_show_klasse"`
	KioskAnzahlEinzelergebnisse int  `json:"kiosk_anzahl_einzelergebnisse"`
	// ShowScheibe: zusätzliche Spalte mit dem Namen der geschossenen
	// Scheibe(n) (z.B. "LG Kombi"/"LP Kombi") - gilt sowohl für die
	// browsbare Ergebnis-Website als auch für den Kiosk-Modus, siehe
	// PSWertungErgebnis.Scheiben.
	ShowScheibe bool `json:"show_scheibe"`
}

// ----------------------------------------------------------------------------
// Store – Wertungen (Konfiguration)
// ----------------------------------------------------------------------------

func (s *Store) ListWertungen(ctx context.Context, preisschiessenID string) ([]PSWertung, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, preisschiessen_id, disziplin_key, typ, short_desc, COALESCE(long_desc,''),
		       COALESCE(wertungsfeld,''), klassen_ids::text[],
		       anz_summe, adler_teiler_id::text, adler_meister_id::text,
		       sort_order, visible, anwesenheit_punkte, serien_modus, serien_anzahl
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
			&x.SortOrder, &x.Visible, &x.AnwesenheitPunkte, &x.SerienModus, &x.SerienAnzahl); err != nil {
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
		if out[i].PunkteRing, err = listWertungPunkte(ctx, s.pool, "ps_wertung_punkte_ring", "ab_wert", out[i].ID); err != nil {
			return nil, err
		}
		if out[i].PunkteTeiler, err = listWertungPunkte(ctx, s.pool, "ps_wertung_punkte_teiler", "bis_wert", out[i].ID); err != nil {
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
		       sort_order, visible, anwesenheit_punkte, serien_modus, serien_anzahl
		FROM ps_wertungen WHERE id=$1`, id).Scan(
		&x.ID, &x.PreisschiessenID, &x.DisziplinKey, &x.Typ, &x.ShortDesc,
		&x.LongDesc, &x.Wertungsfeld, &x.KlassenIDs,
		&x.AnzSumme, &x.AdlerTeilerID, &x.AdlerMeisterID,
		&x.SortOrder, &x.Visible, &x.AnwesenheitPunkte, &x.SerienModus, &x.SerienAnzahl)
	if err != nil {
		return x, err
	}
	if x.Scheiben, err = s.listWertungScheiben(ctx, x.ID); err != nil {
		return x, err
	}
	if x.PunkteRing, err = listWertungPunkte(ctx, s.pool, "ps_wertung_punkte_ring", "ab_wert", x.ID); err != nil {
		return x, err
	}
	x.PunkteTeiler, err = listWertungPunkte(ctx, s.pool, "ps_wertung_punkte_teiler", "bis_wert", x.ID)
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

// listWertungPunkte liest eine Punkte-Lookup-Tabelle (ps_wertung_punkte_ring
// bzw. ps_wertung_punkte_teiler, ueber table/col parametrisiert - beide
// Tabellen haben identische Form bis auf den Namen der Schwellenspalte) fuer
// eine typ='punkte_saison'-Wertung, aufsteigend nach Schwelle sortiert.
func listWertungPunkte(ctx context.Context, pool *pgxpool.Pool, table, col, wertungID string) ([]PSWertungPunktStufe, error) {
	rows, err := pool.Query(ctx, fmt.Sprintf(`SELECT %s, punkte FROM %s WHERE wertung_id=$1 ORDER BY %s`, col, table, col), wertungID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PSWertungPunktStufe{}
	for rows.Next() {
		var x PSWertungPunktStufe
		if err := rows.Scan(&x.Schwelle, &x.Punkte); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// setWertungPunkte ersetzt die komplette Punkte-Lookup-Tabelle einer Wertung
// (DELETE+INSERT, wie setWertungScheiben).
func setWertungPunkte(ctx context.Context, tx pgx.Tx, table, col, wertungID string, stufen []PSWertungPunktStufe) error {
	if _, err := tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE wertung_id=$1`, table), wertungID); err != nil {
		return err
	}
	for _, st := range stufen {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (wertung_id, %s, punkte) VALUES ($1,$2,$3)`, table, col),
			wertungID, st.Schwelle, st.Punkte); err != nil {
			return err
		}
	}
	return nil
}

// orEmptyStrs verhindert, dass ein nil-Slice (z.B. bei einer Adler-Wertung
// ohne ScheibenNamen/KlassenIDs) als SQL NULL statt als leeres Array
// gebunden wird - beide Spalten sind NOT NULL.
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

	serienModus := x.SerienModus
	if serienModus == "" {
		serienModus = "alle"
	}
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO ps_wertungen
		  (preisschiessen_id, disziplin_key, typ, short_desc, long_desc, wertungsfeld,
		   klassen_ids, anz_summe, adler_teiler_id, adler_meister_id, sort_order, visible,
		   anwesenheit_punkte, serien_modus, serien_anzahl)
		VALUES ($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),$7::uuid[],$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING id`,
		x.PreisschiessenID, x.DisziplinKey, x.Typ, x.ShortDesc, x.LongDesc, x.Wertungsfeld,
		orEmptyStrs(x.KlassenIDs), x.AnzSumme, x.AdlerTeilerID,
		x.AdlerMeisterID, x.SortOrder, x.Visible, x.AnwesenheitPunkte, serienModus, x.SerienAnzahl,
	).Scan(&id); err != nil {
		return "", err
	}
	if err := setWertungScheiben(ctx, tx, id, x.Scheiben); err != nil {
		return "", err
	}
	if err := setWertungPunkte(ctx, tx, "ps_wertung_punkte_ring", "ab_wert", id, x.PunkteRing); err != nil {
		return "", err
	}
	if err := setWertungPunkte(ctx, tx, "ps_wertung_punkte_teiler", "bis_wert", id, x.PunkteTeiler); err != nil {
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

	serienModus := x.SerienModus
	if serienModus == "" {
		serienModus = "alle"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE ps_wertungen SET
		  disziplin_key = $1, typ = $2, short_desc = $3, long_desc = NULLIF($4,''),
		  wertungsfeld = NULLIF($5,''), klassen_ids = $6::uuid[], anz_summe = $7,
		  adler_teiler_id = $8, adler_meister_id = $9, sort_order = $10, visible = $11,
		  anwesenheit_punkte = $12, serien_modus = $13, serien_anzahl = $14
		WHERE id = $15`,
		x.DisziplinKey, x.Typ, x.ShortDesc, x.LongDesc, x.Wertungsfeld,
		orEmptyStrs(x.KlassenIDs), x.AnzSumme, x.AdlerTeilerID,
		x.AdlerMeisterID, x.SortOrder, x.Visible, x.AnwesenheitPunkte, serienModus, x.SerienAnzahl, x.ID,
	); err != nil {
		return err
	}
	if err := setWertungScheiben(ctx, tx, x.ID, x.Scheiben); err != nil {
		return err
	}
	if err := setWertungPunkte(ctx, tx, "ps_wertung_punkte_ring", "ab_wert", x.ID, x.PunkteRing); err != nil {
		return err
	}
	if err := setWertungPunkte(ctx, tx, "ps_wertung_punkte_teiler", "bis_wert", x.ID, x.PunkteTeiler); err != nil {
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
		       COALESCE(klasse,''), werte, summe, COALESCE(platz,0), scheiben
		FROM ps_wertung_ergebnisse WHERE ps_wertung_id=$1 ORDER BY platz`, wertungID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PSWertungErgebnis
	for rows.Next() {
		var x PSWertungErgebnis
		if err := rows.Scan(&x.TeilnehmerID, &x.StartNr, &x.Nachname, &x.Vorname, &x.Verein,
			&x.Klasse, &x.Werte, &x.Summe, &x.Platz, &x.Scheiben); err != nil {
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
			   werte, summe, platz, scheiben)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			wertungID, r.TeilnehmerID, r.StartNr, r.Nachname, r.Vorname, r.Verein, r.Klasse,
			r.Werte, r.Summe, r.Platz, orEmptyStrs(r.Scheiben)); err != nil {
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
		RETURNING reload_seconds, title_font_size, list_font_size, anzeige_items, anzeige_items_2, werbung_intervall,
		          bg_color, text_color, row_even_color, row_odd_color,
		          kiosk_show_verein, kiosk_show_klasse, kiosk_anzahl_einzelergebnisse, show_scheibe`,
		preisschiessenID,
	).Scan(&x.ReloadSeconds, &x.TitleFontSize, &x.ListFontSize, &x.AnzeigeItems, &x.AnzeigeItems2, &x.WerbungIntervall,
		&x.BgColor, &x.TextColor, &x.RowEvenColor, &x.RowOddColor,
		&x.KioskShowVerein, &x.KioskShowKlasse, &x.KioskAnzahlEinzelergebnisse, &x.ShowScheibe)
	return x, err
}

var hexColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
var wertungItemRE = regexp.MustCompile(`^wertung:[0-9a-fA-F-]{36}$`)
var validVereinTypen = map[string]bool{"anzahl": true, "prozent": true, "punkte": true}

// validAnzeigeItem prüft das Schlüsselschema für PSAnzeigeConfig.AnzeigeItems
// (siehe dortigen Kommentar) - anders als bei der bisherigen wertung_ids-
// Spalte (UUID[]) übernimmt hier keine DB-FK-Constraint die Validierung, da
// "verein:..." kein FK auf eine Tabelle ist.
func validAnzeigeItem(s string) bool {
	if typ, ok := strings.CutPrefix(s, "verein:"); ok {
		return validVereinTypen[typ]
	}
	return wertungItemRE.MatchString(s)
}

func (s *Store) SetAnzeigeConfig(ctx context.Context, x PSAnzeigeConfig) error {
	if x.WerbungIntervall < 1 {
		return errBadRequest("Werbe-Intervall muss mindestens 1 sein")
	}
	if x.KioskAnzahlEinzelergebnisse < 0 || x.KioskAnzahlEinzelergebnisse > 10 {
		return errBadRequest("Anzahl Einzelergebnisse muss zwischen 0 und 10 liegen")
	}
	for _, c := range []string{x.BgColor, x.TextColor, x.RowEvenColor, x.RowOddColor} {
		if !hexColorRE.MatchString(c) {
			return errBadRequest("Farben müssen im Format #rrggbb angegeben werden")
		}
	}
	for _, item := range x.AnzeigeItems {
		if !validAnzeigeItem(item) {
			return errBadRequest("Ungültiger Eintrag in den angezeigten Wertungen: " + item)
		}
	}
	for _, item := range x.AnzeigeItems2 {
		if !validAnzeigeItem(item) {
			return errBadRequest("Ungültiger Eintrag in den angezeigten Wertungen (Kiosk 2): " + item)
		}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ps_anzeige_config (preisschiessen_id, reload_seconds, title_font_size, list_font_size, anzeige_items, anzeige_items_2, werbung_intervall,
		                                bg_color, text_color, row_even_color, row_odd_color,
		                                kiosk_show_verein, kiosk_show_klasse, kiosk_anzahl_einzelergebnisse, show_scheibe)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (preisschiessen_id) DO UPDATE SET
		  reload_seconds = EXCLUDED.reload_seconds,
		  title_font_size = EXCLUDED.title_font_size,
		  list_font_size = EXCLUDED.list_font_size,
		  anzeige_items = EXCLUDED.anzeige_items,
		  anzeige_items_2 = EXCLUDED.anzeige_items_2,
		  werbung_intervall = EXCLUDED.werbung_intervall,
		  bg_color = EXCLUDED.bg_color,
		  text_color = EXCLUDED.text_color,
		  row_even_color = EXCLUDED.row_even_color,
		  row_odd_color = EXCLUDED.row_odd_color,
		  kiosk_show_verein = EXCLUDED.kiosk_show_verein,
		  kiosk_show_klasse = EXCLUDED.kiosk_show_klasse,
		  kiosk_anzahl_einzelergebnisse = EXCLUDED.kiosk_anzahl_einzelergebnisse,
		  show_scheibe = EXCLUDED.show_scheibe`,
		x.PreisschiessenID, x.ReloadSeconds, x.TitleFontSize, x.ListFontSize, orEmptyStrs(x.AnzeigeItems), orEmptyStrs(x.AnzeigeItems2), x.WerbungIntervall,
		x.BgColor, x.TextColor, x.RowEvenColor, x.RowOddColor,
		x.KioskShowVerein, x.KioskShowKlasse, x.KioskAnzahlEinzelergebnisse, x.ShowScheibe)
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
	scheibeName  string
	wert         float64
}

// loadWertungRows liest die Rohwerte für eine Meister-/Punkt-Wertung. Seit
// Migration 065 gilt serien_modus/serien_anzahl EINHEITLICH FUER DIE GANZE
// WERTUNG (vorher je Scheibe, Migration 063) - dadurch reicht jetzt EIN
// normaler, ungebundener JOIN über alle zugeordneten Scheiben hinweg
// (ps_wertung_scheiben direkt gejoint), statt wie zuvor je Scheibe eine
// eigene Abfrage zu bauen und die Ergebnisse in Go zusammenzuführen -
// letzteres war nötig, weil der Serien-Modus damals zeilenabhängig war
// (und ein Versuch, das per LATERAL abzubilden, siehe frühere Fassung
// dieses Kommentars, katastrophal langsam war). Da der Modus jetzt für die
// gesamte Wertung feststeht, ist der Join auf v_series_results/
// v_scoring_shots strukturell für jede Scheibe identisch und kann von
// Postgres wie gewohnt einmalig materialisiert werden.
//
//   - "ring"/"ring_decimal": normalerweise eine Zeile je Serie
//     (v_series_results), analog gs26_Serien - Quelle für gen_SerienP.py.
//   - "teiler": normalerweise eine Zeile je Einzelschuss (v_scoring_shots),
//     analog gs26_Treffer - Quelle für gen_TeilerP.py.
//
// Die Zuordnung Wertung -> Scheibe läuft über die echte FK-Tabelle
// ps_wertung_scheiben (nicht über Namensvergleich) - der dort hinterlegte
// Faktor je Scheibe (z.B. LG vs. LP in einer kombinierten Wertung) wird
// direkt in der SQL-Abfrage angewandt. Optional zusätzlich nach Klasse
// gefiltert (klassen_ids, leer = alle), und nur abgeschlossene Scheiben
// (Wertungsschüsse erreicht) zählen, wie in copy_Scheiben_pg.py/gen_*.py.
func loadWertungRows(ctx context.Context, pool *pgxpool.Pool, w PSWertung) ([]wertungRow, error) {
	if w.Wertungsfeld != "ring" && w.Wertungsfeld != "ring_decimal" && w.Wertungsfeld != "teiler" {
		return nil, fmt.Errorf("unbekanntes Wertungsfeld %q", w.Wertungsfeld)
	}
	order := "DESC"
	if w.Wertungsfeld == "teiler" {
		order = "ASC"
	}
	if (w.SerienModus == "erste_n" || w.SerienModus == "beste_n") && (w.SerienAnzahl == nil || *w.SerienAnzahl < 1) {
		return nil, fmt.Errorf("serien_anzahl fehlt fuer serien_modus %q", w.SerienModus)
	}
	serienAnzahl := 0
	if w.SerienAnzahl != nil {
		serienAnzahl = *w.SerienAnzahl
	}
	join, err := buildSeriesJoin(w.Wertungsfeld, w.SerienModus, "sv")
	if err != nil {
		return nil, err
	}

	sql := fmt.Sprintf(`
		SELECT pt.id, pt.teilnehmer_nr, sh.last_name, sh.first_name,
		       COALESCE(cl.name,''), COALESCE(sc.name,''), psc.name, sv.wert * ws.faktor AS wert
		FROM ps_wertung_scheiben ws
		JOIN ps_scheiben psc      ON psc.id = ws.scheibe_id
		JOIN disciplines d        ON d.id = psc.discipline_id
		JOIN ps_kauf_scheiben ks  ON ks.scheibe_id = ws.scheibe_id
		JOIN ps_kaeufe k          ON k.id = ks.kauf_id
		JOIN ps_teilnehmer pt     ON pt.id = k.teilnehmer_id
		JOIN shooters sh          ON sh.id = pt.shooter_id
		LEFT JOIN clubs cl        ON cl.id = sh.club_id
		LEFT JOIN shooter_classes sc ON sc.id = pt.class_id
		JOIN v_session_results vsr ON vsr.session_id = ks.session_id
		%s
		WHERE ws.wertung_id = $1
		  AND ks.preisschiessen_id = $2
		  AND (array_length($3::uuid[],1) IS NULL OR pt.class_id = ANY($3::uuid[]))
		  AND vsr.shot_count >= d.match_shot_count
		  AND NOT psc.auswertung_unsichtbar
		  AND $4::int >= 0`, join)

	rows, err := pool.Query(ctx, sql, w.ID, w.PreisschiessenID, w.KlassenIDs, serienAnzahl)
	if err != nil {
		return nil, err
	}
	var out []wertungRow
	for rows.Next() {
		var x wertungRow
		if err := rows.Scan(&x.teilnehmerID, &x.startNr, &x.nachname, &x.vorname,
			&x.verein, &x.klasse, &x.scheibeName, &x.wert); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	sort.Slice(out, func(i, j int) bool {
		if out[i].teilnehmerID != out[j].teilnehmerID {
			return out[i].teilnehmerID < out[j].teilnehmerID
		}
		if order == "DESC" {
			return out[i].wert > out[j].wert
		}
		return out[i].wert < out[j].wert
	})
	return out, nil
}

// buildSeriesJoin liefert den (ungebundenen, einmalig auswertbaren) JOIN,
// der fuer eine einzelne Scheibe je nach serien_modus (Migration 063) die
// Werte-Spalte "sv.wert" liefert:
//
//   - 'alle'/'je_serie': unveraendertes Verhalten von vor Migration 063 -
//     bei Ring/Zehntel ein Wert je Serie, bei Teiler ein Wert je
//     Einzelschuss (keine Restriktion/Aggregation).
//   - 'gesamt': alle Serien der Scheibe zu EINEM Wert zusammengefasst -
//     nutzt total_rings/total_decimal/best_center_distance direkt aus dem
//     ohnehin schon gejointen v_session_results (vsr), kein weiterer Join.
//   - 'erste_n'/'beste_n': nur ausgewaehlte Serien, zu einem Wert
//     zusammengefasst. Wichtig fuer die Performance: die Fensterfunktion
//     partitioniert per PARTITION BY session_id UEBER DIE GESAMTE VIEW,
//     nicht korreliert per WHERE session_id=ks.session_id - so kann
//     Postgres v_series_results/v_series_best_teiler weiterhin einmalig
//     materialisieren und ganz normal gegen ks joinen, statt sie pro
//     Kauf-Scheiben-Zeile erneut auszuwerten (siehe loadWertungRows-
//     Kommentar zum urspruenglich viel zu langsamen LATERAL-Ansatz).
// Der letzte Platzhalter (siehe Aufrufer) ist die serien_anzahl der Wertung
// (0 wenn nicht relevant) - als gebundener Parameter statt einer
// korrelierten Subquery gegen ps_wertungen, da der Wert dem Aufrufer
// ohnehin schon bekannt ist. Die konkrete Platzhalter-Nummer haengt vom
// Aufrufer ab (loadWertungRows: $4; vereinsabend_wertungen.go
// loadPunkteSaisonRows: ebenfalls $4, fuer Ring- UND Teiler-Join gemeinsam).
func buildSeriesJoin(wertungsfeld, serienModus, alias string) (string, error) {
	if wertungsfeld == "teiler" {
		switch serienModus {
		case "alle", "je_serie", "":
			return fmt.Sprintf(`JOIN (SELECT effective_session_id AS session_id, eff_center_distance AS wert
			              FROM v_scoring_shots) %[1]s ON %[1]s.session_id = ks.session_id`, alias), nil
		case "gesamt":
			return fmt.Sprintf(`JOIN (SELECT session_id, best_center_distance AS wert FROM v_session_results) %[1]s ON %[1]s.session_id = ks.session_id`, alias), nil
		case "erste_n":
			return fmt.Sprintf(`JOIN (
				SELECT session_id, MIN(best_teiler) AS wert FROM (
					SELECT session_id, best_teiler,
					       ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY series_no) AS ord
					FROM v_series_best_teiler
				) x WHERE x.ord <= $5
				GROUP BY session_id
			) %[1]s ON %[1]s.session_id = ks.session_id`, alias), nil
		case "beste_n":
			return fmt.Sprintf(`JOIN (
				SELECT vsbt.session_id, MIN(vsbt.best_teiler) AS wert FROM (
					SELECT vsbt.session_id, vsbt.best_teiler,
					       ROW_NUMBER() OVER (PARTITION BY vsbt.session_id ORDER BY vser.rings DESC) AS ord
					FROM v_series_best_teiler vsbt
					JOIN v_series_results vser ON vser.session_id = vsbt.session_id AND vser.series_no = vsbt.series_no
				) vsbt WHERE vsbt.ord <= $5
				GROUP BY vsbt.session_id
			) %[1]s ON %[1]s.session_id = ks.session_id`, alias), nil
		}
		return "", fmt.Errorf("unbekannter serien_modus %q", serienModus)
	}

	col := "rings"
	gesamtCol := "total_rings"
	if wertungsfeld == "ring_decimal" {
		col = "decimal_total"
		gesamtCol = "total_decimal"
	}
	switch serienModus {
	case "alle", "je_serie", "":
		return fmt.Sprintf(`JOIN (SELECT session_id, %s AS wert FROM v_series_results) %[2]s ON %[2]s.session_id = ks.session_id`, col, alias), nil
	case "gesamt":
		return fmt.Sprintf(`JOIN (SELECT session_id, %s AS wert FROM v_session_results) %[2]s ON %[2]s.session_id = ks.session_id`, gesamtCol, alias), nil
	case "erste_n":
		return fmt.Sprintf(`JOIN (
			SELECT session_id, SUM(%[1]s) AS wert FROM (
				SELECT session_id, %[1]s, ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY series_no) AS ord
				FROM v_series_results
			) x WHERE x.ord <= $5
			GROUP BY session_id
		) %[2]s ON %[2]s.session_id = ks.session_id`, col, alias), nil
	case "beste_n":
		return fmt.Sprintf(`JOIN (
			SELECT session_id, SUM(%[1]s) AS wert FROM (
				SELECT session_id, %[1]s, ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY %[1]s DESC) AS ord
				FROM v_series_results
			) x WHERE x.ord <= $5
			GROUP BY session_id
		) %[2]s ON %[2]s.session_id = ks.session_id`, col, alias), nil
	}
	return "", fmt.Errorf("unbekannter serien_modus %q", serienModus)
}

// computeMeisterPunkt berechnet die Platzierung für eine Meister- oder
// Punkt-Wertung: je Teilnehmer die Rohwerte auffüllen (0 bei Ring,
// "schlechtester Wert" bei Teiler) und über rankByBestNValues platzieren.
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
	return rankByBestNValues(rows, w.AnzSumme, fillValue, desc), nil
}

// rankByBestNValues ist der gemeinsame Ranking-Kern fuer computeMeisterPunkt
// (Rohwerte je Scheibe/Schuss eines Preisschiessens) und computePunkteSaison
// (Punkte-Summe je effektivem Schiesstag eines Vereinsabends, siehe
// vereinsabend_wertungen.go): je Teilnehmer die Rohwerte auf mindestens 10
// (bzw. mehr, falls ein Teilnehmer mehr als 10 Werte hat - z.B. eine lange
// Saison mit vielen Vereinsabenden) auffuellen, Summe der besten n Werte
// bilden (bereits nach "bestem Wert zuerst" sortiert) und danach nach dieser
// Summe platzieren, mit den Einzelwerten als Tiebreak bei Gleichstand.
// n<1 wird auf 1 angehoben (alte computeMeisterPunkt-Semantik); ein sehr
// grosses n (z.B. math.MaxInt32 fuer "alle zaehlenden Tage zaehlen" bei
// Punkte-Saison) wird ganz normal auf die tatsaechliche Werteanzahl gekappt.
func rankByBestNValues(rows []wertungRow, n int, fillValue float64, desc bool) []PSWertungErgebnis {
	// Nach Teilnehmer gruppieren (SQL liefert bereits nach pt.id sortiert).
	type group struct {
		meta         wertungRow
		werte        []float64
		scheibenSeen map[string]bool
		scheiben     []string // Reihenfolge der ersten Nennung, siehe scheibenSeen
	}
	order := []string{}
	groups := map[string]*group{}
	for _, r := range rows {
		g, ok := groups[r.teilnehmerID]
		if !ok {
			g = &group{meta: r, scheibenSeen: map[string]bool{}}
			groups[r.teilnehmerID] = g
			order = append(order, r.teilnehmerID)
		}
		g.werte = append(g.werte, r.wert) // Faktor bereits vom Aufrufer angewandt
		if r.scheibeName != "" && !g.scheibenSeen[r.scheibeName] {
			g.scheibenSeen[r.scheibeName] = true
			g.scheiben = append(g.scheiben, r.scheibeName)
		}
	}

	// Pad-Laenge: mindestens 10 (fuer die gewohnte S1..S10/T1..T10-Anzeige),
	// aber mindestens so lang wie der laengste tatsaechliche Werte-Vektor
	// (sonst wuerden bei mehr als 10 Werten - z.B. eine Saison mit > 10
	// Vereinsabenden - Werte stillschweigend abgeschnitten). Global (nicht
	// je Teilnehmer) berechnet, damit alle Werte-Vektoren gleich lang sind
	// und cmpWerte unten sicher elementweise vergleichen kann.
	padLen := 10
	for _, tid := range order {
		if l := len(groups[tid].werte); l > padLen {
			padLen = l
		}
	}

	out := make([]PSWertungErgebnis, 0, len(order))
	for _, tid := range order {
		g := groups[tid]
		werte := append([]float64(nil), g.werte...)
		for len(werte) < padLen {
			werte = append(werte, fillValue)
		}
		useN := n
		if useN < 1 {
			useN = 1
		}
		if useN > len(werte) {
			useN = len(werte)
		}
		sum := 0.0
		for _, v := range werte[:useN] {
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
			Scheiben:     g.scheiben,
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
	// bestem Wert zuerst sortiert). Sind auch dort alle Werte gleich,
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
	return out
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

	var ps Preisschiessen
	for _, w := range wertungen {
		if w.Typ == "punkte_saison" {
			ps, err = store.GetPreisschiessen(ctx, preisschiessenID)
			if err != nil {
				return fmt.Errorf("Preisschiessen fuer Punkte-Saison: %w", err)
			}
			break
		}
	}

	for _, w := range wertungen {
		if w.Typ == "adler" {
			continue
		}
		var ergebnisse []PSWertungErgebnis
		var err error
		if w.Typ == "punkte_saison" {
			ergebnisse, err = computePunkteSaison(ctx, pool, w, ps)
		} else {
			ergebnisse, err = computeMeisterPunkt(ctx, pool, w)
		}
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
