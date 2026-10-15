-- ============================================================================
-- 083_stechen.sql – Stechschuesse im Paarungswettkampf.
--
-- Jeder Stechschuss ist eine eigene Session mit genau einem Wertungsschuss
-- (der Stand-PC beginnt so mit leerer Scheibe, das eigentliche Ergebnis
-- bleibt unveraendert). stechen_no = Durchgang 1, 2, ... je Paarung,
-- stechen_of = die Wertungs-Session des Schuetzen.
-- ============================================================================
BEGIN;

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS stechen_no SMALLINT;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS stechen_of UUID REFERENCES sessions(id);
CREATE INDEX IF NOT EXISTS idx_sessions_stechen ON sessions (event_id) WHERE stechen_no IS NOT NULL;

COMMIT;
