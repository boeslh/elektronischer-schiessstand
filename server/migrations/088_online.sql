-- ============================================================================
-- 088_online.sql – Online-Veroeffentlichung (Online-Anzeige im Internet,
-- siehe online/ und online_sync.go).
--
-- app_settings: Adresse des Online-Servers, API-Schluessel des Vereins,
-- optional ein eigenes CA-Zertifikat (PEM) fuer selbst signierte Server,
-- Ein/Aus. online_publications: was uebertragen wird (Preisschiessen bzw.
-- Rundenwettkampf), in welchem Takt, und der letzte Uebertragungsstand.
-- ============================================================================
BEGIN;

ALTER TABLE app_settings ADD COLUMN IF NOT EXISTS online_url     TEXT    NOT NULL DEFAULT '';
ALTER TABLE app_settings ADD COLUMN IF NOT EXISTS online_api_key TEXT    NOT NULL DEFAULT '';
ALTER TABLE app_settings ADD COLUMN IF NOT EXISTS online_ca_pem  TEXT    NOT NULL DEFAULT '';
ALTER TABLE app_settings ADD COLUMN IF NOT EXISTS online_enabled BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE IF NOT EXISTS online_publications (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind            TEXT NOT NULL CHECK (kind IN ('preisschiessen', 'rwk')),
    source_id       UUID NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    -- Takt in Sekunden: Preisschiessen 60..300, Rundenwettkampf 3..9
    interval_s      INTEGER NOT NULL CHECK (interval_s BETWEEN 3 AND 300),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_attempt_at TIMESTAMPTZ,
    last_success_at TIMESTAMPTZ,
    last_error      TEXT NOT NULL DEFAULT '',
    last_hash       TEXT NOT NULL DEFAULT '',
    online_path     TEXT NOT NULL DEFAULT '',
    UNIQUE (kind, source_id)
);

COMMIT;
