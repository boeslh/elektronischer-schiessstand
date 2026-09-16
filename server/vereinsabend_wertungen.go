// ============================================================================
// vereinsabend_wertungen.go – Punkte-Jahreswertung fuer Vereinsabend/
// Schiesssaison (typ='punkte_saison' in ps_wertungen, Migration 062, siehe
// .claude/plans/wise-scribbling-abelson.md).
//
// Eine Saison ist eine ganz normale preisschiessen-Zeile (kein eigenes
// Datenmodell fuer "Vereinsabend") - ein Vereinsabend ergibt sich rein aus
// der Auswertelogik hier: dem tatsaechlichen effektiven Schiesstag
// (schiesstag_override, sonst sessions.finished_at::date) einer gekauften/
// geschossenen ps_kauf_scheiben-Zeile, gefiltert auf die fuer die Saison
// konfigurierten Wochentage (preisschiessen.saison_wochentage). Je Tag
// zaehlt der beste Ring-/Teiler-Wert (mehrere Scheiben am selben Tag) und
// der Anwesenheitsbonus wird pro Tag nur einmal vergeben (nur wenn an
// diesem Tag keine der Scheiben als Vor-/Nachschuss markiert ist).
//
// Der resultierende Punktewert je (Teilnehmer, Tag) ist EIN Wert im Pool
// des Teilnehmers - genau wie bei computeMeisterPunkt ein Scheiben-Ergebnis
// ein Wert im Pool war. Deshalb wird hier NICHT neu platziert, sondern der
// bestehende Ranking-Kern rankByBestNValues wiederverwendet; "beste X
// Abende zaehlen" (preisschiessen.beste_x_abende, nil = alle) tritt an die
// Stelle von ps_wertungen.anz_summe.
// ============================================================================
package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// lookupPunkteAb sucht in einer Ring-Punktetabelle (ab_wert = untere
// Schranke inklusiv, z.B. "89 Ringe = 8 Punkte") die hoechste erreichte
// Schranke und liefert deren Punkte; 0, wenn keine Schranke erreicht wird.
func lookupPunkteAb(stufen []PSWertungPunktStufe, wert float64) float64 {
	var best *PSWertungPunktStufe
	for i := range stufen {
		if stufen[i].Schwelle <= wert && (best == nil || stufen[i].Schwelle > best.Schwelle) {
			best = &stufen[i]
		}
	}
	if best == nil {
		return 0
	}
	return best.Punkte
}

// lookupPunkteBis sucht in einer Teiler-Punktetabelle (bis_wert = obere
// Schranke inklusiv, z.B. "Teiler 34,4 = 5 Punkte") die niedrigste (engste)
// erreichte Schranke und liefert deren Punkte; 0, wenn keine Schranke
// erreicht wird (Teiler zu gross).
func lookupPunkteBis(stufen []PSWertungPunktStufe, wert float64) float64 {
	var best *PSWertungPunktStufe
	for i := range stufen {
		if stufen[i].Schwelle >= wert && (best == nil || stufen[i].Schwelle < best.Schwelle) {
			best = &stufen[i]
		}
	}
	if best == nil {
		return 0
	}
	return best.Punkte
}

// isoWeekday liefert den ISO-Wochentag (1=Mo..7=So) - time.Weekday zaehlt
// Sonntag als 0, das wird hier auf 7 abgebildet, damit es direkt gegen
// preisschiessen.saison_wochentage passt.
func isoWeekday(t time.Time) int {
	wd := int(t.Weekday())
	if wd == 0 {
		return 7
	}
	return wd
}

// weekdayAllowed prueft den Wochentags-Filter. Ein leeres allowed-Array ist
// eine unvollstaendige Saison-Konfiguration (die UI erzwingt beim Speichern
// mindestens einen Wochentag) - defensiv wird dann NICHT gefiltert, statt
// die komplette Jahreswertung stillschweigend leer zu berechnen.
func weekdayAllowed(t time.Time, allowed []int) bool {
	if len(allowed) == 0 {
		return true
	}
	wd := isoWeekday(t)
	for _, d := range allowed {
		if d == wd {
			return true
		}
	}
	return false
}

// saisonRawRow ist eine einzelne ps_kauf_scheiben-Zeile, wie sie aus der
// Datenbank fuer eine der der Wertung zugeordneten Scheiben gelesen wird -
// vor der Zusammenfassung auf effektive Schiesstage (siehe dayKey/dayAgg).
type saisonRawRow struct {
	teilnehmerID string
	startNr      int
	nachname     string
	vorname      string
	verein       string
	klasse       string
	schiesstag   time.Time
	vorNachschuss bool
	ringWert     *float64
	teilerWert   *float64
}

// loadPunkteSaisonRows liest die Rohdaten fuer eine typ='punkte_saison'-
// Wertung. Seit Migration 065 gilt serien_modus/serien_anzahl einheitlich
// fuer die ganze Wertung (vorher je Scheibe) - deshalb reicht hier wie in
// loadWertungRows EIN Query ueber alle zugeordneten Scheiben (ps_wertung_scheiben
// direkt gejoint) statt einer Schleife mit einer Abfrage je Scheibe. Ring-
// und Teiler-Join sind bewusst LEFT JOIN (anders als in buildSeriesJoin/
// loadWertungRows) - eine reine Teilnahme-Scheibe (scoring_mode='teilnahme')
// hat u.U. keine Serien-/Schuss-Zeile in v_series_results/v_scoring_shots
// und darf trotzdem nicht aus der Auswertung verschwinden (Anwesenheitsbonus
// soll weiterhin gelten).
func loadPunkteSaisonRows(ctx context.Context, pool *pgxpool.Pool, w PSWertung) ([]saisonRawRow, error) {
	ringField := "ring"
	if w.Wertungsfeld == "ring_decimal" {
		ringField = "ring_decimal"
	}
	if (w.SerienModus == "erste_n" || w.SerienModus == "beste_n") && (w.SerienAnzahl == nil || *w.SerienAnzahl < 1) {
		return nil, fmt.Errorf("serien_anzahl fehlt fuer serien_modus %q", w.SerienModus)
	}
	serienAnzahl := 0
	if w.SerienAnzahl != nil {
		serienAnzahl = *w.SerienAnzahl
	}
	ringJoin, err := buildSeriesJoin(ringField, w.SerienModus, "svr")
	if err != nil {
		return nil, fmt.Errorf("Ring-Join: %w", err)
	}
	teilerJoin, err := buildSeriesJoin("teiler", w.SerienModus, "svt")
	if err != nil {
		return nil, fmt.Errorf("Teiler-Join: %w", err)
	}

	sql := fmt.Sprintf(`
		SELECT pt.id, pt.teilnehmer_nr, sh.last_name, sh.first_name,
		       COALESCE(cl.name,''), COALESCE(clx.name,''),
		       COALESCE(ks.schiesstag_override, s.finished_at::date) AS schiesstag,
		       ks.ist_vor_nachschuss,
		       svr.wert * ws.faktor AS ring_wert,
		       svt.wert * ws.faktor AS teiler_wert
		FROM ps_wertung_scheiben ws
		JOIN ps_scheiben psc      ON psc.id = ws.scheibe_id
		JOIN ps_kauf_scheiben ks  ON ks.scheibe_id = ws.scheibe_id
		JOIN ps_kaeufe k          ON k.id = ks.kauf_id
		JOIN ps_teilnehmer pt     ON pt.id = k.teilnehmer_id
		JOIN shooters sh          ON sh.id = pt.shooter_id
		LEFT JOIN clubs cl        ON cl.id = sh.club_id
		LEFT JOIN shooter_classes clx ON clx.id = pt.class_id
		JOIN sessions s           ON s.id = ks.session_id
		LEFT %s
		LEFT %s
		WHERE ws.wertung_id = $1
		  AND ks.preisschiessen_id = $2
		  AND (array_length($3::uuid[],1) IS NULL OR pt.class_id = ANY($3::uuid[]))
		  AND NOT psc.auswertung_unsichtbar
		  AND (ks.schiesstag_override IS NOT NULL OR s.finished_at IS NOT NULL)
		  AND $4::int >= 0`, ringJoin, teilerJoin)

	rows, err := pool.Query(ctx, sql, w.ID, w.PreisschiessenID, w.KlassenIDs, serienAnzahl)
	if err != nil {
		return nil, err
	}
	var out []saisonRawRow
	for rows.Next() {
		var x saisonRawRow
		if err := rows.Scan(&x.teilnehmerID, &x.startNr, &x.nachname, &x.vorname,
			&x.verein, &x.klasse, &x.schiesstag, &x.vorNachschuss, &x.ringWert, &x.teilerWert); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return out, nil
}

// computePunkteSaison berechnet die Punkte-Jahreswertung: Rohdaten je
// Kauf-Scheibe auf effektive Schiesstage zusammenfassen (bester Ring, bester
// (kleinster) Teiler, Anwesenheitsbonus nur einmal pro Tag und nur wenn an
// diesem Tag keine Scheibe als Vor-/Nachschuss markiert ist), auf die
// Saison-Wochentage filtern, je Tag in einen Punktewert umrechnen (Ring-
// Punkte + Teiler-Punkte + Anwesenheits-Punkte) und danach ueber
// rankByBestNValues platzieren (ps.BesteXAbende = "beste X Abende zaehlen",
// nil = alle zaehlenden Tage).
func computePunkteSaison(ctx context.Context, pool *pgxpool.Pool, w PSWertung, ps Preisschiessen) ([]PSWertungErgebnis, error) {
	raw, err := loadPunkteSaisonRows(ctx, pool, w)
	if err != nil {
		return nil, err
	}

	type dayKey struct {
		teilnehmerID string
		tag          string // schiesstag.Format("2006-01-02"), Tagesgranularitaet als Schluessel
	}
	type dayAgg struct {
		meta       saisonRawRow
		ringWert   *float64
		teilerWert *float64
		anwesendOK bool // true, sobald mindestens eine Scheibe dieses Tages kein Vor-/Nachschuss ist
	}
	days := map[dayKey]*dayAgg{}
	var order []dayKey
	for _, r := range raw {
		if !weekdayAllowed(r.schiesstag, ps.SaisonWochentage) {
			continue
		}
		key := dayKey{teilnehmerID: r.teilnehmerID, tag: r.schiesstag.Format("2006-01-02")}
		a, ok := days[key]
		if !ok {
			a = &dayAgg{meta: r}
			days[key] = a
			order = append(order, key)
		}
		if r.ringWert != nil && (a.ringWert == nil || *r.ringWert > *a.ringWert) {
			a.ringWert = r.ringWert
		}
		if r.teilerWert != nil && (a.teilerWert == nil || *r.teilerWert < *a.teilerWert) {
			a.teilerWert = r.teilerWert
		}
		if !r.vorNachschuss {
			a.anwesendOK = true
		}
	}

	dayRows := make([]wertungRow, 0, len(order))
	for _, key := range order {
		a := days[key]
		pts := 0.0
		if a.ringWert != nil {
			pts += lookupPunkteAb(w.PunkteRing, *a.ringWert)
		}
		if a.teilerWert != nil {
			pts += lookupPunkteBis(w.PunkteTeiler, *a.teilerWert)
		}
		if a.anwesendOK {
			pts += w.AnwesenheitPunkte
		}
		dayRows = append(dayRows, wertungRow{
			teilnehmerID: a.meta.teilnehmerID,
			startNr:      a.meta.startNr,
			nachname:     a.meta.nachname,
			vorname:      a.meta.vorname,
			verein:       a.meta.verein,
			klasse:       a.meta.klasse,
			scheibeName:  a.meta.schiesstag.Format("02.01.2006"),
			wert:         pts,
		})
	}

	// Punkte: mehr ist immer besser, unabhaengig von einer Ring-/Teiler-
	// Konfiguration (anders als bei computeMeisterPunkt, wo "teiler" ASC
	// sortiert) - daher hier fest absteigend.
	sort.Slice(dayRows, func(i, j int) bool {
		if dayRows[i].teilnehmerID != dayRows[j].teilnehmerID {
			return dayRows[i].teilnehmerID < dayRows[j].teilnehmerID
		}
		return dayRows[i].wert > dayRows[j].wert
	})

	n := math.MaxInt32 // nil = alle zaehlenden Tage zaehlen
	if ps.BesteXAbende != nil {
		n = *ps.BesteXAbende
	}
	return rankByBestNValues(dayRows, n, 0, true), nil
}
