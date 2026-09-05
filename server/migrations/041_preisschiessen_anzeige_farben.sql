-- ============================================================================
-- 041_preisschiessen_anzeige_farben.sql – Farbgestaltung für den
-- Display-Server (preisanzeige/): Hintergrund-/Schriftfarbe sowie
-- Tabellenfarbe für gerade/ungerade Zeilen. Gilt sowohl für die browsbare
-- Ergebnis-Website (site.go) als auch für den Kiosk-Modus (display.go) -
-- ein Preisschießen bekommt so ein einheitliches Erscheinungsbild an beiden
-- Stellen, siehe preisanzeige/farben.go.
--
-- Defaults entsprechen dem bisherigen Erscheinungsbild der Ergebnis-Website
-- (Cyan-Theme), damit sich für bereits laufende Preisschießen an deren Optik
-- nichts ändert, solange niemand die neuen Felder anpasst.
-- ============================================================================
BEGIN;

ALTER TABLE ps_anzeige_config
    ADD COLUMN bg_color TEXT NOT NULL DEFAULT '#80ffff',
    ADD COLUMN text_color TEXT NOT NULL DEFAULT '#000000',
    ADD COLUMN row_even_color TEXT NOT NULL DEFAULT '#eafeff',
    ADD COLUMN row_odd_color TEXT NOT NULL DEFAULT '#cbf5f8';

COMMIT;
