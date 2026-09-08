-- ============================================================================
-- 048_esp32_calibrations.sql – Kalibrierungs-Backup je ESP32 (Firmware Rev
-- 4.9.0, siehe protokoll-referenz.md Abschnitt 4.6/5.3). Der Stand-PC lädt
-- jede erfolgreich abgeschlossene Kalibrierung ("cal"/"done") hierher hoch
-- (siehe standpc/calibration_backup.go uploadCalibrationBackup) und kann sie
-- über die lokale Admin-GUI per "CAL IMPORT" wiederherstellen.
--
-- Primärschlüssel ist die stabile Geräte-ID (mac), NICHT lane_no - ein
-- ESP32 kann den Stand wechseln, das Backup bleibt trotzdem auffindbar.
-- ============================================================================
BEGIN;

CREATE TABLE esp32_calibrations (
    mac         TEXT PRIMARY KEY,
    offsets_ns  INTEGER[6] NOT NULL,
    sound_mps   INTEGER NOT NULL,
    lane_no     INTEGER,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMIT;
