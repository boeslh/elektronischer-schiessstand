-- ============================================================================
-- 019_shooter_classes_import.sql – Sportklassen: Import-Felder ergaenzen
--
-- shooter_classes war bisher ungenutzt (keine UI, keine Zeilen). Ergaenzt um
-- die Felder aus dem CSV-Export des Verwaltungstools:
--   KLASSENNR   -> code (bereits vorhanden)
--   KLASSE      -> name (bereits vorhanden)
--   KlasseKURZ  -> short_name (neu)
--   ALTERVON    -> min_age (bereits vorhanden)
--   ALTERBIS    -> max_age (bereits vorhanden)
--   TYP         -> type (neu, SMALLINT: 0=Kugel, 1=Bogen, 2=Kugel Auflage)
--   KLASSENTYP  -> sex (Geschlecht, SMALLINT statt bisher CHAR(1):
--                 0=weiblich, 1=maennlich, NULL=offen - wie im Export)
--
-- sex wird von CHAR(1) auf SMALLINT umgestellt, um die numerischen Codes aus
-- dem Export unveraendert zu speichern (Anzeige uebersetzt clientseitig).
-- Tabelle ist leer, daher direkte Spaltenaenderung ohne Datenmigration.
-- ============================================================================
BEGIN;

ALTER TABLE shooter_classes
    ADD COLUMN short_name TEXT,
    ADD COLUMN type       SMALLINT;

ALTER TABLE shooter_classes DROP COLUMN sex;
ALTER TABLE shooter_classes ADD COLUMN sex SMALLINT;

COMMIT;
