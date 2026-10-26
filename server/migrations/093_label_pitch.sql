-- ============================================================================
-- 093_label_pitch.sql – fester Etikettenabstand (Etikett + Luecke) fuer den
-- Etikettendrucker, wenn der Lueckensensor die Luecke nicht sicher erkennt
-- (<STX>c, "Continuous Paper Length"). NULL = Lueckensensor (<STX>c0000).
-- ============================================================================
BEGIN;

ALTER TABLE app_settings ADD COLUMN label_pitch_mm NUMERIC(4,1);

COMMIT;
