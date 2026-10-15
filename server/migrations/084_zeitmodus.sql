-- ============================================================================
-- 084_zeitmodus.sql – Schiesszeiten.
--
-- Disziplin (vorhanden seit 001): combined_time = TRUE -> match_time_s ist
-- die Gesamtzeit fuer Probe und Wertung; FALSE -> sighting_time_s (Probe)
-- und match_time_s (Wertung) getrennt.
-- events.time_mode: wie der Wettkampf mit der Zeit umgeht -
--   'uhrzeit' nur Uhrzeit, 'soft' Countdown ohne Wirkung (darf negativ
--   werden), 'hard' nach Ablauf wird kein Schuss mehr gewertet.
-- sessions.time_started_at: "Zeit starten" aus der Standsteuerung (sonst
--   gilt started_at); sessions.match_started_at: Wechsel in die Wertung.
-- ============================================================================
BEGIN;

ALTER TABLE events ADD COLUMN IF NOT EXISTS time_mode TEXT NOT NULL DEFAULT 'soft';
ALTER TABLE events DROP CONSTRAINT IF EXISTS events_time_mode_check;
ALTER TABLE events ADD CONSTRAINT events_time_mode_check CHECK (time_mode IN ('uhrzeit','soft','hard'));

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS time_started_at TIMESTAMPTZ;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS match_started_at TIMESTAMPTZ;

-- Wechsel in die Wertung: erster Wertungsschuss, falls nicht vorher gesetzt
CREATE OR REPLACE FUNCTION set_session_match_started_at() RETURNS trigger AS $$
BEGIN
    IF NEW.kind = 'match' THEN
        UPDATE sessions SET match_started_at = NEW.fired_at
        WHERE id = NEW.session_id AND (match_started_at IS NULL OR match_started_at > NEW.fired_at);
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_shots_set_match_started_at ON shots;
CREATE TRIGGER trg_shots_set_match_started_at
    AFTER INSERT ON shots
    FOR EACH ROW EXECUTE FUNCTION set_session_match_started_at();

COMMIT;
