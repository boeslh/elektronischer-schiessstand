-- ============================================================================
-- 051_anzeige_config.sql – zentral konfigurierbare Anzeigen-Slots fuer den
-- Display-Server (preisanzeige), aufgerufen unter /anzeige/{id}. Typoffen
-- (params JSONB), damit spaetere Anzeige-Typen (Werbung, Info, Tabelle) ohne
-- neue Migration ergaenzt werden koennen. Slot 1 existiert immer als
-- Fallback-Ziel fuer ungueltige/fehlende IDs (siehe DeleteAnzeigeConfig in
-- server/store.go, das die Zeile beim Loeschen von Slot 1 sofort wieder
-- anlegt).
-- ============================================================================
BEGIN;

CREATE TABLE anzeige_config (
    id         SMALLINT PRIMARY KEY,
    type       TEXT NOT NULL,               -- 'stand' | 'runde' (spaeter weitere)
    name       TEXT NOT NULL DEFAULT '',
    params     JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO anzeige_config (id, type, name, params)
VALUES (1, 'stand', 'Standard', '{"grid_size":"auto","lane_nos":[]}');

INSERT INTO ui_role_tiles (role_key, tile_key) VALUES ('admin', 'anzeigen');

COMMIT;
