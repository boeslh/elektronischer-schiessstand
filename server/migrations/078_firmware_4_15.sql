-- ============================================================================
-- 078_firmware_4_15.sql – Firmware Rev 4.13-4.15 (siehe
-- standpc/firmware/protokoll-referenz.md): MCPWM-Capture, Pulsbreiten-
-- Korrektur (WALKK), Schallgeschwindigkeit aus DHT22.
--
-- shots: je Schuss die Zusatzdaten, die der Simulator zum exakten
-- Nachrechnen braucht:
--   air_w_ns       Pulsbreite der ersten Flanke je Mic (ns, null = keine),
--                  nur bei aktivem MCPWM-Capture vorhanden
--   sound_act_mps  zum Schusszeitpunkt aktive Schallgeschwindigkeit
--                  (status.sound_act_mps, bei DHT=1 aus der Messung)
--   capture        aktiver Erfassungsmodus ("isr" | "mcpwm")
--   walk_k         zum Schusszeitpunkt eingestelltes SET WALKK
--   algo           Auswertepfad des Telegramms ("classic" | "rim") - bei
--                  RIM ist die WALKK-Korrektur in air_ns schon enthalten
--
-- esp32_calibrations: zur Kalibrier-Sicherung die genaue Schall-
-- geschwindigkeit (sound_mps ist seit 4.15 nur noch gerundet), WALKK und der
-- Erfassungsmodus, in dem die Offsets bestimmt wurden (ISR-Offsets passen
-- nicht zu MCPWM und umgekehrt).
-- ============================================================================
BEGIN;

ALTER TABLE shots
    ADD COLUMN IF NOT EXISTS air_w_ns      JSONB,
    ADD COLUMN IF NOT EXISTS sound_act_mps NUMERIC(6,2),
    ADD COLUMN IF NOT EXISTS capture       TEXT,
    ADD COLUMN IF NOT EXISTS walk_k        NUMERIC(5,3),
    ADD COLUMN IF NOT EXISTS algo          TEXT;

ALTER TABLE esp32_calibrations
    ADD COLUMN IF NOT EXISTS sound_set_mps NUMERIC(5,1),
    ADD COLUMN IF NOT EXISTS walk_k        NUMERIC(5,3),
    ADD COLUMN IF NOT EXISTS capture       TEXT;

COMMIT;
