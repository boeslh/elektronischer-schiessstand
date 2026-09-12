-- ============================================================================
-- 055_disciplines_scoring_mode.sql – Elektronisch vs. Papierscheibe.
--
-- scoring_mode='papier' markiert Disziplinen, die auf Papierscheiben
-- geschossen und ueber eine Disag-Wertmaschine oder manuell erfasst werden
-- (statt am elektronischen Stand-PC) - siehe Konzept
-- .claude/plans/wise-scribbling-abelson.md. wertmaschine_caliber_mm liefert
-- das "KAL="-Feld im RM-IV-Konfigstring fuer Zentralfeuer-Scheibentypen,
-- bleibt fuer alle anderen (Luftgewehr/-pistole etc.) NULL.
-- ============================================================================
BEGIN;

ALTER TABLE disciplines
    ADD COLUMN scoring_mode TEXT NOT NULL DEFAULT 'elektronisch'
        CHECK (scoring_mode IN ('elektronisch', 'papier')),
    ADD COLUMN wertmaschine_caliber_mm NUMERIC(4,2);

COMMIT;
