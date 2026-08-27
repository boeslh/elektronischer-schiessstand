-- ============================================================================
-- 013_shots_telegram_raw.sql
--   Firmware Rev 4.6.1: Positionsberechnung (inkl. Kalibrierung) laeuft jetzt
--   auf dem ESP32 selbst, nicht mehr auf dem Stand-PC. Das Telegramm liefert
--   x_um/y_um bereits fertig, dazu Rohdaten (air_ns) und Qualitaetswerte fuer
--   spaetere Analysen/Simulationen. reject-Telegramme (zu wenige Mics) sowie
--   shot-Telegramme mit pos_valid=0 werden ab jetzt ebenfalls gespeichert
--   (status='rejected'), landen aber nicht in Anzeige/Wertung.
--   raw_t_ns wird vom neuen Telegrammformat nicht mehr befuellt -> NOT NULL
--   entfernt (alte Zeilen behalten ihre Werte).
-- ============================================================================
BEGIN;

ALTER TABLE shots
    ALTER COLUMN raw_t_ns DROP NOT NULL,
    ADD COLUMN IF NOT EXISTS air_ns        JSONB,   -- alle Flanken je Mikrofon (ns)
    ADD COLUMN IF NOT EXISTS x_um          BIGINT,  -- Geraeteseitige Position, 0.001mm
    ADD COLUMN IF NOT EXISTS y_um          BIGINT,
    ADD COLUMN IF NOT EXISTS pos_res_um    BIGINT,  -- Rest-Fehler Stufe 1, 0.001mm
    ADD COLUMN IF NOT EXISTS precision_um  BIGINT,  -- RMS Kandidaten-Cluster, 0.001mm
    ADD COLUMN IF NOT EXISTS cluster_hits  SMALLINT,
    ADD COLUMN IF NOT EXISTS pos_valid     BOOLEAN,
    ADD COLUMN IF NOT EXISTS piezo_ns      BIGINT,  -- NULL wenn Piezo nicht ausgeloest
    ADD COLUMN IF NOT EXISTS piezo_ok      BOOLEAN, -- NULL wenn SET PIEZO=0
    ADD COLUMN IF NOT EXISTS clean         BOOLEAN, -- Qualitaetsschwellen erfuellt (nur type=shot)
    ADD COLUMN IF NOT EXISTS reject_reason TEXT,     -- "reason" aus reject-Telegramm bzw. pos_valid=0
    ADD COLUMN IF NOT EXISTS device_ts_ms  BIGINT;   -- "ts" aus dem Telegramm (Geraete-Uptime)

COMMIT;
