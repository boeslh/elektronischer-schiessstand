-- ============================================================================
-- 014_simulator_configs.sql
--   Benannte Parametersaetze fuer den Server-seitigen Kalibrier-Simulator
--   (Nachbau der Firmware-Trilateration aus den gespeicherten air_ns-Rohdaten,
--   siehe server/simulator.go). params enthaelt standoff_steel_mm/
--   standoff_paper_mm/offset_x_um/offset_y_um/sound_mps/mic_offset_ns[6]/
--   target - Feldnamen identisch zur SHOW-Ausgabe der Firmware
--   (sendShowConfig() in schiessstand_firmware.ino), damit der optionale
--   SHOW-Import ohne Mapping funktioniert.
-- ============================================================================
BEGIN;

CREATE TABLE simulator_configs (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL UNIQUE,
    params     JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMIT;
