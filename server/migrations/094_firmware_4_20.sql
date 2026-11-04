-- ============================================================================
-- 094_firmware_4_20.sql – Firmware Rev 4.20.0: neue Mikrofon-Anordnung,
-- Entfernungskorrektur je Mikrofon (SET DIST0..DIST5, 0.001 mm) statt
-- Timing-Offsets (OFS0..OFS5), Schallgeschwindigkeit nur aus der Temperatur.
--
-- esp32_calibrations: eine Kalibrier-Sicherung ab 4.20.0 traegt dist_um
-- (cal/done) - offsets_ns/sound_mps bleiben dann 0. Alte Sicherungen
-- (dist_um NULL) passen nicht zur neuen Geometrie und werden vom Stand-PC
-- nicht auf ein Geraet ab 4.20.0 zurueckgespielt.
-- ============================================================================
BEGIN;

ALTER TABLE esp32_calibrations
    ADD COLUMN IF NOT EXISTS dist_um INTEGER[6];

COMMIT;
