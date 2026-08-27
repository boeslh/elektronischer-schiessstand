-- ============================================================================
-- 015_shots_auto_started_at.sql
--   sessions.started_at wurde bisher nur gesetzt, wenn die Aufsicht den
--   Status explizit auf 'sighting'/'match' umschaltet (Store.SetSessionStatus,
--   store.go). Schuesse koennen aber schon vorher eintreffen (Stand-PC
--   schreibt direkt in "shots", siehe standpc/db.go) - die Session blieb
--   dann ohne started_at, obwohl bereits geschossen wurde: Die Ergebnisse-
--   Ansicht filtert per Datum auf started_at und zeigte solche Sessions
--   nicht an; das Datum selbst erschien im Frontend als "Invalid Date"
--   (leerer String, siehe server/store.go ListResults + web/ergebnisse.html).
--
--   Trigger setzt started_at jetzt automatisch beim ERSTEN Schuss einer
--   Session, mit dessen fired_at (nicht now()) - wichtig, weil Schuesse
--   auch zeitversetzt nachgeschrieben werden koennen (Resync nach einem
--   DB-Ausfall, siehe standpc/db.go ResyncShotsFromLog/NewDBWriter).
--   LEAST(...) statt reinem COALESCE haelt robust den fruehesten bekannten
--   fired_at fest, auch falls Schuesse nicht streng chronologisch eintreffen.
-- ============================================================================
BEGIN;

CREATE OR REPLACE FUNCTION set_session_started_at() RETURNS trigger AS $$
BEGIN
    UPDATE sessions
    SET started_at = LEAST(COALESCE(started_at, NEW.fired_at), NEW.fired_at)
    WHERE id = NEW.session_id;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_shots_set_started_at
    AFTER INSERT ON shots
    FOR EACH ROW EXECUTE FUNCTION set_session_started_at();

-- Backfill: bereits vorhandene Sessions mit Schuessen, aber noch ohne (oder
-- mit zu spaetem) started_at auf den fruehesten tatsaechlichen Schusszeit-
-- punkt setzen.
UPDATE sessions se
SET started_at = sub.min_fired_at
FROM (
    SELECT session_id, MIN(fired_at) AS min_fired_at
    FROM shots
    GROUP BY session_id
) sub
WHERE sub.session_id = se.id
  AND (se.started_at IS NULL OR se.started_at > sub.min_fired_at);

COMMIT;
