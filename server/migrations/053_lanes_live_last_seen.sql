BEGIN;

-- Persistiert den StandPC-Herzschlag (bisher nur im Server-Prozess-Speicher,
-- APIServer.liveStates) zusaetzlich in der DB, damit auch preisanzeige (ein
-- eigener Prozess ohne Zugriff auf den In-Memory-Zustand des Servers, siehe
-- preisanzeige/standanzeige.go loadActiveLaneNos) erkennen kann, welche
-- Staende gerade online sind - fuer die automatische Standanzeige-Auswahl.
ALTER TABLE lanes ADD COLUMN IF NOT EXISTS live_last_seen_at TIMESTAMPTZ;

COMMIT;
