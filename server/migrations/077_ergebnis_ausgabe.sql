-- 077_ergebnis_ausgabe.sql – Einstellungen fuer die Ergebnis-Ausgabe am
-- Stand-PC (siehe ergebnis_ausgabe.go): Mailserver fuer den Versand des
-- Ergebnis-PDFs an die in den Stammdaten hinterlegte Adresse und der
-- CUPS-Drucker fuer den Ausdruck. smtp_password wird zum Versand im Klartext
-- benoetigt (nur ueber die Einstellungs-API schreibbar, nie ausgegeben).
ALTER TABLE app_settings
  ADD COLUMN smtp_host      TEXT NOT NULL DEFAULT '',
  ADD COLUMN smtp_port      INTEGER NOT NULL DEFAULT 587,
  ADD COLUMN smtp_security  TEXT NOT NULL DEFAULT 'starttls'
                            CHECK (smtp_security IN ('starttls','ssl','none')),
  ADD COLUMN smtp_user      TEXT NOT NULL DEFAULT '',
  ADD COLUMN smtp_password  TEXT NOT NULL DEFAULT '',
  ADD COLUMN smtp_from      TEXT NOT NULL DEFAULT '',
  ADD COLUMN smtp_from_name TEXT NOT NULL DEFAULT '',
  ADD COLUMN printer_name   TEXT NOT NULL DEFAULT '';
