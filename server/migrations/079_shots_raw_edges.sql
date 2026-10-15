-- ============================================================================
-- 079_shots_raw_edges.sql – Firmware Rev 4.16.0, SET RAWLOG=1: zu jedem
-- gueltigen Schuss das vollstaendige "raw"-Telegramm (alle steigenden und
-- fallenden Flanken je Mikrofon, unkorrigiert, plus Offsets, aktivierte Mics,
-- Erfassungsmodus, Schallgeschwindigkeit, WALKK/WALKMAX und Fenster zum
-- Schusszeitpunkt - siehe protokoll-referenz.md 4.5c). Der Simulator kann
-- damit jeden Schuss exakt wie die Firmware nachrechnen und Varianten (z.B.
-- andere Pulsbreiten-Regel) durchspielen. NULL = ohne RAWLOG aufgenommen.
-- ============================================================================
BEGIN;

ALTER TABLE shots ADD COLUMN IF NOT EXISTS raw_edges JSONB;

COMMIT;
