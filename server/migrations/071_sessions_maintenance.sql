-- sessions.maintenance: Belegung mit dem "Wartungs-Schuetzen" (nur Admin,
-- Standsteuerung, siehe api.go assignLaneMaintenance). Technisch eine
-- anonyme Session (shooter_id NULL) - die Schuesse landen samt Rohdaten
-- (air_ns, piezo_ns) ganz normal in shots und dienen anschliessend im
-- Simulator als Grundlage fuer die Kalibrierung. Aus der normalen
-- Ergebnisuebersicht werden solche Sessions ausgeblendet (ListResults).
ALTER TABLE sessions ADD COLUMN maintenance BOOLEAN NOT NULL DEFAULT false;
