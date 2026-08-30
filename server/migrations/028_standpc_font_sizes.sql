-- Konfigurierbare Schriftgrößen für die Stand-PC-Anzeige, gepflegt unter
-- Einstellungen und aktiv auf die Stände gepusht (siehe standpc/font_sizes.json).
-- Defaults entsprechen den bisherigen hart codierten Werten in
-- standpc/web/index.html.
ALTER TABLE app_settings
  ADD COLUMN font_name       SMALLINT NOT NULL DEFAULT 16,
  ADD COLUMN font_scheibe    SMALLINT NOT NULL DEFAULT 13,
  ADD COLUMN font_status     SMALLINT NOT NULL DEFAULT 11,
  ADD COLUMN font_menu       SMALLINT NOT NULL DEFAULT 14,
  ADD COLUMN font_menu_ps    SMALLINT NOT NULL DEFAULT 14,
  ADD COLUMN font_ergebnisse SMALLINT NOT NULL DEFAULT 13;
