-- ============================================================================
-- 064_action_type_vereinsabend_admin.sql – Audit-Log-Aktionen fuer die
-- Admin-/Revisor-Funktionen an ps_kauf_scheiben (Vor-/Nachschuss markieren,
-- Schiesstag korrigieren, Scheiben-Zuordnung aendern - siehe
-- .claude/plans/wise-scribbling-abelson.md Abschnitt 3/4). Gilt fuer
-- Preisschiessen wie Vereinsabend-Saison gleichermassen.
-- ============================================================================
BEGIN;

ALTER TYPE action_type ADD VALUE IF NOT EXISTS 'ps_kauf_scheibe_vor_nachschuss_marked';
ALTER TYPE action_type ADD VALUE IF NOT EXISTS 'ps_kauf_scheibe_schiesstag_corrected';
ALTER TYPE action_type ADD VALUE IF NOT EXISTS 'ps_kauf_scheibe_reassigned';

COMMIT;
