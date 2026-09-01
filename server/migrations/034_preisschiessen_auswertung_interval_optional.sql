-- ============================================================================
-- 034_preisschiessen_auswertung_interval_optional.sql – Preisschießen-
-- Auswertung: automatische Hintergrundberechnung abschaltbar machen
--
-- Bisher zwang interval_seconds (NOT NULL, 300-900) jedes Preisschießen zu
-- einer laufenden Hintergrundberechnung (RunAuswertungScheduler,
-- server/preisschiessen_wertungen.go). Für die meisten Preisschießen (noch
-- in Vorbereitung, oder abgeschlossen und nur noch zur Ansicht) ist das
-- unnötige Last auf den Worker-Instanzen - eine Berechnung soll dort nur
-- gezielt per "Jetzt neu berechnen" (claimAuswertungJobNow, unverändert)
-- ausgelöst werden.
--
-- NULL = keine automatische Berechnung, ab jetzt der Default für neu
-- angelegte Preisschießen. claimAuswertungJob (die periodische Job-Auswahl)
-- filtert NULL-Zeilen künftig heraus.
-- ============================================================================
BEGIN;

ALTER TABLE ps_auswertung_status
    ALTER COLUMN interval_seconds DROP NOT NULL,
    ALTER COLUMN interval_seconds DROP DEFAULT;

ALTER TABLE ps_auswertung_status
    DROP CONSTRAINT ps_auswertung_status_interval_seconds_check;
ALTER TABLE ps_auswertung_status
    ADD CONSTRAINT ps_auswertung_status_interval_seconds_check
    CHECK (interval_seconds IS NULL OR interval_seconds BETWEEN 300 AND 900);

COMMENT ON COLUMN ps_auswertung_status.interval_seconds IS
    'NULL = keine automatische Hintergrundberechnung (Default) - nur manueller Trigger ("Jetzt neu berechnen").';

COMMIT;
