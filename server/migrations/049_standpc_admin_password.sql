-- Admin-GUI-Passwort für die Stand-PCs (Firmware Rev 4.9.0 Interaktions-
-- Funktionen: Kalibrierung/Konfiguration lokal am Stand-PC, siehe
-- server/settings.go SetStandpcAdminPassword/pushStandpcAdminPasswordHash).
-- Es wird nur der bcrypt-Hash gespeichert - das Klartext-Passwort verlässt
-- den Server-Prozess nie in persistenter Form (analog ui_roles.password_hash,
-- siehe server/roles.go).
ALTER TABLE app_settings
  ADD COLUMN standpc_admin_password_hash TEXT;
