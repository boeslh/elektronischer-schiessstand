-- ============================================================================
-- 036_preisschiessen_gewinne.sql – Gewinne (Geldbeträge/Sachpreise) je
-- Auswertungsliste und Platz
--
-- Eine "Auswertungsliste" ist entweder eine Teilnehmer-Wertung (ps_wertungen,
-- Meister/Punkt/Adler) oder eine der drei festen Vereins-Auswertungen
-- (Anzahl/Prozent/Punkte, siehe preisschiessen_vereine.go) - genau eine der
-- beiden Referenzen ist je Zeile gesetzt (XOR-Check), niemals beide oder
-- keine. Je Platz ein Geldbetrag und/oder ein Sachpreis-Text (mind. eines
-- von beiden). Gespeichert wird immer listenweise (Store.SetGewinneForListe
-- ersetzt komplett die Platz-Zeilen EINER Liste), analog zum DELETE+INSERT-
-- Muster von ps_verein_punkte_zeitraum.
-- ============================================================================
BEGIN;

CREATE TABLE ps_gewinne (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    preisschiessen_id UUID NOT NULL REFERENCES preisschiessen(id) ON DELETE CASCADE,
    wertung_id        UUID REFERENCES ps_wertungen(id) ON DELETE CASCADE,
    verein_typ        TEXT CHECK (verein_typ IN ('anzahl','prozent','punkte')),
    platz             SMALLINT NOT NULL CHECK (platz >= 1),
    betrag            NUMERIC(10,2),
    sachpreis         TEXT,
    CHECK ((wertung_id IS NOT NULL) <> (verein_typ IS NOT NULL)),
    CHECK (betrag IS NOT NULL OR sachpreis IS NOT NULL)
);

CREATE INDEX idx_ps_gewinne_ps      ON ps_gewinne (preisschiessen_id);
CREATE INDEX idx_ps_gewinne_wertung ON ps_gewinne (wertung_id) WHERE wertung_id IS NOT NULL;

COMMIT;
