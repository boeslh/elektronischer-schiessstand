-- ============================================================================
-- 002_notify.sql – Live-Benachrichtigung bei neuen Schuessen
--
-- Der Server lauscht per LISTEN auf den Kanal 'shot_fired' und verteilt
-- neue Schuesse ohne Polling an alle verbundenen Browser (SSE).
-- Payload: kompaktes JSON mit session_id und den Anzeigewerten.
-- ============================================================================

CREATE OR REPLACE FUNCTION notify_shot() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('shot_fired', json_build_object(
        'shot_id',    NEW.id,
        'session_id', NEW.session_id,
        'shot_no',    NEW.shot_no,
        'x_mm',       NEW.x_mm,
        'y_mm',       NEW.y_mm,
        'ring',       NEW.ring,
        'decimal',    NEW.decimal_value,
        'inner_ten',  NEW.is_inner_ten
    )::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_shots_notify ON shots;
CREATE TRIGGER trg_shots_notify
    AFTER INSERT ON shots
    FOR EACH ROW EXECUTE FUNCTION notify_shot();
