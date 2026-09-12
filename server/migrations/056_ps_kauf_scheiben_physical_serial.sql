-- ============================================================================
-- 056_ps_kauf_scheiben_physical_serial.sql – Seriennummer der physischen
-- Papierscheibe.
--
-- Getrennt von der bestehenden ps_kauf_scheiben.serial_no (intern,
-- automatisch fortlaufend vergeben, migrations/024) - physical_serial_no
-- wird beim Verkauf einer Papierscheiben-Disziplin manuell eingetippt, da
-- vorgedruckte Papierscheiben aus einem Vorrat mit nicht-fortlaufenden
-- Nummern verwendet werden. Eindeutig je Preisschiessen (wie serial_no),
-- NULL erlaubt (nur Papier-Disziplinen befuellen das Feld) - der partielle
-- Unique-Index laesst dabei beliebig viele NULLs zu und verhindert nur
-- doppelt vergebene tatsaechliche Nummern.
-- ============================================================================
BEGIN;

ALTER TABLE ps_kauf_scheiben ADD COLUMN physical_serial_no TEXT;

CREATE UNIQUE INDEX ux_ps_kauf_scheiben_physical_serial
    ON ps_kauf_scheiben (preisschiessen_id, physical_serial_no)
    WHERE physical_serial_no IS NOT NULL;

COMMIT;
