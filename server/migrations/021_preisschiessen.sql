-- ============================================================================
-- 021_preisschiessen.sql – Preisschießen: Anmeldung + Kasse
--
-- Eigenständiges Modul (kein Bezug zu events/competitions – das ist die
-- Meyton-Style Wettkampf-Steuerung für Start-/Ergebnislisten, hier geht es
-- um Scheibenverkauf und ein Teilnehmerkonto). Referenziert nur shooters,
-- disciplines, shooter_classes.
--
-- Guthaben-Modell: Teilnehmer zahlt Bargeld ein (Aufladung), Käufe/
-- Rückgaben verrechnen intern gegen das Guthaben, am Ende ggf. Auszahlung
-- des Restguthabens. Der Saldo wird NICHT redundant gespeichert, sondern
-- aus dem Ledger (ps_guthaben_buchungen) berechnet – gleiches Prinzip wie
-- v_session_results in 001_schema.sql.
-- ============================================================================
BEGIN;

CREATE TABLE preisschiessen (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name               TEXT NOT NULL,
    starts_on          DATE,
    ends_on            DATE,
    -- wie shooter_classes.type: 0=Kugel, 1=Bogen, 2=Kugel Auflage
    shooting_type      SMALLINT NOT NULL DEFAULT 0,
    -- atomarer Zähler für Teilnehmernummern (siehe CreateTeilnehmer)
    next_teilnehmer_nr INT NOT NULL DEFAULT 1,
    active             BOOLEAN NOT NULL DEFAULT TRUE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Verkaufbares Produkt "Scheibe" (Produkt-Sinn, nicht zu verwechseln mit
-- targets/sessions aus 001_schema.sql).
CREATE TABLE ps_scheiben (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    preisschiessen_id  UUID NOT NULL REFERENCES preisschiessen(id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    discipline_id      UUID NOT NULL REFERENCES disciplines(id),
    price              NUMERIC(8,2) NOT NULL,
    target_color       TEXT,
    -- FALSE = darf nur innerhalb eines Sets verkauft werden (nicht einzeln)
    standalone_erlaubt BOOLEAN NOT NULL DEFAULT TRUE,
    active             BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order         SMALLINT NOT NULL DEFAULT 0
);

CREATE INDEX idx_ps_scheiben_preisschiessen ON ps_scheiben (preisschiessen_id);

CREATE TABLE ps_sets (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    preisschiessen_id  UUID NOT NULL REFERENCES preisschiessen(id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    -- kann von der Summe der enthaltenen Einzelscheiben abweichen
    price              NUMERIC(8,2) NOT NULL,
    active             BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order         SMALLINT NOT NULL DEFAULT 0
);

CREATE INDEX idx_ps_sets_preisschiessen ON ps_sets (preisschiessen_id);

CREATE TABLE ps_set_items (
    set_id      UUID NOT NULL REFERENCES ps_sets(id)     ON DELETE CASCADE,
    scheibe_id  UUID NOT NULL REFERENCES ps_scheiben(id) ON DELETE CASCADE,
    quantity    SMALLINT NOT NULL DEFAULT 1,
    PRIMARY KEY (set_id, scheibe_id)
);

-- Klassen-Restriktion: keine Zeile = keine Einschränkung (alle Klassen erlaubt)
CREATE TABLE ps_scheibe_classes (
    scheibe_id  UUID NOT NULL REFERENCES ps_scheiben(id)    ON DELETE CASCADE,
    class_id    UUID NOT NULL REFERENCES shooter_classes(id) ON DELETE CASCADE,
    PRIMARY KEY (scheibe_id, class_id)
);

CREATE TABLE ps_set_classes (
    set_id      UUID NOT NULL REFERENCES ps_sets(id)        ON DELETE CASCADE,
    class_id    UUID NOT NULL REFERENCES shooter_classes(id) ON DELETE CASCADE,
    PRIMARY KEY (set_id, class_id)
);

-- Gating: Scheibe darf erst gekauft werden, wenn (mindestens) eines der
-- verknüpften Sets aktuell (nicht zurückgegeben) gekauft ist.
-- Keine Zeile = kein Gating.
CREATE TABLE ps_scheibe_requires_set (
    scheibe_id      UUID NOT NULL REFERENCES ps_scheiben(id) ON DELETE CASCADE,
    required_set_id UUID NOT NULL REFERENCES ps_sets(id)     ON DELETE CASCADE,
    PRIMARY KEY (scheibe_id, required_set_id)
);

CREATE TABLE ps_teilnehmer (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    preisschiessen_id  UUID NOT NULL REFERENCES preisschiessen(id) ON DELETE CASCADE,
    shooter_id         UUID NOT NULL REFERENCES shooters(id),
    teilnehmer_nr       INT NOT NULL,
    -- zum Anmeldezeitpunkt aus Geburtsjahr + Preisschießen-Ende + Typ
    -- berechnet (analog RecalculateSportsClasses), manuell überschreibbar
    class_id           UUID REFERENCES shooter_classes(id),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (preisschiessen_id, shooter_id),
    UNIQUE (preisschiessen_id, teilnehmer_nr)
);

CREATE INDEX idx_ps_teilnehmer_preisschiessen ON ps_teilnehmer (preisschiessen_id);

-- Eine Kauf-Zeile im Konto (Scheibe ODER Set, nie beides). Set-Käufe sind
-- nur als Ganzes rückgabefähig (returned_at), keine Teilrückgabe.
CREATE TABLE ps_kaeufe (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    teilnehmer_id  UUID NOT NULL REFERENCES ps_teilnehmer(id) ON DELETE CASCADE,
    typ            TEXT NOT NULL CHECK (typ IN ('scheibe','set')),
    scheibe_id     UUID REFERENCES ps_scheiben(id),
    set_id         UUID REFERENCES ps_sets(id),
    -- Preis-Snapshot zum Kaufzeitpunkt (unabhängig von späteren Preisänderungen)
    preis          NUMERIC(8,2) NOT NULL,
    purchased_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    returned_at    TIMESTAMPTZ,
    CONSTRAINT ps_kaeufe_typ_ref CHECK (
        (typ = 'scheibe' AND scheibe_id IS NOT NULL AND set_id IS NULL) OR
        (typ = 'set'     AND set_id     IS NOT NULL AND scheibe_id IS NULL)
    )
);

CREATE INDEX idx_ps_kaeufe_teilnehmer ON ps_kaeufe (teilnehmer_id, purchased_at);

-- Ledger: einzige Quelle für den Guthaben-Saldo eines Teilnehmers.
CREATE TABLE ps_guthaben_buchungen (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    teilnehmer_id  UUID NOT NULL REFERENCES ps_teilnehmer(id) ON DELETE CASCADE,
    typ            TEXT NOT NULL CHECK (typ IN ('aufladung','kauf','rueckgabe','auszahlung')),
    -- Vorzeichen: + bei aufladung/rueckgabe, - bei kauf/auszahlung
    betrag         NUMERIC(10,2) NOT NULL,
    kauf_id        UUID REFERENCES ps_kaeufe(id),
    notiz          TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_ps_buchungen_teilnehmer ON ps_guthaben_buchungen (teilnehmer_id, created_at);

CREATE VIEW v_ps_guthaben AS
SELECT teilnehmer_id, COALESCE(SUM(betrag), 0) AS guthaben
FROM ps_guthaben_buchungen
GROUP BY teilnehmer_id;

-- Neue Hauptmenü-Kachel für alle bestehenden Rollen sichtbar (wie die
-- übrigen operativen Kacheln in 016_ui_roles.sql) – je Rolle über die
-- Benutzerverwaltung abschaltbar.
INSERT INTO ui_role_tiles (role_key, tile_key)
SELECT role_key, 'preisschiessen' FROM ui_roles
ON CONFLICT DO NOTHING;

COMMIT;
