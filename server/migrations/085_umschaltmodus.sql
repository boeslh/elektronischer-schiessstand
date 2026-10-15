-- ============================================================================
-- 085_umschaltmodus.sql – Umschalten Probe -> Wertung je Wettkampf und
-- Vorgaben fuer neue Wettkaempfe.
--
-- events.switch_mode:
--   'individuell'  jeder Schuetze schaltet selbst um (bei Gesamtzeit immer)
--   'zentral'      Zeitstart und Umschalten nur ueber die Standsteuerung; nach
--                  Ablauf der Probezeit kein Schuss bis zur Wertungsfreigabe
--   'automatisch'  nach Ablauf der Probezeit automatisch Wertung
-- app_settings.default_time_mode / default_switch_mode: Vorbelegung neuer
-- Wettkaempfe (Einstellungen).
-- ============================================================================
BEGIN;

ALTER TABLE events ADD COLUMN IF NOT EXISTS switch_mode TEXT NOT NULL DEFAULT 'individuell';
ALTER TABLE events DROP CONSTRAINT IF EXISTS events_switch_mode_check;
ALTER TABLE events ADD CONSTRAINT events_switch_mode_check
    CHECK (switch_mode IN ('individuell','zentral','automatisch'));

ALTER TABLE app_settings ADD COLUMN IF NOT EXISTS default_time_mode TEXT NOT NULL DEFAULT 'soft';
ALTER TABLE app_settings ADD COLUMN IF NOT EXISTS default_switch_mode TEXT NOT NULL DEFAULT 'individuell';

COMMIT;
