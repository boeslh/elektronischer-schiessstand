package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const archiveBundleVersion = 1

// ArchiveBundle ist das exportierte JSON-Archiv.
type ArchiveBundle struct {
	Version                 int             `json:"version"`
	ExportedAt              string          `json:"exported_at"`
	Events                  json.RawMessage `json:"events"`
	Teams                   json.RawMessage `json:"teams"`
	Starters                json.RawMessage `json:"starters"`
	CompetitionParticipants json.RawMessage `json:"competition_participants"`
	Sessions                json.RawMessage `json:"sessions"`
	Shots                   json.RawMessage `json:"shots"`
	SavedAuswertungen       json.RawMessage `json:"saved_auswertungen"`
	Shooters                json.RawMessage `json:"shooters,omitempty"`
	Clubs                   json.RawMessage `json:"clubs,omitempty"`
}

// ImportResult gibt zurück, wie viele Zeilen je Tabelle importiert wurden.
type ImportResult struct {
	Events                  int64    `json:"events"`
	Teams                   int64    `json:"teams"`
	Starters                int64    `json:"starters"`
	CompetitionParticipants int64    `json:"competition_participants"`
	Sessions                int64    `json:"sessions"`
	Shots                   int64    `json:"shots"`
	SavedAuswertungen       int64    `json:"saved_auswertungen"`
	Shooters                int64    `json:"shooters"`
	Clubs                   int64    `json:"clubs"`
	Warnings                []string `json:"warnings,omitempty"`
}

// tableToJSON liest eine Abfrage und gibt das Ergebnis als JSON-Array zurück.
func tableToJSON(ctx context.Context, db *pgxpool.Pool, query string, args ...any) (json.RawMessage, error) {
	var raw json.RawMessage
	err := db.QueryRow(ctx,
		`SELECT COALESCE(json_agg(row_to_json(t)), '[]'::json) FROM (`+query+`) t`,
		args...).Scan(&raw)
	return raw, err
}

func tableToJSONTx(ctx context.Context, tx pgx.Tx, query string, args ...any) (json.RawMessage, error) {
	var raw json.RawMessage
	err := tx.QueryRow(ctx,
		`SELECT COALESCE(json_agg(row_to_json(t)), '[]'::json) FROM (`+query+`) t`,
		args...).Scan(&raw)
	return raw, err
}

// ExportArchivedEvents exportiert die angegebenen Events als Bundle.
// includeOrphans: Schützen/Vereine, die danach in keinen anderen Wettkämpfen mehr vorkommen, ebenfalls exportieren.
func (s *Store) ExportArchivedEvents(ctx context.Context, eventIDs []string, includeOrphans bool) (*ArchiveBundle, error) {
	bundle := &ArchiveBundle{
		Version:    archiveBundleVersion,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
	}
	var err error

	q := func(query string) (json.RawMessage, error) {
		return tableToJSON(ctx, s.pool, query, eventIDs)
	}

	bundle.Events, err = q(`SELECT * FROM events WHERE id = ANY($1::uuid[])`)
	if err != nil {
		return nil, fmt.Errorf("events: %w", err)
	}
	bundle.Teams, err = q(`SELECT * FROM teams WHERE event_id = ANY($1::uuid[])`)
	if err != nil {
		return nil, fmt.Errorf("teams: %w", err)
	}
	bundle.Starters, err = q(`SELECT * FROM starters WHERE event_id = ANY($1::uuid[])`)
	if err != nil {
		return nil, fmt.Errorf("starters: %w", err)
	}
	bundle.CompetitionParticipants, err = q(`SELECT * FROM competition_participants WHERE event_id = ANY($1::uuid[])`)
	if err != nil {
		return nil, fmt.Errorf("competition_participants: %w", err)
	}
	bundle.Sessions, err = q(`SELECT * FROM sessions WHERE event_id = ANY($1::uuid[])`)
	if err != nil {
		return nil, fmt.Errorf("sessions: %w", err)
	}
	bundle.Shots, err = q(`SELECT sh.* FROM shots sh WHERE sh.session_id IN (SELECT id FROM sessions WHERE event_id = ANY($1::uuid[]))`)
	if err != nil {
		return nil, fmt.Errorf("shots: %w", err)
	}
	bundle.SavedAuswertungen, err = q(`SELECT * FROM saved_auswertungen WHERE event_id = ANY($1::uuid[])`)
	if err != nil {
		return nil, fmt.Errorf("saved_auswertungen: %w", err)
	}

	if includeOrphans {
		// Schützen, die ausschließlich in diesen Events als Starter/Session vorkommen
		bundle.Shooters, err = tableToJSON(ctx, s.pool, `
			SELECT * FROM shooters
			WHERE id IN (SELECT DISTINCT shooter_id FROM starters WHERE event_id = ANY($1::uuid[]))
			  AND id NOT IN (SELECT shooter_id FROM starters WHERE event_id != ALL($1::uuid[]))
			  AND id NOT IN (
			      SELECT shooter_id FROM sessions
			      WHERE shooter_id IS NOT NULL
			        AND (event_id IS NULL OR event_id != ALL($1::uuid[]))
			  )`, eventIDs)
		if err != nil {
			return nil, fmt.Errorf("shooters: %w", err)
		}

		// Vereine, bei denen ALLE Mitglieder nach der Löschung verwaist wären
		bundle.Clubs, err = tableToJSON(ctx, s.pool, `
			SELECT cl.* FROM clubs cl
			WHERE cl.id NOT IN (
			    -- Vereine mit mindestens einem "überlebenden" Schützen
			    SELECT DISTINCT sh.club_id FROM shooters sh
			    WHERE sh.club_id IS NOT NULL
			      AND (
			          sh.id IN (SELECT shooter_id FROM starters WHERE event_id != ALL($1::uuid[]))
			          OR sh.id IN (SELECT shooter_id FROM sessions WHERE shooter_id IS NOT NULL
			                       AND (event_id IS NULL OR event_id != ALL($1::uuid[])))
			      )
			)
			AND cl.id NOT IN (
			    SELECT DISTINCT club_id FROM competition_participants
			    WHERE club_id IS NOT NULL AND event_id != ALL($1::uuid[])
			)
			AND cl.id NOT IN (
			    SELECT DISTINCT club_id FROM teams
			    WHERE club_id IS NOT NULL AND (event_id IS NULL OR event_id != ALL($1::uuid[]))
			)
			AND cl.id IN (
			    -- nur Vereine, die überhaupt in diesen Events vorkommen
			    SELECT DISTINCT sh.club_id FROM shooters sh
			    JOIN starters st ON st.shooter_id = sh.id
			    WHERE st.event_id = ANY($1::uuid[]) AND sh.club_id IS NOT NULL
			)`, eventIDs)
		if err != nil {
			return nil, fmt.Errorf("clubs: %w", err)
		}
	}

	return bundle, nil
}

// DeleteArchivedEvents löscht die Events samt abhängiger Daten.
// purgeOrphans: danach verwaiste Schützen/Vereine ebenfalls löschen.
func (s *Store) DeleteArchivedEvents(ctx context.Context, eventIDs []string, purgeOrphans bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	steps := []struct {
		label string
		sql   string
	}{
		{"shots", `DELETE FROM shots WHERE session_id IN (SELECT id FROM sessions WHERE event_id = ANY($1::uuid[]))`},
		{"sessions", `DELETE FROM sessions WHERE event_id = ANY($1::uuid[])`},
		{"starters", `DELETE FROM starters WHERE event_id = ANY($1::uuid[])`},
		{"teams", `DELETE FROM teams WHERE event_id = ANY($1::uuid[])`},
		// competition_participants und saved_auswertungen werden durch CASCADE gelöscht
		{"events", `DELETE FROM events WHERE id = ANY($1::uuid[])`},
	}
	for _, step := range steps {
		if _, err = tx.Exec(ctx, step.sql, eventIDs); err != nil {
			return fmt.Errorf("delete %s: %w", step.label, err)
		}
	}

	if purgeOrphans {
		// Schützen ohne verbleibende Starts oder Sessions
		if _, err = tx.Exec(ctx, `
			DELETE FROM shooters
			WHERE id NOT IN (SELECT DISTINCT shooter_id FROM starters)
			  AND id NOT IN (SELECT DISTINCT shooter_id FROM sessions WHERE shooter_id IS NOT NULL)`); err != nil {
			return fmt.Errorf("delete orphan shooters: %w", err)
		}
		// Vereine ohne verbleibende Schützen, Teilnehmer oder Teams
		if _, err = tx.Exec(ctx, `
			DELETE FROM clubs
			WHERE id NOT IN (SELECT DISTINCT club_id FROM shooters WHERE club_id IS NOT NULL)
			  AND id NOT IN (SELECT DISTINCT club_id FROM competition_participants WHERE club_id IS NOT NULL)
			  AND id NOT IN (SELECT DISTINCT club_id FROM teams WHERE club_id IS NOT NULL)`); err != nil {
			return fmt.Errorf("delete orphan clubs: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// ImportArchiveBundle liest ein exportiertes Bundle in die Datenbank.
// Bereits vorhandene Zeilen (gleiche ID) werden übersprungen.
func (s *Store) ImportArchiveBundle(ctx context.Context, bundle *ArchiveBundle) (*ImportResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	result := &ImportResult{}

	// Hilfsfunktion: INSERT via json_populate_recordset, gibt Anzahl eingefügter Zeilen zurück.
	ins := func(table string, data json.RawMessage, extraWhere string) (int64, error) {
		if len(data) == 0 || string(data) == "[]" || string(data) == "null" {
			return 0, nil
		}
		where := ""
		if extraWhere != "" {
			where = "WHERE " + extraWhere
		}
		sql := fmt.Sprintf(`
			WITH ins AS (
				INSERT INTO %s
				SELECT * FROM json_populate_recordset(null::%s, $1::json) r
				%s
				ON CONFLICT (id) DO NOTHING
				RETURNING id
			) SELECT COUNT(*) FROM ins`, table, table, where)
		var count int64
		if err := tx.QueryRow(ctx, sql, data).Scan(&count); err != nil {
			return 0, err
		}
		return count, nil
	}

	warn := func(msg string) { result.Warnings = append(result.Warnings, msg) }

	// Reihenfolge: erst Stammdaten, dann Events, dann abhängige Tabellen
	if len(bundle.Clubs) > 0 && string(bundle.Clubs) != "[]" {
		result.Clubs, err = ins("clubs", bundle.Clubs, "")
		if err != nil {
			warn(fmt.Sprintf("clubs übersprungen: %v", err))
		}
	}
	if len(bundle.Shooters) > 0 && string(bundle.Shooters) != "[]" {
		result.Shooters, err = ins("shooters", bundle.Shooters, "")
		if err != nil {
			warn(fmt.Sprintf("shooters übersprungen: %v", err))
		}
	}

	result.Events, err = ins("events", bundle.Events, "")
	if err != nil {
		return nil, fmt.Errorf("events: %w", err)
	}

	result.Teams, err = ins("teams", bundle.Teams, "")
	if err != nil {
		warn(fmt.Sprintf("teams teilweise übersprungen: %v", err))
	}

	result.Starters, err = ins("starters", bundle.Starters, "")
	if err != nil {
		warn(fmt.Sprintf("starters teilweise übersprungen: %v", err))
	}

	result.CompetitionParticipants, err = ins("competition_participants", bundle.CompetitionParticipants, "")
	if err != nil {
		warn(fmt.Sprintf("competition_participants teilweise übersprungen: %v", err))
	}

	// Sessions: nur wenn lane_id, calibration_id und discipline_id noch existieren
	result.Sessions, err = ins("sessions", bundle.Sessions,
		`r.lane_id IN (SELECT id FROM lanes)
		 AND r.calibration_id IN (SELECT id FROM calibrations)
		 AND r.discipline_id IN (SELECT id FROM disciplines)`)
	if err != nil {
		warn(fmt.Sprintf("sessions teilweise übersprungen: %v", err))
	}

	// Shots: nur für erfolgreich importierte Sessions
	result.Shots, err = ins("shots", bundle.Shots,
		`r.session_id IN (SELECT id FROM sessions)`)
	if err != nil {
		warn(fmt.Sprintf("shots teilweise übersprungen: %v", err))
	}

	result.SavedAuswertungen, err = ins("saved_auswertungen", bundle.SavedAuswertungen, "")
	if err != nil {
		warn(fmt.Sprintf("saved_auswertungen übersprungen: %v", err))
	}

	return result, tx.Commit(ctx)
}

// ArchivedEventInfo ist eine kompakte Event-Zusammenfassung für die UI.
type ArchivedEventInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	StartsOn     string `json:"starts_on"`
	StarterCount int    `json:"starter_count"`
	SessionCount int    `json:"session_count"`
	ShotCount    int    `json:"shot_count"`
}

func (s *Store) ListArchivedEventsWithStats(ctx context.Context) ([]ArchivedEventInfo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.id, e.name, COALESCE(e.type,''), COALESCE(e.starts_on::text,''),
		       (SELECT COUNT(*) FROM starters st WHERE st.event_id = e.id),
		       (SELECT COUNT(*) FROM sessions se WHERE se.event_id = e.id),
		       (SELECT COUNT(*) FROM shots sh JOIN sessions se ON se.id = sh.session_id WHERE se.event_id = e.id)
		FROM events e
		WHERE e.status = 'archived'
		ORDER BY e.starts_on DESC NULLS LAST, e.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ArchivedEventInfo
	for rows.Next() {
		var ev ArchivedEventInfo
		if err := rows.Scan(&ev.ID, &ev.Name, &ev.Type, &ev.StartsOn,
			&ev.StarterCount, &ev.SessionCount, &ev.ShotCount); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}
