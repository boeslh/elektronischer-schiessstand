-- ============================================================================
-- 050_competitions_liga.sql – Liga-Feld für Wettkämpfe (vor allem für die
-- PDF-Auswertung von Rundenwettkämpfen benötigt: dort müssen Datum,
-- Disziplin UND Liga im Kopf stehen).
-- ============================================================================
BEGIN;

ALTER TABLE events
    ADD COLUMN IF NOT EXISTS liga TEXT;

COMMIT;
