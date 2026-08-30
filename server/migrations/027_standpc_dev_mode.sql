-- Globaler Entwicklermodus fuer Stand-PCs: erlaubt das Setzen von Schuessen
-- per Mausklick auf die Zielscheibe (Testzwecke, ohne Hardware). Singleton-
-- Tabelle (ein Flag pro Spalte, analog lanes.standpc_url) statt generischem
-- Key-Value-Speicher, da es bislang keinen solchen im Schema gibt.
CREATE TABLE app_settings (
    id                SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    standpc_dev_mode  BOOLEAN NOT NULL DEFAULT FALSE
);
INSERT INTO app_settings (id) VALUES (1);
