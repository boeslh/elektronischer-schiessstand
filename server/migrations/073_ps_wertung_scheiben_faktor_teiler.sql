-- ps_wertung_scheiben.faktor_teiler: eigener Teiler-Faktor je Scheibe fuer
-- typ='punkte_saison'-Wertungen (Ring und Teiler brauchen dort
-- unterschiedliche Normierung - ein Faktor > 1 hebt den Ringwert an,
-- verschlechtert aber gleichzeitig den Teiler, da dort kleiner besser ist).
-- faktor bleibt der Ring-Faktor (bzw. bei allen anderen Wertungstypen der
-- einzige Faktor). Vorbelegung mit dem bisherigen faktor, damit bestehende
-- Auswertungen unveraendert bleiben.
ALTER TABLE ps_wertung_scheiben ADD COLUMN faktor_teiler NUMERIC(6,4) NOT NULL DEFAULT 1;
UPDATE ps_wertung_scheiben SET faktor_teiler = faktor;
