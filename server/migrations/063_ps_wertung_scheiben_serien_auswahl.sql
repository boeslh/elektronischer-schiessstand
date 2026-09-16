-- Serien-Auswahl je Scheibe innerhalb einer Wertung (Kombi-Mechanismus
-- ps_wertung_scheiben) - fuer Vereinsabend-Jahreswertungen ("nur erste/beste
-- N Serien zaehlen, zu einem Wert summiert") und als Preisschiessen-Retrofit
-- ("jede Serie ein eigener Wert im Kombi-Wertungs-Pool", z.B.
-- Jugendmeisterschaft 40 Schuss zaehlt als Ganzes UND ihre 4x10-Serien
-- einzeln in eine Kombi-Serien-Wertung).
--
-- Fuenf Modi (siehe Konzept .claude/plans/wise-scribbling-abelson.md):
--   'alle'      Standard, KEINE Aenderung am heutigen Verhalten: bei
--               Ring/Zehntel ein Wert je Serie (v_series_results
--               unrestringiert), bei Teiler ein Wert je Einzelschuss
--               (v_scoring_shots unrestringiert) - identisch zu 'je_serie'.
--   'je_serie'  Explizite Variante von 'alle' fuer den Fall, dass bewusst
--               "jede Serie einzeln" gemeint ist (gleiche Abfrage wie 'alle').
--   'gesamt'    NEU: die ganze Scheibe (alle Serien) zaehlt als EIN
--               summierter Wert (Ring-Summe bzw. bester Teiler ueber die
--               gesamte Scheibe).
--   'erste_n'   NEU: nur die ersten serien_anzahl Serien zaehlen, zu einem
--               Wert summiert (Ring) bzw. bester Teiler nur unter diesen
--               Serien (Teiler).
--   'beste_n'   NEU: wie 'erste_n', aber die serien_anzahl Serien mit der
--               hoechsten Ringsumme werden gewaehlt statt der ersten N.
ALTER TABLE ps_wertung_scheiben
    ADD COLUMN serien_modus TEXT NOT NULL DEFAULT 'alle'
        CHECK (serien_modus IN ('alle', 'je_serie', 'gesamt', 'erste_n', 'beste_n')),
    ADD COLUMN serien_anzahl SMALLINT;

-- Bester Teiler je Serie - Hilfsview fuer 'erste_n'/'beste_n' bei
-- Wertungsfeld='teiler' (vermeidet Wiederholung der Aggregation in mehreren
-- Abfragen).
CREATE VIEW v_series_best_teiler AS
    SELECT effective_session_id AS session_id, series_no,
           MIN(eff_center_distance) AS best_teiler
    FROM v_scoring_shots
    GROUP BY effective_session_id, series_no;
