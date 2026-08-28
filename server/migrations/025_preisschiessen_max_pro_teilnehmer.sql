-- ============================================================================
-- 025_preisschiessen_max_pro_teilnehmer.sql – maximale Kaufmenge je
-- Teilnehmer für Scheiben und Sets
--
-- NULL = unbegrenzt. Wird beim Kauf (Store.kaufen) gegen die Anzahl bereits
-- getätigter (nicht zurückgegebener) Käufe dieser Scheibe/dieses Sets durch
-- denselben Teilnehmer geprüft, und im Angebot (Store.ListAngebot) bereits
-- herausgefiltert, sobald das Limit erreicht ist.
-- ============================================================================
BEGIN;

ALTER TABLE ps_scheiben ADD COLUMN max_pro_teilnehmer INT;
ALTER TABLE ps_sets     ADD COLUMN max_pro_teilnehmer INT;

ALTER TABLE ps_scheiben ADD CONSTRAINT ps_scheiben_max_pro_teilnehmer_check
    CHECK (max_pro_teilnehmer IS NULL OR max_pro_teilnehmer > 0);
ALTER TABLE ps_sets ADD CONSTRAINT ps_sets_max_pro_teilnehmer_check
    CHECK (max_pro_teilnehmer IS NULL OR max_pro_teilnehmer > 0);

COMMIT;
