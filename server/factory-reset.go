// ============================================================================
// factory-reset.go – Datenbank auf Auslieferzustand zurücksetzen
// (Import/Export-Kachel, admin-only).
//
// Anders als die kategoriebasierte Loeschung (delete-selection.go, deckt
// Preisschiessen-Tabellen NICHT ab) wird hier praktisch der komplette
// Datenbestand geleert - nur die als "Betriebsvoraussetzung" geltende
// Hardware-/Anzeigekonfiguration (Staende, Kalibrierungen, ESP32-Zuordnung,
// Rollen/Kacheln, Anzeigen-Slots, Scheiben-/Klassen-Referenzdaten) bleibt
// unangetastet - UND die Disziplinen selbst, gefiltert auf eine vorhandene
// numerische DSB-Regelnummer (rule_no, Format "X.YY") kleiner als eine
// gegebene Schwelle (ueblich: 10, trennt die vier Standard-Luftdruck-
// Disziplinen 1.10/1.11/2.10/2.11 von allen Vereins-/Test-eigenen).
//
// TRUNCATE ... CASCADE statt einzelner DELETEs: erledigt die komplette
// Fremdschluessel-Reihenfolge automatisch (das war genau das Problem, das
// delete-selection.go beim Preisschiessen NICHT loesen konnte, da diese
// Tabellen dort gar nicht bekannt sind). Sicher, weil keine der hier
// bewusst NICHT geleerten Tabellen per Fremdschluessel auf eine der
// geleerten Tabellen verweist (siehe Session-Notiz/Verifikation) - CASCADE
// kann sich also nicht ungewollt auf die Konfigurationstabellen ausweiten.
// ============================================================================
package main

import (
	"context"
	"fmt"
	"net/http"
)

// factoryResetWipeTables: alles ausser Konfiguration/Hardware-Zuordnung und
// den Disziplinen selbst (die werden separat gefiltert geloescht, siehe
// factoryReset). discipline_positions haengt per ON DELETE CASCADE direkt
// an disciplines und wird dadurch automatisch mitbereinigt.
var factoryResetWipeTables = []string{
	"audit_log", "clubs", "competition_participants", "events", "gaue",
	"preisschiessen", "ps_anzeige_config", "ps_auswertung_status", "ps_gewinne",
	"ps_guthaben_buchungen", "ps_kaeufe", "ps_kauf_scheiben", "ps_lane_pending",
	"ps_lane_selfservice", "ps_scheibe_classes", "ps_scheibe_requires_set",
	"ps_scheiben", "ps_set_classes", "ps_set_items", "ps_sets", "ps_teilnehmer",
	"ps_verein_punkte_zeitraum", "ps_verein_teilnahme", "ps_wertung_ergebnisse",
	"ps_wertung_punkte_ring", "ps_wertung_punkte_teiler", "ps_wertung_scheiben",
	"ps_wertungen", "saved_auswertungen", "sessions", "shots", "shooters",
	"starters", "team_members", "teams",
}

// FactoryResetSummary: Zeilenanzahl der wichtigsten geleerten Tabellen (vor
// dem Zuruecksetzen gezaehlt, da TRUNCATE selbst keine Trefferzahl liefert)
// sowie behaltene/entfernte Disziplinen.
type FactoryResetSummary struct {
	Shooters           int `json:"shooters"`
	Clubs              int `json:"clubs"`
	Teams              int `json:"teams"`
	Events             int `json:"events"`
	Sessions           int `json:"sessions"`
	Shots              int `json:"shots"`
	DisciplinesKept    int `json:"disciplines_kept"`
	DisciplinesRemoved int `json:"disciplines_removed"`
}

// factoryReset leert factoryResetWipeTables komplett und behaelt in
// disciplines nur Zeilen mit numerischer rule_no (Format "X..." oder
// "X.YY") kleiner als keepRuleNoBelow. Automatisches Sicherheits-Backup
// vorher, alles in einer Transaktion (alles oder nichts).
func (a *APIServer) factoryReset(ctx context.Context, keepRuleNoBelow int) (FactoryResetSummary, error) {
	var summary FactoryResetSummary

	if _, err := a.createBackup(ctx); err != nil {
		return summary, fmt.Errorf("Sicherheits-Backup vor Zuruecksetzen fehlgeschlagen, Vorgang abgebrochen: %w", err)
	}

	tx, err := a.store.pool.Begin(ctx)
	if err != nil {
		return summary, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `ALTER TABLE shots DISABLE TRIGGER trg_shots_no_delete`); err != nil {
		return summary, err
	}

	if err := tx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM shooters), (SELECT count(*) FROM events),
		(SELECT count(*) FROM sessions), (SELECT count(*) FROM shots),
		(SELECT count(*) FROM clubs), (SELECT count(*) FROM teams)`,
	).Scan(&summary.Shooters, &summary.Events, &summary.Sessions, &summary.Shots,
		&summary.Clubs, &summary.Teams); err != nil {
		return summary, fmt.Errorf("Zaehlen vor dem Zuruecksetzen: %w", err)
	}

	truncateSQL := "TRUNCATE TABLE " + joinIdentifiers(factoryResetWipeTables) + " CASCADE"
	if _, err := tx.Exec(ctx, truncateSQL); err != nil {
		return summary, fmt.Errorf("Zuruecksetzen fehlgeschlagen: %w", err)
	}

	// COALESCE(...,false): rule_no IS NULL macht den Vergleich sonst NULL
	// statt false (SQL-Dreiwertlogik) - "WHERE NOT NULL" loescht dann gar
	// nichts, obwohl eine fehlende Regelnummer eindeutig NICHT das
	// Kriterium "vorhandene Regelnummer < Schwelle" erfuellt.
	tag, err := tx.Exec(ctx, `DELETE FROM disciplines WHERE NOT COALESCE(
		rule_no ~ '^[0-9]+(\.[0-9]+)?$' AND split_part(rule_no, '.', 1)::int < $1,
		false
	)`, keepRuleNoBelow)
	if err != nil {
		return summary, fmt.Errorf("Disziplinen bereinigen fehlgeschlagen: %w", err)
	}
	summary.DisciplinesRemoved = int(tag.RowsAffected())

	if err := tx.QueryRow(ctx, `SELECT count(*) FROM disciplines`).Scan(&summary.DisciplinesKept); err != nil {
		return summary, err
	}

	if _, err := tx.Exec(ctx, `ALTER TABLE shots ENABLE TRIGGER trg_shots_no_delete`); err != nil {
		return summary, err
	}
	if err := tx.Commit(ctx); err != nil {
		return summary, fmt.Errorf("Zuruecksetzen fehlgeschlagen (Commit): %w", err)
	}

	return summary, nil
}

// joinIdentifiers: factoryResetWipeTables ist eine feste, im Code definierte
// Liste (kein Nutzereingabe-Pfad) - einfaches Verbinden mit Komma reicht,
// keine Postgres-Identifier-Sonderzeichen enthalten.
func joinIdentifiers(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}

func (a *APIServer) factoryResetHandler(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		KeepRuleNoBelow int `json:"keep_rule_no_below"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body: " + err.Error())
	}
	if body.KeepRuleNoBelow <= 0 {
		body.KeepRuleNoBelow = 10
	}
	summary, err := a.factoryReset(r.Context(), body.KeepRuleNoBelow)
	if err != nil {
		return nil, err
	}
	return map[string]any{"summary": summary}, nil
}
