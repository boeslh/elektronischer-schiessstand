-- ============================================================================
-- 054_shots_entry_source_aggregate.sql – Herkunft und "Sammel"-Schüsse.
--
-- entry_source unterscheidet, woher eine shots-Zeile stammt: 'sensor' (wie
-- bisher, ESP32/TDOA über standpc/db.go), 'manual' (von Hand getippt) oder
-- 'wertmaschine' (Disag-Wertmaschine, siehe Konzept
-- .claude/plans/wise-scribbling-abelson.md).
--
-- aggregate_count erlaubt, dass EINE shots-Zeile mehrere echte Schüsse auf
-- einmal repräsentiert (z.B. eine ganze Serie oder die ganze Scheibe als ein
-- manuell eingegebener Summenwert) - shot_count/total_rings/total_decimal
-- in v_session_results/v_series_results summieren ab jetzt aggregate_count
-- statt Zeilen zu zählen, damit z.B. preisschiessen_wertungen.go's
-- "vsr.shot_count >= d.match_shot_count"-Vollständigkeitsprüfung auch bei
-- einem einzigen Gesamtwert-Eintrag korrekt funktioniert.
-- ============================================================================
BEGIN;

ALTER TABLE shots
    ADD COLUMN entry_source TEXT NOT NULL DEFAULT 'sensor'
        CHECK (entry_source IN ('sensor', 'manual', 'wertmaschine')),
    ADD COLUMN aggregate_count SMALLINT NOT NULL DEFAULT 1
        CHECK (aggregate_count >= 1);

DROP VIEW IF EXISTS v_series_results;
DROP VIEW IF EXISTS v_session_results;
DROP VIEW IF EXISTS v_scoring_shots;

-- v_scoring_shots-Definition bleibt inhaltlich unveraendert (reicht
-- entry_source/aggregate_count ueber s.* durch) - muss aber neu angelegt
-- werden, da "s.*" bei CREATE VIEW auf die zum Zeitpunkt der Anlage
-- vorhandenen Spalten fixiert wird und neue ALTER-TABLE-Spalten sonst
-- nicht automatisch erscheinen.
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

-- WICHTIG: eff_ring/eff_decimal_value/eff_is_inner_ten sind pro Zeile bereits
-- der VOLLE Beitrag dieser Zeile (bei aggregate_count>1 also die Serien-
-- bzw. Gesamtsumme, nicht ein Pro-Schuss-Wert) - nur shot_count/shots
-- braucht SUM(aggregate_count), die Wertsummen bleiben wie zuvor SUM(eff_*)
-- ohne Multiplikation. inner_tens/best_center_distance sind fuer
-- Sammel-Zeilen (aggregate_count>1) naturgemaess unvollstaendig (keine
-- Einzelschuss-Aufloesung vorhanden) - bewusst hingenommene Grenze der
-- groben Erfassung, keine Verzerrung der Ring-/Zehntelsumme.
CREATE VIEW v_session_results AS
SELECT
    ss.effective_session_id AS session_id,
    SUM(ss.aggregate_count) AS shot_count,
    SUM(ss.eff_ring) - SUM(ss.penalty_rings) AS total_rings,
    ROUND(SUM(ss.eff_decimal_value) - SUM(ss.penalty_rings), 1) AS total_decimal,
    SUM(CASE WHEN ss.eff_is_inner_ten THEN 1 ELSE 0 END) AS inner_tens,
    MIN(ss.eff_center_distance) AS best_center_distance
FROM v_scoring_shots ss
GROUP BY ss.effective_session_id;

CREATE VIEW v_series_results AS
SELECT
    ss.effective_session_id AS session_id,
    ss.series_no,
    SUM(ss.aggregate_count) AS shots,
    SUM(ss.eff_ring) AS rings,
    ROUND(SUM(ss.eff_decimal_value), 1) AS decimal_total
FROM v_scoring_shots ss
GROUP BY ss.effective_session_id, ss.series_no;

COMMIT;
