// ============================================================================
// delete-selection.go – Kategorie-basierte Löschung von Daten aus der
// Live-Datenbank (Import/Export-Kachel, admin-only).
//
// Nutzt dieselbe Kategorie-Auswahl wie der selektive Export/Import
// (CategorySelection aus staged-transfer.go), aber OHNE Konfigurationen
// (Stände/Kalibrierungen sind Betriebsvoraussetzung, kein Datenbestand zum
// Löschen) und OHNE die Aufteilung von Stammdaten/Wettkämpfe in Teil-
// Tabellen - jede Kategorie wird komplett geleert (kein Zeitraum-Filter
// ausser bei Ergebnisse).
//
// Anders als Export/Import läuft das direkt gegen die Live-DB, in EINER
// Transaktion (alles oder nichts): fehlt eine abhängige Kategorie in der
// Auswahl (z.B. Wettkämpfe löschen, aber Ergebnisse, die noch auf die
// Starter verweisen, nicht), bricht die Fremdschlüssel-Prüfung die ganze
// Transaktion ab - es wird dann NICHTS gelöscht, mit einer verständlichen
// Fehlermeldung statt der rohen Postgres-Meldung. Vor dem Löschen wird wie
// beim Restore automatisch ein Sicherheits-Backup angelegt.
//
// disziplinen loescht NUR die disciplines-Zeilen selbst (Kaskade auf
// discipline_positions) - targets/target_rings/shooter_classes sind
// geteilte Referenzdaten (Scheiben-Typen, DSB-Klassen), keine Disziplin-
// eigenen Daten, und bleiben von dieser Funktion unangetastet.
// ============================================================================
package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

func (a *APIServer) deleteSelection(ctx context.Context, sel CategorySelection) (TransferSummary, error) {
	var summary TransferSummary
	if sel.Konfigurationen {
		return summary, errBadRequest("Konfigurationen können hier nicht gelöscht werden")
	}
	if sel.empty() {
		return summary, errBadRequest("keine Kategorie ausgewählt")
	}

	// Sicherheits-Backup vor jeder Löschung - wie beim Restore.
	if _, err := a.createBackup(ctx); err != nil {
		return summary, fmt.Errorf("Sicherheits-Backup vor Löschung fehlgeschlagen, Löschung abgebrochen: %w", err)
	}

	tx, err := a.store.pool.Begin(ctx)
	if err != nil {
		return summary, err
	}
	defer tx.Rollback(ctx)

	if sel.Ergebnisse {
		if _, err := tx.Exec(ctx, `ALTER TABLE shots DISABLE TRIGGER trg_shots_no_delete`); err != nil {
			return summary, err
		}
	}

	type step struct {
		label string
		run   bool
		sql   string
		args  []any
		set   func(n int)
	}
	steps := []step{
		// Sessions ohne started_at (noch keine Schuesse erfasst, z.B. gerade
		// erst zugewiesener Stand) faellt in KEINEN Datumsbereich - werden
		// aber trotzdem immer mitgeloescht, sobald Ergebnisse ausgewaehlt
		// ist: sonst blieben sie dauerhaft uebrig (mit keinem Zeitraum
		// erreichbar) und wuerden spaeter das Loeschen von Stammdaten/
		// Wettkaempfen an genau dieser Session blockieren.
		{"audit_log", sel.Ergebnisse, `DELETE FROM audit_log WHERE
			(session_id IS NOT NULL AND session_id IN (SELECT id FROM sessions
				WHERE started_at IS NULL OR (started_at >= $1 AND started_at < $2)))
			OR (shot_id IS NOT NULL AND shot_id IN (SELECT id FROM shots WHERE session_id IN (
				SELECT id FROM sessions WHERE started_at IS NULL OR (started_at >= $1 AND started_at < $2))))`,
			[]any{sel.DateFrom, sel.DateTo}, nil},
		{"shots", sel.Ergebnisse, `DELETE FROM shots WHERE session_id IN (
			SELECT id FROM sessions WHERE started_at IS NULL OR (started_at >= $1 AND started_at < $2))`,
			[]any{sel.DateFrom, sel.DateTo}, func(n int) { summary.Shots = &n }},
		{"sessions", sel.Ergebnisse, `DELETE FROM sessions WHERE started_at IS NULL OR (started_at >= $1 AND started_at < $2)`,
			[]any{sel.DateFrom, sel.DateTo}, func(n int) { summary.Sessions = &n }},
		{"starters", sel.Wettkaempfe, `DELETE FROM starters`, nil, nil},
		{"teams", sel.Stammdaten, `DELETE FROM teams`, nil, func(n int) { summary.Teams = &n }},
		{"events", sel.Wettkaempfe, `DELETE FROM events`, nil, func(n int) { summary.Events = &n }},
		{"shooters", sel.Stammdaten, `DELETE FROM shooters`, nil, func(n int) { summary.Shooters = &n }},
		{"clubs", sel.Stammdaten, `DELETE FROM clubs`, nil, func(n int) { summary.Clubs = &n }},
		{"gaue", sel.Stammdaten, `DELETE FROM gaue`, nil, nil},
		{"disciplines", sel.Disziplinen, `DELETE FROM disciplines`, nil, func(n int) { summary.Disciplines = &n }},
	}
	for _, s := range steps {
		if !s.run {
			continue
		}
		tag, err := tx.Exec(ctx, s.sql, s.args...)
		if err != nil {
			return summary, deleteLiveError(s.label, err)
		}
		if s.set != nil {
			n := int(tag.RowsAffected())
			s.set(n)
		}
	}

	if sel.Ergebnisse {
		if _, err := tx.Exec(ctx, `ALTER TABLE shots ENABLE TRIGGER trg_shots_no_delete`); err != nil {
			return summary, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return summary, deleteLiveError("commit", err)
	}
	return summary, nil
}

// deleteLiveError liefert bei einer Fremdschlüssel-Verletzung (abhängige
// Daten aus einer nicht mit ausgewählten Kategorie existieren noch) eine
// verständliche Meldung statt der rohen Postgres-Ausgabe. Die Transaktion
// wird in jedem Fall komplett zurückgerollt (alles oder nichts) - es wurde
// dann garantiert NICHTS gelöscht.
func deleteLiveError(label string, err error) error {
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "fremdschlüssel") || strings.Contains(lower, "foreign key") {
		return fmt.Errorf("Löschen abgebrochen (%s): Es existieren noch abhängige Daten aus einer nicht "+
			"mit ausgewählten Kategorie (z.B. Ergebnisse/Wettkämpfe, die noch auf zu löschende Stammdaten "+
			"verweisen). Es wurde NICHTS gelöscht. Zusätzliche Kategorie mit auswählen oder abhängige Daten "+
			"zuerst entfernen: %w", label, err)
	}
	return fmt.Errorf("Löschen (%s) fehlgeschlagen: %w", label, err)
}

func (a *APIServer) deleteSelectionHandler(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		Categories []string `json:"categories"`
		DateFrom   string   `json:"date_from"`
		DateTo     string   `json:"date_to"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body: " + err.Error())
	}
	sel, err := parseCategorySelection(body.Categories, body.DateFrom, body.DateTo)
	if err != nil {
		return nil, err
	}
	summary, err := a.deleteSelection(r.Context(), sel)
	if err != nil {
		return nil, err
	}
	return map[string]any{"summary": summary}, nil
}
