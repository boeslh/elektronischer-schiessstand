-- ============================================================================
-- 089_papier_label.sql – Seriennummern-Vergabe und Etikettendruck fuer
-- Papierscheiben.
--
-- app_settings.paper_serial_mode:
--   'manuell'     - Seriennummer der vorgedruckten Scheibe wird beim Verkauf
--                   eingetippt (bisheriges Verhalten, migrations/056)
--   'fortlaufend' - der Server vergibt die Nummer selbst aus
--                   ps_paper_serial_seq; Nummern, die schon (z. B. manuell)
--                   vergeben sind, werden uebersprungen. Luecken sind erlaubt.
-- app_settings.label_printer: CUPS-Warteschlange des Etikettendruckers
--   (Datamax, DPL, Rohdaten).
-- ps_kauf_scheiben.label_printed_at/label_print_count: wann und wie oft das
--   Etikett gedruckt wurde (Ersatz-Etiketten zaehlen mit).
-- ============================================================================
BEGIN;

ALTER TABLE app_settings
    ADD COLUMN paper_serial_mode TEXT NOT NULL DEFAULT 'manuell'
        CHECK (paper_serial_mode IN ('manuell', 'fortlaufend')),
    ADD COLUMN label_printer TEXT NOT NULL DEFAULT '';

CREATE SEQUENCE ps_paper_serial_seq START 1;

ALTER TABLE ps_kauf_scheiben
    ADD COLUMN label_printed_at  TIMESTAMPTZ,
    ADD COLUMN label_print_count INTEGER NOT NULL DEFAULT 0;

COMMIT;
