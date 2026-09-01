-- ============================================================================
-- 035_preisschiessen_vereinsauswertung.sql – Vereins-Auswertungen je
-- Preisschießen: Teilnehmer-Anzahl, Teilnehmer-Prozent (zu Vereinsmitgliedern),
-- Teilnehmer-Punkte (nach Zeitraum des ersten Beschusses)
--
-- Anders als die Teilnehmer-Wertungen (ps_wertungen/ps_wertung_ergebnisse,
-- siehe 031/032/033) sind das nur drei feste, nicht frei konfigurierbare
-- Auswertungsarten - konfigurierbar ist nur, welche Vereine teilnehmen
-- (ps_verein_teilnahme) und die Punkte-Staffelung nach Zeitraum
-- (ps_verein_punkte_zeitraum, max. 5 - per Anwendungslogik geprüft, nicht
-- per Constraint). Die Berechnung ist billig (Teilnehmerzahlen + ein MIN()
-- über Schusszeiten je Teilnehmer) und läuft deshalb live pro Anfrage, ohne
-- den Batch-Mechanismus aus preisschiessen_wertungen.go.
--
-- Gastgeber-Verein: belegt in allen drei Auswertungen unabhängig von seinen
-- Werten immer den letzten Platz (server/preisschiessen_vereine.go) - höchstens
-- ein Gastgeber je Preisschießen (partieller Unique-Index).
-- ============================================================================
BEGIN;

CREATE TABLE ps_verein_teilnahme (
    preisschiessen_id UUID NOT NULL REFERENCES preisschiessen(id) ON DELETE CASCADE,
    club_id           UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    gastgeber         BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (preisschiessen_id, club_id)
);

CREATE UNIQUE INDEX idx_ps_verein_teilnahme_gastgeber
    ON ps_verein_teilnahme (preisschiessen_id) WHERE gastgeber;

CREATE TABLE ps_verein_punkte_zeitraum (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    preisschiessen_id UUID NOT NULL REFERENCES preisschiessen(id) ON DELETE CASCADE,
    von               DATE NOT NULL,
    bis               DATE NOT NULL,
    punkte            NUMERIC(8,2) NOT NULL,
    sort_order        SMALLINT NOT NULL DEFAULT 0,
    CHECK (bis >= von)
);

CREATE INDEX idx_ps_verein_punkte_zeitraum_ps ON ps_verein_punkte_zeitraum (preisschiessen_id);

COMMIT;
