-- Preisschießen: Stand-Zuweisung ohne Scheibenauswahl. Die Aufsicht weist
-- den Teilnehmer nur noch einem Stand zu ("wartet"); die Scheibenauswahl
-- trifft der Schütze selbst am Stand-PC. Solange keine Scheibe gewählt ist,
-- existiert noch keine sessions-Zeile (discipline_id dort ist NOT NULL) -
-- diese Tabelle bildet den Zwischenzustand ab.
CREATE TABLE ps_lane_pending (
    lane_id       UUID PRIMARY KEY REFERENCES lanes(id) ON DELETE CASCADE,
    teilnehmer_id UUID NOT NULL UNIQUE REFERENCES ps_teilnehmer(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
