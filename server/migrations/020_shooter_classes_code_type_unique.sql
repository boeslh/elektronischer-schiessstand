-- ============================================================================
-- 020_shooter_classes_code_type_unique.sql – Sportklassen: KLASSENNR ist nur
-- INNERHALB einer Schießart eindeutig, nicht global.
--
-- Der reale Export des Verwaltungstools verwendet dieselbe KLASSENNR fuer
-- unterschiedliche Schießarten (z.B. KLASSENNR=20 ist sowohl "Schüler
-- männlich" bei Kugel als auch "Schüler A männlich" bei Bogen). Mit der
-- bisherigen UNIQUE(code)-Regel hat der Import beim Auftreten der zweiten
-- Schießart die zuerst importierte Zeile stillschweigend überschrieben
-- (ON CONFLICT (code) DO UPDATE) - 14 von 48 Zeilen gingen so verloren.
-- ============================================================================
BEGIN;

ALTER TABLE shooter_classes DROP CONSTRAINT shooter_classes_code_key;
ALTER TABLE shooter_classes ADD CONSTRAINT shooter_classes_code_type_key UNIQUE (code, type);

COMMIT;
