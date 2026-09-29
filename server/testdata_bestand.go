// ============================================================================
// testdata_bestand.go – Entwicklung-Kachel: Teilnehmer & Ergebnisse fuer ein
// Preisschiessen aus BESTEHENDEN Schuetzen erzeugen.
//
// Gegenstueck zu testdata_seed.go (legt eigene Testschuetzen an): hier sind
// Schuetzen und Vereine bereits eingespielt. Je Durchlauf (= ein Schiesstag):
//   - meldet N zufaellige, noch nicht angemeldete Schuetzen aus den fuer das
//     Preisschiessen zugelassenen Vereinen (ps_verein_teilnahme) an,
//   - erzeugt M Ergebnisse (beschossene Scheiben) am gewaehlten Datum: jeder
//     neu Angemeldete schiesst mindestens eine Scheibe, der Rest verteilt sich
//     zufaellig auf ALLE Angemeldeten des Preisschiessens,
//   - ein Ergebnis = genau EINE beschossene Scheibe. Je Ergebnis wird zuerst
//     eine noch offene, generierte Scheibe des Teilnehmers verbraucht (z.B.
//     aus seinem Set), sonst - nur einmal je Teilnehmer - ein Set aus seinem
//     Angebot gekauft (die uebrigen Set-Scheiben bleiben fuer spaetere
//     Schiesstage offen), sonst eine einzelne Scheibe nachgekauft
//     (ListAngebot: Klasse, Hoechstmengen, vorausgesetzte Sets),
//   - "max. pro Tag" je Scheibe gilt fuer den gewaehlten Schiesstag; kann ein
//     Teilnehmer heute nichts mehr schiessen, uebernimmt ein anderer,
//   - Anwesenheits-Scheiben (scoring_mode 'teilnahme') nur auf Wunsch.
//
// Anmeldung, Kauf, Einzahlung und Beschuss tragen das gewaehlte Datum
// (Schiesstag = sessions.finished_at::date).
//
// Markierung: ps_teilnehmer.testdaten / ps_kaeufe.testdaten (migrations/072)
// - aufgeraeumt wird ueber CleanupTestdaten (cleanupBestandTestdaten), die
// Schuetzen selbst bleiben unberuehrt.
// ============================================================================
package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"slices"
	"time"
)

// bestandEinzahlungNotiz markiert die zu einem generierten Kauf gehoerende
// Einzahlungsbuchung (hat keine kauf_id) - fuer cleanupBestandTestdaten.
const bestandEinzahlungNotiz = testdatenMarker + ": Kasseneinzahlung (bestehende Schützen)"

type BestandTestdatenParams struct {
	PreisschiessenID string
	Datum            string // YYYY-MM-DD, Schiesstag
	NeueTeilnehmer   int
	Ergebnisse       int
	MitAnwesenheit   bool
}

type BestandTestdatenResult struct {
	TeilnehmerCreated int      `json:"teilnehmer_created"`
	KandidatenFrei    int      `json:"kandidaten_frei"`  // vor der Anmeldung noch nicht angemeldete Schuetzen der zugelassenen Vereine
	Ergebnisse        int      `json:"ergebnisse"`       // beschossene Scheiben (je Scheibe eins)
	SetsGekauft       int      `json:"sets_gekauft"`     // hoechstens ein Set je Teilnehmer
	ScheibenGekauft   int      `json:"scheiben_gekauft"` // einzeln nachgekaufte Scheiben
	ShotsCreated      int      `json:"shots_created"`
	GesamtUmsatz      float64  `json:"gesamt_umsatz"`
	Warnings          []string `json:"warnings"`
}

func (p BestandTestdatenParams) validate() (time.Time, error) {
	if p.PreisschiessenID == "" {
		return time.Time{}, errBadRequest("Preisschießen fehlt")
	}
	day, err := time.ParseInLocation("2006-01-02", p.Datum, berlinLocation())
	if err != nil {
		return time.Time{}, errBadRequest("ungültiges Datum")
	}
	if p.NeueTeilnehmer < 0 || p.NeueTeilnehmer > 1000 {
		return time.Time{}, errBadRequest("Anzahl neuer Teilnehmer muss zwischen 0 und 1000 liegen")
	}
	if p.Ergebnisse < 0 || p.Ergebnisse > 5000 {
		return time.Time{}, errBadRequest("Anzahl Ergebnisse muss zwischen 0 und 5000 liegen")
	}
	if p.Ergebnisse < p.NeueTeilnehmer {
		return time.Time{}, errBadRequest("Jeder neue Teilnehmer schießt mindestens eine Scheibe - Anzahl Ergebnisse muss mindestens der Anzahl neuer Teilnehmer entsprechen")
	}
	return day, nil
}

func berlinLocation() *time.Location {
	if loc, err := time.LoadLocation("Europe/Berlin"); err == nil {
		return loc
	}
	return time.Local
}

// dayTimes liefert n aufsteigende Zeitpunkte am Schiesstag zwischen 17:00
// und 22:00 (typischer Schiessabend).
func dayTimes(day time.Time, n int) []time.Time {
	out := make([]time.Time, n)
	for i := range out {
		out[i] = day.Add(17*time.Hour + time.Duration(rand.IntN(5*3600))*time.Second)
	}
	slices.SortFunc(out, func(a, b time.Time) int { return a.Compare(b) })
	return out
}

func (s *Store) GenerateBestandTestdaten(ctx context.Context, p BestandTestdatenParams) (BestandTestdatenResult, error) {
	res := BestandTestdatenResult{Warnings: []string{}}
	day, err := p.validate()
	if err != nil {
		return res, err
	}

	var psName string
	var startsOn, endsOn *time.Time
	if err := s.pool.QueryRow(ctx,
		`SELECT name, starts_on, ends_on FROM preisschiessen WHERE id=$1`, p.PreisschiessenID,
	).Scan(&psName, &startsOn, &endsOn); err != nil {
		return res, errBadRequest("Preisschießen nicht gefunden")
	}
	if (startsOn != nil && day.Before(*startsOn)) || (endsOn != nil && day.After(*endsOn)) {
		res.Warnings = append(res.Warnings, "Datum liegt außerhalb des Zeitraums des Preisschießens")
	}

	var nClubs int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM ps_verein_teilnahme WHERE preisschiessen_id=$1`, p.PreisschiessenID,
	).Scan(&nClubs); err != nil {
		return res, err
	}
	if nClubs == 0 {
		return res, errBadRequest("keine Vereine für dieses Preisschießen zugelassen – siehe Preisschießen-Bearbeitung > Vereine")
	}

	// ---- Anmeldung: freie Schuetzen der zugelassenen Vereine ----
	var kandidaten []string
	rows, err := s.pool.Query(ctx, `
		SELECT sh.id FROM shooters sh
		WHERE sh.club_id IN (SELECT club_id FROM ps_verein_teilnahme WHERE preisschiessen_id=$1)
		  AND NOT EXISTS (SELECT 1 FROM ps_teilnehmer pt
		                  WHERE pt.preisschiessen_id=$1 AND pt.shooter_id=sh.id)`, p.PreisschiessenID)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return res, err
		}
		kandidaten = append(kandidaten, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	res.KandidatenFrei = len(kandidaten)
	if len(kandidaten) < p.NeueTeilnehmer {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"nur %d noch nicht angemeldete Schützen in den zugelassenen Vereinen - statt %d werden %d angemeldet",
			len(kandidaten), p.NeueTeilnehmer, len(kandidaten)))
	}
	rand.Shuffle(len(kandidaten), func(i, j int) { kandidaten[i], kandidaten[j] = kandidaten[j], kandidaten[i] })
	if len(kandidaten) > p.NeueTeilnehmer {
		kandidaten = kandidaten[:p.NeueTeilnehmer]
	}

	var neu []string // Teilnehmer-IDs der heute Angemeldeten
	for _, shooterID := range kandidaten {
		t, err := s.CreateTeilnehmer(ctx, p.PreisschiessenID, shooterID)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("Anmeldung fehlgeschlagen: %v", err))
			continue
		}
		// created_at (Anmeldezeit) wird beim ersten Ergebnis auf den
		// Schiesstag gesetzt, kurz vor die erste Scheibe.
		if _, err := s.pool.Exec(ctx,
			`UPDATE ps_teilnehmer SET testdaten=true, created_at=$2 WHERE id=$1`, t.ID, day.Add(17*time.Hour),
		); err != nil {
			return res, err
		}
		neu = append(neu, t.ID)
		res.TeilnehmerCreated++
	}

	// ---- Ergebnisse verteilen: jeder neue mindestens eins, Rest zufaellig ----
	var alle []string
	rows, err = s.pool.Query(ctx,
		`SELECT id FROM ps_teilnehmer WHERE preisschiessen_id=$1`, p.PreisschiessenID)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return res, err
		}
		alle = append(alle, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	if len(alle) == 0 {
		if p.Ergebnisse > 0 {
			res.Warnings = append(res.Warnings, "keine angemeldeten Teilnehmer - keine Ergebnisse erzeugt")
		}
		return res, nil
	}
	// Ergebnis-Plaetze: die ersten gehoeren fest den heute Angemeldeten (jeder
	// mindestens eine Scheibe), die uebrigen gehen an zufaellige Angemeldete.
	slots := make([]string, p.Ergebnisse)
	copy(slots, neu)
	rand.Shuffle(len(slots), func(i, j int) { slots[i], slots[j] = slots[j], slots[i] })
	istNeu := map[string]bool{}
	for _, id := range neu {
		istNeu[id] = true
	}
	angemeldetUm := map[string]bool{}

	lanes, err := s.loadTestdatenLanes(ctx)
	if err != nil {
		return res, err
	}
	if len(lanes) == 0 {
		return res, errBadRequest("kein kalibrierter Stand für Testschüsse verfügbar")
	}
	g := bestandGen{p: p, day: day, psName: psName,
		metaCache: map[string]tdScheibeMeta{}, geomCache: map[string]targetGeom{}, res: &res}
	zeiten := dayTimes(day, len(slots))

	ausgefallen := 0
	for i, fest := range slots {
		at := zeiten[i]
		versuche := 1
		if fest == "" {
			versuche = 25 // anderer Teilnehmer, wenn einer heute nichts mehr schiessen kann
		}
		geschossen := false
		for v := 0; v < versuche && !geschossen; v++ {
			tid := fest
			if tid == "" {
				tid = alle[rand.IntN(len(alle))]
			}
			ok, err := s.shootBestandErgebnis(ctx, &g, tid, lanes[i%len(lanes)], at)
			if err != nil {
				res.Warnings = append(res.Warnings, err.Error())
				break
			}
			if !ok {
				continue
			}
			geschossen = true
			res.Ergebnisse++
			// Anmeldung kurz vor der ersten Scheibe des Tages
			if istNeu[tid] && !angemeldetUm[tid] {
				angemeldetUm[tid] = true
				if _, err := s.pool.Exec(ctx, `UPDATE ps_teilnehmer SET created_at=$2 WHERE id=$1`,
					tid, at.Add(-time.Duration(15+rand.IntN(30))*time.Minute)); err != nil {
					return res, err
				}
			}
		}
		if !geschossen {
			ausgefallen++
			if fest != "" {
				res.Warnings = append(res.Warnings, "ein neu angemeldeter Teilnehmer konnte keine Scheibe schießen (kein passendes Angebot bzw. Tageslimit)")
			}
		}
	}
	if ausgefallen > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"%d von %d Ergebnissen nicht erzeugt - Teilnehmer haben ihr Tageslimit (max. pro Tag) erreicht bzw. kein passendes Angebot", ausgefallen, len(slots)))
	}
	return res, nil
}

// bestandGen: gemeinsamer Zustand eines Generator-Durchlaufs.
type bestandGen struct {
	p         BestandTestdatenParams
	day       time.Time
	psName    string
	metaCache map[string]tdScheibeMeta
	geomCache map[string]targetGeom
	res       *BestandTestdatenResult
}

// bestandEinheit: eine gekaufte, noch nicht beschossene Scheibe.
type bestandEinheit struct {
	id, scheibeID, scoringMode string
}

// shootBestandErgebnis erzeugt fuer einen Teilnehmer genau EIN Ergebnis (eine
// beschossene Scheibe) zum Zeitpunkt at. Reihenfolge wie an der Kasse:
//  1. eine noch offene, generierte Scheibe verbrauchen (z.B. aus dem Set),
//  2. sonst - nur wenn er noch nie ein Set gekauft hat - ein Set kaufen,
//  3. sonst eine einzelne Scheibe nachkaufen.
//
// Beachtet dabei "max. pro Tag" der Scheibe (am Schiesstag beschossene
// Scheiben dieser Art). Offene Scheiben aus echten (nicht generierten)
// Kaeufen werden nicht angetastet. ok=false: heute nichts mehr moeglich.
func (s *Store) shootBestandErgebnis(ctx context.Context, g *bestandGen, teilnehmerID string, lane tdLane, at time.Time) (bool, error) {
	var nr int
	var shooterID string
	if err := s.pool.QueryRow(ctx,
		`SELECT teilnehmer_nr, shooter_id FROM ps_teilnehmer WHERE id=$1`, teilnehmerID,
	).Scan(&nr, &shooterID); err != nil {
		return false, err
	}
	kaufZeit := at.Add(-time.Duration(2+rand.IntN(10)) * time.Minute)

	// 1. offene generierte Scheiben
	einheit, err := s.offeneBestandEinheit(ctx, g, teilnehmerID)
	if err != nil {
		return false, err
	}

	// 2. einmalig ein Set
	if einheit == nil {
		var hatSet bool
		if err := s.pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM ps_kaeufe WHERE teilnehmer_id=$1 AND typ='set' AND returned_at IS NULL)`,
			teilnehmerID).Scan(&hatSet); err != nil {
			return false, err
		}
		if !hatSet {
			_, sets, err := s.ListAngebot(ctx, teilnehmerID)
			if err != nil {
				return false, err
			}
			if len(sets) > 0 {
				item := CartItem{Typ: "set", SetID: sets[rand.IntN(len(sets))].ID}
				if _, err := s.bestandKauf(ctx, g, teilnehmerID, nr, item, kaufZeit); err != nil {
					return false, err
				}
				g.res.SetsGekauft++
				if einheit, err = s.offeneBestandEinheit(ctx, g, teilnehmerID); err != nil {
					return false, err
				}
			}
		}
	}

	// 3. einzelne Scheibe nachkaufen
	if einheit == nil {
		scheiben, _, err := s.ListAngebot(ctx, teilnehmerID)
		if err != nil {
			return false, err
		}
		gekaufteSets := map[string]bool{}
		rows, err := s.pool.Query(ctx, `
			SELECT set_id::text FROM ps_kaeufe
			WHERE teilnehmer_id=$1 AND typ='set' AND returned_at IS NULL`, teilnehmerID)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return false, err
			}
			gekaufteSets[id] = true
		}
		rows.Close()

		var kandidaten []PSScheibe
		for _, sc := range scheiben {
			if sc.ScoringMode == "teilnahme" && !g.p.MitAnwesenheit {
				continue
			}
			ok := len(sc.RequiredSetIDs) == 0
			for _, req := range sc.RequiredSetIDs {
				ok = ok || gekaufteSets[req]
			}
			if !ok {
				continue
			}
			if heute, err := s.bestandHeuteErlaubt(ctx, g, teilnehmerID, sc.ID); err != nil {
				return false, err
			} else if heute {
				kandidaten = append(kandidaten, sc)
			}
		}
		if len(kandidaten) == 0 {
			return false, nil
		}
		sc := kandidaten[rand.IntN(len(kandidaten))]
		item := CartItem{Typ: "scheibe", ScheibeID: sc.ID}
		if sc.ScoringMode == "papier" {
			// Papierscheiben verlangen die (je Preisschiessen eindeutige)
			// Seriennummer der physischen Scheibe - als Testdaten erkennbar.
			item.PhysicalSerial = fmt.Sprintf("TD%08d", rand.IntN(100000000))
		}
		kaufIDs, err := s.bestandKauf(ctx, g, teilnehmerID, nr, item, kaufZeit)
		if err != nil {
			return false, err
		}
		g.res.ScheibenGekauft++
		einheiten, err := s.listKaufScheibenForKaeufe(ctx, kaufIDs)
		if err != nil || len(einheiten) == 0 {
			return false, fmt.Errorf("Teilnehmer %d: gekaufte Scheibe nicht gefunden (%v)", nr, err)
		}
		einheit = &bestandEinheit{id: einheiten[0].id, scheibeID: einheiten[0].scheibeID, scoringMode: sc.ScoringMode}
	}

	// Beschuss
	if einheit.scoringMode == "teilnahme" {
		var disciplineID string
		var matchShots int
		if err := s.pool.QueryRow(ctx, `
			SELECT d.id, d.match_shot_count FROM ps_scheiben sc JOIN disciplines d ON d.id = sc.discipline_id
			WHERE sc.id=$1`, einheit.scheibeID).Scan(&disciplineID, &matchShots); err != nil {
			return false, err
		}
		if err := s.finishTeilnahmeUnit(ctx, einheit.id, disciplineID, teilnehmerID, matchShots); err != nil {
			return false, fmt.Errorf("Teilnehmer %d: Anwesenheit nicht abgeschlossen (%v)", nr, err)
		}
		if _, err := s.pool.Exec(ctx, `
			UPDATE sessions SET started_at=$2::timestamptz - interval '1 minute', finished_at=$2
			WHERE id=(SELECT session_id FROM ps_kauf_scheiben WHERE id=$1)`, einheit.id, at); err != nil {
			return false, err
		}
		if _, err := s.pool.Exec(ctx, `
			UPDATE shots SET fired_at=$2
			WHERE session_id=(SELECT session_id FROM ps_kauf_scheiben WHERE id=$1)`, einheit.id, at); err != nil {
			return false, err
		}
		return true, nil
	}
	meta, ok := g.metaCache[einheit.scheibeID]
	if !ok {
		if meta, err = s.loadScheibeMeta(ctx, einheit.scheibeID, g.geomCache); err != nil {
			return false, err
		}
		g.metaCache[einheit.scheibeID] = meta
	}
	shots, _, err := s.shootTestdatenScheibe(ctx, lane, meta, shooterID, g.psName, einheit.id, at)
	if err != nil {
		return false, fmt.Errorf("Teilnehmer %d: Beschuss fehlgeschlagen (%v)", nr, err)
	}
	g.res.ShotsCreated += shots
	return true, nil
}

// offeneBestandEinheit waehlt zufaellig eine noch nicht beschossene Scheibe
// aus GENERIERTEN Kaeufen des Teilnehmers, die heute noch geschossen werden
// darf (max. pro Tag, Anwesenheit nur mit Option). nil = keine.
func (s *Store) offeneBestandEinheit(ctx context.Context, g *bestandGen, teilnehmerID string) (*bestandEinheit, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ks.id, ks.scheibe_id, d.scoring_mode
		FROM ps_kauf_scheiben ks
		JOIN ps_kaeufe k    ON k.id = ks.kauf_id
		JOIN ps_scheiben sc ON sc.id = ks.scheibe_id
		JOIN disciplines d  ON d.id = sc.discipline_id
		WHERE k.teilnehmer_id=$1 AND k.testdaten AND k.returned_at IS NULL
		  AND ks.session_id IS NULL`, teilnehmerID)
	if err != nil {
		return nil, err
	}
	var alle []bestandEinheit
	for rows.Next() {
		var e bestandEinheit
		if err := rows.Scan(&e.id, &e.scheibeID, &e.scoringMode); err != nil {
			rows.Close()
			return nil, err
		}
		if e.scoringMode == "teilnahme" && !g.p.MitAnwesenheit {
			continue
		}
		alle = append(alle, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rand.Shuffle(len(alle), func(i, j int) { alle[i], alle[j] = alle[j], alle[i] })
	for _, e := range alle {
		ok, err := s.bestandHeuteErlaubt(ctx, g, teilnehmerID, e.scheibeID)
		if err != nil {
			return nil, err
		}
		if ok {
			return &e, nil
		}
	}
	return nil, nil
}

// bestandHeuteErlaubt: darf der Teilnehmer am Schiesstag noch eine Scheibe
// dieser Art schiessen? (ps_scheiben.max_pro_tag, gezaehlt werden am
// Schiesstag beschossene Scheiben - Schiesstag wie ueberall
// COALESCE(schiesstag_override, finished_at::date)).
func (s *Store) bestandHeuteErlaubt(ctx context.Context, g *bestandGen, teilnehmerID, scheibeID string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT sc.max_pro_tag IS NULL OR (
		         SELECT COUNT(*) FROM ps_kauf_scheiben ks
		         JOIN ps_kaeufe k ON k.id = ks.kauf_id
		         JOIN sessions se ON se.id = ks.session_id
		         WHERE k.teilnehmer_id=$1 AND ks.scheibe_id=sc.id AND k.returned_at IS NULL
		           AND COALESCE(ks.schiesstag_override, se.finished_at::date) = $3::date
		       ) < sc.max_pro_tag
		FROM ps_scheiben sc WHERE sc.id=$2`, teilnehmerID, scheibeID, g.day.Format("2006-01-02")).Scan(&ok)
	return ok, err
}

// bestandKauf kauft item (inkl. Einzahlung, siehe purchaseTestdatenItems),
// markiert den Kauf als Testdaten und datiert Kauf, Einheiten und Buchungen
// auf kaufZeit zurueck.
func (s *Store) bestandKauf(ctx context.Context, g *bestandGen, teilnehmerID string, nr int, item CartItem, kaufZeit time.Time) ([]string, error) {
	// DB-Zeit vor dem Kauf: alle danach angelegten Buchungen dieses
	// Teilnehmers (Kauf + Einzahlung) gehoeren zu diesem Kauf.
	var vorKauf time.Time
	if err := s.pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&vorKauf); err != nil {
		return nil, err
	}
	kaufIDs, total, err := s.purchaseTestdatenItems(ctx, teilnehmerID, g.p.PreisschiessenID, []CartItem{item})
	if err != nil {
		return nil, fmt.Errorf("Teilnehmer %d: Einkauf fehlgeschlagen (%v)", nr, err)
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE ps_kaeufe SET testdaten=true, purchased_at=$2 WHERE id = ANY($1::uuid[])`, kaufIDs, kaufZeit); err != nil {
		return nil, err
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE ps_kauf_scheiben SET created_at=$2 WHERE kauf_id = ANY($1::uuid[])`, kaufIDs, kaufZeit); err != nil {
		return nil, err
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE ps_guthaben_buchungen
		SET created_at=$3,
		    notiz = CASE WHEN typ='aufladung' THEN $4 ELSE notiz END
		WHERE teilnehmer_id=$1 AND created_at >= $2`,
		teilnehmerID, vorKauf, kaufZeit, bestandEinzahlungNotiz); err != nil {
		return nil, err
	}
	g.res.GesamtUmsatz += total
	return kaufIDs, nil
}

// cleanupBestandTestdaten entfernt alle als Testdaten markierten
// Anmeldungen und Kaeufe (ps_teilnehmer.testdaten / ps_kaeufe.testdaten)
// samt Sessions und Schuessen - die Schuetzen bleiben erhalten. Schuesse
// duerfen laut Schema nicht geloescht werden (trg_shots_no_delete), der
// Trigger wird dafuer wie in CleanupTestdaten kurz deaktiviert.
func (s *Store) cleanupBestandTestdaten(ctx context.Context) (TestdatenCleanupResult, error) {
	var res TestdatenCleanupResult
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx)

	// Sessions aller markierten Kaeufe UND aller Kaeufe markierter Teilnehmer
	const sessionsSQL = `
		SELECT ks.session_id FROM ps_kauf_scheiben ks
		JOIN ps_kaeufe k ON k.id = ks.kauf_id
		JOIN ps_teilnehmer pt ON pt.id = k.teilnehmer_id
		WHERE ks.session_id IS NOT NULL AND (k.testdaten OR pt.testdaten)`
	var sessionIDs []string
	rows, err := tx.Query(ctx, sessionsSQL)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return res, err
		}
		sessionIDs = append(sessionIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}

	if _, err := tx.Exec(ctx, `ALTER TABLE shots DISABLE TRIGGER trg_shots_no_delete`); err != nil {
		return res, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM shots WHERE session_id = ANY($1::uuid[])`, sessionIDs)
	if err != nil {
		return res, err
	}
	res.ShotsDeleted = int(tag.RowsAffected())
	if _, err := tx.Exec(ctx, `ALTER TABLE shots ENABLE TRIGGER trg_shots_no_delete`); err != nil {
		return res, err
	}

	// Kaeufe/Anmeldungen vor den Sessions loeschen (ps_kauf_scheiben.session_id
	// verweist ohne CASCADE auf sessions). Bei markierten Teilnehmern haengt
	// alles per CASCADE dran; bei echten Teilnehmern mit generierten Kaeufen
	// zuerst deren Buchungen (kauf_id ohne CASCADE) und die zugehoerige
	// Einzahlung (Marker-Notiz), dann die Kaeufe (Kauf-Scheiben per CASCADE).
	if _, err := tx.Exec(ctx, `
		DELETE FROM ps_guthaben_buchungen b
		USING ps_teilnehmer pt
		WHERE pt.id = b.teilnehmer_id AND NOT pt.testdaten
		  AND (b.notiz = $1
		       OR b.kauf_id IN (SELECT id FROM ps_kaeufe WHERE testdaten))`,
		bestandEinzahlungNotiz); err != nil {
		return res, err
	}
	tag, err = tx.Exec(ctx, `
		DELETE FROM ps_kaeufe k USING ps_teilnehmer pt
		WHERE pt.id = k.teilnehmer_id AND k.testdaten AND NOT pt.testdaten`)
	if err != nil {
		return res, err
	}
	res.KaeufeDeleted = int(tag.RowsAffected())
	tag, err = tx.Exec(ctx, `DELETE FROM ps_teilnehmer WHERE testdaten`)
	if err != nil {
		return res, err
	}
	res.TeilnehmerDeleted = int(tag.RowsAffected())
	tag, err = tx.Exec(ctx, `DELETE FROM sessions WHERE id = ANY($1::uuid[])`, sessionIDs)
	if err != nil {
		return res, err
	}
	res.SessionsDeleted = int(tag.RowsAffected())
	return res, tx.Commit(ctx)
}

func (a *APIServer) generateBestandTestdatenHandler(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		PreisschiessenID string `json:"preisschiessen_id"`
		Datum            string `json:"datum"`
		NeueTeilnehmer   int    `json:"neue_teilnehmer"`
		Ergebnisse       int    `json:"ergebnisse"`
		MitAnwesenheit   bool   `json:"mit_anwesenheit"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungültiger Body: " + err.Error())
	}
	return a.store.GenerateBestandTestdaten(r.Context(), BestandTestdatenParams{
		PreisschiessenID: body.PreisschiessenID,
		Datum:            body.Datum,
		NeueTeilnehmer:   body.NeueTeilnehmer,
		Ergebnisse:       body.Ergebnisse,
		MitAnwesenheit:   body.MitAnwesenheit,
	})
}
