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
// Reihenfolge, mit hartem Abbruch beim ersten Fehler.
//
// Sonderfall Altbestand: schema_migrations selbst gibt es erst seit Migration
// 067 - auf einer Anlage, die VORHER schon per install-service.sh (alte,
// ungetrackte Schleife) vollstaendig eingerichtet wurde, ist die Tabelle beim
// ersten Aufruf leer, obwohl die Datenbank laengst alle Migrationen hat. Ein
// erneutes Anwenden wuerde je nach Migration mit ganz unterschiedlichen
// Fehlern scheitern (z.B. "Typ existiert bereits" bei 001, aber "Spalte
// existiert nicht" bei einer spaeteren Migration, die eine von einer
// dazwischenliegenden Migration bereits umbenannte/entfernte Spalte
// anspricht) - eine Fehlerklassen-Erkennung reicht dafuer nicht aus. Statt
// dessen: ist schema_migrations LEER (nicht: "existiert noch nicht" - ein
// vorheriger fehlgeschlagener Aufruf kann die Tabelle bereits leer angelegt
// haben, bevor er an einer Migration scheiterte) UND die Datenbank hat
// bereits andere Tabellen, gilt dieser Aufruf als "Altbestand-Abgleich"
// (legacyBootstrap) - darin wird jede Datei toleriert (wie frueher die alte
// Schleife) und nur vermerkt, egal welcher Fehler auftritt. Sobald
// schema_migrations existiert, laufen alle folgenden Aufrufe wieder streng
// (jeder Fehler bricht hart ab) - der Altbestand-Abgleich passiert also nur
// exakt einmal pro Datenbank.
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
// bzw. (Altbestand-Abgleich, siehe Dateikopf) nachtraeglich vermerkten
// Dateinamen (leer = bereits alles aktuell).
func ApplyPendingMigrations(ctx context.Context, pool *pgxpool.Pool, migrationsDir string) ([]string, error) {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return nil, fmt.Errorf("schema_migrations anlegen: %w", err)
	}

	// legacyBootstrap anhand der Zeilenanzahl statt nur "Tabelle existiert"
	// bestimmen: ein FRUEHERER, fehlgeschlagener Aufruf kann die Tabelle
	// bereits (leer) angelegt haben, bevor er an einer Migration scheiterte -
	// dann wuerde "existiert bereits" faelschlich "schon vollstaendig
	// eingerichtet" bedeuten und der Altbestand-Abgleich naechstes Mal gar
	// nicht mehr greifen. Leer + andere Tabellen vorhanden ist der
	// zuverlaessige Marker, unabhaengig davon, WANN/WARUM die Tabelle leer
	// entstanden ist.
	legacyBootstrap := false
	var trackedCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&trackedCount); err != nil {
		return nil, fmt.Errorf("angewandte Migrationen zaehlen: %w", err)
	}
	if trackedCount == 0 {
		if err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name <> 'schema_migrations'
		)`).Scan(&legacyBootstrap); err != nil {
			return nil, fmt.Errorf("Altbestand pruefen: %w", err)
		}
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
			if !legacyBootstrap {
				return applied, fmt.Errorf("Migration %s fehlgeschlagen: %w", name, err)
			}
			// Altbestand-Abgleich (siehe Dateikopf) - Datei nur nachtraeglich
			// vermerken, nicht erneut versuchen. Eine Datei ohne eigenes
			// BEGIN/COMMIT wird von Postgres implizit als eine Transaktion
			// behandelt (Dateien MIT eigenem BEGIN/COMMIT sowieso), ein
			// Fehler bedeutet also zuverlaessig: nichts aus dieser Datei
			// wurde in diesem Lauf uebernommen.
		}
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
			return applied, fmt.Errorf("Migration %s angewandt, aber Versionseintrag fehlgeschlagen: %w", name, err)
		}
		applied = append(applied, name)
	}
	return applied, nil
}
