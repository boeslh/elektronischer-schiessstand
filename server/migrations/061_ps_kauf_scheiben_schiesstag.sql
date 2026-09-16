-- Effektiver Schiesstag einer gekauften Scheibe (Vereinsabend-Jahreswertung):
-- normalerweise das Datum von sessions.finished_at - das Kaufdatum
-- (ps_kauf_scheiben hat kein eigenes Datumsfeld, created_at existiert nicht
-- als separate Spalte) ist bewusst irrelevant, siehe Konzept
-- .claude/plans/wise-scribbling-abelson.md. schiesstag_override erlaubt eine
-- manuelle Korrektur (Vor-/Nachschiessen ODER reine Revisor-Korrektur);
-- ist_vor_nachschuss=true unterdrueckt zusaetzlich den Anwesenheitsbonus fuer
-- diesen Tag (der Schuetze war an diesem Tag nicht wirklich anwesend).
ALTER TABLE ps_kauf_scheiben ADD COLUMN schiesstag_override DATE;
ALTER TABLE ps_kauf_scheiben ADD COLUMN ist_vor_nachschuss BOOLEAN NOT NULL DEFAULT false;
