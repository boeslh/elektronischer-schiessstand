-- ============================================================================
-- 033_preisschiessen_wertung_scheiben.sql – Faktor je Scheibe, echte Auswahl
-- statt Freitext-Namensliste
--
-- Bisher: ps_wertungen.scheiben_namen (TEXT[], Freitext-Namen, fehleranfällig
-- durch Tippfehler/Groß-Kleinschreibung) + EIN globaler faktor je Wertung.
-- Tatsächlich braucht jede Scheibe (z.B. LG vs. LP in einer kombinierten
-- Schüler/Jugend-Wertung) ihren EIGENEN Faktor (analog TEILER_FAKTOR/
-- RING_FAKTOR je Disziplin in gs26/backend/gsConfig.py).
--
-- Neu: ps_wertung_scheiben verknüpft eine Wertung direkt mit den echten,
-- im Preisschießen angelegten ps_scheiben-Zeilen (FK statt Namensvergleich)
-- und trägt den Faktor pro Scheibe.
-- ============================================================================
BEGIN;

CREATE TABLE ps_wertung_scheiben (
    wertung_id  UUID NOT NULL REFERENCES ps_wertungen(id) ON DELETE CASCADE,
    scheibe_id  UUID NOT NULL REFERENCES ps_scheiben(id) ON DELETE CASCADE,
    faktor      NUMERIC(6,4) NOT NULL DEFAULT 1,
    PRIMARY KEY (wertung_id, scheibe_id)
);

CREATE INDEX idx_ps_wertung_scheiben_scheibe ON ps_wertung_scheiben (scheibe_id);

-- Bestehende Konfiguration (Namensliste + globaler Faktor) automatisch nach
-- ps_wertung_scheiben übernehmen, bevor die alten Spalten entfallen.
INSERT INTO ps_wertung_scheiben (wertung_id, scheibe_id, faktor)
SELECT w.id, sc.id, w.faktor
FROM ps_wertungen w
JOIN ps_scheiben sc
  ON sc.preisschiessen_id = w.preisschiessen_id
 AND sc.name = ANY(w.scheiben_namen);

ALTER TABLE ps_wertungen DROP COLUMN scheiben_namen;
ALTER TABLE ps_wertungen DROP COLUMN faktor;

COMMIT;
