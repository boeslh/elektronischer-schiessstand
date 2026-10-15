-- ============================================================================
-- 086_lane_ui_lock.sql – Bedienung an der Schuetzenanzeige sperren.
--
-- lanes.ui_locked: in der Standsteuerung gesetzt (z.B. fuer den Wettkampf);
-- der Stand-PC laesst dann nur noch das Ansehen (Zoom) zu - Umschalten,
-- Stand freigeben, Disziplin, Ergebnis-Ausgabe und Preisschiessen-Menue sind
-- gesperrt. Bleibt gesetzt, bis es in der Standsteuerung aufgehoben wird.
-- ============================================================================
BEGIN;
ALTER TABLE lanes ADD COLUMN IF NOT EXISTS ui_locked BOOLEAN NOT NULL DEFAULT FALSE;
COMMIT;
