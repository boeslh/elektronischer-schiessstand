-- ============================================================================
-- 057_lanes_virtual.sql – Virtuelle Bahn fuer manuelle/Wertmaschinen-Sessions.
--
-- sessions.lane_id/calibration_id sind NOT NULL - eine virtuelle Bahn dient
-- nur dazu, diese Fremdschluessel fuer eine Session zu erfuellen, die nie
-- wirklich an einer echten Bahn geschossen wurde (manuelle Eingabe oder
-- Disag-Wertmaschine, siehe Konzept
-- .claude/plans/wise-scribbling-abelson.md). Wird aus der normalen
-- Stand-Uebersicht/Automatik-Auswahl ausgeschlossen; eine Papier-Disziplin
-- darf NUR auf eine virtuelle Bahn zugewiesen werden (server/store.go
-- assignLane), eine elektronische Disziplin dagegen auch auf eine
-- virtuelle Bahn (kein Grund, das zusaetzlich einzuschraenken).
-- ============================================================================
BEGIN;

ALTER TABLE lanes ADD COLUMN virtual BOOLEAN NOT NULL DEFAULT FALSE;

INSERT INTO lanes (lane_no, name, active, virtual)
VALUES (901, 'Manuelle Eingabe / Wertmaschine', TRUE, TRUE)
ON CONFLICT (lane_no) DO NOTHING;

COMMIT;
