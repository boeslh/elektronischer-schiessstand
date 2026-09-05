-- ============================================================================
-- 042_preisschiessen_anzeige_kiosk_spalten.sql – Spaltensteuerung für den
-- Kiosk-Modus des Display-Servers (preisanzeige/display.go): Verein/Klasse
-- einzeln aus-/einblendbar, dafür eine konfigurierbare Anzahl an
-- Einzelergebnis-Spalten (bisher fest "Beste 5").
-- ============================================================================
BEGIN;

ALTER TABLE ps_anzeige_config
    ADD COLUMN kiosk_show_verein BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN kiosk_show_klasse BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN kiosk_anzahl_einzelergebnisse INT NOT NULL DEFAULT 5;

ALTER TABLE ps_anzeige_config ADD CONSTRAINT ps_anzeige_config_kiosk_anzahl_check
    CHECK (kiosk_anzahl_einzelergebnisse BETWEEN 0 AND 10);

COMMIT;
