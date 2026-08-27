// ============================================================================
// staged-transfer.go – Kategorie-basierter Export/Import (Import/Export-
// Kachel, admin-only).
//
// pg_dump/pg_restore kennen keinen Zeilen-Filter. Um gezielt nur bestimmte
// Kategorien (bzw. bei Ergebnissen einen Zeitraum) zu übertragen, wird die
// Quelle (ein Backup - bei Export vorher frisch von der Live-DB erzeugt, bei
// Import ein vorhandenes/hochgeladenes Backup) in eine temporäre Wegwerf-
// Datenbank restored, dort auf die gewünschte Auswahl zugeschnitten (sicher,
// weil isoliert), erneut exportiert und dieser Teil-Dump entweder als Datei
// gespeichert (Export) oder in die Live-DB eingespielt (Import,
// --single-transaction: alles oder nichts).
//
// Kategorien -> Tabellen:
//   stammdaten      shooters, clubs, gaue, teams, team_members
//   disziplinen     disciplines, targets, target_rings,
//                   discipline_positions, shooter_classes
//   wettkaempfe     events, competition_participants, saved_auswertungen,
//                   starters
//   ergebnisse      sessions, shots (gefiltert auf started_at im Zeitraum)
//   konfigurationen lanes, devices, calibrations, simulator_configs
//
// Konsistenz: shooters/teams/events/starters werden auch OHNE eigene
// Kategorie automatisch als referenzierte Teilmenge mitgenommen, wenn
// ergebnisse/wettkaempfe sie brauchen (z.B. Ergebnisse ohne Stammdaten:
// nur die tatsaechlich beteiligten Schuetzen). disziplinen/konfigurationen/
// clubs+gaue werden NICHT automatisch mitgezogen (keine engere Abhaengigkeit
// im Schema) - die IDs muessen im Ziel bereits existieren, siehe Hinweis im
// Frontend.
// ============================================================================
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type CategorySelection struct {
	Stammdaten      bool
	Disziplinen     bool
	Wettkaempfe     bool
	Ergebnisse      bool
	Konfigurationen bool
	DateFrom        time.Time
	DateTo          time.Time // exklusiv (naechster Tag nach dem gewaehlten 'Bis')
}

func (sel CategorySelection) empty() bool {
	return !sel.Stammdaten && !sel.Disziplinen && !sel.Wettkaempfe &&
		!sel.Ergebnisse && !sel.Konfigurationen
}

// Abgeleitete "wird uebertragen"-Flags fuer die zusammenhaengenden Tabellen -
// zentral an einer Stelle, damit Tabellenliste und Zuschneiden konsistent
// dieselbe Logik verwenden.
type transferPlan struct {
	shooters, teams, teamMembers   bool
	events, compParticipants       bool
	savedAuswertungen, starters    bool
	sessionsShots                  bool
	pruneStarters, pruneEvents     bool // referenzierte Teilmenge statt alles
	pruneTeams, pruneShooters      bool
}

func planFor(sel CategorySelection) transferPlan {
	var p transferPlan
	p.sessionsShots = sel.Ergebnisse
	p.starters = sel.Wettkaempfe || sel.Ergebnisse
	p.events = sel.Wettkaempfe || sel.Ergebnisse
	p.teams = sel.Stammdaten || p.starters
	p.shooters = sel.Stammdaten || p.starters || p.sessionsShots
	p.teamMembers = p.teams || p.shooters
	p.compParticipants = p.events
	p.savedAuswertungen = p.events
	p.pruneStarters = p.starters && !sel.Wettkaempfe
	p.pruneEvents = p.events && !sel.Wettkaempfe
	p.pruneTeams = p.teams && !sel.Stammdaten
	p.pruneShooters = p.shooters && !sel.Stammdaten
	return p
}

func tablesForSelection(sel CategorySelection) []string {
	p := planFor(sel)
	var t []string
	if p.shooters {
		t = append(t, "shooters")
	}
	if sel.Stammdaten {
		t = append(t, "clubs", "gaue")
	}
	if p.teams {
		t = append(t, "teams")
	}
	if p.teamMembers {
		t = append(t, "team_members")
	}
	if p.events {
		t = append(t, "events")
	}
	if p.compParticipants {
		t = append(t, "competition_participants")
	}
	if p.savedAuswertungen {
		t = append(t, "saved_auswertungen")
	}
	if p.starters {
		t = append(t, "starters")
	}
	if p.sessionsShots {
		t = append(t, "sessions", "shots")
	}
	if sel.Disziplinen {
		t = append(t, "disciplines", "targets", "target_rings", "discipline_positions", "shooter_classes")
	}
	if sel.Konfigurationen {
		t = append(t, "lanes", "devices", "calibrations", "simulator_configs")
	}
	return t
}

// withDBName ersetzt den Datenbanknamen in einer Postgres-URI-DSN.
func withDBName(dsn, dbName string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("DSN unlesbar: %w", err)
	}
	u.Path = "/" + dbName
	return u.String(), nil
}

func randomSuffix() (string, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// Felder sind Pointer (omitempty), damit im JSON und in der Frontend-Anzeige
// nur Kategorien auftauchen, die tatsaechlich Teil der Auswahl sind - nicht
// ausgewaehlte Tabellen bleiben in der Staging-DB unangetastet (siehe
// tablesForSelection) und wuerden sonst mit ihrem vollen (Live-)Bestand
// angezeigt, obwohl sie gar nicht Teil des Transfers sind.
type TransferSummary struct {
	Shooters    *int `json:"shooters,omitempty"`
	Clubs       *int `json:"clubs,omitempty"`
	Teams       *int `json:"teams,omitempty"`
	Disciplines *int `json:"disciplines,omitempty"`
	Events      *int `json:"events,omitempty"`
	Sessions    *int `json:"sessions,omitempty"`
	Shots       *int `json:"shots,omitempty"`
	Lanes       *int `json:"lanes,omitempty"`
}

func countStaging(ctx context.Context, stage *pgx.Conn, sel CategorySelection) (TransferSummary, error) {
	p := planFor(sel)
	var shooters, clubs, teams, disciplines, events, sessions, shots, lanes int
	row := stage.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM shooters), (SELECT count(*) FROM clubs),
		(SELECT count(*) FROM teams), (SELECT count(*) FROM disciplines),
		(SELECT count(*) FROM events), (SELECT count(*) FROM sessions),
		(SELECT count(*) FROM shots), (SELECT count(*) FROM lanes)`)
	if err := row.Scan(&shooters, &clubs, &teams, &disciplines,
		&events, &sessions, &shots, &lanes); err != nil {
		return TransferSummary{}, err
	}
	var s TransferSummary
	if p.shooters {
		s.Shooters = &shooters
	}
	if sel.Stammdaten {
		s.Clubs = &clubs
	}
	if p.teams {
		s.Teams = &teams
	}
	if sel.Disziplinen {
		s.Disciplines = &disciplines
	}
	if p.events {
		s.Events = &events
	}
	if p.sessionsShots {
		s.Sessions = &sessions
		s.Shots = &shots
	}
	if sel.Konfigurationen {
		s.Lanes = &lanes
	}
	return s, nil
}

// stagedSetup legt eine Wegwerf-DB an, restored sourcePath vollstaendig
// hinein und schneidet sie gemaess sel zu. Der Aufrufer MUSS die
// zurueckgegebene cleanup-Funktion per defer ausfuehren (schliesst die
// Verbindung und entfernt die Wegwerf-DB), auch im Fehlerfall.
func (a *APIServer) stagedSetup(ctx context.Context, sourcePath string, sel CategorySelection) (
	stage *pgx.Conn, stagingDSN string, cleanup func(), summary TransferSummary, err error) {

	cleanup = func() {} // No-op bis echte Ressourcen existieren

	pgRestore, err := a.resolvePgTool("pg_restore")
	if err != nil {
		return nil, "", cleanup, summary, err
	}

	suffix, err := randomSuffix()
	if err != nil {
		return nil, "", cleanup, summary, err
	}
	stagingName := "schiessstand_transfer_" + suffix
	maintDSN, err := withDBName(a.dsn, "postgres")
	if err != nil {
		return nil, "", cleanup, summary, err
	}
	stagingDSN, err = withDBName(a.dsn, stagingName)
	if err != nil {
		return nil, "", cleanup, summary, err
	}

	maint, err := pgx.Connect(ctx, maintDSN)
	if err != nil {
		return nil, "", cleanup, summary, fmt.Errorf("Verbindung zur Wartungs-DB: %w", err)
	}
	if _, err := maint.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{stagingName}.Sanitize()); err != nil {
		maint.Close(ctx)
		return nil, "", cleanup, summary, fmt.Errorf("Wegwerf-Datenbank anlegen: %w", err)
	}

	var stageConn *pgx.Conn
	cleanup = func() {
		if stageConn != nil {
			stageConn.Close(context.Background())
		}
		_, _ = maint.Exec(context.Background(),
			"DROP DATABASE IF EXISTS "+pgx.Identifier{stagingName}.Sanitize()+" WITH (FORCE)")
		maint.Close(context.Background())
	}

	// 1. Komplette Quelle in die Wegwerf-DB restoren.
	if out, err := exec.CommandContext(ctx, pgRestore, "-d", stagingDSN, sourcePath).CombinedOutput(); err != nil {
		return nil, "", cleanup, summary, fmt.Errorf("pg_restore (Staging) fehlgeschlagen: %s: %w", out, err)
	}

	stageConn, err = pgx.Connect(ctx, stagingDSN)
	if err != nil {
		return nil, "", cleanup, summary, fmt.Errorf("Verbindung zur Wegwerf-DB: %w", err)
	}

	// 2. Auf die gewuenschte Auswahl zuschneiden.
	if err := pruneStagingDB(ctx, stageConn, sel); err != nil {
		return nil, "", cleanup, summary, err
	}

	summary, err = countStaging(ctx, stageConn, sel)
	if err != nil {
		return nil, "", cleanup, summary, err
	}
	if sel.Ergebnisse && (summary.Sessions == nil || *summary.Sessions == 0) {
		return nil, "", cleanup, summary, fmt.Errorf("keine Sessions im gewaehlten Zeitraum gefunden")
	}

	return stageConn, stagingDSN, cleanup, summary, nil
}

// dumpTables dumpt die gegebenen Tabellen aus stagingDSN in eine neue Datei
// (data-only) und liefert deren Pfad.
func (a *APIServer) dumpTables(ctx context.Context, stagingDSN string, tables []string, outPath string) error {
	pgDump, err := a.resolvePgTool("pg_dump")
	if err != nil {
		return err
	}
	args := []string{stagingDSN, "--data-only", "-F", "c", "-f", outPath}
	for _, t := range tables {
		args = append(args, "-t", t)
	}
	if out, err := exec.CommandContext(ctx, pgDump, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("pg_dump (Teilmenge) fehlgeschlagen: %s: %w", out, err)
	}
	return nil
}

// exportBaseFilename baut einen sprechenden Dateinamen aus den gewaehlten
// Kategorien (z.B. "export_ergebnisse-wettkaempfe_20260827_210000.dump").
func exportBaseFilename(sel CategorySelection) string {
	var parts []string
	if sel.Stammdaten {
		parts = append(parts, "stammdaten")
	}
	if sel.Disziplinen {
		parts = append(parts, "disziplinen")
	}
	if sel.Wettkaempfe {
		parts = append(parts, "wettkaempfe")
	}
	if sel.Ergebnisse {
		parts = append(parts, "ergebnisse")
	}
	if sel.Konfigurationen {
		parts = append(parts, "konfigurationen")
	}
	label := strings.Join(parts, "-")
	if label == "" {
		label = "export"
	}
	return fmt.Sprintf("export_%s_%s.dump", label, time.Now().Format("20060102_150405"))
}

// exportSelection erzeugt zunaechst ein frisches Komplett-Backup der
// Live-DB (bleibt zusaetzlich als normales Backup erhalten) und nutzt es
// als Quelle fuer denselben Zuschneide-Ablauf wie beim Import - das Ergebnis
// landet als neue Datei im Backup-Verzeichnis statt in der Live-DB.
func (a *APIServer) exportSelection(ctx context.Context, sel CategorySelection) (string, TransferSummary, error) {
	var summary TransferSummary
	if sel.empty() {
		return "", summary, errBadRequest("keine Kategorie ausgewaehlt")
	}

	sourceFilename, err := a.createBackup(ctx)
	if err != nil {
		return "", summary, fmt.Errorf("Ausgangs-Backup fuer Export: %w", err)
	}
	sourcePath := filepath.Join(a.backupDir, sourceFilename)

	stage, stagingDSN, cleanup, summary, err := a.stagedSetup(ctx, sourcePath, sel)
	defer cleanup()
	if err != nil {
		return "", summary, err
	}
	_ = stage // Zaehlung ist bereits erfolgt; Verbindung bleibt bis cleanup() offen

	filename, outPath := uniquePathFor(a.backupDir, exportBaseFilename(sel))
	if err := a.dumpTables(ctx, stagingDSN, tablesForSelection(sel), outPath); err != nil {
		return "", summary, err
	}
	return filename, summary, nil
}

// importSelection restored die Auswahl aus sourcePath in die Live-DB.
func (a *APIServer) importSelection(ctx context.Context, sourcePath string, sel CategorySelection) (TransferSummary, error) {
	var summary TransferSummary
	if sel.empty() {
		return summary, errBadRequest("keine Kategorie ausgewaehlt")
	}

	stage, stagingDSN, cleanup, summary, err := a.stagedSetup(ctx, sourcePath, sel)
	defer cleanup()
	if err != nil {
		return summary, err
	}
	_ = stage

	pgRestore, err := a.resolvePgTool("pg_restore")
	if err != nil {
		return summary, err
	}
	tmpDir, err := os.MkdirTemp("", "schiessstand-transfer-")
	if err != nil {
		return summary, err
	}
	defer os.RemoveAll(tmpDir)
	transferPath := tmpDir + "/transfer.dump"

	if err := a.dumpTables(ctx, stagingDSN, tablesForSelection(sel), transferPath); err != nil {
		return summary, err
	}

	// trg_shots_set_started_at (Migration 015, AFTER INSERT ON shots) muss
	// waehrend des Restores kurz deaktiviert werden, falls Ergebnisse Teil
	// der Auswahl sind - siehe ausfuehrlicher Kommentar in der vorherigen
	// Fassung dieser Datei / Commit-Historie: der Trigger versucht sonst
	// "sessions" per UPDATE anzufassen, was im Kontext von pg_restore
	// --single-transaction mit "relation sessions existiert nicht"
	// fehlschlaegt (kein Superuser-Workaround wie --disable-triggers
	// noetig, da nur dieser eine eigene Trigger betroffen ist).
	if sel.Ergebnisse {
		if _, err := a.store.pool.Exec(ctx, `ALTER TABLE shots DISABLE TRIGGER trg_shots_set_started_at`); err != nil {
			return summary, fmt.Errorf("Trigger deaktivieren: %w", err)
		}
		defer a.store.pool.Exec(context.Background(), `ALTER TABLE shots ENABLE TRIGGER trg_shots_set_started_at`)
	}

	if out, err := exec.CommandContext(ctx, pgRestore,
		"-d", a.dsn, "--data-only", "--single-transaction", transferPath).CombinedOutput(); err != nil {
		return summary, restoreLiveError(out, err)
	}

	return summary, nil
}

// restoreLiveError liefert bei einem erkannten Unique-Constraint-Konflikt
// (Auswahl ueberschneidet sich mit einem frueheren Import) eine klare,
// verstaendliche Meldung statt der rohen pg_restore-Ausgabe. --single-
// transaction sorgt in diesem Fall garantiert dafuer, dass NICHTS
// uebernommen wurde (alles oder nichts) - das gilt auch bei nur
// teilweiser Ueberschneidung.
func restoreLiveError(out []byte, err error) error {
	lower := strings.ToLower(string(out))
	if strings.Contains(lower, "doppelter schlüsselwert") || strings.Contains(lower, "duplicate key value") {
		return fmt.Errorf("Import abgebrochen: Diese Daten (oder ein Teil davon) wurden bereits " +
			"importiert bzw. existieren schon. Es wurde NICHTS verändert - der Import läuft als " +
			"eine einzelne Transaktion, bei jedem Konflikt (auch bei nur teilweiser Überschneidung) " +
			"wird alles zurückgerollt. Auswahl anpassen oder vorhandene Daten vorher entfernen.")
	}
	return fmt.Errorf("pg_restore (Live-DB) fehlgeschlagen: %s: %w", out, err)
}

// pruneStagingDB behaelt in der (isolierten) Wegwerf-DB nur die per sel
// gewaehlte Auswahl (siehe transferPlan/planFor fuer die Herleitung, welche
// Tabelle voll behalten/auf eine referenzierte Teilmenge zugeschnitten/gar
// nicht angefasst wird - nicht ausgewaehlte Tabellen werden schlicht nicht
// gedumpt, brauchen also KEIN Loeschen).
func pruneStagingDB(ctx context.Context, conn *pgx.Conn, sel CategorySelection) error {
	p := planFor(sel)

	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `ALTER TABLE shots DISABLE TRIGGER trg_shots_no_delete`); err != nil {
		return err
	}

	type step struct {
		label string
		run   bool
		sql   string
		args  []any
	}
	steps := []step{
		// audit_log ist nie Teil des Transfers, blockiert in der Staging-DB
		// aber sonst das Loeschen von sessions/shots (Fremdschluessel).
		{"audit_log", p.sessionsShots, `DELETE FROM audit_log WHERE
			(session_id IS NOT NULL AND session_id NOT IN (
				SELECT id FROM sessions WHERE started_at >= $1 AND started_at < $2))
			OR (shot_id IS NOT NULL AND shot_id NOT IN (
				SELECT id FROM shots WHERE session_id IN (
					SELECT id FROM sessions WHERE started_at >= $1 AND started_at < $2)))`,
			[]any{sel.DateFrom, sel.DateTo}},
		{"shots", p.sessionsShots, `DELETE FROM shots WHERE session_id NOT IN (
			SELECT id FROM sessions WHERE started_at >= $1 AND started_at < $2)`,
			[]any{sel.DateFrom, sel.DateTo}},
		// "NOT (started_at >= $1 AND started_at < $2)" allein wuerde Sessions
		// mit started_at IS NULL (noch keine Schuesse erfasst) NIE loeschen -
		// unter SQL-Dreiwertlogik ist NOT NULL wieder NULL, nie TRUE. Solche
		// Sessions gehoeren zu keinem Zeitraum und muessen explizit mit raus.
		{"sessions", p.sessionsShots, `DELETE FROM sessions WHERE
			started_at IS NULL OR NOT (started_at >= $1 AND started_at < $2)`,
			[]any{sel.DateFrom, sel.DateTo}},
		{"starters", p.pruneStarters, `DELETE FROM starters WHERE
			id NOT IN (SELECT starter_id FROM sessions WHERE starter_id IS NOT NULL)
			AND event_id NOT IN (SELECT event_id FROM sessions WHERE event_id IS NOT NULL)`, nil},
		{"teams", p.pruneTeams, `DELETE FROM teams WHERE
			id NOT IN (SELECT team_id FROM starters WHERE team_id IS NOT NULL)`, nil},
		{"shooters", p.pruneShooters, `DELETE FROM shooters WHERE id NOT IN (
			SELECT shooter_id FROM sessions WHERE shooter_id IS NOT NULL
			UNION SELECT shooter_id FROM starters)`, nil},
		{"events", p.pruneEvents, `DELETE FROM events WHERE id NOT IN (
			SELECT event_id FROM sessions WHERE event_id IS NOT NULL
			UNION SELECT event_id FROM starters
			UNION SELECT event_id FROM teams WHERE event_id IS NOT NULL)`, nil},
	}
	for _, s := range steps {
		if !s.run {
			continue
		}
		if _, err := tx.Exec(ctx, s.sql, s.args...); err != nil {
			return fmt.Errorf("Zuschneiden (%s): %w", s.label, err)
		}
	}

	if _, err := tx.Exec(ctx, `ALTER TABLE shots ENABLE TRIGGER trg_shots_no_delete`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ----------------------------------------------------------------------------
// HTTP-Handler
// ----------------------------------------------------------------------------

func parseCategorySelection(categories []string, dateFrom, dateTo string) (CategorySelection, error) {
	var sel CategorySelection
	for _, c := range categories {
		switch c {
		case "stammdaten":
			sel.Stammdaten = true
		case "disziplinen":
			sel.Disziplinen = true
		case "wettkaempfe":
			sel.Wettkaempfe = true
		case "ergebnisse":
			sel.Ergebnisse = true
		case "konfigurationen":
			sel.Konfigurationen = true
		default:
			return sel, errBadRequest("unbekannte Kategorie: " + c)
		}
	}
	if sel.Ergebnisse {
		from, err := time.ParseInLocation("2006-01-02", dateFrom, time.Local)
		if err != nil {
			return sel, errBadRequest("ungueltiges Datum 'von'")
		}
		toDay, err := time.ParseInLocation("2006-01-02", dateTo, time.Local)
		if err != nil {
			return sel, errBadRequest("ungueltiges Datum 'bis'")
		}
		sel.DateFrom = from
		sel.DateTo = toDay.AddDate(0, 0, 1) // 'bis' ist inklusive -> Ausschlussgrenze = naechster Tag
	}
	return sel, nil
}

func (a *APIServer) exportSelectionHandler(w http.ResponseWriter, r *http.Request) (any, error) {
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
	filename, summary, err := a.exportSelection(r.Context(), sel)
	if err != nil {
		return nil, err
	}
	return map[string]any{"filename": filename, "summary": summary}, nil
}

func (a *APIServer) importSelectionHandler(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		Filename   string   `json:"filename"`
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
	path, err := safeBackupPath(a.backupDir, body.Filename)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, &httpError{code: http.StatusNotFound, msg: "Backup nicht gefunden"}
	}
	summary, err := a.importSelection(r.Context(), path, sel)
	if err != nil {
		return nil, err
	}
	return map[string]any{"summary": summary}, nil
}
