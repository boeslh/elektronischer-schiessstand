// ============================================================================
// manual_result.go – manuelle Ergebniseingabe (Einzelschuss/Serie/Gesamt) und
// Disag-Wertmaschinen-Ergebnisse - gemeinsamer Schreibpfad fuer beide Quellen,
// siehe Konzept .claude/plans/wise-scribbling-abelson.md Abschnitt 3.
//
// AssignVirtualLane erzeugt die Session (auf einer virtuellen Bahn, siehe
// migrations/057), RecordExternalResult schreibt die shots-Zeilen hinein -
// je nach Granularitaet als Einzelschuss (aggregate_count=1), Serie
// (aggregate_count=shots_per_series bzw. tatsaechliche Anzahl) oder
// Gesamtwert (aggregate_count=match_shot_count).
//
// Bei Wertmaschinen-Einzelschuessen wird die Trefferposition aus Teiler+
// Winkel berechnet - siehe positionFromTeilerWinkel. Beide Protokolle liefern
// das (RM IV direkt, RM III rechnet aus seiner kartesischen X/Y-Abweichung
// selbst den einheitenunabhaengigen Winkel und meldet den Teilerwert aus dem
// eigenen Feld der Ergebniszeile, siehe wertmaschine/disag/rmiii.go). Das
// geschieht bewusst HIER zentral im Server (nicht im wertmaschine-Dienst),
// damit die Umrechnung an genau einer Stelle liegt und nach der
// Hardware-Verifikation (siehe Konzept, Abschnitt 7) an nur einer Stelle
// korrigiert werden muss.
// ============================================================================
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
)

var ErrResultExists = errors.New("es liegt bereits ein Ergebnis vor")

// ManualShotInput: eine Eingabezeile fuer die manuelle/Wertmaschinen-
// Ergebniserfassung. Count ist die Anzahl ECHTER Schuesse, die diese Zeile
// repraesentiert (1 bei Granularitaet "shot", shots_per_series bzw. die
// tatsaechliche Anzahl bei "series", match_shot_count bei "total").
type ManualShotInput struct {
	Ring     *int     `json:"ring"`
	Decimal  *float64 `json:"decimal"`
	Teiler   *float64 `json:"teiler"` // roh vom Geraet/Bediener - siehe Konzept: gleiche Einheit wie shots.center_distance (1/100mm), keine weitere Umrechnung
	Winkel   *float64 `json:"winkel"` // Grad, 0=oben/90=rechts - nur zusammen mit Teiler nutzbar
	Count    int      `json:"count"`
	SeriesNo *int     `json:"series_no"`
}

// positionFromTeilerWinkel rechnet Teiler (roh, 1/100mm - siehe
// centerDistanceHundredthMM) + Winkel (Grad, 0=oben/90=rechts) in eine
// Trefferposition in mm um (Polarkoordinate).
//
// KORREKTUR (mit echter Hardware/Nutzer-Rueckmeldung bestaetigt): der vom
// Geraet gemeldete Teilerwert entspricht direkt dem im Verein ueblichen
// "Teiler" (= Abstand vom Zentrum in 1/100mm, siehe centerDistanceHundredthMM)
// - urspruenglich wurde hier faelschlich von 1/10mm ausgegangen und
// zusaetzlich mit 10 multipliziert, wodurch der auf der Papierscheibe
// aufgedruckte und der in der Anzeige gezeigte Teilerwert um den Faktor 10
// auseinanderliefen.
func positionFromTeilerWinkel(teilerRaw, winkelDeg float64) (xMM, yMM float64) {
	radiusMM := teilerRaw / 100
	rad := winkelDeg * math.Pi / 180
	xMM = radiusMM * math.Sin(rad)
	yMM = radiusMM * math.Cos(rad)
	return
}

// centerDistanceHundredthMM gibt einen rohen Teiler-Wert unveraendert als
// shots.center_distance zurueck - der Teilerwert der Wertmaschine ist bereits
// in der im restlichen Schema etablierten Einheit (1/100mm, siehe
// standpc/main.go DisciplineDef.CenterDistance-Kommentar "Abstand Mitte in
// 1/100 mm" bzw. standpc/score.go ScoreResult, wo genau diese Groesse im
// Kommentar/Test als "Teiler" bezeichnet wird). Eigene Funktion nur zur
// Dokumentation der Konvention an einer Stelle, nicht mehr fuer eine
// tatsaechliche Umrechnung (siehe Korrektur bei positionFromTeilerWinkel).
func centerDistanceHundredthMM(teilerRaw float64) float64 {
	return teilerRaw
}

// AssignVirtualLane legt eine Session auf einer virtuellen Bahn an (siehe
// migrations/057) - gemeinsamer Einstiegspunkt fuer manuelle und
// Wertmaschinen-Ergebniserfassung, die nie an einer echten Bahn stattfindet.
func (s *Store) AssignVirtualLane(ctx context.Context, disciplineID, eventID, shooterID string) (string, error) {
	var laneNo int
	if err := s.pool.QueryRow(ctx,
		`SELECT lane_no FROM lanes WHERE virtual AND active ORDER BY lane_no LIMIT 1`,
	).Scan(&laneNo); err != nil {
		return "", fmt.Errorf("keine virtuelle Bahn verfuegbar: %w", err)
	}
	return s.AssignLane(ctx, laneNo, shooterID, disciplineID, eventID)
}

// RecordExternalResult schreibt shots-Zeilen in eine bereits bestehende
// Session (siehe AssignVirtualLane) - Aufrufer loesen die Zuordnung
// (Rundenwettkampf-Starter bzw. Preisschiessen-Scheibeneinheit) und die
// Ueberschreiben-Bestaetigung (ErrResultExists) VOR diesem Aufruf auf, siehe
// RecordRundenwettkampfResult/RecordPreisschiessenResult.
func (s *Store) RecordExternalResult(ctx context.Context, sessionID, granularity string,
	shots []ManualShotInput, source, actor string) error {

	if source != "manual" && source != "wertmaschine" {
		return errBadRequest("ungueltige Quelle")
	}
	if len(shots) == 0 {
		return errBadRequest("keine Schuesse angegeben")
	}
	if granularity == "total" && len(shots) != 1 {
		return errBadRequest(`Granularitaet "total" erwartet genau einen Eintrag`)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	shotNo := 1
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(shot_no),0)+1 FROM shots WHERE session_id=$1`, sessionID,
	).Scan(&shotNo); err != nil {
		return err
	}

	for _, in := range shots {
		count := in.Count
		if count < 1 {
			count = 1
		}
		var xMM, yMM *float64
		var centerDist *float64
		switch {
		case in.Teiler != nil && in.Winkel != nil:
			x, y := positionFromTeilerWinkel(*in.Teiler, *in.Winkel)
			xMM, yMM = &x, &y
			cd := centerDistanceHundredthMM(*in.Teiler)
			centerDist = &cd
		case in.Teiler != nil:
			cd := centerDistanceHundredthMM(*in.Teiler)
			centerDist = &cd
		}
		// Sammelzeilen (count>1: Serie/Gesamt) haben keine sinnvolle
		// Einzelposition - x/y bleiben in diesem Fall NULL, unabhaengig davon
		// ob der Aufrufer versehentlich Teiler/Winkel mitgeschickt hat.
		if count > 1 {
			xMM, yMM = nil, nil
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO shots (
				session_id, shot_no, kind, status, series_no,
				sensor_hits, entry_source, aggregate_count,
				x_mm, y_mm, ring, decimal_value, center_distance
			) VALUES (
				$1::uuid, $2::smallint, 'match', 'valid', $3::smallint,
				0, $4::text, $5::smallint,
				$6::numeric, $7::numeric, $8::smallint, $9::numeric, $10::numeric
			)`,
			sessionID, shotNo, in.SeriesNo,
			source, count,
			xMM, yMM, in.Ring, in.Decimal, centerDist,
		); err != nil {
			return fmt.Errorf("Schuss %d: %w", shotNo, err)
		}
		shotNo++
	}

	action := "manual_score_entry"
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (action, session_id, actor, details)
		VALUES ($1::action_type, $2::uuid, $3::text,
		        jsonb_build_object('source',$4::text,'granularity',$5::text,'count',$6::int))`,
		action, sessionID, actor, source, granularity, len(shots),
	); err != nil {
		return err
	}

	// Eine manuelle/Wertmaschinen-Erfassung ist ein einmaliger, atomarer
	// Vorgang (kein laufender Stand) - die Session wird sofort abgeschlossen,
	// damit die virtuelle Bahn (migrations/057) fuer die naechste Erfassung
	// nicht dauerhaft als "belegt" haengen bleibt (assignLane-Busy-Check).
	if _, err := tx.Exec(ctx, `
		UPDATE sessions SET status='finished',
		  started_at = COALESCE(started_at, now()), finished_at = now()
		WHERE id=$1`, sessionID,
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// hasExistingResult prueft, ob fuer eine Session bereits (Wertungs-)Schuesse
// vorliegen - Grundlage fuer die Ueberschreiben-Bestaetigung (ErrResultExists).
func (s *Store) hasExistingResult(ctx context.Context, sessionID string) (bool, error) {
	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM shots WHERE session_id=$1`, sessionID,
	).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// abortExistingResult setzt eine bereits vorliegende Session auf 'aborted'
// (bleibt zur Nachvollziehbarkeit erhalten, siehe Konzept Abschnitt 3.2) und
// gibt via ihre Existenz-Pruefung an den Aufrufer zurueck, ob ueberhaupt eine
// bestehende Session mit Schuessen gefunden wurde.
func (s *Store) checkOverwrite(ctx context.Context, existingSessionID string, confirmOverwrite bool) error {
	if existingSessionID == "" {
		return nil
	}
	has, err := s.hasExistingResult(ctx, existingSessionID)
	if err != nil {
		return err
	}
	if !has {
		return nil
	}
	if !confirmOverwrite {
		return ErrResultExists
	}
	return s.SetSessionStatus(ctx, existingSessionID, "aborted")
}

// abortOnFailure raeumt eine frisch ueber AssignVirtualLane angelegte Session
// wieder auf, wenn das anschliessende RecordExternalResult fehlschlaegt -
// sonst bliebe die virtuelle Bahn dauerhaft "belegt" (assignLane-Busy-Check),
// da AssignLane seine eigene, bereits committete Transaktion hat und ein
// Fehler in der SEPARATEN RecordExternalResult-Transaktion diese nicht
// automatisch zurueckrollt. Best-effort: ein Fehler hier wird nur geloggt,
// der urspruengliche Fehler bleibt fuer den Aufrufer massgeblich.
func (s *Store) abortOnFailure(ctx context.Context, sessionID string) {
	if err := s.SetSessionStatus(ctx, sessionID, "aborted"); err != nil {
		log.Printf("abortOnFailure: Session %s konnte nicht aufgeraeumt werden: %v", sessionID, err)
	}
}

// RecordRundenwettkampfResult erfasst ein manuelles oder Wertmaschinen-
// Ergebnis fuer einen Rundenwettkampf-Starter - Verknuepfung ueber
// event_id+shooter_id, genau wie ListRundenwettkampfResults es bereits liest
// (sessions.starter_id wird nirgends befuellt, siehe store.go-Kommentare
// dort - kein zweiter, inkonsistenter Verknuepfungsweg).
func (s *Store) RecordRundenwettkampfResult(ctx context.Context, starterID, granularity string,
	shots []ManualShotInput, source, actor string, confirmOverwrite bool) (string, error) {

	var eventID, shooterID, disciplineID string
	if err := s.pool.QueryRow(ctx,
		`SELECT event_id, shooter_id, discipline_id FROM starters WHERE id=$1`, starterID,
	).Scan(&eventID, &shooterID, &disciplineID); err != nil {
		return "", fmt.Errorf("Starter: %w", err)
	}

	var existingSessionID string
	err := s.pool.QueryRow(ctx, `
		SELECT id FROM sessions
		WHERE event_id=$1 AND shooter_id=$2 AND status <> 'aborted'
		ORDER BY started_at DESC NULLS LAST LIMIT 1`, eventID, shooterID,
	).Scan(&existingSessionID)
	if err != nil {
		existingSessionID = ""
	}
	if err := s.checkOverwrite(ctx, existingSessionID, confirmOverwrite); err != nil {
		return "", err
	}

	sessionID, err := s.AssignVirtualLane(ctx, disciplineID, eventID, shooterID)
	if err != nil {
		return "", err
	}
	if err := s.RecordExternalResult(ctx, sessionID, granularity, shots, source, actor); err != nil {
		s.abortOnFailure(ctx, sessionID)
		return "", err
	}
	return sessionID, nil
}

// RecordPreisschiessenResult erfasst ein manuelles oder Wertmaschinen-
// Ergebnis fuer eine gekaufte Papierscheiben-Einheit, identifiziert ueber
// ps_kauf_scheiben.id (Preisschiessen-Pfad ueber die physische Seriennummer
// findet die Einheit vorher, siehe api.go handlePreisschiessenWertmaschine).
func (s *Store) RecordPreisschiessenResult(ctx context.Context, kaufScheibeID, granularity string,
	shots []ManualShotInput, source, actor string, confirmOverwrite bool) (string, error) {

	var existingSessionID *string
	var teilnehmerID, scheibeID string
	if err := s.pool.QueryRow(ctx, `
		SELECT session_id, (SELECT teilnehmer_id FROM ps_kaeufe WHERE id=kauf_id), scheibe_id
		FROM ps_kauf_scheiben WHERE id=$1`, kaufScheibeID,
	).Scan(&existingSessionID, &teilnehmerID, &scheibeID); err != nil {
		return "", fmt.Errorf("Scheiben-Einheit: %w", err)
	}
	var disciplineID, shooterID string
	if err := s.pool.QueryRow(ctx,
		`SELECT discipline_id FROM ps_scheiben WHERE id=$1`, scheibeID,
	).Scan(&disciplineID); err != nil {
		return "", fmt.Errorf("Scheibenart: %w", err)
	}
	if err := s.pool.QueryRow(ctx,
		`SELECT shooter_id FROM ps_teilnehmer WHERE id=$1`, teilnehmerID,
	).Scan(&shooterID); err != nil {
		return "", fmt.Errorf("Teilnehmer: %w", err)
	}

	existing := ""
	if existingSessionID != nil {
		existing = *existingSessionID
	}
	if err := s.checkOverwrite(ctx, existing, confirmOverwrite); err != nil {
		return "", err
	}

	sessionID, err := s.AssignVirtualLane(ctx, disciplineID, "", shooterID)
	if err != nil {
		return "", err
	}
	if err := s.RecordExternalResult(ctx, sessionID, granularity, shots, source, actor); err != nil {
		s.abortOnFailure(ctx, sessionID)
		return "", err
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE ps_kauf_scheiben SET session_id=$1 WHERE id=$2`, sessionID, kaufScheibeID,
	); err != nil {
		return "", err
	}
	return sessionID, nil
}

// FindKaufScheibeByPhysicalSerial loest die neue manuell eingetippte
// Seriennummer der physischen Papierscheibe (migrations/056) auf die
// zugehoerige ps_kauf_scheiben-Einheit auf.
func (s *Store) FindKaufScheibeByPhysicalSerial(ctx context.Context, preisschiessenID, physicalSerialNo string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		SELECT id FROM ps_kauf_scheiben
		WHERE preisschiessen_id=$1 AND physical_serial_no=$2`, preisschiessenID, physicalSerialNo,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("Scheibe mit Seriennummer %q nicht gefunden: %w", physicalSerialNo, err)
	}
	return id, nil
}

// ScheibeLookupResult: Anzeige-Info zu einer per physischer Seriennummer
// gefundenen Papierscheibe - fuer die Live-Anzeige neben dem
// Seriennummer-Eingabefeld in der Wertmaschinen-Bedienoberflaeche (Bediener
// soll bestaetigen koennen, dass er die richtige Scheibe/den richtigen
// Schuetzen vor sich hat, bevor er einliest).
type ScheibeLookupResult struct {
	ScheibeName string `json:"scheibe_name"`
	ShooterName string `json:"shooter_name"`
}

// LookupScheibeByPhysicalSerial wie FindKaufScheibeByPhysicalSerial, liefert
// aber zusaetzlich Anzeige-Info statt nur der internen ID.
func (s *Store) LookupScheibeByPhysicalSerial(ctx context.Context, preisschiessenID, physicalSerialNo string) (ScheibeLookupResult, error) {
	var res ScheibeLookupResult
	var lastName, firstName string
	err := s.pool.QueryRow(ctx, `
		SELECT sc.name, sh.last_name, sh.first_name
		FROM ps_kauf_scheiben ks
		JOIN ps_scheiben sc ON sc.id = ks.scheibe_id
		JOIN ps_kaeufe k ON k.id = ks.kauf_id
		JOIN ps_teilnehmer t ON t.id = k.teilnehmer_id
		JOIN shooters sh ON sh.id = t.shooter_id
		WHERE ks.preisschiessen_id=$1 AND ks.physical_serial_no=$2`,
		preisschiessenID, physicalSerialNo,
	).Scan(&res.ScheibeName, &lastName, &firstName)
	if err != nil {
		return res, fmt.Errorf("Scheibe mit Seriennummer %q nicht gefunden: %w", physicalSerialNo, err)
	}
	res.ShooterName = firstName + " " + lastName
	return res, nil
}

// DisciplineIDForStarter loest die Disziplin eines Rundenwettkampf-Starters
// auf - fuer den wertmaschine-Dienst (siehe api.go wertmaschineConfig), der
// keinen eigenen Datenbankzugriff hat.
func (s *Store) DisciplineIDForStarter(ctx context.Context, starterID string) (string, error) {
	var disciplineID string
	if err := s.pool.QueryRow(ctx,
		`SELECT discipline_id FROM starters WHERE id=$1`, starterID,
	).Scan(&disciplineID); err != nil {
		return "", fmt.Errorf("Starter: %w", err)
	}
	return disciplineID, nil
}

// DisciplineIDForKaufScheibe loest die Disziplin einer gekauften
// Scheiben-Einheit auf - fuer den wertmaschine-Dienst (siehe api.go
// wertmaschineConfig).
func (s *Store) DisciplineIDForKaufScheibe(ctx context.Context, kaufScheibeID string) (string, error) {
	var disciplineID string
	if err := s.pool.QueryRow(ctx, `
		SELECT sc.discipline_id FROM ps_kauf_scheiben ks JOIN ps_scheiben sc ON sc.id = ks.scheibe_id
		WHERE ks.id=$1`, kaufScheibeID,
	).Scan(&disciplineID); err != nil {
		return "", fmt.Errorf("Scheiben-Einheit: %w", err)
	}
	return disciplineID, nil
}
