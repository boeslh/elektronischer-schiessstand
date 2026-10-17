-- ============================================================================
-- 001_init.sql – Online-Anzeige: Vereine, Benutzer, API-Schluessel und
-- veroeffentlichte Anzeigen (Preisschiessen, Rundenwettkaempfe).
-- ============================================================================
BEGIN;

CREATE TABLE IF NOT EXISTS vereine (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    slug        TEXT NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,40}$'),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Benutzer: verein_id NULL = Plattform-Administrator (verwaltet Vereine und
-- Benutzer), sonst Benutzer eines Vereins (Schluessel und Veroeffentlichungen
-- des eigenen Vereins).
CREATE TABLE IF NOT EXISTS users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    verein_id     UUID REFERENCES vereine(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at TIMESTAMPTZ
);

-- Anmeldungen: nur der SHA-256 des Cookie-Werts wird gespeichert.
CREATE TABLE IF NOT EXISTS user_sessions (
    token_hash  TEXT PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- API-Schluessel der Schiessstand-Server: Schluessel "ssk_<key_id>_<geheim>",
-- gespeichert wird nur der SHA-256 des Geheimteils.
CREATE TABLE IF NOT EXISTS api_keys (
    key_id       TEXT PRIMARY KEY,
    verein_id    UUID NOT NULL REFERENCES vereine(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    secret_hash  TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    last_used_ip TEXT,
    revoked_at   TIMESTAMPTZ
);

-- Veroeffentlichungen: je Verein, Art und Quell-ID (UUID auf dem
-- Schiessstand-Server) genau ein aktueller Stand (data = Momentaufnahme).
CREATE TABLE IF NOT EXISTS publications (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    verein_id     UUID NOT NULL REFERENCES vereine(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('preisschiessen', 'rwk')),
    source_id     UUID NOT NULL,
    title         TEXT NOT NULL,
    starts_on     DATE,
    status        TEXT NOT NULL DEFAULT '',
    data          JSONB NOT NULL,
    generated_at  TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (verein_id, kind, source_id)
);
CREATE INDEX IF NOT EXISTS idx_publications_verein ON publications (verein_id, kind, starts_on DESC);

COMMIT;
