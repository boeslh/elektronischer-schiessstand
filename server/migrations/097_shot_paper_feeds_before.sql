-- ============================================================================
-- 097_shot_paper_feeds_before.sql – Leervorschub bei Scheibenwechsel.
--
-- Der Stand-PC loest bei jeder neuen Scheibe und beim Umschalten von Probe
-- auf Wertung einen zusaetzlichen Papiervorschub aus (ACTION TARGETCHANGE,
-- mehrfaches Umschalten ohne Schuss nur einmal). Der erste Schuss danach
-- traegt paper_feeds_before = Zahl dieser Leervorschuebe (0/1) - die
-- Bandkontrolle beruecksichtigt das freie Feld bei der Zuordnung.
-- ============================================================================
BEGIN;

ALTER TABLE shots
    ADD COLUMN IF NOT EXISTS paper_feeds_before SMALLINT NOT NULL DEFAULT 0;

COMMIT;
