// ============================================================================
// migrations.go – Anwenden fehlender Migrationsdateien, verfolgt ueber die
// Tabelle schema_migrations (siehe migrations/067_schema_migrations.sql).
//
// Bislang wurden Migrationen ausschliesslich extern per Schleife in
// install-service.sh angewandt (jede Datei einmal per "psql -f", Fehler nur
// gewarnt - das reicht fuer eine frische, leere Datenbank, laesst aber keine
// verlaessliche Aussage zu, WELCHE Migrationen eine bestehende Datenbank
// schon hat). ApplyPendingMigrations schliesst diese Luecke fuer den Fall
// "Full Restore eines aelteren Backups" (server/backup.go restoreFromFile):
// nur tatsaechlich fehlende Dateien werden nachgezogen, in Dateinamen-
// Reihenfolge, mit hartem Abbruch beim ersten Fehler (bewusst kein
// Toleranz-Verhalten wie in install-service.sh - hier soll ein Fehlschlag
// sofort auffallen statt stillschweigend uebersprungen zu werden).
// ============================================================================
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ApplyPendingMigrations legt schema_migrations bei Bedarf an und wendet
// alle *.sql-Dateien aus migrationsDir an, deren Dateiname noch nicht dort
// eingetragen ist. Liefert die Liste der in diesem Aufruf neu angewandten
// Dateinamen (leer = bereits alles aktuell).
func ApplyPendingMigrations(ctx context.Context, pool *pgxpool.Pool, migrationsDir string) ([]string, error) {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return nil, fmt.Errorf("schema_migrations anlegen: %w", err)
	}

	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("angewandte Migrationen lesen: %w", err)
	}
	done := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return nil, err
		}
		done[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("migrations-Verzeichnis (%s): %w", migrationsDir, err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files) // 3-stelliges Zahlenpraefix -> lexikalisch == chronologisch

	var applied []string
	for _, name := range files {
		if done[name] {
			continue
		}
		sqlBytes, err := os.ReadFile(filepath.Join(migrationsDir, name))
		if err != nil {
			return applied, fmt.Errorf("%s lesen: %w", name, err)
		}
		// QueryExecModeSimpleProtocol: erlaubt mehrere ';'-getrennte
		// Anweisungen in einem Exec (wie "psql -f") - einige Migrationen
		// bringen ihr eigenes BEGIN;/COMMIT; mit (z.B. 001_schema.sql),
		// daher hier bewusst KEIN zusaetzliches pgx-seitiges Begin/Commit
		// drumherum, das sich damit stossen wuerde.
		if _, err := pool.Exec(ctx, string(sqlBytes), pgx.QueryExecModeSimpleProtocol); err != nil {
			return applied, fmt.Errorf("Migration %s fehlgeschlagen: %w", name, err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
			return applied, fmt.Errorf("Migration %s angewandt, aber Versionseintrag fehlgeschlagen: %w", name, err)
		}
		applied = append(applied, name)
	}
	return applied, nil
}
