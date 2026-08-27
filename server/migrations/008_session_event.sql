-- ============================================================================
-- 008_session_event.sql – Veranstaltungszuordnung für Sessions
-- ============================================================================
BEGIN;

ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS event_id UUID REFERENCES events(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_sessions_event ON sessions(event_id);

COMMIT;
