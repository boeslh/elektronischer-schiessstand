-- ============================================================================
-- 091_label_auto_print.sql – Labels der Papierscheiben direkt nach dem
-- Bezahlen in der Kasse drucken (Einstellungen -> Papierscheiben).
-- ============================================================================
BEGIN;

ALTER TABLE app_settings
    ADD COLUMN label_auto_print BOOLEAN NOT NULL DEFAULT false;

COMMIT;
