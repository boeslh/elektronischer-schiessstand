-- ps_teilnehmer.testdaten / ps_kaeufe.testdaten: vom Testdaten-Generator
-- "Teilnehmer & Ergebnisse fuer bestehende Schuetzen" (Entwicklung-Kachel,
-- siehe testdata_bestand.go) erzeugte Anmeldungen bzw. Kaeufe. Anders als
-- beim ersten Generator (eigene Testschuetzen, shooters.notes-Marker) sind
-- die Schuetzen hier echte Mitglieder - aufgeraeumt werden darf deshalb nur,
-- was hier als Testdaten markiert ist (CleanupTestdaten).
ALTER TABLE ps_teilnehmer ADD COLUMN testdaten BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE ps_kaeufe     ADD COLUMN testdaten BOOLEAN NOT NULL DEFAULT false;
