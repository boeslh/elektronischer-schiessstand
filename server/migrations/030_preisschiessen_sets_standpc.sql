-- ============================================================================
-- 030_preisschiessen_sets_standpc.sql – Sets am Stand-PC buchbar (je
-- Preisschießen konfigurierbar)
--
-- Default false: Sets werden am Stand-PC nicht zum Nachbuchen angeboten
-- (sonst wird die Auswahl dort schnell unübersichtlich) - nur die Kasse im
-- Büro bietet Sets weiterhin uneingeschränkt an (Store.ListAngebot bleibt
-- dafür unverändert; gefiltert wird nur die Stand-PC-Sicht in
-- Store.GetLanePreisschiessenInfo).
-- ============================================================================
BEGIN;

ALTER TABLE preisschiessen ADD COLUMN sets_at_standpc BOOLEAN NOT NULL DEFAULT false;

COMMIT;
