-- ============================================================================
-- 024_preisschiessen_scheiben_einheiten.sql – Preisschießen: einzelne
-- Scheiben-Einheiten mit Seriennummer + Beschossen-Status
--
-- Jede gekaufte Scheibe (auch die N Stück, die in einem Set enthalten sind)
-- bekommt jetzt eine eigene Zeile mit fortlaufender Seriennummer je
-- Preisschießen. Der Status (gekauft/begonnen/beendet) wird NICHT
-- gespeichert, sondern beim Lesen aus der verknüpften Session/den Schüssen
-- berechnet (gleiches Prinzip wie v_session_results in 001_schema.sql):
--   - session_id NULL oder keine Schüsse            -> gekauft
--   - mindestens ein Schuss, aber Wertungsschüsse
--     < Schusszahl der Disziplin                    -> begonnen
--   - Wertungsschüsse >= Schusszahl der Disziplin    -> beendet
--
-- Die Standzuweisung nutzt die bestehende Standbelegung (sessions/lanes,
-- Store.AssignLane) wieder - hier wird nur die entstandene Session mit der
-- gekauften Scheiben-Einheit verknüpft.
-- ============================================================================
BEGIN;

ALTER TABLE preisschiessen
    ADD COLUMN next_scheibe_serial INT NOT NULL DEFAULT 1;

CREATE TABLE ps_kauf_scheiben (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    preisschiessen_id UUID NOT NULL REFERENCES preisschiessen(id) ON DELETE CASCADE,
    kauf_id           UUID NOT NULL REFERENCES ps_kaeufe(id) ON DELETE CASCADE,
    scheibe_id        UUID NOT NULL REFERENCES ps_scheiben(id),
    serial_no         INT NOT NULL,
    -- gesetzt, sobald einem Stand zugewiesen (Store.AssignPreisschiessenLane)
    session_id        UUID REFERENCES sessions(id),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (preisschiessen_id, serial_no)
);

CREATE INDEX idx_ps_kauf_scheiben_kauf ON ps_kauf_scheiben (kauf_id);

COMMIT;
