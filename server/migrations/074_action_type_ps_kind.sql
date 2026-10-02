-- 074_action_type_ps_kind.sql – Audit-Log-Aktion fuer das nachtraegliche
-- Umstellen eines Preisschiessens auf Vereinsabend-Saison (und zurueck),
-- siehe preisschiessen.go SetPreisschiessenKind (nur Admin).
ALTER TYPE action_type ADD VALUE IF NOT EXISTS 'ps_kind_changed';
