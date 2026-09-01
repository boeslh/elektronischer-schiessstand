-- ============================================================================
-- 031_preisschiessen_wertungen.sql – Auswerte-Logik je Preisschiessen
--
-- Ersetzt fachlich gs26_Listen (siehe gs26/admin/listen.php,
-- gs26/backend/gen_SerienP.py / gen_TeilerP.py / gen_Adler.py), aber an
-- preisschiessen_id gehaengt statt an eine jahresspezifische MySQL-Tabelle:
-- ein Preisschiessen (z.B. ein Gauschiessen-Jahrgang) konfiguriert seine
-- eigenen "Wertungen" (Meister/Punkt/Adler) im neuen Auswertung-Tab von
-- preisschiessen.html.
--
-- Zwei-Ebenen-Modell wie im Vorbild: die Ranking-Berechnung
-- (server/preisschiessen_wertungen.go) ist bei grossen Preisschiessen
-- rechenintensiv (bis zu 5 Minuten) und laeuft deshalb periodisch im
-- Hintergrund (ps_auswertung_status steuert Intervall/Status), NICHT live
-- pro Anfrage. ps_wertung_ergebnisse ist der billige, denormalisierte
-- Ergebnis-Cache, den sowohl die Preisschiessen-Seite als auch der separate
-- Anzeige-Prozess (preisanzeige/) lesen.
-- ============================================================================
BEGIN;

CREATE TABLE ps_wertungen (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    preisschiessen_id UUID NOT NULL REFERENCES preisschiessen(id) ON DELETE CASCADE,
    disziplin_key     TEXT NOT NULL,             -- z.B. 'AM-LG-Meister'
    typ               TEXT NOT NULL CHECK (typ IN ('meister','punkt','adler')),
    short_desc        TEXT NOT NULL,
    long_desc         TEXT,
    -- nur meister/punkt: welches Feld aus v_series_results/v_scoring_shots
    -- gewertet wird. 'ring' = Ganzring-Summe, 'ring_decimal' = Zehntel-Summe
    -- (Zehntelwertung), 'teiler' = bester Teiler (center_distance) je Schuss.
    wertungsfeld      TEXT CHECK (wertungsfeld IN ('ring','ring_decimal','teiler')),
    faktor            NUMERIC(6,4) NOT NULL DEFAULT 1,  -- ersetzt RING_FAKTOR/TEILER_FAKTOR
    scheiben_namen    TEXT[] NOT NULL DEFAULT '{}',     -- ps_scheiben.name-Werte dieses Preisschiessens
    klassen_ids       UUID[] NOT NULL DEFAULT '{}',     -- shooter_classes.id, leer = alle Klassen
    anz_praemie       SMALLINT NOT NULL DEFAULT 5,       -- ANZ_RING_PRAEMIE / ANZ_TEILER_PRAEMIE
    -- nur typ='adler': referenziert zwei andere Zeilen dieser Tabelle
    -- (bereits berechnete Punkt- bzw. Meister-Wertung, die alternierend
    -- gemischt werden, siehe gen_Adler.py)
    adler_teiler_id   UUID REFERENCES ps_wertungen(id) ON DELETE SET NULL,
    adler_meister_id  UUID REFERENCES ps_wertungen(id) ON DELETE SET NULL,
    praemie           BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order        SMALLINT NOT NULL DEFAULT 0,
    visible           BOOLEAN NOT NULL DEFAULT TRUE,
    UNIQUE (preisschiessen_id, disziplin_key)
);

CREATE INDEX idx_ps_wertungen_preisschiessen ON ps_wertungen (preisschiessen_id);

-- Batch-Steuerung: ein Zustandsdatensatz je Preisschiessen. Der atomare
-- Claim (status <> 'running' -> 'running') ist die Grundlage sowohl fuer
-- den periodischen Scheduler als auch fuer den manuellen "Jetzt neu
-- berechnen"-Trigger UND fuer die Mehr-Rechner-Skalierung (mehrere
-- --worker-only-Prozesse auf verschiedenen Maschinen koennen dieselbe
-- Tabelle sicher gemeinsam abarbeiten, siehe Store.ClaimAuswertungJob).
CREATE TABLE ps_auswertung_status (
    preisschiessen_id  UUID PRIMARY KEY REFERENCES preisschiessen(id) ON DELETE CASCADE,
    interval_seconds   INT NOT NULL DEFAULT 600 CHECK (interval_seconds BETWEEN 300 AND 900),
    status             TEXT NOT NULL DEFAULT 'idle' CHECK (status IN ('idle','running','error')),
    last_started_at    TIMESTAMPTZ,
    last_finished_at   TIMESTAMPTZ,
    last_duration_ms   INT,
    last_error         TEXT
);

-- Ergebnis-Cache: das Einzige, was der Anzeige-Prozess liest. Komplett
-- denormalisiert (Name/Verein/Klasse als Snapshot), damit dort keine Joins
-- noetig sind - unbedenklich bei Polling im Sekundentakt durch mehrere
-- Anzeige-Bildschirme gleichzeitig.
CREATE TABLE ps_wertung_ergebnisse (
    ps_wertung_id   UUID NOT NULL REFERENCES ps_wertungen(id) ON DELETE CASCADE,
    teilnehmer_id   UUID NOT NULL,
    start_nr        INT NOT NULL,
    nachname        TEXT NOT NULL,
    vorname         TEXT NOT NULL,
    verein          TEXT,
    klasse          TEXT,
    werte           NUMERIC(8,2)[] NOT NULL,   -- S1..S10 bzw. T1..T10
    summe_praemie   NUMERIC(8,2) NOT NULL,     -- P (Summe der besten anz_praemie Werte)
    platz           INT,
    platz_p         INT,
    PRIMARY KEY (ps_wertung_id, teilnehmer_id)
);

CREATE INDEX idx_ps_wertung_ergebnisse_platz ON ps_wertung_ergebnisse (ps_wertung_id, platz);

-- Anzeige-Konfiguration: welche Wertungen der eigenstaendige Anzeige-Prozess
-- (preisanzeige/) in welcher Reihenfolge/mit welchem Reload-Intervall zeigt.
CREATE TABLE ps_anzeige_config (
    preisschiessen_id UUID PRIMARY KEY REFERENCES preisschiessen(id) ON DELETE CASCADE,
    reload_seconds    INT NOT NULL DEFAULT 5,
    title_font_size   INT NOT NULL DEFAULT 20,
    wertung_ids       UUID[] NOT NULL DEFAULT '{}'  -- Reihenfolge = Anzeige-Reihenfolge
);

COMMIT;
