-- ============================================================================
-- 090_session_import.sql – Herkunft importierter Durchgaenge.
--
-- sessions.imported_local_sid: lokale Session-ID des Stands (Schussprotokoll
-- des Stand-PC, z.B. Standalone-Betrieb in der Android-App), aus der diese
-- Session importiert wurde - erkennt einen bereits uebernommenen Durchgang.
-- ============================================================================
BEGIN;

ALTER TABLE sessions ADD COLUMN imported_local_sid TEXT;

CREATE INDEX idx_sessions_imported_local_sid ON sessions (imported_local_sid)
    WHERE imported_local_sid IS NOT NULL;

COMMIT;
