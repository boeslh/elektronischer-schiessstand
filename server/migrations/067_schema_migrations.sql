-- schema_migrations: Versions-Tabelle, damit der Server (siehe migrations.go)
-- erkennen kann, welche Migrationsdateien bereits angewandt wurden - noetig
-- fuer den automatischen Nachzug fehlender Migrationen nach einem "Full
-- Restore" eines aelteren Backups (server/backup.go restoreFromFile).
--
-- version = Dateiname der jeweiligen migrations/*.sql-Datei (z.B.
-- "001_schema.sql") - eindeutig und bereits chronologisch sortierbar dank
-- des durchgaengigen 3-stelligen Praefixes.
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
