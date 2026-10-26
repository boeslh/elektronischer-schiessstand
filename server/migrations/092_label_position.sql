-- ============================================================================
-- 092_label_position.sql – Positionierung der Papierscheiben-Labels auf dem
-- Etikettendrucker (Datamax, DPL):
--   label_present_mm     Vorschub zum Abreissen nach dem Druck; der Drucker
--                        zieht das Etikett vor dem naechsten Druck zurueck
--                        (<STX>Kf / <STX>f). NULL = Einstellung des Druckers.
--   label_sop_adjust_mm  Verschiebung des Druckbeginns (<STX>O), 0 = keine.
-- ============================================================================
BEGIN;

ALTER TABLE app_settings
    ADD COLUMN label_present_mm    NUMERIC(4,1),
    ADD COLUMN label_sop_adjust_mm NUMERIC(4,1) NOT NULL DEFAULT 0;

COMMIT;
