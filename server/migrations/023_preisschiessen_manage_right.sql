-- ============================================================================
-- 023_preisschiessen_manage_right.sql – eigenes Recht fürs Bearbeiten/
-- Löschen von Preisschießen (inkl. Scheiben/Sets)
--
-- Bisher genügte die "preisschiessen"-Kachel (Sichtbarkeit von Übersicht,
-- Anmeldung und Kasse) auch, um ein Preisschießen selbst anzulegen,
-- umzubenennen oder zu löschen. Das wird jetzt wie "Ergebnisse korrigieren"
-- (can_correct_results, 016_ui_roles.sql) als eigenes, serverseitig
-- durchgesetztes Sonderrecht behandelt.
--
-- Default: nur admin. Weitere Rollen können das Recht direkt per
--   UPDATE ui_roles SET can_manage_preisschiessen = TRUE WHERE role_key = '...';
-- gesetzt werden, oder über die Benutzerverwaltung (Kachel-Matrix-Seite).
-- ============================================================================
BEGIN;

ALTER TABLE ui_roles
    ADD COLUMN can_manage_preisschiessen BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE ui_roles SET can_manage_preisschiessen = TRUE WHERE role_key = 'admin';

COMMENT ON COLUMN ui_roles.can_manage_preisschiessen IS
    'Darf Preisschießen (inkl. Scheiben/Sets) anlegen, bearbeiten und löschen - unabhängig von der "preisschiessen"-Kachel, die nur Übersicht/Anmeldung/Kasse freischaltet.';

-- Bugfix: die "preisschiessen"-Kachel fehlte bisher in der Kachel-Whitelist
-- (knownTileKeys in roles.go) und konnte daher in der Benutzerverwaltung gar
-- nicht pro Rolle zugewiesen werden. Ohne Nachtrag hier bliebe die Kachel für
-- alle Rollen außer denen aus 021_preisschiessen.sql weiterhin unsichtbar.
INSERT INTO ui_role_tiles (role_key, tile_key)
SELECT role_key, 'preisschiessen' FROM ui_roles
ON CONFLICT DO NOTHING;

COMMIT;
