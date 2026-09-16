-- Dritter scoring_mode-Wert "teilnahme": eine Disziplin, die reine
-- Anwesenheit ohne echtes Schiessergebnis repraesentiert (Vereinsabend-
-- Jahreswertung, Anwesenheitsbonus ohne geschossene Scheibe). Eine darauf
-- basierende Scheibe geht beim Kauf sofort auf "beendet" (siehe
-- server/preisschiessen.go purchaseItem/createKaufEinheiten), optional mit
-- einem editierbaren Ringwert.
ALTER TABLE disciplines DROP CONSTRAINT disciplines_scoring_mode_check;
ALTER TABLE disciplines ADD CONSTRAINT disciplines_scoring_mode_check
    CHECK (scoring_mode IN ('elektronisch', 'papier', 'teilnahme'));
