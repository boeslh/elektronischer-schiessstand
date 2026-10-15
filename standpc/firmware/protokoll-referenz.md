# Schnittstellen-Referenz: ESP32-Schießstand-Firmware ↔ Stand-PC

**Zweck dieses Dokuments:** vollständige, aktuelle Beschreibung des Verhaltens
und des Kommunikationsprotokolls der ESP32-Firmware, damit eine Stand-PC-Software
(egal in welcher Sprache/Session entwickelt) korrekt darauf abgestimmt werden
kann. Beschreibt den **Ist-Zustand**, keine geplanten Erweiterungen (dafür siehe
`docs/remote-interaktion-konzept.md` im selben Repo).

- Firmware-Datei: `src/schiessstand_firmware.ino`
- Firmware-Version zum Zeitpunkt dieser Doku: **4.17.0**
- Bei Abweichungen zwischen diesem Dokument und dem Quellcode gilt der
  Quellcode als maßgeblich.
- Rev 4.9.0 hat die erste Umsetzungsstufe aus
  `docs/remote-interaktion-konzept.md` bereits implementiert: Geräte-ID
  (MAC), Korrelations-ID, `ACTION`-Namensraum (LED/Scheibenwechsel),
  `CAL IMPORT` (atomarer Kalibrierungs-Restore) und das
  Netzwerk-Sicherheitsnetz (`NET CONFIRM`/`NET STATUS`). Dieses Dokument
  beschreibt bereits den **neuen** Stand - noch offen aus dem Konzept sind
  nur die serverseitige Umsetzung (passives Mitschneiden, Datenhaltung) und
  die optionale `AUTH`-Token-Absicherung.
- Rev 4.9.1 hat zusätzlich `TESTSHOOT [<z_mm>]` ergänzt - ein synthetischer
  Testschuss (zufällige Position, plausible Rohdaten nach aktueller
  Kalibrierung) zum Prüfen der Kommunikationsstrecke ohne echte Sensorik,
  siehe 5.7 und 7.7.
- Rev 4.12.0 hat die Endposition bei `ALGO=CLASSIC` (Stufe 2) überarbeitet:
  `SET CLUSTERCORR`, `SET CLUSTERITER`, `SET LSQ`, `SET LSQPELLETR` (siehe
  5.2 und 7.3). **Achtung:** Die Defaults `CLUSTERCORR=1`/`CLUSTERITER=5`
  ändern die gemeldeten Positionen gegenüber 4.11.x auch ohne neuen
  `SET`-Befehl; `CLUSTERCORR=0` + `CLUSTERITER=0` + `LSQ=0` stellt das
  bisherige Verhalten exakt wieder her. Außerdem trägt jetzt auch die
  `config`-Antwort auf `SHOW #<id>` das `corr`-Feld (siehe 2).
- Rev 4.13.0 hat `SET CAPTURE=ISR|MCPWM` ergänzt (Default `ISR` = bisheriges
  Verhalten): Zeitstempel der Luft-Mics optional per MCPWM-Hardware-Capture
  statt GPIO-Interrupt, siehe 5.2 und 7.1. Neue Felder `capture` in `status`
  (aktiver Modus, plus `cap_*`-Diagnose) und `config` (eingestellter Modus).
  Telegramm-Formate und Zeitbezüge (`air_ns` usw.) bleiben unverändert.
- Rev 4.14.0 hat die Pulsbreiten-Korrektur ergänzt (`SET WALKK`/`WALKCAL`/
  `WALKMAX`, Default `WALKK=0` = aus, nur mit `CAPTURE=MCPWM` wirksam) -
  gleicht die amplitudenabhängige Auslöseverzögerung der Komparatoren aus,
  siehe 5.2 und 7.1. Neu: Feld `air_w_ns` in `shot` (nur bei aktivem MCPWM),
  `walk_k`/`walk_fitted` in `cal`/`done`, `walk_*` in `config`.
- Rev 4.15.0 hat `SET DHT=0|1` ergänzt (Default 0): Schallgeschwindigkeit aus
  Temperatur/Luftfeuchte eines DHT22 an GPIO15, siehe 5.2 und 7.2.
  `SET SOUNDSPEED` gilt als Ersatzwert weiter, jetzt mit Nachkommastelle und
  neuem Default 342.5 (nur bei leerem NVS bzw. nach `CAL RESET`). Neue Felder:
  `status`: `sound_act_mps`, `sound_src`, bei `DHT=1` `temp_c`/`rh_pct`/
  `dht_age_s`/`dht_err`; `config`: `sound_set_mps`, `dht`; `cal`/`done`:
  `sound_set_mps`, `sound_act_mps`. **`sound_mps` bleibt eine gerundete
  Ganzzahl** (Kompatibilität) - für den genauen Wert `sound_set_mps` lesen.
- Rev 4.16.0: neues optionales Telegramm `raw` (`SET RAWLOG=1`, Default 0) mit
  allen unkorrigierten Flanken und den aktiven Parametern zu jedem gültigen
  Schuss - für den Server-Simulator, siehe 4.5c. `shot` trägt jetzt
  `sound_act_mps`. Der Offline-Puffer (3.7) fasst jetzt 32 Zeilen à 1024 Byte
  statt 64 à 360 - lange `shot`-Zeilen wurden vorher beim Puffern
  abgeschnitten. Abgelehnte Schüsse verhalten sich unverändert.
- Rev 4.16.1 (Bugfix): Sendepuffer vergrößert, weil Telegramme abgeschnitten
  wurden - `config` (Antwort auf `SHOW`, typisch ~1000, max. ~1230 Zeichen)
  jetzt 1536 statt 900 Byte, `shot` 1024 statt 900 Byte, Offline-Puffer
  1024 Byte + Signatur-Präfix je Zeile. Felder und Formate unverändert. Ein
  Host sollte trotzdem mit Zeilen bis mindestens 2 KB (`config`) bzw. 2,6 KB
  (`raw`) rechnen.
- Rev 4.17.0: **Zonen-Kalibrierung** (nur `ALGO=CLASSIC`, Default aus): je
  Bereich der Scheibe ein eigener Satz aus Schallgeschwindigkeit, `OFS0-5` und
  `OFFSETX/Y` - A = im Loch der Vorsatzscheibe (nur Schussband), B = neben dem
  Band (nur Vorsatzscheibe), C = Band + Vorsatzscheibe. Neue Befehle
  `SET ZONES/ZHOLE/ZBAND/ZBLEND/ZCAL` (5.2) und `SHOWZONES` (5.1, Telegramm
  `zones`, 4.3b), neue Felder `zones`/`z_hole_um` in `config` und `zone` in
  `shot`. Ablauf siehe 7.3c. `CAL RESET` verwirft auch die Zonen-Sätze.
  Werks-Defaults (nur bei leerem NVS): `RADIUS` 4000 statt 200 (0,2 mm war
  ein Einheitenfehler), `BSHIFTPCT` 30 statt 40.

---

## 1. Systemüberblick

```
┌─────────────┐   TCP, ESP32 verbindet aktiv    ┌─────────────┐
│    ESP32    │ ───────────────────────────────▶│   Stand-PC  │
│ (TCP-Client)│◀───────────────────────────────│ (TCP-Server)│
└─────────────┘   selbe Verbindung, bidirektional └─────────────┘
       │
       └── zusätzlich: identisches Protokoll über USB/Serial (115200 Baud),
           für lokale Bedienung/Diagnose per Terminal
```

- Der **ESP32 ist TCP-Client**, der **Stand-PC ist TCP-Server**. Der ESP32
  baut die Verbindung zu `host:port` auf (`SET HOST`/`SET PORT`), nicht
  umgekehrt. Der Stand-PC muss also einen TCP-Server auf dem konfigurierten
  Port betreiben und eingehende Verbindungen annehmen.
- **Eine einzige TCP-Verbindung pro Gerät**, bidirektional, zeilenbasiert:
  - ESP32 → Stand-PC: überwiegend JSON-Objekte, eine Zeile pro Nachricht
    (Ausnahme: Testmodus, siehe Abschnitt 8 - dort Klartext).
  - Stand-PC → ESP32: Text-Befehle, eine Zeile pro Befehl (`SET ...`, `SHOW`,
    `CAL START`, ...).
- Das **gleiche Befehls-/Antwortprotokoll** läuft identisch über die
  USB/Serial-Schnittstelle (115200 Baud, 8N1) - Unterschied nur im
  Zeilenende (siehe Abschnitt 2) und im lokalen Echo (Serial echot
  eingehende Zeichen sofort zurück, TCP nicht).
- Es gibt **keinen zweiten Kanal** (kein REST/HTTP/MQTT auf dem ESP32) und
  **keine Authentifizierung** auf dem TCP-Port - jeder, der den Port
  erreicht, kann Befehle senden.
- Die komplette Befehlsverarbeitung ist **nicht-blockierend**: kein Befehl
  darf/kann die Schusserfassung (Interrupt-Routinen laufen unabhängig weiter)
  länger als einen `loop()`-Durchlauf (Mikrosekunden) aufhalten. Auch
  mehrsekündige Aktionen wie der Papiervorschub laufen über eine eigene
  Zustandsmaschine (`servicePaperStepper()`), nie über `delay()`.

## 2. Transport & Framing

- **Zeilentrenner TCP:** reines `\n` (LF), sowohl für ausgehende JSON-Zeilen
  als auch für erwartete eingehende Befehlszeilen. `\r` wird beim Empfang
  toleriert/ignoriert (Zeilenpuffer wird bei `\n` **oder** `\r` abgeschlossen).
- **Zeilentrenner Serial:** ausgehend `\r\n` (terminalfreundlich), eingehend
  wie TCP toleriert.
- **Eingehende Befehlszeile:** maximal 96 Zeichen (`pollCommands()`,
  längere Eingaben werden abgeschnitten/verworfen, nicht gepuffert).
- **Groß-/Kleinschreibung:** Befehls-Schlüsselwörter (`SET`, `SHOW`, `CAL`,
  Parametername vor `=`) sind **case-insensitiv** (intern auf Großschreibung
  normalisiert). **Werte** nach `=` sind **case-sensitiv** - wichtig für
  `SET SSID=...` und `SET PASS=...`, dort NICHT automatisch umschreiben.
- **Keine Escaping-Konventionen** über das übliche JSON-Escaping hinaus -
  das Protokoll ist ein einfacher Text-Zeilenstrom, kein Multiplexing,
  keine Frame-Header, keine Prüfsummen.
- **Korrelation Anfrage/Antwort (seit Rev 4.9.0):** jeder Befehl kann mit
  einem Suffix `" #<id>"` (Leerzeichen, `#`, danach nur Ziffern) enden, z.B.
  `SET LANE=2 #7`. Jede JSON-Zeile, die die Firmware **während der
  Verarbeitung genau dieses einen Befehls** sendet, trägt dann zusätzlich
  `"corr":<id>` (Ganzzahl, kein String). Antworten auf Befehle **ohne**
  Suffix enthalten kein `corr`-Feld. Das Suffix wird rein textuell anhand
  des letzten `" #"` in der Zeile erkannt und danach abgetrennt, **bevor**
  der eigentliche Befehl geparst wird - bei `SET SSID=...`/`SET PASS=...`
  mit einem Wert, der zufällig auf `" #<Ziffern>"` endet, würde das
  fälschlich als Korrelations-ID interpretiert und abgeschnitten (seltener
  Grenzfall, aber zu vermeiden). Ohne Suffix verhält sich alles wie zuvor:
  Antworten kommen in Verarbeitungsreihenfolge, ohne Suffix ist die einzige
  Zuordnungshilfe weiterhin die Reihenfolge plus charakteristische Felder
  (`"set":"<key>"` in der `ok`-Antwort). **Bis Rev 4.11.5** fehlte `corr`
  bei Zeilen über ca. 130 Byte - insbesondere bei der `config`-Antwort auf
  `SHOW #<id>`, die dadurch beim Stand-PC in den Timeout lief. Seit Rev
  4.12.0 wird `corr` an jede Zeile bis ca. 1000 Byte angehängt, also an
  alle Telegramme dieser Firmware.

## 3. Verbindungslebenszyklus

1. **Boot:** ESP32 lädt Konfiguration aus NVS (`loadConfig()`), initialisiert
   Sensorik, startet WLAN (`WiFi.begin`) falls `SET SSID` gesetzt ist (leere
   SSID = WLAN komplett aus, keine Reconnect-Versuche).
2. **WLAN-Verbindungsaufbau:** Standard DHCP, außer `SET STATIC=1` mit
   gültiger `SET IP`/`SET GW` - dann feste IP (`SET SUBNET`, `SET DNS`
   optional, DNS-Default = Gateway). Seit Rev 4.12.1: ist das WLAN länger
   als 30 s nicht verbunden (auch direkt nach dem Boot, z.B. weil der Access
   Point noch nicht bereit ist), startet der ESP32 den WLAN-Verbindungsaufbau
   selbst neu - unabhängig davon, ob der Core-Autoreconnect aufgegeben hat.
3. **TCP-Verbindungsaufbau** (`maintainNetwork()`, aufgerufen bei jedem
   `loop()`): sobald WLAN verbunden ist, versucht der ESP32 `tcp.connect(host,
   port, timeout=2000ms)`. Bei Erfolg: `tcp.setNoDelay(true)` (kein
   Nagle-Delay) und TCP-Keepalive (5 s Leerlauf, 2 s Intervall, 3 Proben),
   danach wird **sofort ein `status`-Telegramm gesendet** (Abschnitt 4.2).
4. **Reconnect-Backoff bei Fehlschlag:** Startwert 1000 ms, verdoppelt sich
   nach jedem Fehlschlag bis maximal 8000 ms (bis Rev 4.12.0: 16000 ms).
   Backoff-Reset bei erfolgreicher Verbindung und nach jedem
   WLAN-(Wieder-)Verbindungsaufbau (dann sofortiger TCP-Versuch). Bricht die
   WLAN-Verbindung selbst ab (`WiFi.status() != WL_CONNECTED`), wird die
   TCP-Verbindung sofort beendet (`tcp.stop()`) und der Zyklus beginnt nach
   WLAN-Reconnect neu. Schließt der Stand-PC die Verbindung (FIN/RST), baut
   der ESP32 sie sofort neu auf; ist der Stand-PC ohne Abmeldung weg
   (abgeschaltet), erkennt der Keepalive das nach ca. 11 s.
5. **Nach jedem Boot** sendet der ESP32 unmittelbar nach `setup()`:
   `status` gefolgt von `{"type":"ready"}`. Das ist der früheste Zeitpunkt,
   zu dem über Serial etwas ankommt (WLAN/TCP können zu diesem Zeitpunkt
   noch nicht verbunden sein - `ready`/`status` gehen dann nur über Serial,
   sobald TCP steht, wiederholt `maintainNetwork()` das `status`-Telegramm).
6. **Verbindungsabbruch:** Der ESP32 erkennt es über `tcp.connected()` und
   versucht automatisch erneut (Schritt 3/4). Es gibt keinen "disconnect"-
   Telegrammtyp - der Stand-PC erkennt den Abbruch nur am Wegfallen der
   TCP-Verbindung auf seiner (Server-)Seite.
7. **Sendepuffer bei Verbindungsverlust:** Während die TCP-Verbindung fehlt,
   werden **ausschließlich** `shot`- und `reject`-Telegramme in einem
   Ringpuffer (`TXBUF_SLOTS=32` Zeilen à max. `TXBUF_LINE=1024` Byte inkl.
   Signatur-Präfix; bis Rev 4.15: 64 à 360 - längere `shot`-Zeilen wurden
   dabei abgeschnitten)
   zwischengespeichert; alle anderen Telegrammtypen (`status`, `config`,
   `cal`, ...) gehen bei fehlender Verbindung **verloren**, nicht gepuffert.
   Läuft der Puffer bei anhaltender Trennung über 32 Zeilen hinaus, wird die
   jeweils älteste verworfen (reines Ringpuffer-Überschreiben, kein
   Fehlertelegramm dafür). Bei Wiederverbindung wird der komplette Puffer
   **vor** der nächsten Live-Zeile ausgeliefert (Reihenfolge bleibt erhalten,
   aber ggf. mit Lücke durch verworfene ältere Einträge).
   → **Praxisfolge für den Stand-PC:** nach einer Reconnect-Phase kann ein
   kurzer Schwall älterer `shot`/`reject`-Zeilen ankommen, die zeitlich vor
   dem `status`-Telegramm dieser neuen Verbindung liegen (das `status`-
   Telegramm selbst wird bei Connect frisch gesendet und läuft nicht durch
   diesen Puffer). Anhand `seq` (siehe 4.5) lässt sich die eigentliche
   zeitliche Reihenfolge/Lücke erkennen.
8. **Standbeleuchtung (LED, GPIO22)** spiegelt automatisch den
   Verbindungsstatus: an beim Boot, bleibt an während `tcp.connected()`,
   sonst nach 10 s aus, danach (falls WLAN selbst noch steht) ein
   1 s-Diagnoseblinken nach weiteren 1 s Wartezeit. Kein Telegramm begleitet
   diese automatischen Zustandswechsel - rein visuell vor Ort. Seit Rev 4.9.0
   per `ACTION LIGHT=ON|OFF` fernsteuerbar (siehe 5.5) - das pausiert die
   automatische Anzeige komplett, bis `ACTION LIGHT=AUTO` sie wieder
   freigibt (nicht persistent, nach Reboot immer wieder `AUTO`).

## 4. Telegramme ESP32 → Stand-PC (Referenz)

Alle Telegramme sind einzeilige JSON-Objekte mit Pflichtfeld `"type"`. Felder
in eckigen Klammern `[...]` unten sind bedingt (nur unter der genannten
Voraussetzung vorhanden).

### 4.1 `ready`

Einmalig nach jedem Boot, direkt nach dem ersten `status`.

```json
{"type":"ready"}
```

### 4.2 `status` — Betriebsstatus

Gesendet: bei jedem neuen TCP-Connect, sonst nur auf Anfrage (`STATUS`-Befehl).
**Kein periodisches Heartbeat** - der Stand-PC muss `STATUS` selbst anfragen,
wenn er es regelmäßig braucht.

```json
{"type":"status","version":"4.9.0","mac":"AA:BB:CC:DD:EE:FF","lane":1,
 "uptime_s":12345,"shots":42,"window_ms":1,"debounce_ms":100,"mics":6,
 "buffered":0,"test_mode":0,"capture":"isr",
 "sound_act_mps":343.85,"sound_src":"dht","temp_c":20.0,"rh_pct":50.0,
 "dht_age_s":12,"dht_err":0}
```

Bei aktivem MCPWM-Capture (siehe `capture`):

```json
{..., "test_mode":0,"capture":"mcpwm","cap_sync_ns":-120,"cap_check_ns":85,
 "cap_reanchor":0}
```

| Feld | Typ | Bedeutung |
|---|---|---|
| `version` | string | Firmware-Version (`FW_VERSION`) |
| `mac` | string | **Geräte-ID seit Rev 4.9.0**: Basis-MAC-Adresse (`esp_read_mac()`, Format `AA:BB:CC:DD:EE:FF`), unabhängig vom WLAN-Verbindungsstatus - stabile Kennung für die Stand-PC-Datenhaltung, unabhängig von der frei vergebbaren `lane` |
| `lane` | uint | Bahnnummer (`SET LANE`) |
| `uptime_s` | uint64 | Sekunden seit Boot (`esp_timer`, nicht Wanduhrzeit) |
| `shots` | uint | Zähler aller Auslösungen seit Boot/`RESET` (inkl. Rejects, siehe 4.5) |
| `window_ms` | uint | aktuelles `SET WINDOW` |
| `debounce_ms` | uint | aktuelles `SET DEBOUNCE` |
| `mics` | int | Anzahl Luft-Mikrofonkanäle (Hardware-Konstante, aktuell immer 6) |
| `buffered` | uint | Anzahl aktuell im Reconnect-Ringpuffer wartender `shot`/`reject`-Zeilen (siehe 3.7) |
| `test_mode` | 0/1 | ob `SET TESTMODE=1` aktiv ist |
| `capture` | `"isr"`\|`"mcpwm"` | **seit Rev 4.13.0**: **tatsächlich aktive** Zeitstempel-Erfassung der Luft-Mics. Kann trotz `SET CAPTURE=MCPWM` `"isr"` sein, wenn die MCPWM-Initialisierung beim Boot fehlschlug (Grund steht in der seriellen Boot-Ausgabe) |
| `cap_sync_ns` | int | nur bei `capture:"mcpwm"`: Boot-Prüfung des Gruppen-Syncs (Soft-Catch Gruppe 1 gegen Anker aus Gruppe 0). Der Wert ist die Differenz zweier Callback-Latenzen, normal also nahe 0 (Betrag typ. < 2000). Bei mehr als ±20000 wäre der Boot auf ISR zurückgefallen |
| `cap_check_ns` | int | nur bei `capture:"mcpwm"`: letzte Gegenprüfung der Zeitbasis (alle 10 s in Ruhephasen). Ebenfalls Differenz zweier Callback-Latenzen, normal nahe 0 (Betrag typ. < 2000) und ohne Trend über die Betriebszeit |
| `sound_act_mps` | float (m/s) | **seit Rev 4.15.0**: aktuell für die Auswertung verwendete Schallgeschwindigkeit |
| `sound_src` | `"dht"`\|`"fixed"` | **seit Rev 4.15.0**: `"dht"` = aus der DHT22-Messung berechnet, `"fixed"` = `SET SOUNDSPEED` (bei `DHT=0`, vor der ersten gültigen Messung oder wenn die letzte gültige Messung älter als 5 min ist) |
| `temp_c`,`rh_pct` | float oder `null` | nur bei `SET DHT=1`: gefilterte Messwerte (Median der letzten 3 gültigen Messungen), `null` = noch keine gültige Messung |
| `dht_age_s` | uint | nur bei `SET DHT=1` und gültiger Messung: Sekunden seit der letzten gültigen Messung (Messabstand 30 s) |
| `dht_err` | uint | nur bei `SET DHT=1`: fehlgeschlagene/verworfene Messungen seit Boot (Prüfsumme, Timeout, unplausibel). Einzelne Fehler sind normal (Interrupts während des Auslesens), ein stetig steigender Wert ohne `temp_c` deutet auf Verkabelung/Sensor |
| `cap_reanchor` | uint | nur bei `capture:"mcpwm"`: wie oft die Zeitbasis wegen einer Abweichung > 50 µs neu verankert wurde. Soll 0 bleiben - steigt der Wert, bitte melden |

Verbindungsstatus (WLAN/TCP) ist **nicht** hier, sondern in `confignet` (4.4).

### 4.3b `zones` — Zonen-Kalibrierung (seit Rev 4.17.0)

Gesendet: auf `SHOWZONES`. Eigene Zeile, damit `config` nicht weiter wächst.

```json
{"type":"zones","mac":"AA:BB:CC:DD:EE:FF","zones":1,"active":1,
 "z_hole_um":15250,"z_band_um":45000,"z_blend_um":2250,
 "a":{"sound_mps":336.9,"mic_offset_ns":[0,1371,-10627,-4091,-6309,-641],
      "offset_x_um":-845,"offset_y_um":-2577},
 "b":null,
 "c":{"sound_mps":348.9,"mic_offset_ns":[0,-2192,-6494,-4316,-7408,-5993],
      "offset_x_um":208,"offset_y_um":1365}}
```

| Feld | Bedeutung |
|---|---|
| `zones` | `SET ZONES` (eingeschaltet) |
| `active` | 1 = Zonen wirken tatsächlich (`ZONES=1`, `ZHOLE>0` und `ALGO=CLASSIC`) |
| `z_hole_um`, `z_band_um`, `z_blend_um` | Grenzen, siehe `SET ZHOLE/ZBAND/ZBLEND` |
| `a`, `b`, `c` | Satz der Zone oder `null` (= Zone nutzt die Basiswerte `SOUNDSPEED`/`OFS<i>`/`OFFSETX/Y`) |

### 4.3 `config` — Auswertungs-/Betriebsparameter

Gesendet: auf `SHOW`-Befehl, und automatisch am Ende von `CAL START`
(direkt nach dem `cal`/`done`-Telegramm, siehe 4.6).

```json
{"type":"config","mac":"AA:BB:CC:DD:EE:FF","lane":1,"debounce_ms":100,
 "reject_lockout_ms":5,"send_reject":0,"window_ms":1,"debug":0,
 "outlier_um":5000,"cluster_radius_um":200,"min_cluster_hits":2,
 "max_precision_um":2000,"min_mics":5,"tdoa_us":750,
 "mic_offset_ns":[0,0,0,0,0,0],"mic_enabled":[1,1,1,1,1,1],"cal_shots":10,
 "standoff_paper_mm":28.00,
 "mic_half_x_mm":115.00,"bullet_shift_pct":40,"bullet_shift_cap_mm":3.00,
 "cluster_corr":1,"cluster_iter":5,"lsq":0,"lsq_pellet_r_mm":2.25,
 "algo":"classic","pellet_r_mm":2.25,"max_sigma_um":2000,"capture":"isr",
 "walk_k":0.000,"walk_cal":0,"walk_max_us":150,
 "use_piezo":1,"piezo_req":1,"piezo_min_us":100,"piezo_max_us":1400,
 "test_cooldown_ms":3000,"offset_x_um":0,"offset_y_um":0,"sound_mps":343,
 "sound_set_mps":342.5,"dht":0,"raw_log":0,"zones":0,"z_hole_um":0,
 "paper_feed_mm":50.00,"paper_speed_mmps":5.00,"paper_auto":1,
 "paper_trigger":"piezo","paper_dir_invert":1,"paper_jog_speed_mmps":75.00}
```

Feldbedeutung deckungsgleich mit den `SET`-Parametern in Abschnitt 5 (gleicher
Name, siehe dortige Tabelle für Bereich/Default/Einheit). `mic_offset_ns`/
`mic_enabled` sind Arrays der Länge 6, Index = Mikrofonkanal (siehe 7.1).
`mac` wie bei `status` (4.2). Das Feld `target` (bis Rev 4.11.0: `"steel"`
oder `"paper"`) ist **seit Rev 4.11.1 entfallen** - es gibt nur noch den
Papier-Messmodus (siehe 7.2). `tools/replay_shot.py`/`calibrate_mics.py`
nehmen ohne dieses Feld `paper` an; `--target steel` bleibt zum Nachrechnen
älterer Logs aus der Zeit vor Rev 4.11.1 nutzbar.

### 4.4 `confignet` — Netzwerkkonfiguration + Live-Verbindungsstatus

Gesendet nur auf `SHOWNET`-Befehl (nicht automatisch bei Connect).

```json
{"type":"confignet","mac":"AA:BB:CC:DD:EE:FF","ssid":"MeinWLAN","pass":"****",
 "host":"192.168.1.10","port":9000,"static_ip":0,"ip":"","gateway":"",
 "subnet":"255.255.255.0","dns":"","wifi_ip":"192.168.1.42",
 "tcp_connected":true,"net_pending":false}
```

| Feld | Bedeutung |
|---|---|
| `mac` | wie bei `status` (4.2) |
| `pass` | **immer maskiert** als `"****"` falls gesetzt, sonst `""` - das tatsächliche Passwort wird nie ausgegeben |
| `wifi_ip` | aktuelle WLAN-IP oder `"none"` falls nicht verbunden - **live**, keine Konfiguration |
| `tcp_connected` | `true`/`false` - naturgemäß beim Empfänger dieser Nachricht immer `true` (er empfängt sie ja gerade über diese Verbindung) |
| `net_pending` | **seit Rev 4.9.0**: `true`, solange eine per `SET SSID/PASS/HOST/PORT/STATIC/IP/GW/SUBNET/DNS` geänderte Netzwerkkonfiguration noch nicht per `NET CONFIRM` bestätigt wurde (siehe 5.6/7.6) |
| `net_confirm_deadline_s` | nur vorhanden, wenn zusätzlich `net_pending:true` **und** das Gerät bereits mit den neuen Werten gebootet hat (Rücksprung-Watchdog aktiv): verbleibende Sekunden bis zum automatischen Rücksprung auf die zuletzt bestätigte Konfiguration |
| übrige Felder | 1:1 die per `SET SSID/PASS/HOST/PORT/STATIC/IP/GW/SUBNET/DNS` konfigurierten Werte |

### 4.5 `shot` / `reject` — Schussauswertung (Kernfunktion)

**Jede** Auslösung (mindestens 1 Mikrofon getriggert) erzeugt genau **eine**
Telegrammzeile, entweder `shot` oder `reject` - abhängig davon, ob die
Mindestzahl an Mikrofonen (`SET MINMICS`) erreicht wurde.

**Seit Rev 4.11.5 (`SET SENDREJECT`, Default 0):** `reject`-Telegramme gehen
standardmäßig **nur über Serial** raus, **nicht per TCP** (auch nicht über den
Reconnect-Puffer, 3.7), und tragen dann **kein** `seq`-Feld. `seq` und der
`shots`-Zähler (4.2) werden in diesem Modus nur bei gültigen `shot`-
Telegrammen erhöht - der Stand-PC sieht also eine lückenlose `seq`-Folge nur
aus `shot`-Zeilen. Mit `SET SENDREJECT=1` gilt das bisherige Verhalten (Rev
4.6.1-4.11.4): `reject` geht auch per TCP und teilt sich den fortlaufenden
`seq`-Zähler mit `shot`, keine Auslösung verschwindet spurlos.

**Ablaufreihenfolge pro Auslösung** (wichtig für die Implementierung der
Stand-PC-Seite):
1. Sammelfenster läuft ab (`SET WINDOW`, ggf. verlängert bis `SET PIEZOMAX`
   bei aktivem Piezo).
2. Automatischer Papiervorschub wird ggf. **schon hier** ausgelöst
   (`PAPERTRIGGER=ANY|PIEZO`), noch bevor feststeht, ob genug Mikrofone für
   eine Positionslösung vorhanden sind.
3. Reject-Prüfung (`airHits < MINMICS`) → `reject`-Telegramm (per TCP nur
   bei `SENDREJECT=1`), **fertig**.
3b. Seit Rev 4.11.5, nur bei `SET PIEZO=1` und `SET PIEZOREQ=1` (Default):
   ohne gültige Piezo-Bestätigung (kein Piezo-Signal oder außerhalb
   `PIEZOMIN`..`PIEZOMAX`) ebenfalls `reject` (`reason` `"no piezo"` bzw.
   `"piezo out of window"`), **fertig**. Mit `PIEZOREQ=0` wird ein solcher
   Schuss wie bis Rev 4.11.4 als `shot` mit `piezo_ok:0`/`clean:0` gemeldet.
4. Sonst: Trilateration, ggf. `cand`-Debug-Zeilen (nur `SET DEBUG=3`),
   `shot`-Telegramm.
5. `PAPERTRIGGER=CLEAN` wird ggf. erst **hier** ausgelöst (nach Feststehen
   von `clean`).

#### `reject`

```json
{"type":"reject","seq":43,"reason":"only 3 mic(s)","hits":3,"piezo_ns":1180}
```

| Feld | Typ | Bedeutung |
|---|---|---|
| `seq` | uint | **nur bei `SET SENDREJECT=1`**: fortlaufende Sequenznummer, gemeinsam mit `shot` (siehe unten). Bei `SENDREJECT=0` (Default) fehlt das Feld, und die Zeile erscheint nur auf Serial |
| `algo` | `"classic"` oder `"rim"` | **seit Rev 4.11.0**: welcher Auswertepfad (`SET ALGO`) dieses Telegramm erzeugt hat - wichtig, weil `air_ns`/die übrigen Feldnamen je nach Pfad unterschiedlich zu interpretieren sind (siehe 4.5b) |
| `reason` | string | bei `algo:"classic"` `"only <n> mic(s)"`, seit Rev 4.11.5 (nur `PIEZO=1` + `PIEZOREQ=1`) zusätzlich `"no piezo"` oder `"piezo out of window"`; bei `algo:"rim"` zusätzlich möglich: `"fit failed"` (Gate/Ausgleichsrechnung ohne Lösung trotz genug Mikrofonen) |
| `hits` | int | Anzahl Mikrofone, die ausgelöst haben (nach `SET MICEN<i>`-Maskierung) |
| `piezo_ns` | int oder `null` | nur vorhanden, wenn `SET PIEZO=1`: Verzögerung des Piezo-Signals relativ zum ersten Luftschall-Ereignis in ns, oder `null` falls Piezo nicht ausgelöst hat. **Feld fehlt komplett**, wenn `SET PIEZO=0`. |
| `synthetic` | 1, sonst fehlt das Feld | **nur bei `TESTSHOOT`** (seit Rev 4.9.1, siehe 5.7/7.7): markiert einen synthetischen Testschuss - bei echten Auslösungen fehlt dieses Feld komplett |

#### `shot`

```json
{"type":"shot","seq":44,
 "air_ns":[[0,15200],[820],[],[930,940],[1200],[]],
 "x_um":12500,"y_um":-3200,"pos_res_um":180,"precision_um":95,
 "cluster_hits":4,"pos_valid":1,
 "piezo_ns":1180,"piezo_ok":1,
 "clean":1,"hits":5,"ts":123456}
```

| Feld | Typ | Bedeutung |
|---|---|---|
| `seq` | uint | fortlaufende Sequenznummer, beginnt bei 1 nach Boot oder `RESET`-Befehl, **überläuft nicht in der Praxis** (uint32). Bei `SET SENDREJECT=0` (Default) zählen nur `shot`-Telegramme, bei `SENDREJECT=1` alle Auslösungen (shot+reject zusammen) |
| `algo` | `"classic"` oder `"rim"` | **seit Rev 4.11.0**, siehe oben bei `reject` und 4.5b |
| `air_ns` | Array[6] von Arrays | **nur bei `algo:"classic"`** (siehe 4.5b für `algo:"rim"`): pro Mikrofonkanal (Index = Kanal, siehe 7.1) alle erfassten Flankenzeiten in ns relativ zum ersten Ereignis des Fensters (`firstAirCC`); leeres Array `[]` = Kanal hat in diesem Fenster nicht ausgelöst; **erste Flanke pro Kanal** (`air_ns[i][0]`) ist die für die Positionslösung verwendete Zeit; weitere Flanken (Nachschwinger/Mehrfachtrigger) sind rein informativ |
| `air_w_ns` | Array[6] von int oder `null` | **seit Rev 4.14.0, nur bei aktivem MCPWM-Capture** (`status.capture:"mcpwm"`, sonst fehlt das Feld): Pulsbreite der ersten Flanke je Kanal in ns = Zeit, die das Mic-Signal über der Komparatorschwelle blieb (Maß für die Amplitude). `null` = keine plausible fallende Flanke (z.B. Breite > `WALKMAX`). Grundlage der `WALKK`-Korrektur; `air_ns` selbst bleibt **unkorrigiert** (Rohzeiten) |
| `x_um_pre`,`y_um_pre`,`precision_um_pre`,`cluster_hits_pre` | — | **nur bei `SET DEBUG=3`**: Roh-Position vor dem Verifizierungsschritt (siehe 7.3), zum Vergleich mit dem finalen Ergebnis |
| `x_um`,`y_um` | int (0.001 mm) | finale Trefferposition im Zielkoordinatensystem (Ursprung = Scheiben-/Plattenzentrum, siehe 7.2), inkl. `SET OFFSETX/OFFSETY`-Nachkorrektur |
| `pos_res_um` | int (0.001 mm) | Rest-Fehler der gewählten Stufe-1-Dreier-Kombination gegen die nicht beteiligten Mikrofone - Maß für Konsistenz der Rohmessung (siehe 7.3), **unbeeinflusst** von `OFFSETX/OFFSETY` |
| `precision_um` | int (0.001 mm) | RMS-Streuung der (bis zu 2) nächstgelegenen weiteren Kandidatenlösungen um das finale Ergebnis - je kleiner, desto übereinstimmender die verschiedenen Mikrofon-Kombinationen |
| `cluster_hits` | int | Anzahl Kandidatenlösungen innerhalb `SET RADIUS` um die finale Position |
| `pos_valid` | 0/1 | ob überhaupt eine geometrische Lösung gefunden wurde (bei ≥3 Mics fast immer 1) |
| `sound_act_mps` | float (m/s) | **seit Rev 4.16.0**: für diesen Schuss verwendete Schallgeschwindigkeit (bei `SET DHT=1` aus der Messung, ändert sich daher über die Zeit) |
| `raw_follows` | 0/1 | **seit Rev 4.16.0**: 1 = als **unmittelbar nächste Zeile** folgt ein `raw`-Telegramm mit derselben `seq` (siehe 4.5c) - der Host kann mit der Anzeige darauf warten. 0 bei `RAWLOG=0` oder wenn dieser `shot` mangels TCP-Verbindung im Offline-Puffer landet (das `raw` wird nicht gepuffert und käme beim Nachliefern nie an) |
| `zone` | `"A"`/`"B"`/`"C"` | **seit Rev 4.17.0**, nur bei aktiver Zonen-Kalibrierung: Zone mit dem größten Anteil an der Lage (7.3c). `x_um`/`y_um` sind dann bereits mit den Zonen-Sätzen gerechnet, `pos_res_um`/`precision_um`/`cluster_hits` stammen aus der Rechnung dieser Zone |
| `piezo_ns` | int oder `null` | wie bei `reject`, nur wenn `SET PIEZO=1` |
| `piezo_ok` | 0/1 | nur wenn `SET PIEZO=1`: ob die Piezo-Verzögerung im erwarteten Fenster (`PIEZOMIN`..`PIEZOMAX`) lag |
| `clean` | 0/1 | **zusammenfassendes Gütekriterium**, siehe Formel unten - der Stand-PC kann sich hierauf verlassen, statt die Schwellenlogik selbst nachzubilden |
| `hits` | int | Anzahl auslösender Mikrofone (identisch zur Definition bei `reject`) |
| `ts` | uint64 (ms) | Zeitstempel des ersten Ereignisses dieser Auslösung, **Millisekunden seit Geräte-Boot** (`esp_timer`), **keine Wanduhrzeit/Unixzeit** - für eine absolute Zeit muss der Stand-PC selbst beim Empfang der Zeile die eigene Uhrzeit anhängen |
| `synthetic` | wie bei `reject` | **nur bei `TESTSHOOT`** (5.7/7.7) |

**`clean`-Formel** (alle Bedingungen müssen erfüllt sein):
```
clean = pos_valid
        AND pos_res_um < outlier_um            (SET OUTLIER, Default 5000)
        AND cluster_hits >= min_cluster_hits    (SET MINCLUSTER, Default 2)
        AND precision_um <= max_precision_um    (SET MAXPRECISION, Default 2000)
        AND (use_piezo == 0 OR piezo_ok == 1)
```

#### 4.5b `shot` / `reject` bei `algo:"rim"` (seit Rev 4.11.0, `SET ALGO=RIM`)

`SET ALGO=RIM` (Default weiterhin `CLASSIC`, siehe 5.2) ersetzt Trigger UND
Positionslösung durch den in `schusserkennung-rev5.md` beschriebenen Ansatz
(Ringpuffer-Erfassung + Piezo-Anker + robuste Ausgleichsrechnung mit
Lochrand-Modell). Das Telegrammformat bleibt strukturell gleich (`type`,
`seq`, `clean`, `hits`, `ts`, `synthetic`), einige Felder sind aber anders
definiert oder entfallen/kommen neu hinzu:

```json
{"type":"shot","seq":44,"algo":"rim",
 "air_ns":[[-820000,-819200],[-818300],[],[820,940],[-817100],[]],
 "x_um":12480,"y_um":-3190,"sigma_x_um":410,"sigma_y_um":390,
 "rms_ns":145,"t0_ns":-819180,"used_mask":47,"pos_valid":1,
 "clean":1,"hits":5,"ts":123456}
```

| Feld | Typ | Bedeutung |
|---|---|---|
| `air_ns` | Array[6] von Arrays | wie bei `algo:"classic"`, aber die Zeiten sind relativ zum **Piezo** (nicht zur ersten Flanke) und können **negativ** sein (Ereignis vor dem Piezo, der Regelfall); enthält alle innerhalb des Gate-Zeitfensters `[-PIEZOMAX, +postWait]` erfassten Kandidatenflanken, nicht nur die erste - der Löser wählt selbst die passende aus |
| `air_w_ns` | Array[6] von int oder `null` | wie bei `algo:"classic"` (nur bei aktivem MCPWM), hier die Pulsbreite der jeweils **ersten** Kandidatenflanke in `air_ns[i]`. Achtung: bei `algo:"rim"` sind die `air_ns`-Zeiten offset- **und** (bei `WALKK>0`) pulsbreiten-korrigiert |
| `x_um`,`y_um` | int (0.001 mm) | wie bei `algo:"classic"`, inkl. `SET OFFSETX/OFFSETY` |
| `sigma_x_um`,`sigma_y_um` | int (0.001 mm) | **ersetzt `precision_um`/`cluster_hits`**: 1-Sigma-Unsicherheit aus der Kovarianzmatrix der Ausgleichsrechnung - der wahre Wert liegt mit ca. 68% Wahrscheinlichkeit innerhalb ±1σ, mit ca. 99% innerhalb ±2σ je Achse |
| `rms_ns` | float | RMS der Zeit-Residuen der verwendeten Mikrofone (Diagnose, Gegenstück zu `pos_res_um`) |
| `t0_ns` | float | geschätzter Einschlagzeitpunkt relativ zum Piezo (typisch negativ - das Piezo löst planmäßig nach dem Einschlag aus) |
| `used_mask` | uint (Bitmaske) | Bit `i` gesetzt = Mikrofon `i` ging in die finale Lösung ein (Inlier) |
| `sound_act_mps`, `raw_follows` | | **seit Rev 4.16.0**, wie bei `algo:"classic"` |
| `pos_res_um`,`precision_um`,`cluster_hits`,`piezo_ns`,`piezo_ok` | — | **entfallen bei `algo:"rim"`** (kein Feld im Telegramm) - das Piezo ist hier immer der Trigger selbst, eine gesonderte Verzögerungsprüfung entfällt |
| `clean` | 0/1 | bei `algo:"rim"`: `pos_valid AND max(sigma_x_um,sigma_y_um) <= max_sigma_um` (`SET MAXSIGMA`, siehe 5.2) - **andere Formel** als bei `algo:"classic"` (dortige Formel bleibt unverändert gültig für `algo:"classic"`) |

Ablaufreihenfolge unterscheidet sich ebenfalls: es gibt kein festes
`SET WINDOW`, ausgewertet wird stattdessen, sobald nach dem Piezo-Anker eine
von der Geometrie/Schallgeschwindigkeit abhängige Wartezeit verstrichen ist
(nicht separat konfigurierbar). `PAPERTRIGGER=ANY` und `=PIEZO` verhalten
sich bei `algo:"rim"` identisch (das Piezo triggert immer).

#### 4.5c `raw` — Rohdaten für den Simulator (seit Rev 4.16.0, `SET RAWLOG=1`)

Nur bei `SET RAWLOG=1` (Default 0) und **nur zu gültigen Schüssen**: direkt
nach dem `shot`-Telegramm folgt eine `raw`-Zeile mit **derselben `seq`**.
Ob eine folgt, zeigt das Feld `raw_follows` im `shot`. Empfehlung für den
Host: bei `raw_follows:1` auf die nächste Zeile warten, aber mit einem kurzen
Timeout (z.B. 500 ms) - bricht die Verbindung genau zwischen beiden Zeilen
ab, kommt das `raw` nicht mehr.
Abgelehnte Schüsse erzeugen kein `raw` (Reject-Verhalten unverändert).
Enthält alle steigenden und fallenden Flanken aus den Ringpuffern der
Firmware in einem Fenster um den Schuss - **unkorrigiert** (kein Offset,
keine Pulsbreiten-Korrektur) und ohne die Fenster-/Anzahl-Grenzen der
Auswertung - plus die zum Schusszeitpunkt aktiven Parameter. Damit lässt
sich jeder Schuss im Simulator mit beiden Auswertepfaden und beliebigen
Parametern nachrechnen.

```json
{"type":"raw","seq":44,"algo":"classic","ref":"first","capture":"mcpwm",
 "sound_act_mps":343.85,"walk_k":0.500,"walk_max_us":150,
 "win_pre_ns":1450000,"win_post_ns":1600000,
 "ofs_ns":[0,-1234,880,312,-95,40],"mic_en":[1,1,1,1,1,1],"piezo_ns":1180000,
 "rise_ns":[[0],[816667],[930000],[1200000],[1500000],[-38200,1650000]],
 "fall_ns":[[40000],[856667],[970000],[1240000],[1540000],[1689000]],
 "truncated":0,"ts":123456}
```

| Feld | Typ | Bedeutung |
|---|---|---|
| `seq` | uint | identisch zum unmittelbar vorausgehenden `shot` |
| `algo` | `"classic"`\|`"rim"` | Pfad, der den Schuss ausgewertet hat |
| `ref` | `"first"`\|`"piezo"` | Zeitnullpunkt aller `*_ns`-Werte: `"first"` (CLASSIC) = erste Flanke des Sammelfensters, identisch zu `air_ns` im `shot`; `"piezo"` (RIM) = Piezo-Flanke |
| `capture` | `"isr"`\|`"mcpwm"` | aktive Erfassung. Bei `"isr"` sind die `fall_ns`-Listen immer leer |
| `sound_act_mps`, `walk_k`, `walk_max_us`, `ofs_ns`, `mic_en` | | zum Schusszeitpunkt aktive Werte (`ofs_ns`/`mic_en` je Kanal, Index wie 7.1) |
| `win_pre_ns`, `win_post_ns` | int (ns) | Fenster relativ zum Nullpunkt. **RIM:** genau das Kandidatenfenster der Auswertung. **CLASSIC:** Sammelfenster plus Vorlauf `PIEZOMAX+50µs`; die Auswertung selbst nutzte je Kanal nur die erste Flanke ab 0 innerhalb von `tdoa_us` (`SET TDOA`, aus `config`) - Flanken vor 0 sind rein informativ (z.B. während der Sperrzeit verworfene) |
| `piezo_ns` | int oder `null` | Piezo relativ zum Nullpunkt (RIM immer 0) |
| `rise_ns` | Array[6] von Arrays | steigende Flanken (= Auslösungen) je Kanal, aufsteigend, aus `[-win_pre_ns, win_post_ns + 500000]`. Die 20-µs-Totzeit der Firmware ist bereits angewandt |
| `fall_ns` | Array[6] von Arrays | fallende Flanken je Kanal, gleiches Fenster. Preller < 2 µs sind zur jeweils letzten Flanke zusammengefasst |
| `truncated` | 0/1 | 1 = mehr als 16 Flanken einer Art bei mindestens einem Kanal, es wurden nur die ersten 16 gesendet |
| `ts` | uint64 (ms) | wie bei `shot` |
| `synthetic` | 1 | nur bei `TESTSHOOT` |

**Nachrechnen im Simulator** (identisch zur Firmware):
- CLASSIC: je Kanal die erste Flanke aus `rise_ns` mit `0 ≤ t ≤ tdoa_us`;
  RIM: alle Flanken mit `−win_pre_ns ≤ t − ofs_ns[i] ≤ win_post_ns`, davon die
  ersten 8 je Kanal. Offset abziehen: `t − ofs_ns[i]`. Kanäle mit
  `mic_en = 0` ignorieren.
- Pulsbreite einer steigenden Flanke `r`: letzte fallende Flanke `f` mit
  `r < f < min(nächste steigende Flanke desselben Kanals, r + walk_max_us)`,
  Breite `f − r`; kürzer als 300 ns oder keine solche Flanke = ungültig.
- Pulsbreiten-Korrektur: `t += walk_k · (w − w̄)`, `w̄` = Mittel der gültigen
  Breiten der verwendeten Flanken dieses Schusses (CLASSIC: erste Flanke je
  Kanal; RIM: jeweils früheste Kandidatenflanke je Kanal), ungültig = keine
  Korrektur. RIM: danach je Kanal neu sortieren.

`raw` wird **nicht** im Offline-Puffer (3.7) gehalten - bei fehlender
TCP-Verbindung geht es nur über Serial raus. Länge typisch 400-600, maximal
etwa 2000 Zeichen.

#### `cand` — Kandidaten-Debug (nur `SET DEBUG=3`)

Vor jedem `shot`-Telegramm werden **alle** geometrisch lösbaren
Dreier-Kombinationen der erfassten Mikrofone einzeln ausgegeben (bis zu
`C(6,3)=20` Zeilen pro Schuss) - nur relevant, wenn der Stand-PC eine
Rohdaten-Analyse/Diagnose anbieten will, für den Normalbetrieb ignorierbar.

```json
{"type":"cand","seq":44,"x_mm":12.50,"y_mm":-3.20,"ref":0,"a":2,"b":4,
 "t_ref_ns":0,"t_a_ns":930,"t_b_ns":1200}
```

`ref`/`a`/`b` = beteiligte Mikrofonindizes, `t_*_ns` = deren rohe (nur um
`mic_offset_ns` korrigierte) Flankenzeiten.

### 4.6 `cal` — Kalibrierungs-Fortschritt/-Ergebnis

Ausgelöst durch den `CAL`-Befehl (Abschnitt 5.3) **und** implizit durch jede
Auslösung, während eine Kalibrierung läuft (`calActive`). Fünf `state`-Werte:

| `state` | Wann | Zusatzfelder |
|---|---|---|
| `"start"` | direkt nach `CAL START` | `need` (= `SET CALSHOTS`) |
| `"waiting"` | nach jeder für die Kalibrierung verwertbaren Auslösung, solange noch nicht genug gesammelt | `progress`, `need` |
| `"skipped"` | eine Auslösung während `calActive` war **nicht verwertbar** (<3 Mics, oder bei `SET PIEZO=1` fehlende/unplausible Piezo-Bestätigung) - zählt nicht zum Fortschritt | `reason` (`"only <n> mic(s)"` oder `"no piezo confirmation"`), `progress`, `need` |
| `"done"` | Zielzahl (`need`) erreicht - Kalibrierung abgeschlossen, Offsets **automatisch ins NVS geschrieben** | `mac` (seit Rev 4.9.0, wie bei `status`), `offsets_ns` (Array[6], ns), `sound_mps` (`SET SOUNDSPEED` ganzzahlig gerundet, **nicht** durch die Kalibrierung verändert - siehe 7.4), `sound_set_mps` (seit Rev 4.15.0: derselbe Wert genau), `sound_act_mps` (seit Rev 4.15.0: während der Kalibrierung aktive Schallgeschwindigkeit, bei `DHT=1` aus der Messung), `walk_k` (seit Rev 4.14.0: aktueller `SET WALKK`), `walk_fitted` (1 = `walk_k` wurde in diesem Lauf mitbestimmt und gespeichert, nur bei `WALKCAL=1` und aktivem MCPWM), `shots` (= Anzahl verwendeter Schüsse). **Direkt danach folgt automatisch ein volles `config`-Telegramm (4.3)** in derselben Verbindung. Für einen Stand-PC-seitigen Kalibrierungs-Speicher (siehe Konzept Abschnitt 7.1) genügt es, auf dieses Telegramm zu lauschen und `offsets_ns`/`sound_mps` zusammen mit `mac` abzulegen - kein separater Upload-Befehl nötig. Restore siehe `CAL IMPORT` (5.3). |
| `"aborted"` | nach `CAL ABORT` | — |

`CAL STATUS` liefert zusätzlich außerhalb des laufenden Sammelvorgangs:

```json
{"type":"cal","state":"idle","progress":0,"need":5}
```
(`state` hier `"waiting"` statt `"idle"`, falls gerade eine Kalibrierung läuft)

**Wichtig:** Während `calActive` läuft, werden `cal`-Telegramme **zusätzlich**
zu den normalen `shot`/`reject`-Telegrammen für dieselbe Auslösung gesendet -
eine Auslösung während laufender Kalibrierung erzeugt also potenziell **zwei**
Zeilen (erst `cal`, dann `shot`/`reject`).

### 4.7 `ok` / `error` — Befehlsbestätigung

Antwort auf **jeden** erkannten Befehl (außer reine Abfragen wie `SHOW`, die
direkt das jeweilige Datentelegramm liefern). Kein einheitliches Schema -
Felder hängen vom Befehl ab, siehe Abschnitt 5 für die jeweils exakte
Antwortzeile. Gemeinsame Muster:

```json
{"type":"ok","set":"debounce","value":150}
{"type":"ok","set":"ssid","reboot_required":true}
{"type":"ok","cmd":"reboot"}
{"type":"error","msg":"debounce 10-5000"}
{"type":"error","msg":"unknown command"}
{"type":"error","msg":"unknown key"}
```

- `reboot_required:true` erscheint bei allen netzwerkrelevanten `SET`-Keys
  (`SSID`,`PASS`,`HOST`,`PORT`,`STATIC`,`IP`,`GW`/`GATEWAY`,`SUBNET`,`DNS`)
  und bei `FACTORY` - die Änderung ist bereits im NVS gespeichert, wirkt sich
  aber erst nach einem `REBOOT`-Befehl (oder Power-Cycle) aus.
- Ein unbekannter Befehl oder unbekannter `SET`-Key liefert
  `{"type":"error","msg":"unknown command"}` bzw. `{"type":"error","msg":"unknown key"}`.
- Bei syntaktisch falschem `SET` (kein erkennbares `=`) erfolgt **gar keine**
  Antwort (Befehl wird stillschweigend verworfen, siehe `handleSet()`
  Rückgabewert `false` bei `eq < 5`).

### 4.8 `pong`

Antwort auf `PING`.

```json
{"type":"pong"}
```

## 5. Befehle Stand-PC → ESP32 (Referenz)

Alle Befehle sind einzeilige Texte (siehe Framing, Abschnitt 2). Antworten
siehe Abschnitt 4.

### 5.1 Reine Abfragen (keine Zustandsänderung)

| Befehl | Antwort |
|---|---|
| `SHOW` | `config`-Telegramm (4.3) |
| `SHOWNET` | `confignet`-Telegramm (4.4) |
| `SHOWZONES` | `zones`-Telegramm (4.3b), seit Rev 4.17.0 |
| `STATUS` | `status`-Telegramm (4.2) |
| `PING` | `pong` (4.8) |
| `HELP` oder `?` | **nur über Serial**, mehrzeiliger Klartext-Hilfetext (kein JSON, wird nicht über `emitLine` gesendet - **kommt beim TCP-Client nicht an**, dort keine Reaktion) |

### 5.2 `SET <KEY>=<value>` — Konfigurationsparameter

Alle Werte NVS-persistent (Namespace `schiessstd`) und wirken - **außer den
netzwerkbezogenen Keys** - sofort ohne Reboot. Schlüssel case-insensitiv,
Werte case-sensitiv.

| Key | Bereich | Default | Reboot nötig | Bedeutung |
|---|---|---|---|---|
| `SSID` | Text | `""` (WLAN aus) | ✅ | WLAN-Name |
| `PASS` | Text | `""` | ✅ | WLAN-Passwort |
| `HOST` | IP/Hostname | `192.168.1.10` | ✅ | Ziel-Host (Stand-PC) |
| `PORT` | 1-65535 | 9000 | ✅ | TCP-Port des Stand-PC |
| `LANE` | 1-999 | 1 | – | Bahnnummer |
| `DEBOUNCE` | 10-5000 (ms) | 100 | – | Sperrzeit nach einer Auslösung |
| `SENDREJECT` | 0/1 | 0 | – | 0 = `reject` nur über Serial, ohne `seq`, zählt nicht in `seq`/`shots`; 1 = `reject` auch per TCP, mit gemeinsamer `seq` (Verhalten bis Rev 4.11.4), siehe 4.5 |
| `WINDOW` | 1-50 (ms) | 1 | – | Mindest-Sammelfenster (wird bei aktivem Piezo automatisch bis `PIEZOMAX+200µs` verlängert). Nur `ALGO=CLASSIC` - bei `ALGO=RIM` wirkungslos, siehe 4.5b |
| `DEBUG` | 0-3 | 0 | – | 3 = zusätzliche `cand`/`*_pre`-Diagnosefelder; `shot`/`reject` selbst gehen unabhängig davon raus (`reject` per TCP nur bei `SENDREJECT=1`) |
| `OUTLIER` | 0-500000 (0.001mm) | 5000 | – | Schwelle für `pos_res_um` in `clean`-Bewertung (nur `ALGO=CLASSIC`) |
| `RADIUS` | 0-500000 (0.001mm) | 4000 (bis Rev 4.16: 200) | – | Umkreis für `cluster_hits` und Verifizierungs-Mittelpunkt (nur `ALGO=CLASSIC`) |
| `MINCLUSTER` | 0-20 | 2 | – | Mindest-`cluster_hits` für `clean` (nur `ALGO=CLASSIC`) |
| `MAXPRECISION` | 0-500000 (0.001mm) | 2000 | – | Max. `precision_um` für `clean` (nur `ALGO=CLASSIC`) |
| `MINMICS` | 3-6 | 5 | – | Mindestzahl Mics, sonst `reject` |
| `TDOA` | 100-5000 (µs) | 750 | – | Geometrie-Plausibilitätsfenster (ISR-Ebene). Nur `ALGO=CLASSIC` - bei `ALGO=RIM` wirkungslos (Gate ergibt sich aus `PIEZOMIN/MAX`, siehe 4.5b) |
| `STANDOFFPAPER` | 5.0-100.0 (mm) | 28.0 | – | Mic-Standoff (rechtwinklig zur Scheibe) |
| `MICHALFX` | 5.0-300.0 (mm) | 115.0 | – | horizontaler Mic-Abstand zur Mittellinie |
| `BSHIFTPCT` | 0-100 (%) | 30 (bis Rev 4.16: 40) | – | Kugeldurchmesser-Korrektur, 0=aus (nur `ALGO=CLASSIC`) - seit Rev 4.11.4 Teil der `CAL START`-Kostenfunktion, siehe 7.4 |
| `BSHIFTCAP` | 0.0-20.0 (mm) | 3.0 | – | Kappung der Korrektur je Mikrofon (nur `ALGO=CLASSIC`) |
| `CLUSTERCORR` | 0/1 | 1 | – | **seit Rev 4.12.0**: 1 = alle Schnittpunkte werden vor der Mittelung in Stufe 2 kugeldurchmesser-korrigiert (`BSHIFTPCT`/`BSHIFTCAP`, wirkungslos bei `BSHIFTPCT=0`); 0 = nur die Stufe-1-Lösung (Verhalten bis 4.11.5). Bei 1 beziehen sich `cluster_hits`/`precision_um` auf die korrigierten Punkte. Nur `ALGO=CLASSIC`, siehe 7.3 |
| `CLUSTERITER` | 0-10 | 5 | – | **seit Rev 4.12.0**: zusätzliche Durchgänge, in denen der `RADIUS`-Cluster um den jeweils neuen Mittelpunkt neu gebildet wird (Mean-Shift, Abbruch sobald stabil); 0 = einmalig um die Stufe-1-Lösung (bis 4.11.5). Nur `ALGO=CLASSIC` |
| `LSQ` | 0/1 | 0 | – | **seit Rev 4.12.0**: Endposition zusätzlich per Ausgleichsrechnung über alle ausgelösten Mics verfeinern (braucht ≥ 4 Mics, bei ≥ 5 Echo-Schutz durch probeweises Weglassen eines Mics). Bei Fehlschlag bleibt die Cluster-Position. Nur `ALGO=CLASSIC` |
| `LSQPELLETR` | 0.0-5.0 (mm) | 2.25 | – | **seit Rev 4.12.0**: Lochrand-Radius für `LSQ` (0 = Punktquelle) - unabhängig von `PELLETR` (`ALGO=RIM`) |
| `ALGO` | `CLASSIC`\|`RIM` | `CLASSIC` | – | **seit Rev 4.11.0**: Auswertepfad, siehe 4.5b/7.3b. `RIM` ignoriert `PIEZO=0` (Piezo ist dort immer der Trigger) |
| `PELLETR` | 0.0-10.0 (mm) | 2.25 | – | Lochrand-Radius für `ALGO=RIM` (0=Punktquelle) |
| `MAXSIGMA` | 0.0-50.0 (mm) | 2.0 | – | `clean`-Schwelle für `ALGO=RIM` (max. 1-Sigma-Unsicherheit) |
| `PIEZO` | 0\|1 | 1 | – | Piezo als Trigger-Bestätigung nutzen |
| `CAPTURE` | `ISR`\|`MCPWM` | `ISR` | ✅ | **seit Rev 4.13.0**: Zeitstempel der Luft-Mics. `ISR` = GPIO-Interrupt + CPU-Zykluszähler (bisher, Interrupt-Latenz-Jitter ~1-3 µs); `MCPWM` = Hardware-Capture (12,5 ns Auflösung, kein Interrupt-Jitter, GPIO23 muss unbeschaltet bleiben, siehe 7.1). Wirkt in beiden `ALGO`-Pfaden. Nach dem Umschalten `CAL RESET` + neue `CAL START`-Serie, da die alten `OFS`-Werte die ISR-Latenz mit einkalibriert haben. Aktiver Modus: `status.capture` |
| `WALKK` | 0.0-1.0 | 0.0 | – | **seit Rev 4.14.0**: Amplituden-Korrektur über die Pulsbreite, siehe 7.1. Je Mic `t += WALKK·(w − w̄)` mit `w` = Pulsbreite (`air_w_ns`), `w̄` = Mittel der gültigen Breiten dieses Schusses. 0 = aus, 0.5 = Pulsmitte (für eine symmetrische Halbwelle amplitudenunabhängig). Wirkt sofort, aber **nur bei aktivem `CAPTURE=MCPWM`** (sonst `warn` in der `ok`-Antwort), in beiden `ALGO`-Pfaden |
| `WALKCAL` | 0/1 | 0 | – | **seit Rev 4.14.0**: 1 = `CAL START` bestimmt `WALKK` zusammen mit den Offsets (Start beim aktuellen Wert) und speichert ihn |
| `WALKMAX` | 5-500 (µs) | 150 | – | **seit Rev 4.14.0**: größte plausible Pulsbreite; längere gelten als ungültig (`null`), das Mic bleibt dann unkorrigiert |
| `PIEZOREQ` | 0/1 | 1 | – | nur wirksam bei `PIEZO=1` und `ALGO=CLASSIC`: 1 = Schuss ohne gültige Piezo-Bestätigung wird `reject` (siehe 4.5, Schritt 3b), 0 = `shot` mit `piezo_ok:0`/`clean:0` |
| `PIEZOMIN` | 0-5000 (µs) | 100 | – | Min. erwartete Piezo-Verzögerung nach dem ersten Luft-Ereignis |
| `PIEZOMAX` | 0-5000 (µs) | 1400 | – | Max. erwartete Piezo-Verzögerung / Ausreißer-Obergrenze |
| `TESTMODE` | 0\|1 | 0 | – (nicht persistent) | Sensor-Diagnosemodus, siehe Abschnitt 8 |
| `TESTCOOLDOWN` | 0-10000 (ms) | 3000 | – | Mindestabstand ident. Meldungen im Testmodus |
| `OFFSETX` | -50000..50000 (0.001mm) | 0 | – | konstanter Nachkorrektur-Offset x |
| `OFFSETY` | -50000..50000 (0.001mm) | 0 | – | wie `OFFSETX`, y |
| `SOUNDSPEED` | 300.0-400.0 (m/s) | 342.5 | – | Schallgeschwindigkeit bei `DHT=0` bzw. ohne gültige Messung. Seit Rev 4.15.0 mit Nachkommastelle, Default 342.5 (bis 4.14: Ganzzahl, Default 343 - ein gespeicherter Wert bleibt beim Update erhalten). **Rein manuell**, wird von `CAL START` NICHT verändert. Antwort enthält zusätzlich `active_mps` |
| `ZONES` | 0/1 | 0 | – | **seit Rev 4.17.0**: Zonen-Kalibrierung an (wirkt nur mit `ZHOLE>0` und `ALGO=CLASSIC`), siehe 7.3c. Antwort `{"type":"ok","set":"zones","value":1,"active":1}` |
| `ZHOLE` | 0–60000 | 0 | 0.001 mm | **seit Rev 4.17.0**: Radius des Lochs der Vorsatzscheibe (Zone A). LG 15250 (Ø 30,5 mm), LP 29750 (Ø 59,5 mm) - der Stand-PC setzt den Wert beim Scheibenwechsel je Scheibentyp. 0 = Zonen wirkungslos |
| `ZBAND` | 0–100000 | 45000 | 0.001 mm | **seit Rev 4.17.0**: halbe Breite des Schussbands (Grenze Zone B/C bei \|x\| = `ZBAND`) |
| `ZBLEND` | 0–10000 | 2250 | 0.001 mm | **seit Rev 4.17.0**: lineare Überblendung ± um jede Zonengrenze (ein Diabolo am Rand trifft beide Bereiche) |
| `ZCAL` | `<A\|B\|C>,<mps>,<ofs0>,…,<ofs5>,<offx>,<offy>` bzw. `<A\|B\|C>,OFF` | OFF | m/s, ns, 0.001 mm | **seit Rev 4.17.0**: Satz einer Zone, atomar (alle 9 Werte geprüft, dann geschrieben). `mps` 300–400 (wirkt als Differenz zu `SOUNDSPEED`, eine DHT-Messung bleibt erhalten), `ofs` ±20000, `offx/offy` ±50000. `OFF` = Zone nutzt die Basiswerte. Antwort `{"type":"ok","set":"zcal","zone":"A","valid":1}` |
| `RAWLOG` | 0/1 | 0 | – | **seit Rev 4.16.0**: 1 = zu jedem gültigen Schuss ein `raw`-Telegramm mit allen unkorrigierten Flanken und Parametern (siehe 4.5c). Wirkt sofort |
| `DHT` | 0/1 | 0 | – | **seit Rev 4.15.0**: 1 = Schallgeschwindigkeit aus Temperatur/Luftfeuchte des DHT22 an **GPIO15** (siehe 7.2), Messung alle 30 s in Ruhephasen, erste Messung ca. 2,5 s nach dem Einschalten. 0 = fest `SOUNDSPEED`. Wirkt sofort |
| `PAPERFEED` | 10.0-100.0 (mm) | 50.0 | – | Vorschubstrecke je Auslösung |
| `PAPERSPEED` | 0.5-30.0 (mm/s) | 5.0 | – | Geschwindigkeit 1. Hälfte, danach Abbremsrampe |
| `PAPERAUTO` | 0\|1 | 1 | – | automatischen Vorschub überhaupt ausführen |
| `PAPERTRIGGER` | `ANY`\|`PIEZO`\|`CLEAN` | `PIEZO` | – | wann automatisch vorgeschoben wird |
| `PAPERDIR` | 0\|1 | 1 | – | Vorschub-Drehrichtung invertieren |
| `PAPERJOGSPEED` | 1.0-100.0 (mm/s) | 75.0 | – | Geschwindigkeit manueller Dauerbetrieb (Kippschalter) |
| `CALSHOTS` | 3-20 | 10 | – | Anzahl Kalibrier-Schüsse für `CAL START` |
| `OFS0`..`OFS5` | -20000..20000 (ns) | 0 | – | Timing-Offset je Mikrofonkanal (normalerweise durch `CAL START` gesetzt) |
| `MICEN0`..`MICEN5` | 0\|1 | 1 | – | Mikrofonkanal für Auswertung UND Kalibrierung berücksichtigen |
| `STATIC` | 0\|1 | 0 | ✅ | statische IP an/aus |
| `IP` | IPv4 | `""` | ✅ | statische IP-Adresse |
| `GW` (auch `GATEWAY`) | IPv4 | `""` | ✅ | Gateway |
| `SUBNET` | IPv4-Maske | `255.255.255.0` | ✅ | Subnetzmaske |
| `DNS` | IPv4 oder leer | `""` (=Gateway) | ✅ | DNS-Server |

Antwortformat einheitlich `{"type":"ok","set":"<key-lowercase>","value":...}`
(bzw. zusätzlich `"reboot_required":true` bei den markierten Keys), bei
ungültigem Wert `{"type":"error","msg":"<key> <bereich>"}`. Ausnahme:
`SSID`/`PASS`/`HOST` bestätigen nur mit `reboot_required`, ohne den Wert
zurückzuspiegeln (Passwort wird nie zurückgesendet).

⚠️ **Netzwerk-Sicherheitsnetz (seit Rev 4.9.0):** die 9 mit ✅ markierten
Keys lösen zusätzlich `armNetWatchdogIfNeeded()` aus - der jeweils **erste**
solche `SET`-Befehl seit dem letzten Boot sichert die bis dahin gültige
(nicht die gerade neu gesetzte!) Netzwerkkonfiguration als Rücksprungpunkt
und setzt `net_pending` (sichtbar in `confignet`, 4.4). Details und
Ablaufdiagramm siehe Abschnitt 7.6 sowie die Befehle `NET CONFIRM`/
`NET STATUS` (5.6).

**Kurzformen** (funktional identisch zu `SET DEBOUNCE=`/`SET WINDOW=`):
`DEBOUNCE=<ms>` und `WINDOW=<ms>` funktionieren auch ohne führendes `SET `.

### 5.3 `CAL` — Kalibrierung

| Befehl | Wirkung |
|---|---|
| `CAL START` | startet Sammlung von `SET CALSHOTS` verwertbaren Schüssen; läuft parallel zum Normalbetrieb (jede Auslösung erzeugt weiterhin auch `shot`/`reject`); Ergebnis siehe `cal`/`"done"` (4.6) |
| `CAL ABORT` | bricht ab, Offsets bleiben unverändert |
| `CAL STATUS` | liefert aktuellen Fortschritt, ohne den Ablauf zu beeinflussen |
| `CAL RESET` | setzt alle `OFS0..OFS5` auf 0 und `SOUNDSPEED` auf 342.5 (bis Rev 4.14: 343) zurück (persistiert), Antwort `{"type":"ok","cmd":"cal_reset"}` |
| `CAL IMPORT OFS0=<ns>,...,OFS5=<ns>,SOUNDSPEED=<mps>` | **seit Rev 4.9.0**: atomarer Bulk-Restore einer vom Stand-PC gespeicherten Kalibrierung (siehe 4.6/7.1) - alle Werte zuerst validieren, **erst danach** schreiben, kein Teilzustand bei Abbruch mitten in der Übertragung. Kommagetrennte `KEY=WERT`-Paare, Reihenfolge beliebig, nicht alle Keys müssen vorkommen (nur die angegebenen werden geändert). Gültige Keys: `OFS0`..`OFS5` (-20000..20000 ns) und `SOUNDSPEED` (300-400 m/s) - identische Wertebereiche wie bei den einzelnen `SET`-Befehlen. Antwort `{"type":"ok","cmd":"cal_import"}` oder `{"type":"error","msg":"cal import: bad key=value (...)"}` bei irgendeinem ungültigen Paar (dann wurde **nichts** geschrieben). |

Ablauf im Detail siehe Abschnitt 7.4.

### 5.4 Sonstige Befehle

| Befehl | Wirkung | Antwort |
|---|---|---|
| `REBOOT` | sofortiger Neustart (100 ms Verzögerung nach der Bestätigung) | `{"type":"ok","cmd":"reboot"}`, danach Verbindungsabbruch |
| `FACTORY` | **löscht den kompletten NVS-Namespace** (alle `SET`-Werte inkl. WLAN-Zugangsdaten!) - erfordert danach `REBOOT` | `{"type":"ok","cmd":"factory","reboot_required":true}` |
| `RESET` | setzt `shots`-Zähler und `seq`-Zähler auf 0 zurück, keine Konfigurationsänderung | `{"type":"ok","cmd":"reset"}` |
| `TESTSHOOTPAPER` | löst genau einen Papiervorschub aus (wie nach einer echten Auslösung), **ohne** `shot`-Telegramm/Zählererhöhung - zum Testen der Mechanik. Fehler, falls bereits ein Vorschub läuft. | `{"type":"ok","cmd":"testshootpaper"}` oder `{"type":"error","msg":"paper feed busy"}` |

⚠️ **`FACTORY` ist unauthentifiziert erreichbar** - jeder mit TCP-Zugriff auf
den Port kann damit auch die WLAN-Zugangsdaten des Geräts löschen. Der
Stand-PC sollte diesen Befehl selbst nur nach expliziter Nutzerbestätigung
senden.

### 5.5 `ACTION` — kurzlebige Bedienbefehle (seit Rev 4.9.0)

Bewusst getrennt von `SET`: `ACTION`-Befehle lösen ein Ereignis aus oder
setzen einen **nicht persistenten** Laufzeit-Zustand, statt dauerhafte
Konfiguration zu ändern.

| Befehl | Wirkung | Antwort |
|---|---|---|
| `ACTION LIGHT=ON` | schaltet die Standbeleuchtung (GPIO22) sofort fest an, **pausiert** die automatische Verbindungsanzeige (3.8) komplett | `{"type":"ok","action":"light","state":"on"}` |
| `ACTION LIGHT=OFF` | wie `ON`, aber fest aus | `{"type":"ok","action":"light","state":"off"}` |
| `ACTION LIGHT=AUTO` | gibt die Kontrolle zurück an die automatische Verbindungsanzeige | `{"type":"ok","action":"light","state":"auto"}` |
| `ACTION TARGETCHANGE` | Scheibenwechsel/Papiervorschub - **identischer Codepfad wie `TESTSHOOTPAPER`** (5.4), nur unter einem für den produktiven Bedienfall sprechenden Namen: kein `shot`-Telegramm, kein Zähler-Inkrement, kein Abschluss-Telegramm (siehe 7.5) | `{"type":"ok","action":"targetchange"}` oder `{"type":"error","msg":"paper feed busy"}` |

`ACTION LIGHT`-Zustand ist **nicht NVS-persistent** - nach jedem Reboot
(auch nach `NET`-bedingtem Auto-Reboot, siehe 5.6) ist die Beleuchtung immer
wieder im automatischen Modus.

### 5.6 `NET` — Sicherheitsnetz für Netzwerk-Fernkonfiguration (seit Rev 4.9.0)

Siehe Abschnitt 7.6 für den vollständigen Ablauf. Kurzreferenz:

| Befehl | Wirkung |
|---|---|
| `NET STATUS` | zeigt den aktuellen Zustand: `{"type":"net","pending":false}`, oder `{"type":"net","pending":true,"reboot_required":true}` (Änderung gesetzt, aber noch nicht in diesen Wert gebootet), oder `{"type":"net","pending":true,"confirm_deadline_s":<n>}` (bereits mit den neuen Werten gebootet, Rücksprung-Watchdog zählt) |
| `NET CONFIRM` | bestätigt die aktuell laufende Netzwerkkonfiguration endgültig, löscht den Rücksprungpunkt. **Nur möglich, wenn das Gerät bereits mit den neuen Werten läuft** (`confirm_deadline_s` sichtbar) - vor dem entsprechenden Reboot liefert es `{"type":"error","msg":"reboot required before confirming"}`, ohne anstehende Änderung `{"type":"error","msg":"no pending network change"}` |

### 5.7 `TESTSHOOT [<z_mm>]` — synthetischer Testschuss (seit Rev 4.9.1)

Erzeugt einen kompletten, plausiblen Schuss-Durchlauf **ohne echte Sensorik**,
um die Kommunikationsstrecke zum Stand-PC Ende-zu-Ende zu prüfen. Ablauf und
Herleitung der Rohdaten siehe Abschnitt 7.7.

**Syntax:** `TESTSHOOT` oder `TESTSHOOT <z_mm>` (Leerzeichen, Fließkommazahl).
Ohne Parameter gilt `z_mm = 25.0` ("gesamte LG-Scheibe"). Gültiger Bereich:
`1.0`-`500.0`.

**Sofortige Antwort** (bevor das eigentliche `shot`/`reject`-Telegramm folgt):

```json
{"type":"ok","cmd":"testshoot","z_mm":25.00,"target_x_um":12345,"target_y_um":-6789}
```

`target_x_um`/`target_y_um` ist die zufällig gewählte "wahre" Position (gleich-
verteilt in `[-z_mm*1000, +z_mm*1000]` µm je Achse, unabhängig voneinander) -
der Stand-PC kann sie als Referenz speichern und mit dem `x_um`/`y_um` des
nachfolgenden `shot`-Telegramms vergleichen (siehe 7.7 für die zu erwartende
Abweichung).

**Danach läuft der normale Schuss-Ablauf unverändert** (Abschnitt 4.5): nach
Ablauf des Sammelfensters entsteht ein reguläres `shot`- oder (bei zu wenigen
aktiven Mikrofonen) `reject`-Telegramm, inklusive `air_ns` mit den passend zur
aktuellen Kalibrierung erzeugten Rohwerten, inklusive automatischem
Papiervorschub falls konfiguriert. **Einziger Unterschied zu einem echten
Schuss:** ein zusätzliches Feld `"synthetic":1` in diesem `shot`/`reject`-
Telegramm (bei echten Schüssen fehlt das Feld komplett) - der Stand-PC sollte
danach filtern, um Testschüsse nicht versehentlich in eine echte Wertung zu
übernehmen.

**Fehlerfälle** (keine Zustandsänderung, kein nachfolgendes `shot`-Telegramm):

| Antwort | Ursache |
|---|---|
| `{"type":"error","msg":"testshoot z 1.0-500.0"}` | `z_mm` außerhalb des gültigen Bereichs |
| `{"type":"error","msg":"shot in progress"}` | ein Sammelfenster läuft gerade (echt oder synthetisch) |
| `{"type":"error","msg":"debounce active"}` | `SET DEBOUNCE`-Sperrzeit nach der letzten Auslösung noch aktiv |
| `{"type":"error","msg":"testshoot not allowed during active calibration"}` | `CAL START` läuft gerade - ein synthetischer Schuss würde die Kalibrierung mit unrealistisch perfekten Werten verfälschen |
| `{"type":"error","msg":"testshoot: no mic enabled (SET MICEN0..MICEN5)"}` | alle Mikrofonkanäle per `SET MICEN<i>=0` deaktiviert |

## 6. Persistenz (NVS)

- Namespace `"schiessstd"` (`Preferences`-Library, ESP32-NVS-Flash).
- Jeder `SET`-Befehl öffnet NVS kurz **schreibend** (`saveVal<T>()`), schreibt
  genau einen Key, schließt wieder. Kein Batching, keine Transaktionalität
  über mehrere Keys hinweg für die eigentlichen Konfigurationswerte -
  **Ausnahme:** `CAL IMPORT` (5.3, validiert alle Werte vor dem ersten
  Schreiben) und das Netzwerk-Sicherheitsnetz selbst (7.6, das Zurückschreiben
  der `*_prv`-Werte erfolgt als ein zusammenhängender `Preferences`-Block vor
  dem Reboot).
- `FACTORY` löscht den gesamten Namespace (inkl. `net_pnd`/`*_prv`-Keys);
  danach gelten beim nächsten Boot wieder alle in Abschnitt 5.2 genannten
  Defaults - **kein** Watchdog nötig, da WLAN dabei komplett deaktiviert wird
  (leere SSID), das Gerät bleibt also nicht mit einer falschen, aber aktiven
  Netzwerkkonfiguration hängen.
- `SET TESTMODE` und `ACTION LIGHT` werden **bewusst nicht persistiert** -
  nach jedem Reboot ist beides immer im Ausgangszustand (`TESTMODE=0` bzw.
  `LIGHT=AUTO`), unabhängig vom letzten Zustand vor dem Neustart.
- **Geräte-ID seit Rev 4.9.0:** die Basis-MAC-Adresse (`mac`-Feld, siehe 4.2)
  wird nicht im NVS gespeichert (sie steht bereits fest im Werks-Efuse),
  aber in `status`/`config`/`confignet`/`cal` mitgesendet - Zuordnung durch
  den Stand-PC kann sich jetzt darauf statt nur auf `lane` (Nutzer-vergeben)
  oder die Absender-IP stützen.
- **Zusätzliche NVS-Keys seit Rev 4.9.0** (nicht über `SET` erreichbar,
  ausschließlich intern für das Netzwerk-Sicherheitsnetz, siehe 7.6):
  `net_pnd` (bool) sowie je ein `*_prv`-Key pro Netzwerkfeld (`ssid_prv`,
  `pass_prv`, `host_prv`, `port_prv`, `static_prv`, `ip_prv`, `gw_prv`,
  `subnet_prv`, `dns_prv`) - enthalten die zuletzt per `NET CONFIRM`
  bestätigte (oder beim allerersten Boot: die Werks-)Netzwerkkonfiguration.

## 7. Fachliches Modell (Positionsmessung)

### 7.1 Mikrofon-Kanäle

6 Luftschall-Mikrofone, Index 0-5 (Reihenfolge = `air_ns`-Array-Index in
jedem `shot`-Telegramm sowie Index bei `OFS<i>`/`MICEN<i>`):

| Index | GPIO | Position (Normalstand) |
|---|---|---|
| 0 | 25 | links unten |
| 1 | 26 | rechts unten |
| 2 | 27 | links oben |
| 3 | 14 | rechts oben |
| 4 | 4 | links mitte |
| 5 | 13 | rechts mitte |

Index 4/5 lagen bis Rev 4.11.2 auf GPIO32/33 - seit Rev 4.11.3 auf GPIO4/13
umverkabelt, um den GPIO-Interrupt-Dispatch-Bias zwischen den beiden
Hardware-Bänken (GPIO 0-31 vs. 32-39) zu vermeiden, der bei Treffern nahe
der Scheibenmitte einen positionsabhängigen, nicht kalibrierbaren
Zeitstempel-Fehler an genau diesen beiden Kanälen verursachte.

Zusätzlich ein optionales Piezo-Kontaktmikrofon (GPIO34, Stahlplatte) als
Trigger-Bestätigung (`SET PIEZO`) - kein eigener Index in `air_ns`, sondern
separate `piezo_ns`/`piezo_ok`-Felder. GPIO34 bleibt in der "zweiten Bank",
das ist unkritisch, da die Piezo-Verzögerung (`PIEZOMIN`..`PIEZOMAX`,
≥100µs) weit außerhalb des ns-Bereichs dieses Effekts liegt.

**Zeitstempel-Erfassung (`SET CAPTURE`, seit Rev 4.13.0):**

- `ISR` (Default): jede Flanke löst einen GPIO-Interrupt aus, der den
  CPU-Zykluszähler liest. Zwischen Flanke und Lesen liegt die
  Interrupt-Latenz samt Jitter (WLAN-Interrupts, gemeinsamer Dispatcher).
- `MCPWM`: jeder Kanal hängt an einem MCPWM-Capture-Kanal (Index 0-2 an
  Gruppe 0, Index 3-5 an Gruppe 1, gleiche GPIOs wie oben). Die Hardware
  friert den Zählerstand (APB, 80 MHz) zum Flankenzeitpunkt ein; die
  Firmware rechnet ihn exakt in CPU-Zyklen um, alles Weitere (Ringpuffer,
  Auswertung, Kalibrierung, `TESTSHOOT`) ist unverändert. Beide Gruppen
  werden beim Boot über einen Impuls auf **GPIO23** gleichzeitig genullt -
  dieser Pin wird dabei als Ausgang getrieben und **muss unbeschaltet
  bleiben**. Das Piezo bleibt ein normaler GPIO-Interrupt (dient nur als
  Gate mit ≥100 µs Spielraum).

**Amplituden-Korrektur über die Pulsbreite (`SET WALKK`, seit Rev 4.14.0):**

Ein leises Signal (fernes Mic, schräger Schalleinfall) überschreitet die
Komparatorschwelle später als ein lautes. Diese Verzögerung hängt von der
Trefferlage ab und lässt sich daher nicht per `OFS` wegkalibrieren. Ohne
zusätzliche Hardware dient die **Pulsbreite** als Amplitudenmaß: Bei
`CAPTURE=MCPWM` erfasst jeder Capture-Kanal zusätzlich die fallende Flanke;
Pulsbreite = Zeit von der steigenden Flanke bis zur letzten fallenden Flanke
vor der nächsten gespeicherten steigenden (höchstens `WALKMAX`, Preller
< 300 ns zählen nicht). Ein lautes Signal ergibt einen breiteren Puls. Die
Korrektur `WALKK·(w − w̄)` bezieht sich auf das Schuss-Mittel `w̄`, damit
verschiebt sich die geschätzte Einschlagzeit (Piezo-Gate bei `ALGO=RIM`)
nicht. Empfohlenes Vorgehen: `CAPTURE=MCPWM`, `CAL RESET`, `WALKCAL=1`,
`CAL START` - danach steht der ermittelte Wert in `cal.walk_k`.

### 7.2 Koordinatensystem

- **Ursprung:** Mittelpunkt der Zielfläche (Papierscheibe).
- **x:** nach rechts, **y:** nach oben, **z:** rechtwinklig von der Zielfläche
  weg Richtung Schütze - jeweils aus Schützensicht.
- **Einheit in allen Telegrammen:** 0.001 mm (Mikrometer) als Ganzzahl
  (`x_um`, `y_um`, `pos_res_um`, `precision_um`, `offset_x_um`, `offset_y_um`
  usw.) - **nicht** Millimeter, trotz des `_um`-Suffix ist es tatsächlich
  Mikrometer (1000 = 1.0 mm).
- Geometrie: Mic-Y-Abstand zur Mitte fest ±85 mm, Mic-Standoff (Z) per
  `SET STANDOFFPAPER` (Default 28 mm), horizontaler Mic-Abstand per
  `SET MICHALFX` (Default 115 mm). **Bis Rev 4.11.0** gab es zusätzlich einen
  `STEEL`-Modus (direkter Beschuss einer Stahlplatte ohne Papier, ±100 mm/
  30 mm) - seit Rev 4.11.1 entfernt, da nicht mehr genutzt (siehe
  Changelog in `schiessstand_firmware.ino`). Das Piezo (siehe 4.5b/7.3b)
  sitzt weiterhin auf einer Stahlplatte **hinter** der Papierscheibe - das
  ist reine Trigger-Hardware, kein Messmodus, und von der Entfernung nicht
  betroffen.
- Die Position wird per **TDOA-Hyperbel-Trilateration** aus den ersten
  Flankenzeiten von mindestens 3 Mikrofonen berechnet, mit einer
  Schallgeschwindigkeit: bei `SET DHT=1` aus der DHT22-Messung, sonst
  `SET SOUNDSPEED` (Default 342.5 m/s).
- **Schallgeschwindigkeit aus Temperatur/Luftfeuchte (seit Rev 4.15.0):** Sie
  ändert sich um ca. 0,6 m/s (0,18 %) je Kelvin; der Fehler wirkt wie ein
  Maßstabsfehler, der mit dem Abstand vom Zentrum wächst. Berechnung:
  `c = 331,3·√(1 + T/273,15)·(1 + 0,16·x_w)` mit dem Wasserdampf-
  Stoffmengenanteil `x_w` (Sättigungsdampfdruck nach Magnus, Luftdruck fest
  1013,25 hPa). Gegenüber der ausführlichen freETarget-Formel höchstens
  0,12 m/s Abweichung zwischen -5 und 30 °C. Der DHT22 wird ohne
  Interrupt-Sperre ausgelesen (Mic-Flanken bleiben unberührt), nur in
  Ruhephasen. **Sensor möglichst nah an der Messstrecke** (Luft zwischen
  Scheibe und Mikrofonen) platzieren, nicht neben warmer Elektronik.

### 7.3 Auswertungs-Pipeline je Auslösung (Kurzfassung)

Gilt für `SET ALGO=CLASSIC` (Default). Für `SET ALGO=RIM` siehe 7.3b.

1. Für jede lösbare **Dreier-Kombination** erfasster Mikrofone wird eine
   Kandidatenposition berechnet (TDOA, quadratische Gleichung).
2. Die Kombination mit dem **kleinsten mittleren Rest-Fehler** gegen die
   nicht beteiligten (aber ebenfalls ausgelösten) Mikrofone gewinnt als
   "Stufe-1"-Lösung → `pos_res_um`.
3. Optionale **Kugeldurchmesser-Korrektur** (`SET BSHIFTPCT`/`BSHIFTCAP`):
   verschiebt die Stufe-1-Lösung Richtung/weg von nicht beteiligten
   Mikrofonen, basierend auf deren signiertem Rest-Fehler (Geschoss ~4,5 mm
   Durchmesser strahlt nicht exakt aus dem Lochmittelpunkt ab).
4. **Verifizierung (Stufe 2):** Mittelpunkt **aller** Kandidatenlösungen
   innerhalb `SET RADIUS` um die Stufe-1-Lösung wird als finales Ergebnis
   verwendet (robuster als eine einzelne Dreier-Kombination) → `x_um`/`y_um`.
   `cluster_hits`/`precision_um` werden relativ zu diesem finalen Punkt neu
   berechnet. Seit Rev 4.12.0:
   - `SET CLUSTERCORR=1` (Default): Jeder Kandidat wird vorher mit
     derselben Kugeldurchmesser-Korrektur wie in Schritt 3 verschoben (mit
     seiner eigenen Referenz und seinen eigenen nicht beteiligten Mics).
   - `SET CLUSTERITER=n` (Default 5): Der Cluster wird bis zu `n`-mal um den
     jeweils neuen Mittelpunkt neu gebildet (Mean-Shift).
   - `SET LSQ=1` (Default 0): Anschließend wird die Position per
     Gauss-Newton-Ausgleichsrechnung über x, y und die Startzeit mit allen
     ausgelösten Mics verfeinert (Modell: Schall vom nächstgelegenen Punkt
     des Lochrands, Radius `LSQPELLETR`).

   `CLUSTERCORR=0` + `CLUSTERITER=0` + `LSQ=0` entspricht exakt dem
   Verhalten bis Rev 4.11.5.
5. `SET OFFSETX`/`OFFSETY` werden **erst danach** addiert (reine
   Nachkorrektur, z.B. gegen Messgitter-Versatz) - beeinflusst NICHT
   `pos_res_um`/`precision_um`/`cluster_hits`.
6. `SET DEBUG=3` macht zusätzlich die Stufe-1-Rohwerte (`*_pre`-Felder) und
   jede einzelne Dreier-Kombination (`cand`-Zeilen) sichtbar.

### 7.3b Auswertungs-Pipeline bei `SET ALGO=RIM` (seit Rev 4.11.0)

Siehe `schusserkennung-rev5.md` für die vollständige Herleitung; Kurzfassung:

1. **Ringpuffer statt Sammelfenster:** jede Luft-Mic-Flanke wird laufend in
   einem Ringpuffer je Kanal aufgezeichnet, unabhängig von Sperrzeit/
   Fenster-Zustand - geht dadurch bei einem Mündungsknall-Fehlausloeser (bei
   10m ~38ms vor dem Einschlag) nicht mehr verloren.
2. **Piezo als alleiniger Trigger:** nur eine Piezo-Flanke startet die
   Auswertung ("Anker", `t=0`). Ausgewertet wird rückwirkend: der Einschlag
   muss in `[-PIEZOMAX, -PIEZOMIN]` relativ zum Anker liegen ("Gate") -
   Mündungsknall, Echos und Störflanken fallen dadurch automatisch heraus.
3. **MSAC-Hypothesen:** jede Mic-Dreierkombination × Kandidatenflanken wird
   geschlossen gelöst und nach Konsens der übrigen Mikrofone bewertet - ein
   einzelnes Echo kann so nicht mehr "gewinnen".
4. **Robuste Ausgleichsrechnung** (Gauss-Newton, Huber-Gewichte) über alle
   Inlier-Mikrofone gleichzeitig, inkl. **Lochrand-Modell** (`SET PELLETR`):
   der erste Schall kommt vom Randpunkt, der dem jeweiligen Mikrofon am
   nächsten liegt, nicht vom Lochmittelpunkt.
5. **Güte:** `sigma_x_um`/`sigma_y_um` aus der Kovarianzmatrix statt
   `precision_um`/`cluster_hits`; `clean` nutzt `SET MAXSIGMA` statt
   `MAXPRECISION`/`RADIUS`/`MINCLUSTER` (die drei bleiben nur für
   `ALGO=CLASSIC` wirksam).

### 7.3c Zonen-Kalibrierung (seit Rev 4.17.0, `SET ZONES=1`)

Der Knall ist je nach getroffenen Papierlagen unterschiedlich laut; ein
leiseres Signal erreicht die Komparatorschwelle später, und zwar umso später,
je weiter das Mikrofon entfernt ist. Mit einem einzigen Parametersatz bleiben
dadurch je Bereich systematische Abweichungen (Foto-Studie 12.10.2026: 2–4 mm).

| Zone | Bereich | Bedingung |
|---|---|---|
| A | im Loch der Vorsatzscheibe (nur Schussband) | r < `ZHOLE` |
| B | neben dem Schussband (nur Vorsatzscheibe) | \|x\| > `ZBAND` |
| C | Schussband + Vorsatzscheibe | sonst |

Ablauf je gültigem Schuss (nur `ALGO=CLASSIC`):

1. Lage wie bisher mit den Basiswerten (`SOUNDSPEED`, `OFS<i>`, `OFFSETX/Y`).
2. Zonen-Gewichte aus dieser Lage: wA = Rampe(r, `ZHOLE`), wB = 1 − Rampe(\|x\|,
   `ZBAND`), Gewichte {wA, (1−wA)·wB, (1−wA)·(1−wB)}; Rampe = 1 bis Grenze −
   `ZBLEND`, 0 ab Grenze + `ZBLEND`, linear dazwischen.
3. Für jede Zone mit Gewicht > 0 neu rechnen mit ihrem Satz (`ZCAL`; ohne Satz
   die Basiswerte), Ergebnis inkl. Nachkorrektur der Zone, Lage gewichtet
   mitteln; `pos_res_um`/`precision_um`/`cluster_hits` von der Zone mit dem
   größten Anteil.
4. Ändern sich die Gewichte mit der neuen Lage, Schritte 2–3 wiederholen
   (höchstens 3 Durchgänge).

Der Server-Simulator rechnet identisch (`server/sim_zones.go`); die Sätze
entstehen dort per Foto-Kalibrierung („One-Click“) und werden per `SET ZCAL`
übertragen. `CAL START` und `CAL IMPORT` ändern nur die Basiswerte, `CAL RESET`
verwirft zusätzlich die Zonen-Sätze.

### 7.4 Kalibrierungsablauf (`CAL START`)

Läuft in **beiden** Auswertepfaden (`SET ALGO`) gleich ab und kalibriert
jeweils den Pfad, der zum Zeitpunkt von `CAL START` aktiv ist (bei `ALGO=RIM`
wird intern die RMS-Residuen-Summe der Ausgleichsrechnung statt der
`pos_res_um`-Summe minimiert - Ergebnis sind in beiden Fällen dieselben
`OFS0..OFS5`-Mikrofon-Timing-Offsets).

1. `CAL START` → `cal`/`"start"`. Sammlung beginnt **parallel** zum
   Normalbetrieb (jede Auslösung erzeugt weiterhin normal `shot`/`reject`).
2. Jede Auslösung mit **≥3 Mikrofonen und** (falls `SET PIEZO=1`) einer
   plausiblen Piezo-Bestätigung zählt als Kalibrier-Schuss → `cal`/`"waiting"`
   mit Fortschritt. Andere Auslösungen während der Sammlung → `cal`/`"skipped"`.
   Bei `ALGO=RIM` genügt **≥3 Mikrofone innerhalb des Gate-Zeitfensters** -
   eine gesonderte Piezo-Bestätigung entfällt (das Piezo ist dort immer der
   Trigger selbst).
3. Nach `SET CALSHOTS` (Default 10) gesammelten Schüssen: automatische
   Optimierung per Koordinatenabstieg (11 Runden, Schrittweite halbiert sich
   je Runde, Referenzmikrofon 0 bleibt fix bei Offset 0 - Kalibrierungsfreiheitsgrad),
   minimiert die Summe der Rest-Fehler über alle gesammelten Schüsse. Bei
   `ALGO=CLASSIC` ist das seit Rev 4.11.4 der Rest-Fehler **nach** Anwendung
   der Kugeldurchmesser-Korrektur (`SET BSHIFTPCT`/`BSHIFTCAP`), nicht mehr
   der reine `pos_res_um`-Wert davor - vorher war `BSHIFTPCT`/`BSHIFTCAP` für
   die Kalibrier-Suche komplett wirkungslos. `CLUSTERCORR`/`CLUSTERITER`/`LSQ`
   (Rev 4.12.0) wirken nur auf die Endposition (Stufe 2) und ändern das
   Kalibrierergebnis daher nicht, verlängern aber die Rechenzeit der Suche.
4. Ergebnis wird **automatisch in NVS gespeichert** (`OFS0..OFS5`), `cal`/`"done"`
   gefolgt von einem vollen `config`-Telegramm.
5. **`SOUNDSPEED` wird von diesem Vorgang nicht verändert** - trotz des
   Felds `sound_mps` im `"done"`-Telegramm (das ist nur der aktuell
   gültige, unveränderte Wert, informativ mitgeliefert). Bei `SET DHT=1`
   gehen die Offsets mit der jeweils gemessenen Schallgeschwindigkeit in die
   Suche ein (`sound_act_mps`).
6. `CAL ABORT` jederzeit möglich, verwirft die bisher gesammelten (noch
   nicht ausgewerteten) Schüsse ohne die gespeicherten Offsets zu ändern.
7. **Restore (seit Rev 4.9.0):** eine zuvor über `cal`/`"done"` mitgeschnittene
   Kalibrierung lässt sich per `CAL IMPORT` (5.3) atomar zurückschreiben,
   ohne den gesamten Sammelvorgang (Schritte 1-3) zu wiederholen - z.B. nach
   einem `FACTORY`-Reset oder Tausch der Mikrofon-Platine.

### 7.5 Papiervorschub / Scheibenwechsel

- Läuft über eine eigene, nicht-blockierende Schrittmotor-Steuerung
  (NEMA17/TMC2209, 1600 Schritte/Umdrehung, 50,2 mm/Umdrehung).
- **Automatischer Vorschub** nach einer Auslösung: gesteuert durch
  `SET PAPERAUTO` (an/aus) und `SET PAPERTRIGGER` (`ANY`=jede Auslösung,
  `PIEZO`=nur bei Piezo-Bestätigung (Default), `CLEAN`=nur bei `clean:1`).
  Strecke `SET PAPERFEED` (mm), Geschwindigkeitsprofil `SET PAPERSPEED`
  (erste Hälfte konstant, zweite Hälfte lineare Abbremsrampe).
- **Kein eigenes "fertig"-Telegramm** für den Papiervorschub - weder für den
  automatischen noch für `TESTSHOOTPAPER`. Der Stand-PC kann den Abschluss
  nur indirekt ableiten (z.B. Zeitablauf `paperFeedMm/paperSpeedMmS`, oder
  daran, dass ein erneutes `TESTSHOOTPAPER` nicht mehr mit
  `"paper feed busy"` abgelehnt wird).
- **Manueller Dauerbetrieb** (Einfädeln) über zwei Kippschalter-GPIOs direkt
  am Gerät (`PAPER_SW_FWD_PIN`/`PAPER_SW_REV_PIN`) - **nicht** fernsteuerbar,
  rein lokale Hardware-Bedienung ohne jedes Telegramm.
- **Seit Rev 4.9.0** gibt es mit `ACTION TARGETCHANGE` (5.5) einen für den
  produktiven Bedienfall benannten Fernbefehl - technisch identisch zu
  `TESTSHOOTPAPER` (gleicher Codepfad, gleiche Einschränkungen: kein
  Abschluss-Telegramm, kein `shot`-Telegramm/Zähler, `"paper feed busy"` bei
  Überlappung). `TESTSHOOTPAPER` bleibt zusätzlich für Diagnosezwecke
  bestehen - beide Befehle sind austauschbar, nur die Namensgebung
  unterscheidet sich.

### 7.6 Netzwerk-Sicherheitsnetz (seit Rev 4.9.0)

Schützt davor, dass eine fehlerhafte Fern-Netzwerkkonfiguration (falsches
WLAN-Passwort, falsche statische IP, falscher Zielport, ...) das Gerät
dauerhaft unerreichbar macht - nach Vorbild der "Bestätigen Sie die neue
Einstellung innerhalb von X Sekunden"-Mechanik gängiger Router.

**Betroffene Keys:** `SSID`, `PASS`, `HOST`, `PORT`, `STATIC`, `IP`, `GW`/
`GATEWAY`, `SUBNET`, `DNS` (alle mit ✅ in der Tabelle in 5.2).

**Ablauf:**

1. Der **erste** `SET`-Befehl auf einen dieser Keys seit dem letzten Boot
   sichert die **bis dahin gültigen** Werte aller neun Keys (nicht nur des
   gerade geänderten!) in eigene NVS-Keys (`ssid_prv`, `pass_prv`, ...,
   siehe Abschnitt 6) und setzt `net_pnd=true` im NVS. Jeder **weitere**
   solche `SET`-Befehl im selben Boot-Zyklus überschreibt diesen
   Rücksprungpunkt **nicht** erneut - er bleibt die zuletzt bestätigte
   Konfiguration, auch wenn mehrere Felder nacheinander geändert werden.
   `confignet` zeigt ab hier `"net_pending":true` (noch ohne
   `net_confirm_deadline_s` - der Watchdog läuft erst nach dem Reboot).
2. Ein `REBOOT` (manuell oder z.B. nach `SET SSID=...`) startet das Gerät
   mit den **neuen** Werten. Da `net_pnd` im NVS noch `true` ist, aktiviert
   `setup()` jetzt den Rücksprung-Watchdog mit einem Zeitfenster von 3
   Minuten (`NET_CONFIRM_TIMEOUT_MS`) - **rein zeitbasiert**, unabhängig
   davon, ob WLAN/TCP mit den neuen Werten überhaupt zustande kommt. Ab hier
   zeigt `confignet` zusätzlich `net_confirm_deadline_s` (verbleibende
   Sekunden), `NET STATUS` liefert dasselbe.
3. **Bestätigen:** Der Stand-PC (oder ein Bediener über Serial) sendet
   `NET CONFIRM` **über die neue, gerade funktionierende Verbindung** -
   das ist zugleich der Beweis, dass die neue Konfiguration tatsächlich
   erreichbar ist. Das löscht `net_pnd` und den Rücksprungpunkt endgültig;
   `NET CONFIRM` **vor** diesem Reboot wird mit einem Fehler abgelehnt
   (würde das Sicherheitsnetz aufheben, ohne dass die neuen Werte je
   getestet wurden).
4. **Kein Confirm innerhalb von 3 Minuten** (egal ob wegen falscher
   Zugangsdaten, falscher IP, oder schlicht vergessen): das Gerät schreibt
   automatisch die gesicherten `*_prv`-Werte zurück in die aktiven Keys,
   löscht `net_pnd` und **startet neu** - danach läuft wieder die zuletzt
   bestätigte, bekanntermaßen erreichbare Konfiguration.
5. Reihenfolge mehrerer Änderungen vor dem ersten Reboot ist beliebig - erst
   der Reboot "scharf schaltet" die Watchdog-Uhr, nicht der einzelne `SET`.

⚠️ Zwischen Schritt 1 (erster `SET`) und Schritt 2 (Reboot) läuft das Gerät
noch mit der **alten** Konfiguration - in dieser Phase ist noch kein
Risiko vorhanden, der Watchdog beginnt bewusst erst nach dem Reboot zu
zählen (siehe Schritt 2). Ein Gerät, das **nie neu bootet**, behält die alte
Konfiguration unbegrenzt mit `net_pending:true` in der Warteschleife -
das ist beabsichtigt, kein Fehlerzustand.

### 7.7 `TESTSHOOT` — Herleitung der synthetischen Rohdaten (seit Rev 4.9.1)

`TESTSHOOT` erzeugt keinen separaten Telegrammtyp, sondern speist synthetische
Werte GENAU in die Puffer ein, die sonst die Mikrofon-/Piezo-Interrupts
füllen - bei `ALGO=CLASSIC` (`airCC`/`airCount`/`firstAirCC`/`firstHitTimeUs`/
`piezoCC`/`piezoSeen`), bei `ALGO=RIM` (seit Rev 4.11.0) analog in die
Ringpuffer (`ringCC`/`ringUs`) plus einen synthetischen Piezo-Anker
(`piezoAnchorCC`/`piezoAnchorUs`/`piezoPending`) - der komplette weitere
Ablauf (Fenster-/Anker-Logik, `processShot()`/`processShotAnchored()`,
automatischer Papiervorschub, `clean`-Bewertung) läuft danach **exakt so wie
bei einem echten Schuss**, ohne jede Sonderbehandlung im Auswertungscode.
Einzige Ausnahme: die Kalibrierungs-Sammlung (`CAL START`) wird bewusst
**nicht** durchlaufen - `TESTSHOOT` wird abgelehnt, solange eine Kalibrierung
aktiv ist (5.7), damit keine synthetischen, unrealistisch perfekten Werte in
eine echte Kalibrierung einfließen.

**Herleitung je aktivem Mikrofonkanal** (`SET MICEN0..MICEN5`, deaktivierte
Kanäle bleiben wie bei einem echten Schuss unberücksichtigt):

1. Zufällige Zielposition `(x, y)` gleichverteilt in `[-z_mm, +z_mm]` mm je
   Achse (unabhängig voneinander, also quadratischer, kein kreisförmiger
   Auswahlbereich).
2. Für jeden aktiven Mikrofonkanal `i`: physikalischer Abstand zum Ziel unter
   der aktuellen Zielgeometrie (`MIC_X[i]`/`MIC_Y[i]`/`micStandoffMm`, siehe
   7.2 - identisch zu den Werten, die auch `solveAirPosition()` für echte
   Schüsse verwendet) und daraus die Schall-Laufzeit mit der aktuellen
   `SET SOUNDSPEED`.
3. Nullpunkt = das am schnellsten erreichte Mikrofon, der Piezo folgt mit
   einer Verzögerung in der Mitte des erlaubten Fensters `SET PIEZOMIN`..
   `SET PIEZOMAX`.
4. Die so berechnete (kalibrationsfreie) Laufzeit wird je Kanal um den
   aktuellen `SET OFS<i>`-Wert verschoben, bevor sie in `airCC[i][0]`
   geschrieben wird - denn `processShot()` zieht genau diesen Offset beim
   Auswerten wieder ab (siehe 7.3). Ergebnis: die Positionslösung
   (`solveAirPosition()`) erhält exakt so kalibrierte Werte, wie sie ein
   echter Treffer an dieser Stelle unter der **aktuell gültigen**
   Kalibrierung erzeugt hätte - inklusive etwaiger Restfehler durch
   unperfekte Kalibrierung (ein `TESTSHOOT` bei schlecht kalibrierten
   Offsets liefert also bewusst KEINE perfekt rekonstruierte Position,
   sondern zeigt genau den Fehler, den auch ein echter Schuss dort hätte).
5. Rundungen (Fließkomma-Nanosekunden → ganzzahlige CPU-Zyklen, siehe
   `cpuMHz`) sind die einzige Quelle einer (typischerweise verschwindend
   kleinen, deutlich unter 1 µm liegenden) Abweichung zwischen
   `target_x_um`/`target_y_um` aus der `TESTSHOOT`-Bestätigung (5.7) und dem
   `x_um`/`y_um` im nachfolgenden `shot`-Telegramm.

**Praktische Nutzung für die Stand-PC-Anbindung:** `TESTSHOOT` eignet sich,
um ohne Munition/Sensorik vor Ort zu prüfen, dass (a) die TCP-Verbindung und
das Zeilenprotokoll korrekt funktionieren, (b) der Stand-PC `shot`-Telegramme
korrekt parst und positioniert darstellt, und (c) automatischer
Papiervorschub/`clean`-Bewertung mit der aktuell hinterlegten Kalibrierung
plausibel reagieren - jeweils unter Ausschluss über das `synthetic`-Feld aus
einer echten Wertung.

## 8. Testmodus (`SET TESTMODE=1`) — Warnung für die Stand-PC-Anbindung

⚠️ **Im Testmodus verlässt die Firmware das JSON-Zeilenformat.** Jede
Sensor-Auslösung (6 Luftmikrofone + Piezo) erzeugt eine **Klartext**-Zeile
(kein `"type"`-Feld, kein gültiges JSON):

```
AIR2 links-oben       +0.930 ms
AIR3 rechts-oben       +0.940 ms
PIEZO stahlplatte      +1.180 ms
----------
```

(10 `-` als Trennlinie nach 5 s Stille zwischen zwei Serien.) Diese Zeilen
gehen über **denselben** Kanal (Serial **und** TCP) wie die normalen
JSON-Telegramme. Ein Stand-PC-Parser, der jede Zeile als JSON erwartet, muss
mit `SET TESTMODE=1` entweder rechnen (Zeilen, die nicht mit `{` beginnen,
verwerfen/gesondert behandeln) oder diesen Befehl aus der Ferne gar nicht
erst zulassen. Zusätzlich loggen auch Papiervorschub-Zustandswechsel im
Testmodus Klartextzeilen (`PAPER STEP(GPIO21) Impuls #100` usw.). Der Modus
ist **nicht NVS-persistent** - ein Reboot beendet ihn zuverlässig.

## 9. Praktische Hinweise für die Stand-PC-Implementierung

1. **Zeilenparser:** Erwarte ausschließlich `\n`-terminierte Zeilen. Jede
   Zeile ist unabhängig - kein mehrzeiliges JSON, keine Fortsetzungszeilen.
   Ignoriere/logge robust jede Zeile, die nicht mit `{` beginnt (Testmodus,
   siehe 8) oder kein gültiges JSON ist.
2. **`type` ist der einzige verlässliche Dispatch-Schlüssel** - alle anderen
   Felder sind je nach `type` unterschiedlich oder bedingt vorhanden (siehe
   die `[...]`-Markierungen in Abschnitt 4).
3. **Keine Wanduhrzeit vom ESP32** - `ts` in `shot` und `uptime_s` in
   `status` sind Gerätezeit seit Boot. Der Stand-PC muss beim Empfang einer
   Zeile selbst einen Zeitstempel (Ankunftszeit) vergeben, wenn eine absolute
   Zeit gebraucht wird.
4. **Sequenznummern (`seq`) zählen standardmäßig nur `shot`** (`SET
   SENDREJECT=0`, Rejects kommen dann gar nicht per TCP an); nur bei
   `SENDREJECT=1` laufen sie über `shot` UND `reject` gemeinsam. Sie
   beginnen bei `RESET` neu bei 0 - der Stand-PC sollte `seq`-Lücken/-
   Rücksprünge als Hinweis auf einen `RESET`-Befehl oder einen Neustart
   interpretieren, nicht als Fehler.
5. **Nach einem Reconnect** können gepufferte `shot`/`reject`-Zeilen (siehe
   3.7) vor dem frischen `status`-Telegramm ankommen oder - bei langer
   Downtime - mit Lücken im `seq`-Zähler, weil ältere Pufferzeilen verworfen
   wurden.
6. **Keine Authentifizierung, kein TLS** - wenn der Stand-PC mehr als reine
   Diagnosebefehle sendet (insbesondere `SET`, `FACTORY`, `REBOOT`), sollte
   er selbst sicherstellen, dass nur autorisierte Software auf diesen TCP-Port
   zugreifen kann (z.B. per Netzwerksegmentierung/Firewall) - die Firmware
   selbst prüft nichts.
7. **Ein Stand-PC-seitiger Befehl blockiert nichts auf dem ESP32.** Seit Rev
   4.9.0 kann jeder Befehl mit `" #<id>"` versehen werden, damit die
   Antwort(en) darauf per `"corr":<id>` eindeutig zuordenbar sind (Abschnitt
   2/2. oben) - empfohlen, sobald der Stand-PC mehrere Befehle ohne auf die
   jeweilige Antwort zu warten hintereinander sendet.
8. **`FACTORY` löscht auch die WLAN-Zugangsdaten** - danach ist das Gerät nur
   noch per USB/Serial erreichbar, bis erneut `SET SSID`/`SET PASS` gesetzt
   und `REBOOT` ausgeführt wurde.
9. **Netzwerkänderungen selbst sind jetzt abgesichert** (7.6): der Stand-PC
   muss nach einer `SET SSID/HOST/PORT/...`-Änderung plus `REBOOT` aktiv
   `NET CONFIRM` senden, sobald die neue Verbindung steht - sonst rollt das
   Gerät die Änderung nach spätestens 3 Minuten selbstständig zurück. Ein
   Stand-PC, der Netzwerkkonfiguration fernsteuert, sollte diesen Schritt
   fest in seinen Ablauf einbauen (nicht nur als manuelle Option), sonst
   verpufft jede Netzwerkänderung nach 3 Minuten automatisch wieder.
10. **Geräte-ID (`mac`) ist jetzt in `status`/`config`/`confignet`/`cal`
    enthalten** (4.2) - für eine Stand-PC-Datenhaltung (Konfiguration,
    Netzwerkeinstellungen, Kalibrierungshistorie je physischem Gerät) sollte
    darüber statt über `lane` oder die Absender-IP verknüpft werden, sobald
    mehr als ein Gerät im Einsatz ist oder Geräte/Bahnen getauscht werden
    können.
11. **Kalibrierung lässt sich jetzt passiv mitschneiden und aktiv
    zurückspielen** (7.4, 4.6, 5.3): auf `cal`/`"done"` lauschen und
    `mac`+`offsets_ns`+`sound_mps` speichern, bei Bedarf per `CAL IMPORT`
    wiederherstellen - kein Umweg mehr über einzelne `SET OFS<i>`-Befehle
    nötig.
12. Aus dem Konzept (`docs/remote-interaktion-konzept.md`) noch **nicht**
    umgesetzt: die serverseitige Persistenz selbst (liegt außerhalb dieser
    Firmware) und die optionale `AUTH`-Token-Absicherung schreibender
    Befehle - der TCP-Port bleibt weiterhin ohne jede Authentifizierung
    erreichbar (Punkt 6 oben gilt unverändert, `NET`/`ACTION`/`CAL IMPORT`
    eingeschlossen).
13. **`TESTSHOOT` (5.7/7.7) erzeugt reguläre `shot`/`reject`-Telegramme**
    (ein `reject` davon bei `SENDREJECT=0` also nur auf Serial) -
    ein Stand-PC, der jede Auslösung automatisch in eine Wertung übernimmt,
    **muss** auf das zusätzliche Feld `"synthetic":1` prüfen und solche
    Zeilen davon ausschließen (bei echten Schüssen fehlt das Feld komplett,
    ein simples "Feld vorhanden?"-Check reicht). Ansonsten ist ein
    `TESTSHOOT` in jeder Hinsicht ein normaler Eintrag im `seq`-Zähler,
    zählt in `shots` (`status`, 4.2) mit und löst ggf. automatischen
    Papiervorschub aus wie ein echter Treffer.
