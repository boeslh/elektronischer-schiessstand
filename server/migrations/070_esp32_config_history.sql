-- esp32_config_history: Verlauf der ESP32-Gerätekonfiguration je Stand (siehe
-- esp32_config_history.go). Der Stand-PC meldet regelmaessig die komplette
-- Konfiguration (SHOW) - gespeichert werden aber NUR die Werte, die sich
-- gegenueber dem bisherigen Stand geaendert haben (changes = neue Werte,
-- previous = alte Werte derselben Schluessel), plus eine Zeile, wenn eine
-- andere Platine (mac) meldet. Der erste Eintrag eines Stands enthaelt
-- zwangslaeufig alle Werte. Der Zustand zu einem Zeitpunkt ergibt sich durch
-- Aufsummieren aller Eintraege bis dorthin - Grundlage fuer "Konfiguration
-- wiederherstellen" (z.B. nach Platinentausch) und fuer die Nachvollzieh-
-- barkeit, wann Werte geaendert oder neu kalibriert wurde.
CREATE TABLE esp32_config_history (
    id          BIGSERIAL PRIMARY KEY,
    lane_no     INTEGER NOT NULL,
    mac         TEXT NOT NULL,
    changes     JSONB NOT NULL,   -- {"OFS1":"834", ...} Firmware-SET-Schluessel -> neuer Wert
    previous    JSONB NOT NULL,   -- {"OFS1":"812", ...} bisheriger Wert (fehlt beim ersten Eintrag)
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_esp32_config_history_lane ON esp32_config_history (lane_no, id);
