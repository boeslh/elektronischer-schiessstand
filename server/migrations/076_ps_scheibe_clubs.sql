-- 076_ps_scheibe_clubs.sql – Vereins-Restriktion je Preisschiessen-Scheibe,
-- analog ps_scheibe_classes: keine Zeile = alle Vereine erlaubt. Eine
-- beschraenkte Scheibe darf nur von Teilnehmern der eingetragenen Vereine
-- (shooters.club_id) gekauft werden - auch als Teil eines Sets: ein Set ist
-- nur kaufbar, wenn alle darin enthaltenen beschraenkten Scheiben den Verein
-- des Teilnehmers zulassen (siehe preisschiessen.go ListAngebot/purchaseItem).
CREATE TABLE ps_scheibe_clubs (
    scheibe_id  UUID NOT NULL REFERENCES ps_scheiben(id) ON DELETE CASCADE,
    club_id     UUID NOT NULL REFERENCES clubs(id)       ON DELETE CASCADE,
    PRIMARY KEY (scheibe_id, club_id)
);
