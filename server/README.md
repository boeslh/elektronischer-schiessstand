# server – zentrale Standsteuerung ("Workstation")

Standverwaltung, Sitzungssteuerung, Aufsichts-UI, Live-Ticker
(pg_notify→SSE), Audit-Trail. Stand-PCs holen Session + Kalibrierung
über GET /api/lanes/{nr}/session (null = Stand frei).

## Einrichtung
    psql -f migrations/001_schema.sql     # Datenmodell + Grunddaten
    psql -f migrations/002_notify.sql     # Live-Trigger
    go mod tidy && go build -o server .
    ./server -dsn "postgres://user:pass@host/schiessstand" -listen :8090
    # Aufsichts-UI: http://server:8090

## API (Auszug)
    GET  /api/lanes                    Übersicht mit Belegung/Ergebnis
    POST /api/lanes/{nr}/assign        Stand belegen
    GET  /api/lanes/{nr}/session       ← Stand-PC-Endpunkt
    POST /api/sessions/{id}/status     sighting|match|paused|finished|aborted
    POST /api/sessions/{id}/shots/{n}/annul   Kampfrichter-Annullierung
    GET  /events                       SSE-Live-Feed aller Stände

Vollständige Installationsanleitung: ../README.md
