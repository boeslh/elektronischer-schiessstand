-- ============================================================================
-- 095_session_strip_photos.sql – Foto des Papierbands je Session
-- (Bandkontrolle, siehe strip_check.go / web/bandkontrolle.html). Je Session
-- hoechstens ein Foto; ein neues Foto ersetzt das alte samt Markierungen.
-- markers: Loecher, Zuordnungen und Einstellungen der Bandkontrolle.
-- ============================================================================
BEGIN;

CREATE TABLE IF NOT EXISTS session_strip_photos (
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
