-- lane_calibration_transfers: im Simulator zur Uebertragung freigegebene
-- Kalibrierung je Stand (siehe calibration_transfer.go). Der Stand-PC holt
-- sie in seiner Admin-Oberflaeche ab und schreibt sie per CAL IMPORT/SET auf
-- den ESP32 - bewusst manuell ausgeloest am Stand, nie automatisch vom
-- Server. Je Stand gilt nur die jeweils zuletzt freigegebene Kalibrierung
-- (eine neue Freigabe ersetzt die alte); applied_* halten fest, ob und auf
-- welchem Geraet sie uebernommen wurde.
CREATE TABLE lane_calibration_transfers (
    lane_no        INTEGER PRIMARY KEY,
    params         JSONB NOT NULL,        -- SimParams (server/simulator.go)
    note           TEXT NOT NULL DEFAULT '',
    created_by     TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    applied_at     TIMESTAMPTZ,
    applied_mac    TEXT,
    applied_ok     BOOLEAN,
    applied_result JSONB
);
