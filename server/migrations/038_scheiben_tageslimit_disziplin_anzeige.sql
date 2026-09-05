-- ============================================================================
-- 038_scheiben_tageslimit_disziplin_anzeige.sql
--
-- ps_scheiben: zwei neue Eigenschaften
--   - auswertung_unsichtbar: Scheibe darf weiterhin gekauft/beschossen werden,
--     ihre Schüsse fließen aber in KEINE Teilnehmer-Wertung (ps_wertungen)
--     ein, solange das Flag gesetzt ist - siehe
--     server/preisschiessen_wertungen.go loadWertungRows. Typischer
--     Anwendungsfall: gegen Ende des Preisschießens gesetzt, um laufende
--     Zwischenstände nicht in der Auswertung zu verändern.
--   - max_pro_tag: wie max_pro_teilnehmer (025_preisschiessen_max_pro_
--     teilnehmer.sql), aber je Kalendertag statt über die gesamte Laufzeit -
--     "Tag" meint das Datum, keine rollierende 24h-Spanne, siehe
--     Store.purchaseItem.
--
-- disciplines: eine neue Eigenschaft
--   - anzeige: steuert, was am Stand-PC (und in davon abgeleiteten Live-
--     Ansichten) während des Schießens tatsächlich angezeigt wird -
--     'voll' (Standard, heutiges Verhalten), 'teilverdeckt' (Trefferbild
--     grafisch, aber Ring/Zehntel/Teiler als "-"), 'verdeckt' (auch kein
--     Trefferbild, nur "-"). Siehe standpc/web.go maskShot.
-- ============================================================================
BEGIN;

ALTER TABLE ps_scheiben
    ADD COLUMN auswertung_unsichtbar BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN max_pro_tag INT;

ALTER TABLE ps_scheiben ADD CONSTRAINT ps_scheiben_max_pro_tag_check
    CHECK (max_pro_tag IS NULL OR max_pro_tag > 0);

ALTER TABLE disciplines
    ADD COLUMN anzeige TEXT NOT NULL DEFAULT 'voll'
    CHECK (anzeige IN ('voll', 'teilverdeckt', 'verdeckt'));

COMMIT;
