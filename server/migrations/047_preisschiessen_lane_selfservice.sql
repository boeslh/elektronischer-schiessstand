-- ============================================================================
-- 047_preisschiessen_lane_selfservice.sql – Stand für Preisschießen-
-- Selbstbedienung reservieren, ohne dabei schon einen Teilnehmer festzulegen.
-- Der Teilnehmer sucht sich am Stand-PC selbst aus (siehe
-- Store.SearchTeilnehmerForSelfServiceLane/SelectTeilnehmerAtLane in
-- server/preisschiessen.go).
--
-- Bewusst unabhängig von ps_lane_pending (migrations/026): diese Tabelle
-- beschreibt den DAUERHAFTEN "Stand X läuft im Selbstbedienungsmodus für
-- Preisschießen Y"-Zustand, der über mehrere Teilnehmer-Durchgänge hinweg
-- bestehen bleibt - "Stand freigeben" räumt nur den transienten
-- ps_lane_pending-Zustand ab, diese Zeile bleibt dabei erhalten. Erst die
-- bewusste Stand-PC-Aktion "Preisschießmodus verlassen" (oder die
-- Büro-Übersicht) löscht sie wieder.
-- ============================================================================
BEGIN;

CREATE TABLE ps_lane_selfservice (
    lane_id           UUID PRIMARY KEY REFERENCES lanes(id) ON DELETE CASCADE,
    preisschiessen_id UUID NOT NULL REFERENCES preisschiessen(id) ON DELETE CASCADE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMIT;
