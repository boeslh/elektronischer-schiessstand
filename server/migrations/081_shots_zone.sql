-- ============================================================================
-- 081_shots_zone.sql – Zonen-Kalibrierung (Firmware Rev 4.17.0, siehe
-- standpc/firmware/protokoll-referenz.md 7.3c): Zone mit dem groessten Anteil
-- an der Lage ("A" Loch der Vorsatzscheibe, "B" neben dem Schussband, "C"
-- Band + Vorsatzscheibe), null = ohne Zonen-Kalibrierung gerechnet.
-- ============================================================================
BEGIN;

ALTER TABLE shots
    ADD COLUMN IF NOT EXISTS zone TEXT;

COMMIT;
