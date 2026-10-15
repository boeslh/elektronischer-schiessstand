-- ============================================================================
-- 080_session_photos.sql – Foto der beschossenen Scheibe je Session
-- (Simulator, Foto-Kalibrierung, siehe sim_photo_store.go). Je Session
-- hoechstens ein Foto; ein neues Foto ersetzt das alte samt Markierungen.
-- markers: Schrauben, Innenkreis, Loecher und Einstellungen der Foto-
-- Kalibrierung (Format siehe web/simulator-foto.js markerState()), damit
-- spaetere Auswertungen ohne erneutes Markieren moeglich sind.
-- ============================================================================
BEGIN;

CREATE TABLE IF NOT EXISTS session_photos (
    session_id   UUID PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    content_type TEXT NOT NULL,
    filename     TEXT NOT NULL DEFAULT '',
    data         BYTEA NOT NULL,
    markers      JSONB,
    uploaded_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    uploaded_by  TEXT NOT NULL DEFAULT '',
    markers_at   TIMESTAMPTZ
);

COMMIT;
