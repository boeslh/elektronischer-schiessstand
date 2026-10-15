-- ============================================================================
-- 087_runde_best_of.sql – Mannschaftswertung im Rundenwettkampf: die besten
-- N Schuetzen je Mannschaft zaehlen (0 = alle). Wird beim Anlegen des
-- Wettkampfs festgelegt, damit Auswertung, PDF und Live-Anzeigen (Ergebnis
-- und Hochrechnung) dieselbe Wertung verwenden. Default 5 = bisheriges
-- Verhalten der Anzeigen und Vorauswahl der Auswertung.
-- ============================================================================
BEGIN;
ALTER TABLE events ADD COLUMN IF NOT EXISTS best_of SMALLINT NOT NULL DEFAULT 5;
ALTER TABLE events DROP CONSTRAINT IF EXISTS events_best_of_check;
ALTER TABLE events ADD CONSTRAINT events_best_of_check CHECK (best_of BETWEEN 0 AND 20);
COMMIT;
