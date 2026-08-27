-- ============================================================================
-- 006_competitions.sql – Wettkämpfe erweitern
-- ============================================================================
BEGIN;

-- events um Wettkampf-spezifische Felder ergänzen
ALTER TABLE events
    ADD COLUMN IF NOT EXISTS type          VARCHAR(10)
        CHECK (type IN ('einzel','runde','gruppe')),
    ADD COLUMN IF NOT EXISTS discipline_id UUID REFERENCES disciplines(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS location      TEXT,
    ADD COLUMN IF NOT EXISTS active        BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS updated_at    TIMESTAMPTZ NOT NULL DEFAULT now();

-- Teilnehmer-Einheiten für Runden- und Gruppenwettkämpfe
-- (Einzelwettkampf: Starter direkt in starters-Tabelle, kein Eintrag hier)
CREATE TABLE IF NOT EXISTS competition_participants (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id       UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    team_id        UUID REFERENCES teams(id)  ON DELETE CASCADE,
    club_id        UUID REFERENCES clubs(id)  ON DELETE CASCADE,
    gau_id         UUID REFERENCES gaue(id)   ON DELETE CASCADE,
    sort_order     SMALLINT DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT cp_one_entity CHECK (
        (team_id IS NOT NULL)::int +
        (club_id IS NOT NULL)::int +
        (gau_id  IS NOT NULL)::int = 1
    )
);

CREATE INDEX IF NOT EXISTS idx_comp_participants_event ON competition_participants(event_id);

COMMIT;
