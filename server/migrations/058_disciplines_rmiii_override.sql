-- Fest hinterlegter RM-III-Einstellungsstring je Disziplin, der die
-- automatische Berechnung (server/wertmaschine_config.go computeRMIIIConfig)
-- ersetzt, wenn gesetzt. Grund: die automatische Berechnung erwies sich beim
-- Test mit echter Hardware mehrfach als fehlerhaft - ein manuelles Override
-- (mit Vorschlagswert per "Standard einsetzen"-Button in disciplines.html)
-- ist der zuverlaessigere Weg fuer den produktiven Einsatz.
ALTER TABLE disciplines ADD COLUMN rmiii_config_override TEXT;
