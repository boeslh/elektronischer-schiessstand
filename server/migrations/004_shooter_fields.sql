-- ============================================================================
-- 004_shooter_fields.sql – Schützen-Stammdaten erweitern
-- ============================================================================
BEGIN;

ALTER TABLE shooters
    ADD COLUMN IF NOT EXISTS title        TEXT,
    ADD COLUMN IF NOT EXISTS gender       CHAR(1),         -- 'M' / 'W'
    ADD COLUMN IF NOT EXISTS street       TEXT,
    ADD COLUMN IF NOT EXISTS zip          VARCHAR(10),
    ADD COLUMN IF NOT EXISTS city         TEXT,
    ADD COLUMN IF NOT EXISTS phone        TEXT,
    ADD COLUMN IF NOT EXISTS mobile       TEXT,
    ADD COLUMN IF NOT EXISTS email        TEXT,
    ADD COLUMN IF NOT EXISTS sports_class SMALLINT,        -- DSB-Sportklasse
    ADD COLUMN IF NOT EXISTS age_group    SMALLINT,        -- DSB-Altersgruppe
    ADD COLUMN IF NOT EXISTS entry_date   DATE,            -- Eintrittsdatum Verein
    ADD COLUMN IF NOT EXISTS interests    TEXT;            -- kommagetrennte Disziplinen

COMMIT;
