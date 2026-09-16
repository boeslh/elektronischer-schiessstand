-- ============================================================================
-- 066_disciplines_rmiv_override.sql – manuelles Override fuer den RM-IV/
-- RMIII-Win-Konfigurationsstring (SCH=...;-Format, siehe wertmaschine_config.go
-- BuildDisagConfig case "rmiv") - analog zu rmiii_config_override
-- (Migration 058). Eine Papier-Disziplin kann so parallel an Staenden mit
-- unterschiedlichem Wertmaschinen-Protokoll (rmiii vs. rmiv/rmiii-win, je
-- Wertungs-PC einzeln konfigurierbar, siehe wertmaschine/config.go) genutzt
-- werden - beide Override-Strings sind unabhaengig voneinander, leer =
-- automatische Berechnung.
-- ============================================================================
BEGIN;

ALTER TABLE disciplines ADD COLUMN rmiv_config_override TEXT;

COMMIT;
