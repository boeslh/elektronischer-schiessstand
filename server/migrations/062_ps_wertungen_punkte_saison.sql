-- ============================================================================
-- 062_ps_wertungen_punkte_saison.sql – Punkte-Jahreswertung fuer Vereinsabend/
-- Schiesssaison (siehe .claude/plans/wise-scribbling-abelson.md).
--
-- Ein neuer ps_wertungen.typ='punkte_saison' wertet nicht wie meister/punkt
-- einzelne Scheiben, sondern EINEN kombinierten Punktewert je effektivem
-- Schiesstag (siehe server/vereinsabend_wertungen.go): Ring-Punkte (Lookup
-- in ps_wertung_punkte_ring) + Teiler-Punkte (Lookup in
-- ps_wertung_punkte_teiler) + anwesenheit_punkte (nur wenn an diesem Tag
-- nicht vor-/nachgeschossen wurde). Die bestehende ps_wertung_scheiben-
-- Zuordnung (Scheiben-Typen + Faktor + serien_modus, Migration 063)
-- bestimmt weiterhin, welche Scheiben in eine Wertung eingehen - hier
-- zusaetzlich, ob eine Ring-/Teiler-Auswertung ueberhaupt sinnvoll ist
-- (bei einer reinen Teilnahme-Scheibe bleiben beide Lookups leer/0).
-- ============================================================================
BEGIN;

ALTER TABLE ps_wertungen DROP CONSTRAINT ps_wertungen_typ_check;
ALTER TABLE ps_wertungen ADD CONSTRAINT ps_wertungen_typ_check
    CHECK (typ IN ('meister', 'punkt', 'adler', 'punkte_saison'));

ALTER TABLE ps_wertungen ADD COLUMN anwesenheit_punkte NUMERIC(6,2) NOT NULL DEFAULT 0;

-- ab_wert = untere Schranke inklusiv (z.B. 89 Ringe = 8 Punkte -> ab 89
-- Ringen gibt es mindestens 8 Punkte); es gilt jeweils die hoechste
-- erreichte Schranke (siehe lookupPunkteAb in vereinsabend_wertungen.go).
CREATE TABLE ps_wertung_punkte_ring (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wertung_id UUID NOT NULL REFERENCES ps_wertungen(id) ON DELETE CASCADE,
    ab_wert    NUMERIC(6,2) NOT NULL,
    punkte     NUMERIC(6,2) NOT NULL,
    UNIQUE (wertung_id, ab_wert)
);

-- bis_wert = obere Schranke inklusiv (z.B. Teiler 34,4 = 5 Punkte -> bis
-- Teiler 34,4 gibt es 5 Punkte); es gilt jeweils die niedrigste (engste)
-- erreichte Schranke (siehe lookupPunkteBis).
CREATE TABLE ps_wertung_punkte_teiler (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wertung_id UUID NOT NULL REFERENCES ps_wertungen(id) ON DELETE CASCADE,
    bis_wert   NUMERIC(8,2) NOT NULL,
    punkte     NUMERIC(6,2) NOT NULL,
    UNIQUE (wertung_id, bis_wert)
);

COMMIT;
