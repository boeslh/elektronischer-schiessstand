-- ============================================================================
-- 022_preisschiessen_negativ_guthaben.sql – Preisschießen: Minus-Guthaben
--
-- Der Kaufvorgang an der Kasse soll vom Bezahlvorgang entkoppelt werden:
-- ein Teilnehmer darf mehrere Scheiben/Sets kaufen, auch wenn das Guthaben
-- dabei ins Minus rutscht, und erst danach bewusst bezahlen. Wie weit das
-- Minus je Preisschießen gehen darf, ist konfigurierbar (0 = wie bisher
-- kein Minus erlaubt).
-- ============================================================================
BEGIN;

ALTER TABLE preisschiessen
    ADD COLUMN max_negative_guthaben NUMERIC(10,2) NOT NULL DEFAULT 0;

COMMENT ON COLUMN preisschiessen.max_negative_guthaben IS
    'Wie weit das Teilnehmer-Guthaben in diesem Preisschießen ins Minus rutschen darf (Betrag in EUR, 0 = kein Minus erlaubt).';

COMMIT;
