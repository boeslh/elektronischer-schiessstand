-- ============================================================================
-- 003_stammdaten.sql – Gaue + Vereine (Stammdaten)
-- ============================================================================
BEGIN;

CREATE TABLE gaue (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    gau_no     TEXT NOT NULL UNIQUE,   -- z.B. "418"
    name       TEXT NOT NULL,           -- z.B. "Massenhausen"
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE clubs
    ADD COLUMN external_no  TEXT UNIQUE,     -- VEREINNR, z.B. "418001"
    ADD COLUMN gau_id       UUID REFERENCES gaue(id),
    ADD COLUMN member_count INT;

COMMIT;
