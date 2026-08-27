-- ============================================================================
-- 011_saved_auswertungen.sql – Gespeicherte Auswertungskonfigurationen
-- ============================================================================
BEGIN;

CREATE TABLE IF NOT EXISTS saved_auswertungen (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL,
    type       TEXT NOT NULL DEFAULT 'runde',   -- runde | meister | mannschaft | variabel
    event_id   UUID REFERENCES events(id) ON DELETE CASCADE,
    params     JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_saved_auswertungen_event ON saved_auswertungen(event_id);

COMMIT;
