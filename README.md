# Elektronischer Schießstand – DIY-System für Druckluftwaffen

Komplettes Open-Source-System zur elektronischen Treffererfassung für
Luftgewehr/Luftpistole (10 m, 4,5 mm Diabolo):

```
┌─────────────────────── Stand (je Bahn) ────────────────────────┐
│  Abprallblech + 4 Piezos ──► Komparator ──► ESP32 (Rev 3.4)    │
│  optional: 4 Luft-Mikros  ──► 2. LM339  ──┘                    │
│        │ USB-Serial ODER WLAN-TCP                              │
│        ▼                                                       │
│  Stand-PC (Go: standpc)  – TDOA/TOA, Wertung, Schussprotokoll, │
│                            Schützenanzeige (Browser :8081)     │
└────────────────────────┬───────────────────────────────────────┘
                         │ PostgreSQL + REST (Session-Polling)
                         ▼
              Server (Go: server, Port :8090)
              – Standverwaltung, Sitzungssteuerung,
                Aufsichts-UI, Live-Ticker, Audit-Trail
```

| Komponente | Verzeichnis | Sprache | Aufgabe |
|---|---|---|---|
| Firmware | `standpc/firmware/` | C++ (Arduino) | Zeitstempel-Erfassung (ns), Übertragung |
| Stand-PC | `standpc/` | Go | Positionsberechnung, Wertung, Anzeige |
| Simulator | `standpc/simulator/` | Go | Hardware-Ersatz für Tests |
| Server | `server/` | Go | Zentrale Verwaltung + Datenbank |

Messgenauigkeit (verifiziert im Simulator, 200 ns Triggerjitter):
**Stahl-Modus ±0,5 mm RMS**, **Hybrid-Modus ±0,07 mm RMS** im 10-mm-Zentrum.

---

## 1. Voraussetzungen (Debian 12 „Bookworm")

### 1.1 Go ≥ 1.22 installieren

Das Debian-Paket `golang-go` ist zu alt (1.19). Offizielles Tarball verwenden:

```bash
GO_VER=1.22.5
wget https://go.dev/dl/go${GO_VER}.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go${GO_VER}.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.profile
source ~/.profile
go version          # muss go1.22.x zeigen
```

Für Raspberry Pi (Server): `linux-arm64.tar.gz` statt `linux-amd64`.

### 1.2 PostgreSQL (nur Server-Rechner)

```bash
sudo apt update && sudo apt install -y postgresql
sudo -u postgres psql -c "CREATE USER schiessstand WITH PASSWORD 'CHANGEME';"
sudo -u postgres psql -c "CREATE DATABASE schiessstand OWNER schiessstand;"
```

### 1.3 Serielle Berechtigungen (Stand-PC)

```bash
sudo usermod -aG dialout $USER     # danach ab- und wieder anmelden!
```

Optional – stabiler Gerätename trotz Umsteckens (CH340-Beispiel):

```bash
sudo tee /etc/udev/rules.d/99-schiessstand.rules << 'EOF'
SUBSYSTEM=="tty", ATTRS{idVendor}=="1a86", ATTRS{idProduct}=="7523", SYMLINK+="schiessstand0"
EOF
sudo udevadm control --reload
```

Dann in der `config.json`: `"serial_port": "/dev/schiessstand0"`.

### 1.4 Entwicklung mit VS Code Remote-SSH

1. Lokal: Extension **Remote – SSH** installieren
2. `F1` → „Remote-SSH: Connect to Host" → Debian-PC wählen
3. Remote die Extension **Go** installieren – sie richtet `gopls` und den
   Debugger `delve` automatisch auf dem Zielsystem ein
4. Ordner `standpc/` bzw. `server/` öffnen; Build/Test/Debug laufen
   vollständig remote

---

## 2. Firmware (ESP32) übersetzen und flashen

Hardware: ESP32 DevKit (WROOM-32). Pinbelegung siehe
`schaltplan-rev34-hybrid.html`.

### Variante A: Arduino IDE (einfachster Weg)

1. Arduino IDE 2.x installieren
2. *Datei → Einstellungen → Zusätzliche Boardverwalter-URLs*:
   `https://espressif.github.io/arduino-esp32/package_esp32_index.json`
3. *Werkzeuge → Board → Boardverwalter*: **esp32 by Espressif** installieren
   (Version ≥ 2.0 – nötig für `esp_cpu_get_cycle_count`)
4. Board: **ESP32 Dev Module** · Port: `/dev/ttyUSB0` · Upload Speed: 921600
5. `standpc/firmware/schiessstand_firmware.ino` öffnen → **Hochladen**

### Variante B: arduino-cli (Kommandozeile / Remote-Session)

```bash
curl -fsSL https://raw.githubusercontent.com/arduino/arduino-cli/master/install.sh | sh
export PATH=$PATH:~/bin
arduino-cli config init
arduino-cli config add board_manager.additional_urls \
  https://espressif.github.io/arduino-esp32/package_esp32_index.json
arduino-cli core update-index
arduino-cli core install esp32:esp32

cd standpc/firmware
arduino-cli compile --fqbn esp32:esp32:esp32 .
arduino-cli upload  --fqbn esp32:esp32:esp32 -p /dev/ttyUSB0 .
```

### Erstkonfiguration (einmalig, danach im NVS persistent)

Seriellen Monitor öffnen (`arduino-cli monitor -p /dev/ttyUSB0 -c baudrate=115200`
oder Arduino-IDE-Monitor, 115200 Baud) und eingeben:

```
SET LANE=1
SET SSID=Vereins-WLAN        ← leer lassen = nur USB-Serial
SET PASS=CHANGEME
SET HOST=192.168.1.10        ← IP des Stand-PCs (TCP-Modus)
SET PORT=9000
SET HYBRID=0                 ← 1 = Luft-Mikrofone aktiv (Rev 3.4)
SHOW                         ← Kontrolle
REBOOT                       ← übernimmt SSID/HOST
```

Alle Werte überleben Reboot und sogar Firmware-Updates (NVS-Partition).

---

## 3. Stand-PC-Software übersetzen

```bash
cd standpc
go mod tidy                  # lädt Abhängigkeiten (pgx, serial)
go test ./...                # alle Tests müssen PASS zeigen
go build -o standpc .
go build -o simulator/simulator ./simulator
```

Konfiguration: `config.json` kopieren und anpassen – die Datei ist
durchkommentiert (`_kommentar_*`-Felder). Wichtigste Einträge:

| Feld | Bedeutung |
|---|---|
| `transport` | `serial` (USB), `tcp` (ESP32 verbindet sich per WLAN hierher) oder `both` |
| `sensors` | Piezo-Positionen auf dem Blech in mm – **exakt ausmessen!** |
| `sound_speed_mps` | Körperschall im Blech; Startwert 3000, per Kalibrierung verfeinern |
| `plate_angle_deg` | Neigung des Abprallblechs |
| `hybrid` | Luft-TOA-Feinmessung: `enabled`, Mikrofonpositionen (x/y/z), `gate_us` |
| `server_url` | URL des zentralen Servers; leer = autarker Betrieb |
| `postgres_dsn` | leer = nur lokales Schussprotokoll |

Start:

```bash
./standpc -config config.json
# Schützenanzeige: http://<stand-pc>:8081
```

### Als Dienst (systemd)

```bash
sudo tee /etc/systemd/system/standpc.service << 'EOF'
[Unit]
Description=Schiessstand Stand-PC
After=network-online.target

[Service]
User=schuetze
WorkingDirectory=/opt/schiessstand/standpc
ExecStart=/opt/schiessstand/standpc/standpc -config config.json
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
sudo systemctl enable --now standpc
journalctl -u standpc -f        # Live-Log
```

---

## 4. Server übersetzen und einrichten

```bash
cd server
go mod tidy
go build -o server .

# Datenbank-Schema einspielen (einmalig):
PGPASSWORD=CHANGEME psql -h 127.0.0.1 -U schiessstand -d schiessstand \
  -f migrations/001_schema.sql
PGPASSWORD=CHANGEME psql -h 127.0.0.1 -U schiessstand -d schiessstand \
  -f migrations/002_notify.sql

./server -dsn "postgres://schiessstand:CHANGEME@127.0.0.1/schiessstand" \
         -listen :8090
# Aufsichts-UI: http://<server>:8090
```

systemd analog zu oben (`ExecStart=.../server -dsn ... -listen :8090`).

Beim ersten Aufruf der Aufsichts-UI werden automatisch 6 Stände angelegt
(änderbar über `POST /api/lanes/init {"count":n}`). Die mitgelieferten
Grunddaten enthalten die ISSF-Scheiben **LG 10 m** und **LP 10 m** sowie
die Disziplinen *Luftgewehr 40* und *Luftpistole 40*.

---

## 5. Test ohne Hardware: der Simulator

Der Simulator bildet die Firmware physikalisch nach (TDOA-Zeiten invers
berechnet, Zykluszähler-Quantisierung, Hybrid-Vorläufer/Störflanken) und
spricht dasselbe Protokoll.

```bash
# TCP (wie ESP32 über WLAN):
./simulator/simulator -config config.json -connect 127.0.0.1:9000

# Virtueller serieller Port (testet den USB-Pfad):
./simulator/simulator -config config.json -pty
#   → Ausgabe „/dev/pts/N" als serial_port eintragen

# Modi:
#   -mode auto   -interval 8s -spread 1.5    Schützenmodell (sigma in mm)
#   -mode manual                              "shot 2.5 -1.0", "r", "q"
# Realismus:
#   -noise 0.2    Triggerjitter sigma in µs
#   -dropout 0.02 Sensorausfall-Wahrscheinlichkeit
```

Empfohlener Komplett-Test: Server starten → Stand in der Aufsichts-UI
belegen → Simulator feuern lassen → Treffer erscheinen live in
Schützenanzeige (:8081), Aufsichts-UI (:8090) und Datenbank.

---

## 6. Inbetriebnahme / Kalibrierung (Kurzfassung)

1. **Schwellen einstellen:** RV1 (Stahl) so, dass Klopfen mit dem
   Fingerknöchel auslöst, Umgebungslärm nicht (~80–120 mV).
   Hybrid: RV2 (Luft) separat auf ~15–20 mV.
2. **Sensorpositionen** mit Messschieber auf ±0,2 mm in `config.json`
   (bzw. Server-Kalibrierung) eintragen.
3. **Schallgeschwindigkeit:** 5 Referenzschüsse auf bekannte Punkte;
   `sound_speed_mps` so anpassen, dass Soll = Ist (typ. 2000–3200 m/s
   je nach Blech – NICHT der Tabellenwert für Stahl-Longitudinalwellen!).
4. **Blech↔Scheibe-Offset:** Schuss auf Scheibenmitte → Abweichung in
   `plate_offset_x/y_mm` eintragen.
5. **DEBOUNCE prüfen:** Löst ein Schuss mehrfach aus → Wert erhöhen
   (`DEBOUNCE=150`), Standard 100 ms.

---

## 7. Fehlersuche

| Symptom | Ursache / Abhilfe |
|---|---|
| `permission denied /dev/ttyUSB0` | `dialout`-Gruppe fehlt (Abschnitt 1.3), neu anmelden |
| Keine Schüsse, ESP32-Status ok | Schwelle zu hoch (RV1) oder Pull-ups fehlen (LM339 = Open Collector!) |
| Schuss löst mehrfach aus | Blech schwingt nach → `DEBOUNCE=` erhöhen, Blech rückseitig dämpfen |
| `reject "only N sensor(s)"` | Sensor-Kabel/Lötstelle prüfen; Ankopplung (Silikon) erneuern |
| Position systematisch versetzt | `plate_offset_*`, Blechwinkel, Sensorkoordinaten prüfen |
| Position skaliert falsch | `sound_speed_mps` kalibrieren (Schritt 6.3) |
| Hybrid fällt auf `[steel]` zurück | Mikrofone entkoppelt? RV2-Schwelle? `gate_us` testweise auf 25 |
| WLAN verbindet, TCP nicht | `SET HOST=` zeigt auf falsche IP; Firewall Port 9000 am Stand-PC |
| DB-Queue wächst (`WARNUNG ... Queue`) | Server/Netz prüfen – Schüsse sind im lokalen Protokoll gesichert |
| `go build`: Versionsfehler | Go < 1.22 → Tarball-Installation (Abschnitt 1.1) |

**Datensicherheit:** Jeder Schuss wird VOR der Datenbank ins lokale
Schussprotokoll geschrieben (`shotlog/laneNN_DATUM.jsonl`, append-only,
fsync) – inklusive der Rohzeitstempel. Damit lässt sich jeder Schuss nach
einer Kalibrierungskorrektur neu berechnen und bei DB-Ausfall nachweisen.

---

## 8. Dokumente im Projekt

| Datei | Inhalt |
|---|---|
| `schaltplan-rev34-hybrid.html` | Vollständiger Schaltplan mit Pinbelegungen (Stahl + Hybrid) |
| `standpc/firmware/schiessstand_firmware.ino` | Firmware Rev 3.4 (Befehle im Dateikopf) |
| `server/migrations/001_schema.sql` | Datenmodell inkl. Workflow-Doku am Dateiende |
| `datenmodell.sql` | identisch, Standalone-Version |

---

## 9. Hardware-Dimensionierung (großes Preisschießen)

Lastabschätzung für ein großes Preisschießen (Stand 2026-09-09), zugrunde
gelegtes Szenario: 20 gleichzeitig belegte Stände, ca. 1 Schuss/15s je Stand,
3 Standanzeige-Displays (`/anzeige/{id}`), Preisschießen-Kioskseite alle
30s aufgerufen, Neuauswertung des Preisschießens alle 5 Minuten (Default im
Code sogar alle 30s), ca. 1 Neuanmeldung/Scheibenkauf pro Minute.

### Lastabschätzung

| Quelle | Rate | Was passiert |
|---|---|---|
| Schüsse (20 Stände × 1/15s) | ~1,3/s | Stand-PC schreibt **direkt** über eigene Postgres-Verbindung (`standpc/db.go`) – umgeht den zentralen `server`-Prozess vollständig |
| Session-/Kalibrierungs-Poll (20 Stände, alle 3s) | ~6,7 Anfragen/s | `GET /api/lanes/{no}/session`, einfacher indizierter Read |
| Livestate-Heartbeat (20 Stände, alle 3s) | ~6,7 Anfragen/s | `PUT /api/lanes/{no}/livestate`, inkl. 1 kleinem UPDATE je Aufruf |
| 3 Anzeige-Displays (Standanzeige, Reload ~8s Standard) | ~20-25 einfache Queries/s | pro Kachel: Session, Ergebnis-View, aktuelle Serie, Ringgeometrie |
| Preisschießen-Kiosk-Seite (alle 30s) | <1 Anfrage/s | liest nur den bereits vorberechneten Cache |
| Neuauswertung Preisschießen | alle 5 Min. (Default-Ticker: 30s) | einzige "schwerere" Aufgabe, Aggregation über wenige tausend Zeilen |
| Neuanmeldung/Scheibenkauf | ~1/Min. | vernachlässigbar |

Insgesamt ca. **30-40 einfache, indizierte Datenbankzugriffe pro Sekunde**
beim Server – für Postgres eine triviale Last, keine der Aufgaben ist
rechenintensiv (kein Video, keine Aggregation über Millionen Zeilen).

**Realer Referenzwert** (gemessen am 2026-09-09, Datenbestand nach
regelmäßigem Aufräumen "wie bei einem mittleren Verein"): 1.918 Schützen,
6.178 Sessions, **55.065 Schüsse**, 1.008 Preisschießen-Teilnehmer,
Datenbankgröße insgesamt **38 MB**. Dieser Rechner (4 Kerne, 4 GB RAM) lief
zu diesem Zeitpunkt bereits mit Server + Preisanzeige + 8 Stand-PC-Instanzen
+ Postgres gleichzeitig, ohne spürbare Verzögerung.

### Variante A – alles auf einem System

Stand-PCs bleiben in jedem Fall pro Stand ein eigenes Gerät (physische
Verkabelung zum Blech) – "alles zusammen" heißt hier: Server, Datenbank und
Display-Server (`preisanzeige`) auf einer Maschine.

| Komponente | Empfehlung | Begründung |
|---|---|---|
| CPU | 4 Kerne (z.B. Intel N100/N305, beliebiger moderner i3/Ryzen 3) | Reserve für die Neuauswertung parallel zum Normalbetrieb |
| RAM | 8 GB | Postgres + 2 Go-Binaries sind klein; 8 GB gibt Puffer statt Minimalbetrieb |
| Storage | SSD (auch klein, 60 GB+) | kein Kapazitätsproblem (DB bleibt im MB-Bereich), sondern fsync-Latenz/Zuverlässigkeit – keine SD-Karte |
| Ausfallsicherheit | USV empfehlenswert | ein Rechner trägt jetzt Anzeigen UND Wertung UND zentrale DB gleichzeitig |

Ein Raspberry Pi 5 (8 GB) würde diese Last rechnerisch ebenfalls schaffen;
für einen großen/wichtigen Wettkampftag empfiehlt sich trotzdem ein
günstiger x86-Mini-PC mit SSD statt SD-Karte (Haltbarkeit unter Dauerlast an
einem einzelnen wichtigen Tag), nicht wegen fehlender Rechenleistung.
