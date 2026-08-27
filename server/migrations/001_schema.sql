-- ============================================================================
--  ELEKTRONISCHER SCHIESSSTAND – DATENMODELL (PostgreSQL 14+)
--  Rev 1.0
--
--  Architektur-Vorbild: Meyton ShootMasterII
--    - Disziplinen/Scheiben sind DATEN, kein Code
--    - Schüsse werden NIE gelöscht, nur umgewidmet/annulliert (Audit-Trail)
--    - Stand-PC führt zusätzlich ein lokales append-only Schussprotokoll;
--      diese DB ist die zentrale "Workstation"-Datenbank
--    - Sonderfälle (Kreuzschuss, Durchflieger, Waffenstörung, Strafringe,
--      Wiederholungsserie, Standwechsel) sind im Modell abgebildet
-- ============================================================================

BEGIN;

CREATE EXTENSION IF NOT EXISTS "pgcrypto";   -- für gen_random_uuid()

-- ============================================================================
-- 1. ENUMS / WERTELISTEN
-- ============================================================================

-- Status eines einzelnen Schusses. Schüsse werden nie gelöscht!
CREATE TYPE shot_status AS ENUM (
    'valid',            -- regulärer Wertungs-/Probeschuss
    'annulled',         -- vom Kampfrichter annulliert (zählt nicht)
    'cross_shot_in',    -- Kreuzschuss: auf DIESER Scheibe eingeschlagen,
                        -- gehört aber einem fremden Schützen
    'cross_shot_out',   -- Kreuzschuss: dieser Schütze hat auf fremde
                        -- Scheibe geschossen (virtueller Eintrag)
    'pass_through',     -- Durchflieger (Loch von vorherigem Schuss o.ä.)
    'malfunction',      -- Schuss während anerkannter Waffenstörung
    'rejected'          -- techn. verworfen (z.B. <3 Sensoren, Störsignal)
);

-- Probe oder Wertung
CREATE TYPE shot_kind AS ENUM ('sighting', 'match');   -- Probe / Wertung

-- Schießstellungen (ISSF)
CREATE TYPE shooting_position AS ENUM ('standing', 'prone', 'kneeling', 'rest');
-- stehend / liegend / kniend / aufgelegt (Auflage)

-- Sitzungs-/Scheibenstatus
CREATE TYPE session_status AS ENUM (
    'assigned',   -- Stand belegt, noch nicht gestartet
    'sighting',   -- Probe läuft
    'match',      -- Wertung läuft
    'paused',     -- unterbrochen (Störung, Pause)
    'finished',   -- regulär beendet
    'aborted'     -- abgebrochen
);

-- Wettkampfstatus
CREATE TYPE event_status AS ENUM ('planned', 'running', 'finished', 'archived');

-- Typen von Kampfrichter-/Systemaktionen für den Audit-Trail
CREATE TYPE action_type AS ENUM (
    'shot_recorded',        -- Schuss automatisch erfasst
    'shot_annulled',        -- Schuss annulliert
    'shot_reassigned',      -- Kreuzschuss einem anderen Schützen zugeordnet
    'shot_reclassified',    -- Probe<->Wertung umgewidmet
    'penalty_applied',      -- Strafringe vergeben
    'series_repeated',      -- Wiederholungsserie angeordnet
    'malfunction_accepted', -- Waffenstörung anerkannt
    'malfunction_rejected', -- Waffenstörung nicht anerkannt
    'card_yellow',          -- gelbe Karte
    'card_green',           -- grüne Karte
    'lane_changed',         -- Standwechsel
    'time_adjusted',        -- Restzeit geändert
    'session_started',
    'session_finished',
    'calibration_changed',
    'manual_score_entry'    -- manueller Treffereintrag (Notbetrieb)
);

-- ============================================================================
-- 2. STAMMDATEN  (Meyton: "Stammdaten" in Starterlisten-Programm)
-- ============================================================================

CREATE TABLE clubs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    short_name  TEXT,
    association TEXT,                       -- Verband (z.B. DSB-Landesverband)
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE shooters (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    last_name     TEXT NOT NULL,
    first_name    TEXT NOT NULL,
    birth_date    DATE,                     -- für automatische Klassenermittlung
    pass_no       TEXT UNIQUE,              -- DSB-Passnummer (Meyton: PassNr)
    country       CHAR(3) DEFAULT 'GER',    -- IOC-Code
    club_id       UUID REFERENCES clubs(id),
    notes         TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_shooters_name ON shooters (last_name, first_name);

-- Wettkampfklassen (Schüler, Jugend, AK..., Meyton: importierbare Klassen).
-- Als Daten modelliert, damit neue DSB-Klassen ohne Codeänderung möglich sind.
CREATE TABLE shooter_classes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code        TEXT NOT NULL UNIQUE,       -- z.B. "10", "11", "40" (DSB-Nr.)
    name        TEXT NOT NULL,              -- z.B. "Herren I", "Schüler m"
    min_age     SMALLINT,
    max_age     SMALLINT,
    sex         CHAR(1),                    -- 'm', 'w', NULL = offen
    valid_from  DATE,
    valid_to    DATE
);

CREATE TABLE teams (                         -- Mannschaften
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name      TEXT NOT NULL,
    club_id   UUID REFERENCES clubs(id),
    event_id  UUID                            -- FK folgt nach events (s.u.)
);

-- ============================================================================
-- 3. DISZIPLINEN & SCHEIBEN  (Meyton: "Disziplin erstellen")
--    Vollständig datengetrieben: Ringe, Werte, Zeiten, Stellungen, Serien
-- ============================================================================

-- Scheibendefinition: Geometrie als Ring-Tabelle.
-- Beispiel LG 10m: Ring 10 = 0,5mm Durchm., Ringabstand 5mm, Innenzehner 0,5mm
CREATE TABLE targets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,            -- z.B. "LG 10m ISSF"
    description     TEXT,
    card_width_mm   NUMERIC(6,2) NOT NULL,    -- Spiegelkarton-Breite
    card_height_mm  NUMERIC(6,2) NOT NULL,
    inner_ten_d_mm  NUMERIC(6,3),             -- Innenzehner-Durchmesser
    caliber_mm      NUMERIC(5,2) NOT NULL DEFAULT 4.5,
    -- Wertung "Rand zählt": Treffer zählt, wenn der Schusslochrand den Ring
    -- berührt -> effektiver Radius = Ringradius + Kaliber/2
    edge_scoring    BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE target_rings (
    target_id     UUID NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    ring_value    SMALLINT NOT NULL,          -- 1..10
    diameter_mm   NUMERIC(7,3) NOT NULL,      -- Außendurchmesser dieses Rings
    PRIMARY KEY (target_id, ring_value)
);

-- Disziplin = Regelwerk (Meyton: Wettbewerb mit Regelnummer)
CREATE TABLE disciplines (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                TEXT NOT NULL,        -- "Luftgewehr 40 Schuss"
    rule_no             TEXT,                 -- DSB-Regelnummer, z.B. "1.10"
    distance_m          NUMERIC(5,2) NOT NULL DEFAULT 10,
    target_id           UUID NOT NULL REFERENCES targets(id),
    sighting_target_id  UUID REFERENCES targets(id),  -- Probescheibe (falls anders)
    match_shot_count    SMALLINT NOT NULL,    -- Wertungsschüsse gesamt (z.B. 40)
    max_sighting_shots  SMALLINT,             -- NULL = unbegrenzt
    shots_per_series    SMALLINT NOT NULL DEFAULT 10,
    decimal_scoring     BOOLEAN NOT NULL DEFAULT FALSE,  -- Zehntelwertung
    -- Zeiten in Sekunden (NULL = keine Begrenzung)
    sighting_time_s     INTEGER,
    match_time_s        INTEGER,
    combined_time       BOOLEAN NOT NULL DEFAULT TRUE,
                        -- TRUE: Probe+Wertung in gemeinsamer Zeit (ISSF aktuell)
    active              BOOLEAN NOT NULL DEFAULT TRUE,   -- Disziplinauswahl
    notes               TEXT
);

-- Stellungen einer Disziplin (z.B. 3x20: kniend/liegend/stehend)
CREATE TABLE discipline_positions (
    discipline_id  UUID NOT NULL REFERENCES disciplines(id) ON DELETE CASCADE,
    position_no    SMALLINT NOT NULL,         -- Reihenfolge 1..n
    position       shooting_position NOT NULL,
    shot_count     SMALLINT NOT NULL,
    PRIMARY KEY (discipline_id, position_no)
);

-- ============================================================================
-- 4. ANLAGE: STÄNDE, GERÄTE, KALIBRIERUNG
-- ============================================================================

CREATE TABLE lanes (                          -- Stände
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    lane_no     SMALLINT NOT NULL UNIQUE,     -- Standnummer
    name        TEXT,
    active      BOOLEAN NOT NULL DEFAULT TRUE
);

-- Messeinheit (ESP32-Einheit) – ein Gerät pro Stand, aber tauschbar
CREATE TABLE devices (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    serial_no    TEXT UNIQUE,                 -- z.B. ESP32-MAC
    fw_version   TEXT,
    lane_id      UUID REFERENCES lanes(id),
    last_seen_at TIMESTAMPTZ
);

-- Kalibrierung: Sensorpositionen + Geometrie + Schallparameter.
-- Historisiert (valid_from/valid_to), damit alte Schüsse mit der DAMALS
-- gültigen Kalibrierung nachvollziehbar bleiben.
CREATE TABLE calibrations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    lane_id         UUID NOT NULL REFERENCES lanes(id),
    valid_from      TIMESTAMPTZ NOT NULL DEFAULT now(),
    valid_to        TIMESTAMPTZ,              -- NULL = aktuell gültig
    -- Sensorpositionen auf dem Blech in mm, Blech-Koordinatensystem.
    -- JSON-Array: [{"x":0,"y":0},{"x":250,"y":0},...] Index = Sensor-Nr.
    sensor_pos      JSONB NOT NULL,
    -- Geometrie Blech <-> Papierscheibe (schräges Abprallblech!)
    plate_angle_deg NUMERIC(5,2) NOT NULL,    -- Blechwinkel zur Scheibe
    plate_offset_x  NUMERIC(7,2) NOT NULL DEFAULT 0,  -- mm, Versatz-Korrektur
    plate_offset_y  NUMERIC(7,2) NOT NULL DEFAULT 0,
    -- Materialschall im Blech (NICHT Luft!), kalibriert ermittelt
    sound_speed_mps NUMERIC(8,2) NOT NULL,
    -- Restfehler aus Kalibrier-Messreihe (Qualitätsmaß)
    rms_error_mm    NUMERIC(6,3),
    notes           TEXT,
    created_by      TEXT
);

CREATE INDEX idx_calibrations_lane_valid
    ON calibrations (lane_id, valid_from DESC);

-- ============================================================================
-- 5. VERANSTALTUNGEN & STARTERLISTEN  (Meyton: Starterlisten-Programm)
-- ============================================================================

CREATE TABLE events (                         -- Veranstaltungen
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    starts_on   DATE,
    ends_on     DATE,
    status      event_status NOT NULL DEFAULT 'planned',
    notes       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE teams
    ADD CONSTRAINT fk_teams_event
    FOREIGN KEY (event_id) REFERENCES events(id);

-- Ein Eintrag der Starterliste = ein Start eines Schützen
-- (Meyton: welcher Schütze, welcher Tag, welcher Stand, welche Disziplin)
CREATE TABLE starters (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id       UUID NOT NULL REFERENCES events(id),
    shooter_id     UUID NOT NULL REFERENCES shooters(id),
    discipline_id  UUID NOT NULL REFERENCES disciplines(id),
    class_id       UUID REFERENCES shooter_classes(id),
    team_id        UUID REFERENCES teams(id),
    start_no       TEXT,                      -- Startnummer
    start_time     TIMESTAMPTZ,
    planned_lane   UUID REFERENCES lanes(id),
    is_final       BOOLEAN NOT NULL DEFAULT FALSE,
    UNIQUE (event_id, shooter_id, discipline_id, start_time)
);

CREATE INDEX idx_starters_event ON starters (event_id);

-- ============================================================================
-- 6. SITZUNGEN ("Scheiben")
--    Eine Session = ein Schütze schießt eine Scheibe/Durchgang an einem Stand.
--    Auch freies Training ohne Event (starter_id NULL).
-- ============================================================================

CREATE TABLE sessions (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    lane_id        UUID NOT NULL REFERENCES lanes(id),
    device_id      UUID REFERENCES devices(id),
    calibration_id UUID NOT NULL REFERENCES calibrations(id),
    discipline_id  UUID NOT NULL REFERENCES disciplines(id),
    shooter_id     UUID REFERENCES shooters(id),   -- NULL = anonymes Training
    starter_id     UUID REFERENCES starters(id),   -- NULL = freies Training
    status         session_status NOT NULL DEFAULT 'assigned',
    started_at     TIMESTAMPTZ,
    finished_at    TIMESTAMPTZ,
    -- Restzeit-Verwaltung (Meyton: "Änderung der verbleibenden Wettkampfzeit")
    time_bonus_s   INTEGER NOT NULL DEFAULT 0,
    -- Verkettung bei Standwechsel: neue Session verweist auf alte
    continued_from UUID REFERENCES sessions(id),
    notes          TEXT
);

CREATE INDEX idx_sessions_lane_started ON sessions (lane_id, started_at DESC);
CREATE INDEX idx_sessions_starter ON sessions (starter_id);

-- ============================================================================
-- 7. SCHÜSSE – das Herzstück
--    Append-only: UPDATE nur für status/Umwidmung, DELETE nie (per Trigger
--    verhindert). Rohdaten (Timestamps) werden mitgespeichert, damit jeder
--    Schuss mit anderer Kalibrierung NEU berechnet werden kann.
-- ============================================================================

CREATE TABLE shots (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id     UUID NOT NULL REFERENCES sessions(id),
    -- fortlaufende Nummer innerhalb der Session (inkl. annullierter!)
    shot_no        SMALLINT NOT NULL,
    kind           shot_kind NOT NULL DEFAULT 'match',
    status         shot_status NOT NULL DEFAULT 'valid',
    position_no    SMALLINT,                  -- Stellung (bei 3x20 etc.)
    series_no      SMALLINT,                  -- Seriennummer (1..n)
    fired_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- ---- ROHDATEN (unveränderlich, Quelle: ESP32) ----
    device_seq     INTEGER,                   -- "seq" aus ESP32-Telegramm
    raw_t_ns       BIGINT[] NOT NULL,         -- TDOA-Timestamps in ns
    sensor_hits    SMALLINT NOT NULL,         -- Anzahl ausgelöster Sensoren
    confidence     NUMERIC(4,3),              -- Residuum-basiert 0..1

    -- ---- BERECHNETE WERTE (mit calibration der Session) ----
    x_mm           NUMERIC(7,3),              -- Position auf der SCHEIBE,
    y_mm           NUMERIC(7,3),              -- Ursprung = Scheibenmitte
    ring           SMALLINT,                  -- Ganzring 0..10
    decimal_value  NUMERIC(4,1),              -- Zehntel 0.0..10.9
    is_inner_ten   BOOLEAN NOT NULL DEFAULT FALSE,
    divisor        NUMERIC(8,1),              -- Teiler (trad. Schießen),
                                              -- Abstand zur Mitte in 1/100mm

    -- ---- SONDERFÄLLE ----
    -- Kreuzschuss: Schuss physisch auf dieser Scheibe, gewertet für anderen
    scored_for_session UUID REFERENCES sessions(id),
    penalty_rings  SMALLINT NOT NULL DEFAULT 0,   -- Strafringe (negativ wirkend)

    UNIQUE (session_id, shot_no)
);

CREATE INDEX idx_shots_session ON shots (session_id, shot_no);
CREATE INDEX idx_shots_fired_at ON shots (fired_at);

-- DELETE-Schutz: Schüsse dürfen nie gelöscht werden (Meyton-Prinzip)
CREATE OR REPLACE FUNCTION prevent_shot_delete() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'Schüsse dürfen nicht gelöscht werden – Status auf '
                    '''annulled'' setzen (shot id=%)', OLD.id;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_shots_no_delete
    BEFORE DELETE ON shots
    FOR EACH ROW EXECUTE FUNCTION prevent_shot_delete();

-- ============================================================================
-- 8. AUDIT-TRAIL  (Meyton: Schussprotokoll + Wettkampfsteuerungs-Aktionen)
--    Jede Aktion – automatisch oder durch Kampfrichter – wird protokolliert.
-- ============================================================================

CREATE TABLE audit_log (
    id          BIGSERIAL PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    action      action_type NOT NULL,
    session_id  UUID REFERENCES sessions(id),
    shot_id     UUID REFERENCES shots(id),
    lane_id     UUID REFERENCES lanes(id),
    actor       TEXT NOT NULL DEFAULT 'system',  -- Benutzername/'system'
    -- Vorher/Nachher-Zustand für Nachvollziehbarkeit
    details     JSONB
);

CREATE INDEX idx_audit_session ON audit_log (session_id, occurred_at);

-- ============================================================================
-- 9. ERGEBNIS-SICHTEN  (Meyton: AuswertungII arbeitet auf solchen Daten)
--    Ergebnisse werden NICHT redundant gespeichert, sondern aus den Schüssen
--    berechnet – Views garantieren Konsistenz auch nach Umwidmungen.
-- ============================================================================

-- Wertungsfähige Schüsse einer Session (inkl. zugeordneter Kreuzschüsse)
CREATE VIEW v_scoring_shots AS
SELECT
    COALESCE(s.scored_for_session, s.session_id) AS effective_session_id,
    s.*
FROM shots s
WHERE s.kind = 'match'
  AND s.status IN ('valid', 'cross_shot_in')
  AND s.status <> 'annulled';

-- Session-Ergebnis: Ganzring- und Zehntelsumme, Serien
CREATE VIEW v_session_results AS
SELECT
    ss.effective_session_id AS session_id,
    COUNT(*)                          AS shot_count,
    SUM(ss.ring) - SUM(ss.penalty_rings)          AS total_rings,
    ROUND(SUM(ss.decimal_value)
          - SUM(ss.penalty_rings), 1)             AS total_decimal,
    SUM(CASE WHEN ss.is_inner_ten THEN 1 ELSE 0 END) AS inner_tens,
    MIN(ss.divisor)                   AS best_divisor   -- bester Teiler
FROM v_scoring_shots ss
GROUP BY ss.effective_session_id;

-- Serien-Ergebnisse (für Anzeige "S1 S2 S3 ..." wie in Wettkampfsteuerung)
CREATE VIEW v_series_results AS
SELECT
    ss.effective_session_id AS session_id,
    ss.series_no,
    COUNT(*)                 AS shots,
    SUM(ss.ring)             AS rings,
    ROUND(SUM(ss.decimal_value), 1) AS decimal_total
FROM v_scoring_shots ss
GROUP BY ss.effective_session_id, ss.series_no;

-- ============================================================================
-- 10. GRUNDDATEN: ISSF LG/LP-Scheiben + Standard-Disziplinen
-- ============================================================================

-- LG-Scheibe 10m (ISSF): Zehner-Durchm. 0,5mm, Ringabstand 2,5mm radial
INSERT INTO targets (id, name, card_width_mm, card_height_mm,
                     inner_ten_d_mm, caliber_mm, edge_scoring)
VALUES ('00000000-0000-0000-0000-00000000a010',
        'LG 10m ISSF', 80, 80, 0.5, 4.5, TRUE)
ON CONFLICT DO NOTHING;

INSERT INTO target_rings (target_id, ring_value, diameter_mm) VALUES
('00000000-0000-0000-0000-00000000a010', 10,  0.5),
('00000000-0000-0000-0000-00000000a010',  9,  5.5),
('00000000-0000-0000-0000-00000000a010',  8, 10.5),
('00000000-0000-0000-0000-00000000a010',  7, 15.5),
('00000000-0000-0000-0000-00000000a010',  6, 20.5),
('00000000-0000-0000-0000-00000000a010',  5, 25.5),
('00000000-0000-0000-0000-00000000a010',  4, 30.5),
('00000000-0000-0000-0000-00000000a010',  3, 35.5),
('00000000-0000-0000-0000-00000000a010',  2, 40.5),
('00000000-0000-0000-0000-00000000a010',  1, 45.5)
ON CONFLICT DO NOTHING;

-- LP-Scheibe 10m (ISSF): Zehner 11,5mm, Ringabstand 8mm radial
INSERT INTO targets (id, name, card_width_mm, card_height_mm,
                     inner_ten_d_mm, caliber_mm, edge_scoring)
VALUES ('00000000-0000-0000-0000-00000000b010',
        'LP 10m ISSF', 170, 170, 5.0, 4.5, TRUE)
ON CONFLICT DO NOTHING;

INSERT INTO target_rings (target_id, ring_value, diameter_mm) VALUES
('00000000-0000-0000-0000-00000000b010', 10,  11.5),
('00000000-0000-0000-0000-00000000b010',  9,  27.5),
('00000000-0000-0000-0000-00000000b010',  8,  43.5),
('00000000-0000-0000-0000-00000000b010',  7,  59.5),
('00000000-0000-0000-0000-00000000b010',  6,  75.5),
('00000000-0000-0000-0000-00000000b010',  5,  91.5),
('00000000-0000-0000-0000-00000000b010',  4, 107.5),
('00000000-0000-0000-0000-00000000b010',  3, 123.5),
('00000000-0000-0000-0000-00000000b010',  2, 139.5),
('00000000-0000-0000-0000-00000000b010',  1, 155.5)
ON CONFLICT DO NOTHING;

-- Disziplinen: LG 40 (DSB 1.10) und LP 40 (DSB 2.10)
INSERT INTO disciplines (name, rule_no, distance_m, target_id,
                         match_shot_count, shots_per_series,
                         decimal_scoring, match_time_s, combined_time)
VALUES
('Luftgewehr 40 Schuss',  '1.10', 10,
 '00000000-0000-0000-0000-00000000a010', 40, 10, FALSE, 4500, TRUE),
('Luftpistole 40 Schuss', '2.10', 10,
 '00000000-0000-0000-0000-00000000b010', 40, 10, FALSE, 4500, TRUE)
ON CONFLICT DO NOTHING;

COMMIT;

-- ============================================================================
--  HINWEISE ZUR VERWENDUNG
-- ============================================================================
--
--  Ringwert-Berechnung (im Backend, nicht in der DB):
--    r = sqrt(x² + y²)                          Abstand Schussmitte→Scheibenmitte
--    effektiv = r - caliber/2  (edge_scoring)   "Rand zählt"
--    Ring     = höchster ring_value mit effektiv <= diameter/2
--    Zehntel  = lineare Interpolation zwischen den Ringradien
--    Teiler   = r * 100  (in 1/100 mm)
--
--  Kreuzschuss-Workflow (Meyton: "Kreuzschüsse"):
--    1. Schuss schlägt auf Stand B ein -> shots-Zeile bei Session B,
--       status='cross_shot_in', scored_for_session = Session A
--    2. Audit-Log: action='shot_reassigned', details={von,nach,grund}
--    3. v_scoring_shots ordnet ihn automatisch Session A zu
--
--  Wiederholungsserie:
--    Alte Serie: alle Schüsse status='annulled' (einzeln, mit Audit-Log
--    action='series_repeated'); neue Schüsse bekommen dieselbe series_no.
--
--  Neuberechnung nach Kalibrierungskorrektur:
--    UPDATE der berechneten Felder (x_mm, y_mm, ring, ...) aus raw_t_us
--    mit der neuen calibration; raw_t_us bleibt unangetastet.
--    Audit-Log: action='calibration_changed'.
--
--  Lokales Schussprotokoll (Stand-PC):
--    Append-only JSON-Lines-Datei je Session, gespiegelt aus den
--    ESP32-Telegrammen VOR der DB-Speicherung. Format:
--    {"ts":"...","seq":12,"t_us":[...],"x":2.4,"y":-1.8,"ring":9}
--    Dient als Nachweis bei DB-/Netzwerkausfall (Meyton-Logdatei-Prinzip).
-- ============================================================================
