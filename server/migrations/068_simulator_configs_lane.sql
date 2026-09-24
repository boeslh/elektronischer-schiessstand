-- simulator_configs.lane_no: Bahn, aus der die Kalibrier-Schuesse beim
-- Speichern stammten (aus der gerade im Simulator geladenen Session
-- uebernommen, siehe api.go saveSimulatorConfig) - ermoeglicht die Auswahl
-- gespeicherter Kalibrierungen nach Stand UND Datum/Uhrzeit (created_at gab
-- es schon) statt nur nach freiem Namen. NULL bei aelteren, vor dieser
-- Migration gespeicherten Configs (unbekannte Bahn).
ALTER TABLE simulator_configs ADD COLUMN lane_no INTEGER;
