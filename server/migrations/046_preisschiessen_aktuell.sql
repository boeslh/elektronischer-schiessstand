-- ============================================================================
-- 046_preisschiessen_aktuell.sql – "Aktuell"-Flag je Preisschießen: höchstens
-- eines kann gleichzeitig gesetzt sein (partieller Unique-Index), gesetzt
-- über die Grunddaten im Verwaltung-Tab (siehe UpdatePreisschiessen, das beim
-- Setzen automatisch alle anderen zurücksetzt).
--
-- Wirkt sich auf den Display-Server aus: /ps/aktuell/... löst immer auf das
-- Preisschießen mit aktuell=true auf (siehe preisanzeige/site.go
-- handleAktuellRedirect) - Clients (insbesondere Kiosk-Bildschirme) können so
-- einen festen Pfad verwenden, unabhängig von der jeweiligen Preisschießen-ID.
-- ============================================================================
BEGIN;

ALTER TABLE preisschiessen
    ADD COLUMN aktuell BOOLEAN NOT NULL DEFAULT false;

CREATE UNIQUE INDEX idx_preisschiessen_aktuell ON preisschiessen (aktuell) WHERE aktuell;

COMMIT;
