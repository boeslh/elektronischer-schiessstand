-- 075_ui_role_sessions_browser.sql – Rollen-Sitzungen nur noch als Browser-
-- Sitzungs-Cookie (endet mit dem Schliessen des Browsers) statt 1 Jahr.
-- browser_session=true kennzeichnet Sitzungen, die bereits so entstanden
-- sind - aeltere Sitzungen (persistentes Jahres-Cookie) gelten fuer Rollen
-- MIT Passwort nicht mehr, dort ist eine neue Anmeldung noetig (siehe
-- roles.go ResolveOrCreateSession).
ALTER TABLE ui_role_sessions ADD COLUMN browser_session BOOLEAN NOT NULL DEFAULT false;
