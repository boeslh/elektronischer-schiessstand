-- Saison-Discriminator: eine Vereinsabend-Saison ist eine spezielle
-- preisschiessen-Zeile (siehe Konzept .claude/plans/wise-scribbling-abelson.md),
-- um Scheiben-/Wertungs-/Anzeige-/Export-Infrastruktur wiederzuverwenden.
ALTER TABLE preisschiessen ADD COLUMN kind TEXT NOT NULL DEFAULT 'preisschiessen'
    CHECK (kind IN ('preisschiessen', 'vereinsabend_saison'));

-- ISO-Wochentage 1=Mo..7=So (mehrere moeglich, z.B. Di+Do). Reiner Filter in
-- der Auswertelogik ("nur Scheiben, deren Schiesstag auf einen dieser
-- Wochentage faellt, zaehlen") - KEIN Terminkalender.
ALTER TABLE preisschiessen ADD COLUMN saison_wochentage SMALLINT[] NOT NULL DEFAULT '{}';

-- Gate fuer die "Als Vor-/Nachschuss markieren"-Aktion in der Bedienoberflaeche.
ALTER TABLE preisschiessen ADD COLUMN vorschiessen_erlaubt BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE preisschiessen ADD COLUMN nachschiessen_erlaubt BOOLEAN NOT NULL DEFAULT false;

-- NULL = alle zaehlenden Tage gehen in die Jahreswertung ein, sonst nur die
-- besten X Tage (analog ps_wertungen.anz_summe, aber auf Tage statt Scheiben
-- angewandt - siehe computePunkteSaison).
ALTER TABLE preisschiessen ADD COLUMN beste_x_abende SMALLINT;
