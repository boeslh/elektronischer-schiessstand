-- ============================================================================
-- 012_shots_center_distance_sessions_cache.sql
--   - shots.divisor       → center_distance
--   - sessions: cached Ergebnisspalten + list_name für Preisschießen
-- ============================================================================
BEGIN;

-- 1. Shots: Spalte umbenennen
ALTER TABLE shots RENAME COLUMN divisor TO center_distance;

-- 2. Sessions: Cache-Spalten + Preisschießen-Zuordnung
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS rings                SMALLINT,
    ADD COLUMN IF NOT EXISTS rings_decimal        NUMERIC(6,1),
    ADD COLUMN IF NOT EXISTS best_center_distance NUMERIC(8,1),
    ADD COLUMN IF NOT EXISTS list_name            TEXT;

-- 3. Views neu erstellen (RENAME hat v_scoring_shots via s.* invalidiert,
--    v_session_results referenziert die alte Spalte direkt)
DROP VIEW IF EXISTS v_series_results;
DROP VIEW IF EXISTS v_session_results;
DROP VIEW IF EXISTS v_scoring_shots;

CREATE VIEW v_scoring_shots AS
SELECT
    COALESCE(s.scored_for_session, s.session_id) AS effective_session_id,
    s.*
FROM shots s
WHERE s.kind = 'match'
  AND s.status IN ('valid', 'cross_shot_in')
  AND s.status <> 'annulled';

CREATE VIEW v_session_results AS
SELECT
    ss.effective_session_id                          AS session_id,
    COUNT(*)                                         AS shot_count,
    SUM(ss.ring)   - SUM(ss.penalty_rings)           AS total_rings,
    ROUND(SUM(ss.decimal_value)
          - SUM(ss.penalty_rings), 1)                AS total_decimal,
    SUM(CASE WHEN ss.is_inner_ten THEN 1 ELSE 0 END) AS inner_tens,
    MIN(ss.center_distance)                          AS best_center_distance
FROM v_scoring_shots ss
GROUP BY ss.effective_session_id;

CREATE VIEW v_series_results AS
SELECT
    ss.effective_session_id              AS session_id,
    ss.series_no,
    COUNT(*)                             AS shots,
    SUM(ss.ring)                         AS rings,
    ROUND(SUM(ss.decimal_value), 1)      AS decimal_total
FROM v_scoring_shots ss
GROUP BY ss.effective_session_id, ss.series_no;

COMMIT;
