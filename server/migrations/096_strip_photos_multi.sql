-- ============================================================================
-- 096_strip_photos_multi.sql – Bandkontrolle: mehrere Fotos je Scheibe
-- (z.B. Foto 1 mit Schuss 20-30, Foto 2 mit Schuss 1-15; Überschneidungen
-- und Lücken möglich). Vorhandene Fotos werden zu Foto 1.
-- ============================================================================
BEGIN;

ALTER TABLE session_strip_photos ADD COLUMN IF NOT EXISTS photo_no SMALLINT NOT NULL DEFAULT 1;
ALTER TABLE session_strip_photos DROP CONSTRAINT IF EXISTS session_strip_photos_pkey;
ALTER TABLE session_strip_photos ADD PRIMARY KEY (session_id, photo_no);
ALTER TABLE session_strip_photos ALTER COLUMN photo_no DROP DEFAULT;

COMMIT;
