-- ============================================================================
-- 043_preisschiessen_wertung_scheiben_spalte.sql – zeigt für jeden
-- Teilnehmer einer Wertung, welche Scheibe(n) er tatsächlich geschossen hat
-- (z.B. "LG Kombi" vs. "LP Kombi" in einer kombinierten Wertung) - siehe
-- preisschiessen_wertungen.go computeMeisterPunkt.
--
-- ps_anzeige_config.show_scheibe steuert, ob die Spalte in der browsbaren
-- Ergebnis-Website UND im Kiosk-Modus des Display-Servers erscheint (Default
-- aus, rein optional).
-- ============================================================================
BEGIN;

ALTER TABLE ps_wertung_ergebnisse
    ADD COLUMN scheiben TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE ps_anzeige_config
    ADD COLUMN show_scheibe BOOLEAN NOT NULL DEFAULT false;

COMMIT;
