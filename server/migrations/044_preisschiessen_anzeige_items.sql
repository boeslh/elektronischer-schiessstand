-- ============================================================================
-- 044_preisschiessen_anzeige_items.sql – erlaubt neben Teilnehmer-Wertungen
-- auch die drei festen Vereins-Auswertungen (Anzahl/Prozent/Punkte) in der
-- Anzeige-Konfiguration auszuwählen, damit sie im Kiosk-Modus des
-- Display-Servers mit rotieren (siehe preisanzeige/display.go).
--
-- Ersetzt die bisherige wertung_ids (UUID[], nur Teilnehmer-Wertungen) durch
-- anzeige_items (TEXT[]) mit demselben Schlüsselschema wie die
-- Listen-Navigation der browsbaren Ergebnis-Website (site.go loadSidebar):
-- "wertung:<uuid>" bzw. "verein:anzahl"/"verein:prozent"/"verein:punkte".
-- ============================================================================
BEGIN;

ALTER TABLE ps_anzeige_config
    ADD COLUMN anzeige_items TEXT[] NOT NULL DEFAULT '{}';

UPDATE ps_anzeige_config
    SET anzeige_items = ARRAY(SELECT 'wertung:' || x::text FROM unnest(wertung_ids) AS x);

ALTER TABLE ps_anzeige_config DROP COLUMN wertung_ids;

COMMIT;
