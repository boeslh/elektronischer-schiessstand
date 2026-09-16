-- ============================================================================
-- 065_serien_modus_je_wertung.sql – Serien-Auswahl (alle/gesamt/erste_n/
-- beste_n) von ps_wertung_scheiben (je Scheibe, Migration 063) auf
-- ps_wertungen (je Wertung) verschoben - Nutzer-Feedback: je Scheibe war
-- das bei mehreren Scheiben pro Kombi-Wertung unuebersichtlich, und
-- praktisch wird der Modus ohnehin immer einheitlich fuer eine ganze
-- Wertung gesetzt. Faktor bleibt bewusst je Scheibe (ps_wertung_scheiben),
-- da unterschiedliche Zielgroessen/Kaliber weiterhin unterschiedliche
-- Normierungsfaktoren brauchen.
--
-- Alle bisherigen Datensaetze haben serien_modus='alle' (durchgehend
-- geprueft) - die UPDATE-Zeile ist daher nur eine Vorsichtsmassnahme fuer
-- den Fall, dass zwischenzeitlich doch ein abweichender Wert gesetzt wurde.
-- ============================================================================
BEGIN;

ALTER TABLE ps_wertungen
    ADD COLUMN serien_modus TEXT NOT NULL DEFAULT 'alle'
        CHECK (serien_modus IN ('alle', 'je_serie', 'gesamt', 'erste_n', 'beste_n')),
    ADD COLUMN serien_anzahl SMALLINT;

UPDATE ps_wertungen w SET
    serien_modus = COALESCE(
        (SELECT ws.serien_modus FROM ps_wertung_scheiben ws WHERE ws.wertung_id = w.id AND ws.serien_modus <> 'alle' LIMIT 1),
        'alle'),
    serien_anzahl = (
        SELECT ws.serien_anzahl FROM ps_wertung_scheiben ws WHERE ws.wertung_id = w.id AND ws.serien_modus <> 'alle' LIMIT 1);

ALTER TABLE ps_wertung_scheiben DROP COLUMN serien_modus;
ALTER TABLE ps_wertung_scheiben DROP COLUMN serien_anzahl;

COMMIT;
