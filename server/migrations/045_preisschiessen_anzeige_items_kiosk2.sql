-- ============================================================================
-- 045_preisschiessen_anzeige_items_kiosk2.sql – zweite, unabhängige Auswahl
-- an Wertungen/Vereins-Auswertungen für eine zweite Kiosk-Anzeige
-- ("/ps/{id}/kiosk2", siehe preisanzeige/display.go) - alle übrigen
-- Einstellungen (Reload, Schriftgrößen, Farben, Kiosk-Spaltenoptionen,
-- Scheibe-Anzeige) gelten für beide Kiosk-Anzeigen gemeinsam.
-- ============================================================================
BEGIN;

ALTER TABLE ps_anzeige_config
    ADD COLUMN anzeige_items_2 TEXT[] NOT NULL DEFAULT '{}';

COMMIT;
