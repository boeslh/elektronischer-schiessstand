-- ============================================================================
-- 040_preisschiessen_anzeige_list_font_size.sql – Schriftgröße des
-- Listentexts im Kiosk-Modus des Display-Servers (preisanzeige/), analog zu
-- title_font_size (siehe migrations/034 bzw. wo diese Spalte angelegt wurde).
-- ============================================================================
BEGIN;

ALTER TABLE ps_anzeige_config
    ADD COLUMN list_font_size INT NOT NULL DEFAULT 18;

ALTER TABLE ps_anzeige_config ADD CONSTRAINT ps_anzeige_config_list_font_size_check
    CHECK (list_font_size > 0);

COMMIT;
