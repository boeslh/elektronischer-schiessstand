-- ============================================================================
-- 032_preisschiessen_wertungen_summe.sql – Begriffsklärung: "Prämie" -> "Summe"
--
-- "Prämie" war irreführend benannt und wurde mit "Preisgeld" verwechselt.
-- Tatsächlich geht es um die Unterscheidung Einzelwertung (nur die beste
-- Scheibe/der beste Schuss zählt) vs. Summenwertung (mehrere Scheiben/
-- Schüsse werden addiert) - Preisgeld ist ein eigenständiges, hier noch
-- nicht abgebildetes Thema und kann sowohl für Einzel- als auch für
-- Summenwertungen vergeben werden.
--
-- Eine Einzelwertung ist einfach eine Summenwertung mit anz_summe=1, braucht
-- also kein eigenes Feld/Konzept - deshalb entfaellt auch die bisherige
-- zweite Platzierung (platz_p) wieder: es gibt nur noch EINE Platzierung je
-- Wertung, gesteuert allein über anz_summe (siehe
-- server/preisschiessen_wertungen.go, computeMeisterPunkt).
--
-- Das bisherige Bool-Feld ps_wertungen.praemie ("Prämie/Preisgeld
-- vorgesehen") war unbenutzt (keine Berechnungslogik las es) und entfällt
-- ersatzlos - ein künftiges Preisgeld-Feature bekommt vermutlich eine eigene
-- Platz->Betrag-Tabelle statt eines Bool-Flags hier.
-- ============================================================================
BEGIN;

ALTER TABLE ps_wertungen RENAME COLUMN anz_praemie TO anz_summe;
ALTER TABLE ps_wertungen DROP COLUMN praemie;

ALTER TABLE ps_wertung_ergebnisse RENAME COLUMN summe_praemie TO summe;
ALTER TABLE ps_wertung_ergebnisse DROP COLUMN platz_p;

COMMIT;
