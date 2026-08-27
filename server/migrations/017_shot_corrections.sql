-- ============================================================================
-- 017_shot_corrections.sql – Original- vs. korrigierte Trefferwerte.
-- Simulator-Neuberechnung ("Übernehmen") und manuelle Revisor-Korrektur
-- schreiben ab jetzt in separate corrected_*-Spalten statt die Original-
-- messung zu überschreiben. Alle Auswertungen (v_scoring_shots ->
-- v_session_results -> v_series_results) verwenden den effektiven Wert
-- (COALESCE(korrigiert, original)) automatisch.
-- ============================================================================

ALTER TYPE action_type ADD VALUE IF NOT EXISTS 'shot_correction_reverted';

ALTER TABLE shots
    ADD COLUMN corrected_x_mm            NUMERIC(7,3),
    ADD COLUMN corrected_y_mm            NUMERIC(7,3),
    ADD COLUMN corrected_ring            SMALLINT,
    ADD COLUMN corrected_decimal_value   NUMERIC(4,1),
    ADD COLUMN corrected_is_inner_ten    BOOLEAN,
    ADD COLUMN corrected_center_distance NUMERIC(8,1),
    ADD COLUMN corrected_at              TIMESTAMPTZ,
    ADD COLUMN corrected_by              TEXT;

DROP VIEW IF EXISTS v_series_results;
DROP VIEW IF EXISTS v_session_results;
DROP VIEW IF EXISTS v_scoring_shots;

CREATE VIEW v_scoring_shots AS
SELECT
    COALESCE(s.scored_for_session, s.session_id)             AS effective_session_id,
    COALESCE(s.corrected_ring, s.ring)                       AS eff_ring,
    COALESCE(s.corrected_decimal_value, s.decimal_value)     AS eff_decimal_value,
    COALESCE(s.corrected_is_inner_ten, s.is_inner_ten)       AS eff_is_inner_ten,
    COALESCE(s.corrected_center_distance, s.center_distance) AS eff_center_distance,
    s.*
FROM shots s
WHERE s.kind = 'match'
  AND s.status IN ('valid', 'cross_shot_in')
  AND s.status <> 'annulled';

CREATE VIEW v_session_results AS
SELECT
    ss.effective_session_id                              AS session_id,
    COUNT(*)                                             AS shot_count,
    SUM(ss.eff_ring)   - SUM(ss.penalty_rings)           AS total_rings,
    ROUND(SUM(ss.eff_decimal_value)
          - SUM(ss.penalty_rings), 1)                    AS total_decimal,
    SUM(CASE WHEN ss.eff_is_inner_ten THEN 1 ELSE 0 END) AS inner_tens,
    MIN(ss.eff_center_distance)                          AS best_center_distance
FROM v_scoring_shots ss
GROUP BY ss.effective_session_id;

CREATE VIEW v_series_results AS
SELECT
    ss.effective_session_id              AS session_id,
    ss.series_no,
    COUNT(*)                             AS shots,
    SUM(ss.eff_ring)                     AS rings,
    ROUND(SUM(ss.eff_decimal_value), 1)  AS decimal_total
FROM v_scoring_shots ss
GROUP BY ss.effective_session_id, ss.series_no;
