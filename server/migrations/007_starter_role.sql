-- ============================================================================
-- 007_starter_role.sql – Rolle eines Starters im Wettkampf
-- ============================================================================
BEGIN;

ALTER TABLE starters
    ADD COLUMN IF NOT EXISTS role VARCHAR(2)
        CHECK (role IN ('S','E','AK'))
        NOT NULL DEFAULT 'S';   -- Stammschütze als Standard

COMMIT;
