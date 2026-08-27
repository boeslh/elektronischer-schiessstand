-- ============================================================================
-- 005_teams.sql – Mannschaften erweitern und Mannschaftsmitglieder anlegen
-- ============================================================================
BEGIN;

-- Bestehende teams-Tabelle um Stammdaten-Felder erweitern
ALTER TABLE teams
    ADD COLUMN IF NOT EXISTS short_name  TEXT,
    ADD COLUMN IF NOT EXISTS season      TEXT,
    ADD COLUMN IF NOT EXISTS discipline  TEXT,
    ADD COLUMN IF NOT EXISTS gau_id      UUID REFERENCES gaue(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS notes       TEXT,
    ADD COLUMN IF NOT EXISTS active      BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS updated_at  TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE INDEX IF NOT EXISTS idx_teams_club ON teams(club_id);
CREATE INDEX IF NOT EXISTS idx_teams_gau  ON teams(gau_id);

-- Zuordnungstabelle Mannschaft ↔ Schütze
CREATE TABLE IF NOT EXISTS team_members (
    team_id     UUID NOT NULL REFERENCES teams(id)    ON DELETE CASCADE,
    shooter_id  UUID NOT NULL REFERENCES shooters(id) ON DELETE CASCADE,
    position    SMALLINT,
    joined_at   DATE,
    notes       TEXT,
    PRIMARY KEY (team_id, shooter_id)
);

CREATE INDEX IF NOT EXISTS idx_team_members_shooter ON team_members(shooter_id);

COMMIT;
