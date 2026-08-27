-- ============================================================================
-- 016_ui_roles.sql – Benutzer-/Rechteverwaltung (rollenbasiert, kein
-- individuelles Login: 4 feste Rollen mit optionalem gemeinsamem Passwort).
-- Siehe Plan "Benutzer- und Rechteverwaltung (rollenbasiert)".
-- ============================================================================

ALTER TYPE action_type ADD VALUE IF NOT EXISTS 'role_switched';
ALTER TYPE action_type ADD VALUE IF NOT EXISTS 'shot_value_corrected';
ALTER TYPE action_type ADD VALUE IF NOT EXISTS 'session_recalibrated';

CREATE TABLE ui_roles (
    role_key            TEXT PRIMARY KEY,     -- 'admin' | 'developer' | 'anwender' | 'revisor'
    display_name        TEXT NOT NULL,
    password_hash       TEXT,                 -- NULL = kein Passwort noetig
    can_correct_results BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order          SMALLINT NOT NULL DEFAULT 0
);

-- Welche Hauptmenü-Kacheln (tile_key) je Rolle sichtbar sind. Fehlender
-- Eintrag = nicht sichtbar. "benutzerverwaltung" ist bewusst NICHT hier
-- steuerbar, sondern serverseitig hart auf role_key='admin' verdrahtet.
CREATE TABLE ui_role_tiles (
    role_key TEXT NOT NULL REFERENCES ui_roles(role_key) ON DELETE CASCADE,
    tile_key TEXT NOT NULL,
    PRIMARY KEY (role_key, tile_key)
);

-- Cookie-Token -> aktive Rolle im Browser (Rollenwechsel statt Login).
CREATE TABLE ui_role_sessions (
    token        TEXT PRIMARY KEY,
    role_key     TEXT NOT NULL REFERENCES ui_roles(role_key) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO ui_roles (role_key, display_name, can_correct_results, sort_order) VALUES
    ('admin',     'Admin',      TRUE,  0),
    ('developer', 'Entwickler', FALSE, 1),
    ('anwender',  'Anwender',   FALSE, 2),
    ('revisor',   'Revisor',    TRUE,  3);

INSERT INTO ui_role_tiles (role_key, tile_key)
SELECT 'admin', t FROM unnest(ARRAY['lanes','stammdaten','disciplines','wettkampf',
    'standaktion','ergebnisse','simulator','auswertung','settings','archiv']) t
UNION ALL
SELECT 'developer', t FROM unnest(ARRAY['lanes','stammdaten','disciplines','wettkampf',
    'standaktion','ergebnisse','simulator','auswertung','archiv']) t
UNION ALL
SELECT 'anwender', t FROM unnest(ARRAY['lanes','stammdaten','disciplines','wettkampf',
    'standaktion','ergebnisse','auswertung','archiv']) t
UNION ALL
SELECT 'revisor', t FROM unnest(ARRAY['lanes','stammdaten','disciplines','wettkampf',
    'standaktion','ergebnisse','auswertung','archiv']) t;
