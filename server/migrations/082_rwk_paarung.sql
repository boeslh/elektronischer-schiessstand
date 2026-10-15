-- ============================================================================
-- 082_rwk_paarung.sql – Paarungswettkampf als zweite Art des Rundenwettkampfs.
--
-- events.runde_modus: 'mannschaft' (bisher: Mannschaftsergebnis entscheidet)
-- oder 'paarung' (je Paarung schiesst ein Heim- gegen einen Gastschuetzen,
-- das bessere Ergebnis bringt der Mannschaft einen Punkt; bei 4 Paarungen
-- und Punktgleichstand zaehlt das Mannschaftsergebnis als weiterer Punkt).
-- starters.pair_no: Paarung (1..5) eines Starters; je Wettkampf und
-- Mannschaft hoechstens einmal vergeben, NULL = keiner Paarung zugeordnet.
-- ============================================================================
BEGIN;

ALTER TABLE events
    ADD COLUMN IF NOT EXISTS runde_modus TEXT NOT NULL DEFAULT 'mannschaft';
ALTER TABLE events DROP CONSTRAINT IF EXISTS events_runde_modus_check;
ALTER TABLE events ADD CONSTRAINT events_runde_modus_check
    CHECK (runde_modus IN ('mannschaft', 'paarung'));

ALTER TABLE starters
    ADD COLUMN IF NOT EXISTS pair_no SMALLINT;
ALTER TABLE starters DROP CONSTRAINT IF EXISTS starters_pair_no_check;
ALTER TABLE starters ADD CONSTRAINT starters_pair_no_check
    CHECK (pair_no IS NULL OR pair_no BETWEEN 1 AND 10);
CREATE UNIQUE INDEX IF NOT EXISTS uq_starters_event_team_pair
    ON starters (event_id, team_id, pair_no) WHERE pair_no IS NOT NULL;

COMMIT;
