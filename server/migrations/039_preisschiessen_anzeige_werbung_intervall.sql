-- ============================================================================
-- 039_preisschiessen_anzeige_werbung_intervall.sql – Werbe-Intervall für den
-- Display-Server (preisanzeige/)
--
-- Steuert, nach wie vielen Teilnehmer-Zeilen innerhalb einer Ergebnisliste
-- (Wertung oder Vereins-Auswertung) ein Werbebild eingeblendet wird - die
-- Bilder selbst liegen als Dateien auf dem Rechner, auf dem preisanzeige
-- läuft (Standard /opt/ps/bilder/<preisschiessen_id>/{main,lists}/, siehe
-- preisanzeige/werbung.go), nicht in der Datenbank.
-- ============================================================================
BEGIN;

ALTER TABLE ps_anzeige_config
    ADD COLUMN werbung_intervall INT NOT NULL DEFAULT 20;

ALTER TABLE ps_anzeige_config ADD CONSTRAINT ps_anzeige_config_werbung_intervall_check
    CHECK (werbung_intervall > 0);

COMMIT;
