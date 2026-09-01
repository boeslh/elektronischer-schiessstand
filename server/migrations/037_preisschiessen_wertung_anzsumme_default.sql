-- ============================================================================
-- 037_preisschiessen_wertung_anzsumme_default.sql – Default für
-- ps_wertungen.anz_summe von 5 auf 1 (Einzelwertung) geändert
--
-- Der bisherige Default 5 stammte unveraendert aus der Umbenennung
-- anz_praemie -> anz_summe (032_preisschiessen_wertungen_summe.sql) und aus
-- der urspruenglichen ANZ_RING_PRAEMIE/ANZ_TEILER_PRAEMIE-Uebernahme
-- (031_preisschiessen_wertungen.sql) - ein Summenwertungs-Wert (>1) als
-- unauffaelliger Default fuehrt leicht zu ungewollten Summenwertungen mit
-- normierendem Scheiben-Faktor statt der eigentlich gewuenschten reinen
-- Einzelwertung (siehe Chat: Faktor 5 statt 1 auf einer Scheibe versehentlich
-- mit anz_summe=5 verwechselt). 1 = Einzelwertung ist der harmlosere,
-- erwartungskonformere Default; Summenwertung bleibt bewusst waehlbar.
-- ============================================================================
BEGIN;

ALTER TABLE ps_wertungen ALTER COLUMN anz_summe SET DEFAULT 1;

COMMIT;
