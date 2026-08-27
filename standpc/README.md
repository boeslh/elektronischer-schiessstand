# standpc – Stand-PC-Software

Empfängt Telegramme vom ESP32 (Serial oder TCP), berechnet die
Trefferposition (Stahl-TDOA, optional Luft-TOA-Verfeinerung), wertet
nach Scheibendefinition, protokolliert lokal (append-only JSONL),
schreibt in die zentrale DB und zeigt live im Browser an.

## Build
    go mod tidy && go test ./... && go build -o standpc .
    go build -o simulator/simulator ./simulator

## Start
    ./standpc -config config.json        # Anzeige: http://localhost:8081

## Dateien
    main.go      Konfiguration, Pipeline-Verdrahtung
    serial.go    USB-Serial-Reader (Auto-Reconnect)
    tcp.go       TCP-Server für ESP32-WLAN-Verbindungen
    transport.go gemeinsamer Telegramm-Dispatch
    tdoa.go      Stahl-Solver (Gauss-Newton) + Blech→Scheibe-Geometrie
    hybrid.go    Luft-TOA-Verfeinerung (Gating + Multilateration)
    score.go     Ring/Zehntel/Teiler nach Scheibendefinition
    shotlog.go   lokales Schussprotokoll (fsync, Tagesrotation)
    db.go        asynchroner PostgreSQL-Writer mit Retry-Queue
    session.go   Session+Kalibrierung vom Server (Poll, 3 s)
    web.go       Schützenanzeige (SSE, eingebettetes HTML)
    simulator/   Hardware-Simulator (siehe Haupt-README §5)

Vollständige Installations-/Kalibrieranleitung: ../README.md
