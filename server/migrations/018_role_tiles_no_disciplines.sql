-- ============================================================================
-- 018_role_tiles_no_disciplines.sql – Anwender und Revisor sehen die Kachel
-- "Disziplinen" standardmäßig nicht mehr (Admin/Entwickler weiterhin schon).
-- Rein datenseitige Anpassung der in 016_ui_roles.sql gesetzten Vorgaben.
-- ============================================================================

DELETE FROM ui_role_tiles
WHERE role_key IN ('anwender', 'revisor') AND tile_key = 'disciplines';
