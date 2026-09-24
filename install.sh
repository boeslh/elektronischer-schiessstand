#!/usr/bin/env bash
# ============================================================================
# install.sh – interaktive Gesamtinstallation des elektronischen Schiessstand-
# Systems auf einem frischen Debian 12 ("Bookworm") Rechner.
#
# Wird aus dem entpackten Archiv von package.sh heraus ausgefuehrt (dieses
# Verzeichnis muss server/, standpc/, preisanzeige/, wertmaschine/ und
# db-backups/demodb1.dump enthalten):
#
#   sudo ./install.sh
#
# Fragt nacheinander, welche Komponenten installiert werden sollen (Server,
# StandPC, Preisanzeige/Display-Server, Wertmaschine), legt bei Bedarf den
# Systembenutzer an, baut die Binaries und richtet je Komponente einen
# systemd-Service ein (wiederverwendet die bestehenden
# <komponente>/install-service.sh-Skripte).
#
# Nicht-interaktive Nutzung einzelner Komponenten weiterhin ueber die
# jeweiligen <komponente>/install-service.sh moeglich - dieses Skript ist der
# gefuehrte Weg fuer eine komplette Neuinstallation.
# ============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GO_VERSION="1.26.4"

# ── Root prüfen ─────────────────────────────────────────────────────────────
if [[ $EUID -ne 0 ]]; then
  echo "Bitte mit sudo ausführen: sudo $0" >&2
  exit 1
fi

# ── Hilfsfunktionen ─────────────────────────────────────────────────────────
# Debian-Installationen ab DVD/ISO tragen einen "deb cdrom:..."-Eintrag in
# /etc/apt/sources.list ein - ohne eingelegtes Medium bricht "apt-get update"
# daran mit Fehlercode ab, obwohl die eigentlichen Netz-Repos erreichbar
# waeren. Zeile auskommentieren (Backup als .bak), statt den Fehler bei jedem
# apt-get update zu riskieren.
if grep -qE '^\s*deb\s+cdrom:' /etc/apt/sources.list 2>/dev/null; then
  echo "    Deaktiviere cdrom-Eintrag in /etc/apt/sources.list (Installations-DVD nicht eingelegt)..."
  cp /etc/apt/sources.list /etc/apt/sources.list.bak
  sed -i -E 's/^(\s*deb\s+cdrom:.*)$/# \1/' /etc/apt/sources.list
fi

apt_ensure() {
  # apt_ensure pkg1 pkg2 ... - installiert nur, was tatsaechlich fehlt (kein
  # "apt-get update" bei bereits vollstaendiger Paketliste).
  local missing=() p
  for p in "$@"; do
    dpkg -s "$p" &>/dev/null || missing+=("$p")
  done
  if (( ${#missing[@]} > 0 )); then
    echo "    Installiere: ${missing[*]}"
    # "|| true": ein einzelnes nicht erreichbares Repo (z.B. verbliebener
    # cdrom-Eintrag) liefert einen Fehlercode, auch wenn alle anderen Quellen
    # erfolgreich aktualisiert wurden - apt-get install schlaegt danach von
    # selbst fehl, falls das Paket wirklich nirgends verfuegbar ist.
    apt-get update -qq || true
    apt-get install -y -qq "${missing[@]}"
  fi
}
ask_yn() {
  # ask_yn "Frage" default(j|n)  ->  Rueckgabewert 0=ja, 1=nein
  local prompt="$1" default="${2:-j}" reply hint
  [[ "$default" == "j" ]] && hint="[J/n]" || hint="[j/N]"
  read -r -p "${prompt} ${hint} " reply </dev/tty || reply=""
  reply="${reply:-$default}"
  [[ "$reply" =~ ^[jJyY] ]]
}

ask_val() {
  # ask_val "Frage" "Standard"  ->  Ausgabe auf stdout
  local prompt="$1" default="$2" reply
  read -r -p "${prompt} [${default}] " reply </dev/tty || reply=""
  echo "${reply:-$default}"
}

section() {
  echo ""
  echo "━━━ $1 ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
}

service_exists() {
  # service_exists <name> (ohne .service) -> 0 wenn die Unit-Datei existiert,
  # egal ob gerade aktiv/inaktiv.
  [[ -f "/etc/systemd/system/$1.service" ]]
}

# existing_standpc_lanes: Anzahl bereits vorhandener StandPC-Instanzen
# (schiessstand-standpc + schiessstand-standpc-laneNN) auf diesem Rechner -
# fuer die Update-Erkennung unten.
existing_standpc_lanes() {
  local n=0
  service_exists schiessstand-standpc && n=1
  local f
  for f in /etc/systemd/system/schiessstand-standpc-lane*.service; do
    [[ -f "$f" ]] || continue
    n=$((n + 1))
  done
  echo "$n"
}

echo "╔══════════════════════════════════════════════════════════════╗"
echo "║   Elektronischer Schiessstand – Installation (Debian 12)     ║"
echo "╚══════════════════════════════════════════════════════════════╝"

# ── 1. Basis-Pakete ──────────────────────────────────────────────────────────
# Diese Pakete haben alle Prioritaet "optional" - auf einer minimalen Debian-
# Installation (Netinst ohne "Standard-Systemwerkzeuge", schlankes Cloud-
# Image) NICHT garantiert vorhanden. sudo wird sowohl hier als auch in allen
# <komponente>/install-service.sh-Skripten fuer "sudo -u <user>" gebraucht,
# curl fuer den Go-Download (Schritt 4), openssl fuer PSK/Passwort-Erzeugung.
section "1/7 Basis-Pakete"
apt_ensure sudo curl ca-certificates openssl

# ── 1b. Systembenutzer ───────────────────────────────────────────────────────
section "2/7 Systembenutzer"
RUN_USER="$(ask_val "Unter welchem Benutzer sollen die Dienste laufen?" "myshoot")"
if id "${RUN_USER}" &>/dev/null; then
  echo "    Benutzer '${RUN_USER}' existiert bereits."
else
  echo "    Lege Benutzer '${RUN_USER}' an..."
  useradd -m -s /bin/bash "${RUN_USER}"
  echo "    Passwort setzen (fuer lokale Anmeldung/SSH):"
  passwd "${RUN_USER}" || echo "    WARNUNG: Passwort nicht gesetzt, spaeter mit 'sudo passwd ${RUN_USER}' nachholen."
fi
usermod -aG dialout "${RUN_USER}" 2>/dev/null || true
TARGET_HOME="/home/${RUN_USER}"

# ── 2. Quellcode an Ort und Stelle bringen ──────────────────────────────────
section "3/7 Quellcode"
if [[ "$(cd "${SCRIPT_DIR}" && pwd)" == "$(cd "${TARGET_HOME}" && pwd 2>/dev/null || echo /nonexistent)" ]]; then
  echo "    Wird bereits aus ${TARGET_HOME} ausgefuehrt - kein Kopieren noetig."
  DEPLOY_DIR="${TARGET_HOME}"
else
  echo "    Kopiere server/, standpc/, preisanzeige/, wertmaschine/ nach ${TARGET_HOME}..."
  for comp in server standpc preisanzeige wertmaschine; do
    if [[ -d "${SCRIPT_DIR}/${comp}" ]]; then
      if command -v rsync &>/dev/null; then
        rsync -a "${SCRIPT_DIR}/${comp}/" "${TARGET_HOME}/${comp}/"
      else
        mkdir -p "${TARGET_HOME}/${comp}"
        cp -a "${SCRIPT_DIR}/${comp}/." "${TARGET_HOME}/${comp}/"
      fi
    fi
  done
  chown -R "${RUN_USER}:${RUN_USER}" "${TARGET_HOME}"
  DEPLOY_DIR="${TARGET_HOME}"
fi
SERVER_DIR="${DEPLOY_DIR}/server"
STANDPC_DIR="${DEPLOY_DIR}/standpc"
PREISANZEIGE_DIR="${DEPLOY_DIR}/preisanzeige"
WERTMASCHINE_DIR="${DEPLOY_DIR}/wertmaschine"

# ── 3. Voraussetzungen (Go-Toolchain, psql-Client) ──────────────────────────
section "4/7 Voraussetzungen"
NEED_GO=true
if command -v /usr/local/go/bin/go &>/dev/null; then
  CUR_GO="$(/usr/local/go/bin/go version | grep -oP 'go\K[0-9]+\.[0-9]+' || echo 0.0)"
  MAJ="${CUR_GO%%.*}"; MIN="${CUR_GO##*.}"
  if (( MAJ > 1 || (MAJ == 1 && MIN >= 22) )); then
    NEED_GO=false
    echo "    Go-Toolchain gefunden: $(/usr/local/go/bin/go version)"
  fi
fi
if $NEED_GO; then
  echo "    Installiere Go ${GO_VERSION} (Debian-Paket ist zu alt fuer dieses Projekt)..."
  TMP_TGZ="$(mktemp --suffix=.tar.gz)"
  ARCH="$(dpkg --print-architecture 2>/dev/null || echo amd64)"
  [[ "$ARCH" == "arm64" ]] || ARCH="amd64"
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz" -o "${TMP_TGZ}" \
    || { echo "FEHLER: Go-Download fehlgeschlagen (kein Internetzugang?)." >&2; exit 1; }
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "${TMP_TGZ}"
  rm -f "${TMP_TGZ}"
  for prof in /etc/profile.d/go.sh; do
    echo 'export PATH=$PATH:/usr/local/go/bin' > "$prof"
    chmod 644 "$prof"
  done
  echo "    OK: $(/usr/local/go/bin/go version)"
fi

apt_ensure postgresql-client

# ── 4. Komponentenauswahl ────────────────────────────────────────────────────
# Erkennt bereits vorhandene Installationen (systemd-Unit vorhanden) und
# fragt dann nach einem UPDATE statt einer Neuinstallation - Entscheidung
# liegt in jedem Fall beim Benutzer (ask_yn), nichts laeuft automatisch.
section "5/7 Komponentenauswahl"
INSTALL_SERVER=false; INSTALL_STANDPC=false; INSTALL_PREISANZEIGE=false; INSTALL_WERTMASCHINE=false
SERVER_EXISTING=false; STANDPC_EXISTING_COUNT=0; PREISANZEIGE_EXISTING=false; WERTMASCHINE_EXISTING=false

if service_exists schiessstand-server; then
  SERVER_EXISTING=true
  echo "    Server ist bereits installiert ($(systemctl is-active schiessstand-server 2>/dev/null))."
  ask_yn "Server aktualisieren (Binary neu bauen, Konfiguration beibehalten, DB-Schema aktualisieren)?" j && INSTALL_SERVER=true
else
  ask_yn "Server (zentrale Verwaltung, Datenbank) installieren?" j && INSTALL_SERVER=true
fi

STANDPC_EXISTING_COUNT="$(existing_standpc_lanes)"
if (( STANDPC_EXISTING_COUNT > 0 )); then
  echo "    StandPC ist bereits installiert (${STANDPC_EXISTING_COUNT} Instanz(en) gefunden)."
  ask_yn "StandPC aktualisieren (bestehende Konfiguration bleibt erhalten, kann pro Bahn ueberschrieben werden)?" j && INSTALL_STANDPC=true
else
  ask_yn "StandPC (Treffererfassung je Bahn) installieren?" j && INSTALL_STANDPC=true
fi

if service_exists schiessstand-preisanzeige; then
  PREISANZEIGE_EXISTING=true
  echo "    Preisanzeige ist bereits installiert ($(systemctl is-active schiessstand-preisanzeige 2>/dev/null))."
  ask_yn "Preisanzeige aktualisieren?" j && INSTALL_PREISANZEIGE=true
else
  ask_yn "Preisanzeige/Display-Server (Kiosk/Ergebnisse) installieren?" n && INSTALL_PREISANZEIGE=true
fi

if service_exists schiessstand-wertmaschine; then
  WERTMASCHINE_EXISTING=true
  echo "    Wertmaschine-Anbindung ist bereits installiert ($(systemctl is-active schiessstand-wertmaschine 2>/dev/null))."
  ask_yn "Wertmaschine-Anbindung aktualisieren?" j && INSTALL_WERTMASCHINE=true
else
  ask_yn "Wertmaschine-Anbindung (Disag RM III/IV) installieren?" n && INSTALL_WERTMASCHINE=true
fi

if ! $INSTALL_SERVER && ! $INSTALL_STANDPC && ! $INSTALL_PREISANZEIGE && ! $INSTALL_WERTMASCHINE; then
  echo "Nichts ausgewaehlt - Installation abgebrochen."
  exit 0
fi

NEEDS_DB=false
$INSTALL_STANDPC && NEEDS_DB=true
$INSTALL_PREISANZEIGE && NEEDS_DB=true

# ── 5. Server-Adresse/Datenbank klären ──────────────────────────────────────
section "6/7 Server & Datenbank"
DB_HOST="127.0.0.1"
DB_PASSWORD=""
SERVER_URL="http://localhost:8090"
WERTMASCHINE_PSK=""

if $INSTALL_SERVER; then
  # ---- lokale Postgres-Installation ----
  apt_ensure postgresql

  # Bei einem Update bestehende Werte aus server.env als Vorgabe uebernehmen,
  # statt Passwort/Adresse jedes Mal neu zu erfinden - einfach Enter druecken
  # behaelt den bisherigen Stand bei.
  DEFAULT_DB_PASSWORD="$(openssl rand -hex 12)"
  DEFAULT_SERVER_LISTEN=":8090"
  if $SERVER_EXISTING && [[ -f /etc/schiessstand/server.env ]]; then
    EXISTING_DSN="$(grep -oP '(?<=SCHIESSSTAND_DSN=).*' /etc/schiessstand/server.env || true)"
    EXISTING_PW="$(echo "${EXISTING_DSN}" | grep -oP '(?<=schiessstand:)[^@]*' || true)"
    [[ -n "${EXISTING_PW}" ]] && DEFAULT_DB_PASSWORD="${EXISTING_PW}"
    EXISTING_LISTEN="$(grep -oP '(?<=SCHIESSSTAND_LISTEN=).*' /etc/schiessstand/server.env || true)"
    [[ -n "${EXISTING_LISTEN}" ]] && DEFAULT_SERVER_LISTEN="${EXISTING_LISTEN}"
  fi
  DB_PASSWORD="$(ask_val "Passwort fuer die Datenbankrolle 'schiessstand'" "${DEFAULT_DB_PASSWORD}")"
  if ! sudo -u postgres psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='schiessstand'" | grep -q 1; then
    # CREATEDB: der Server legt fuer den selektiven Export/Import (Import/
    # Export-Kachel) kurzzeitig eine Wegwerf-Datenbank an (siehe
    # staged-transfer.go stagedSetup) - ohne dieses Recht schlaegt die
    # Funktion mit "keine Berechtigung, um Datenbank zu erzeugen" fehl.
    sudo -u postgres psql -c "CREATE USER schiessstand WITH PASSWORD '${DB_PASSWORD}' CREATEDB;" >/dev/null
  else
    sudo -u postgres psql -c "ALTER USER schiessstand WITH PASSWORD '${DB_PASSWORD}' CREATEDB;" >/dev/null
  fi
  if ! sudo -u postgres psql -tAc "SELECT 1 FROM pg_database WHERE datname='schiessstand'" | grep -q 1; then
    sudo -u postgres psql -c "CREATE DATABASE schiessstand OWNER schiessstand;" >/dev/null
  fi

  SERVER_LISTEN="$(ask_val "HTTP-Adresse des Servers" "${DEFAULT_SERVER_LISTEN}")"
  DB_HOST="127.0.0.1"
  DSN="postgres://schiessstand:${DB_PASSWORD}@${DB_HOST}/schiessstand"

  # Demo-Restore nur bei einer echten Erstinstallation anbieten - bei einem
  # Update ist der Server schon in Betrieb, ein Ueberschreiben mit der
  # Demo-DB waere hier eher ein Unfall als eine sinnvolle Option.
  RESTORE_DEMO=false
  DEMO_DUMP="${DEPLOY_DIR}/db-backups/demodb1.dump"
  if ! $SERVER_EXISTING && [[ -f "${DEMO_DUMP}" ]] && ask_yn "Demo-Datenbank (demodb1.dump) jetzt einspielen?" j; then
    RESTORE_DEMO=true
  fi

  MIGRATE_ARGS=()
  if $RESTORE_DEMO; then
    echo "    Spiele ${DEMO_DUMP} ein..."
    PGPASSWORD="${DB_PASSWORD}" pg_restore --no-owner --role=schiessstand \
      -h "${DB_HOST}" -U schiessstand -d schiessstand "${DEMO_DUMP}" \
      || echo "    WARNUNG: pg_restore meldete Fehler (teilweise normal bei bereits vorhandenen Objekten)."
    # Dump enthaelt bereits das komplette Schema zum Dump-Zeitpunkt - keine
    # zusaetzlichen Migrationen mehr draufsetzen, um Doppel-Anlage von
    # Typen/Tabellen zu vermeiden.
    MIGRATE_ARGS=(--no-migrate)
  fi

  bash "${SERVER_DIR}/install-service.sh" \
    --user "${RUN_USER}" \
    --dsn "${DSN}" \
    --listen "${SERVER_LISTEN}" \
    --backup-dir "${TARGET_HOME}/db-backups" \
    "${MIGRATE_ARGS[@]+"${MIGRATE_ARGS[@]}"}"

  WERTMASCHINE_PSK="$(grep -oP '(?<=SCHIESSSTAND_WERTMASCHINE_PSK=).*' /etc/schiessstand/server.env || true)"
  SERVER_URL="http://localhost${SERVER_LISTEN}"

elif $NEEDS_DB || $INSTALL_WERTMASCHINE; then
  echo "    Der Server wird NICHT auf diesem Rechner installiert."
  SERVER_IP="$(ask_val "IP-Adresse/Hostname des Servers" "192.168.1.10")"
  SERVER_PORT="$(ask_val "Server-Port" "8090")"
  SERVER_URL="http://${SERVER_IP}:${SERVER_PORT}"
  DB_HOST="${SERVER_IP}"
  if $NEEDS_DB; then
    DB_PASSWORD="$(ask_val "Passwort der Datenbankrolle 'schiessstand' auf dem Server" "")"
    echo "    Hinweis: Die Postgres-Instanz auf ${SERVER_IP} muss Verbindungen von"
    echo "    diesem Rechner annehmen (listen_addresses/pg_hba.conf)."
  fi
  if $INSTALL_WERTMASCHINE; then
    WERTMASCHINE_PSK="$(ask_val "Wertmaschine-PSK (Hex, vom Server-Log/server.env übernehmen)" "")"
  fi
fi

DSN="postgres://schiessstand:${DB_PASSWORD}@${DB_HOST}/schiessstand"

# ── 6. Komponenten installieren ─────────────────────────────────────────────
section "7/7 Komponenten"

if $INSTALL_STANDPC; then
  echo ""
  echo "--- StandPC ---"
  DEFAULT_LANE_COUNT=1
  (( STANDPC_EXISTING_COUNT > 0 )) && DEFAULT_LANE_COUNT="${STANDPC_EXISTING_COUNT}"
  LANE_COUNT="$(ask_val "Wie viele Stände/StandPC-Instanzen auf diesem Rechner?" "${DEFAULT_LANE_COUNT}")"
  if ! [[ "${LANE_COUNT}" =~ ^[0-9]+$ ]] || (( LANE_COUNT < 1 )); then
    echo "    Ungueltige Zahl, verwende 1."
    LANE_COUNT=1
  fi

  # ESP32-PSK: bestehenden Wert wiederverwenden (config.json vom letzten
  # Lauf), sonst den Firmware-Standard (AUTH_PSK_DEFAULT_HEX in
  # schiessstand_firmware.ino) vorschlagen - JEDE ESP32 verwendet diesen
  # Wert ab Werk, bis er einmal per "SET PSK=<hex>" am Geraet geaendert
  # wurde. Ein hier frei erfundener/zufaelliger Schluessel wuerde mit einer
  # unveraenderten ESP32 zu "TCP: Signatur ungueltig" fuehren.
  FIRMWARE_DEFAULT_PSK="df07c9827321073c62f4edd830aa8b6adbac934a126605b25b33006c03fb2c9f"
  ESP32_PSK=""
  if [[ -f "${STANDPC_DIR}/config.json" ]]; then
    ESP32_PSK="$(grep -oP '"esp32_psk_hex"\s*:\s*"\K[^"]*' "${STANDPC_DIR}/config.json" || true)"
  fi
  [[ -z "${ESP32_PSK}" ]] && ESP32_PSK="${FIRMWARE_DEFAULT_PSK}"
  ESP32_PSK="$(ask_val "ESP32-PSK (muss mit dem an den Geraeten dieser Anlage eingestellten Wert uebereinstimmen)" "${ESP32_PSK}")"

  for ((i=1; i<=LANE_COUNT; i++)); do
    if (( i == 1 )); then
      CONFIG_PATH="${STANDPC_DIR}/config.json"
      SVC_NAME="schiessstand-standpc"
    else
      LN="$(printf '%02d' "$i")"
      CONFIG_PATH="${STANDPC_DIR}/config-lane${LN}.json"
      SVC_NAME="schiessstand-standpc-lane${LN}"
    fi
    TCP_PORT=$((9200 + i))
    HTTP_PORT=$((9000 + i))

    if [[ -f "${CONFIG_PATH}" ]] && ! ask_yn "    ${CONFIG_PATH} existiert bereits - überschreiben?" n; then
      echo "    Überspringe ${CONFIG_PATH} (bestehende Konfiguration bleibt erhalten)."
    else
      cat > "${CONFIG_PATH}" << EOF
{
  "lane_no": ${i},
  "transport": "tcp",
  "serial_port": "/dev/ttyUSB$((i-1))",
  "baud_rate": 115200,
  "tcp_listen": ":${TCP_PORT}",
  "esp32_psk_hex": "${ESP32_PSK}",
  "sensors": [
    { "x_mm": 0,   "y_mm": 0   },
    { "x_mm": 250, "y_mm": 0   },
    { "x_mm": 250, "y_mm": 250 },
    { "x_mm": 0,   "y_mm": 250 }
  ],
  "sound_speed_mps": 3000,
  "plate_angle_deg": 30,
  "plate_offset_x_mm": 0,
  "plate_offset_y_mm": 0,
  "caliber_mm": 4.5,
  "edge_scoring": true,
  "shot_log_dir": "${STANDPC_DIR}/shotlog",
  "postgres_dsn": "${DSN}",
  "server_url": "${SERVER_URL}",
  "session_id": "",
  "http_listen": ":${HTTP_PORT}",
  "hybrid": {
    "enabled": false,
    "air_sound_speed_mps": 343,
    "gate_us": 15,
    "air_sensors": [
      { "x_mm": -20,  "y_mm": -20,  "z_mm": 40 },
      { "x_mm": 270,  "y_mm": -20,  "z_mm": 40 },
      { "x_mm": 270,  "y_mm": 270,  "z_mm": 40 },
      { "x_mm": -20,  "y_mm": 270,  "z_mm": 40 }
    ]
  },
  "shot_display": {
    "color_ring10":     "#FF0000",
    "color_ring9":      "#F3FB06",
    "color_other":      "#0209F9",
    "color_previous":   "#AAAAAA",
    "opacity_previous": 0.85,
    "fill_color":       "#000000"
  }
}
EOF
      chown "${RUN_USER}:${RUN_USER}" "${CONFIG_PATH}"
      echo "    Geschrieben: ${CONFIG_PATH} (Bahn ${i}, TCP :${TCP_PORT}, HTTP :${HTTP_PORT})"
    fi

    bash "${STANDPC_DIR}/install-service.sh" \
      --config "${CONFIG_PATH}" \
      --user "${RUN_USER}" \
      --name "${SVC_NAME}"
  done
  echo ""
  echo "    Hinweis: Sensor-Positionen/Schallgeschwindigkeit (sensors/"
  echo "    sound_speed_mps) sind Platzhalter je physischem Stand - vor dem"
  echo "    ersten Einsatz je Bahn kalibrieren (Admin-GUI, siehe README)."
fi

if $INSTALL_PREISANZEIGE; then
  echo ""
  echo "--- Preisanzeige/Display-Server ---"
  DEFAULT_PA_LISTEN=":8091"
  if $PREISANZEIGE_EXISTING && [[ -f /etc/schiessstand/preisanzeige.json ]]; then
    EX="$(grep -oP '"listen_addr"\s*:\s*"\K[^"]*' /etc/schiessstand/preisanzeige.json || true)"
    [[ -n "${EX}" ]] && DEFAULT_PA_LISTEN="${EX}"
  fi
  PA_LISTEN="$(ask_val "HTTP-Adresse der Preisanzeige" "${DEFAULT_PA_LISTEN}")"
  bash "${PREISANZEIGE_DIR}/install-service.sh" \
    --user "${RUN_USER}" \
    --dsn "${DSN}" \
    --listen "${PA_LISTEN}"
fi

if $INSTALL_WERTMASCHINE; then
  echo ""
  echo "--- Wertmaschine ---"
  DEFAULT_WM_COM="/dev/ttyUSB0"; DEFAULT_WM_PROTO="rmiii-win"; DEFAULT_WM_LISTEN=":8092"
  if $WERTMASCHINE_EXISTING && [[ -f /etc/schiessstand/wertmaschine.json ]]; then
    EX="$(grep -oP '"com_port"\s*:\s*"\K[^"]*' /etc/schiessstand/wertmaschine.json || true)"
    [[ -n "${EX}" ]] && DEFAULT_WM_COM="${EX}"
    EX="$(grep -oP '"protocol"\s*:\s*"\K[^"]*' /etc/schiessstand/wertmaschine.json || true)"
    [[ -n "${EX}" ]] && DEFAULT_WM_PROTO="${EX}"
    EX="$(grep -oP '"http_listen"\s*:\s*"\K[^"]*' /etc/schiessstand/wertmaschine.json || true)"
    [[ -n "${EX}" ]] && DEFAULT_WM_LISTEN="${EX}"
    if [[ -z "${WERTMASCHINE_PSK}" ]]; then
      EX="$(grep -oP '"server_psk_hex"\s*:\s*"\K[^"]*' /etc/schiessstand/wertmaschine.json || true)"
      [[ -n "${EX}" ]] && WERTMASCHINE_PSK="${EX}"
    fi
  fi
  WM_COM="$(ask_val "Serieller Port/COM-Port der Wertmaschine" "${DEFAULT_WM_COM}")"
  WM_PROTO="$(ask_val "Protokoll (rmiii / rmiv / rmiii-win)" "${DEFAULT_WM_PROTO}")"
  WM_LISTEN="$(ask_val "HTTP-Adresse der Wertmaschinen-Bedienoberfläche" "${DEFAULT_WM_LISTEN}")"
  if [[ -z "${WERTMASCHINE_PSK}" ]]; then
    echo "    WARNUNG: Kein Wertmaschine-PSK bekannt - Anbindung an den Server"
    echo "    wird fehlschlagen, bis der PSK nachgetragen wird (/etc/schiessstand/wertmaschine.json)."
  fi
  bash "${WERTMASCHINE_DIR}/install-service.sh" \
    --user "${RUN_USER}" \
    --server-url "${SERVER_URL}" \
    --psk-hex "${WERTMASCHINE_PSK}" \
    --com-port "${WM_COM}" \
    --protocol "${WM_PROTO}" \
    --listen "${WM_LISTEN}"
fi

# ── Zusammenfassung ──────────────────────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║  Installation abgeschlossen.                                  ║"
echo "╠══════════════════════════════════════════════════════════════╣"
$INSTALL_SERVER       && echo "║  Server       : ${SERVER_URL}"
$INSTALL_PREISANZEIGE && echo "║  Preisanzeige : http://localhost${PA_LISTEN}"
$INSTALL_WERTMASCHINE && echo "║  Wertmaschine : http://localhost${WM_LISTEN}"
$INSTALL_STANDPC      && echo "║  StandPC      : http://localhost:9001 ... :$((9000 + LANE_COUNT))"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  DB-Backups   : ${TARGET_HOME}/db-backups                     "
echo "║  Logs         : sudo journalctl -u schiessstand-<dienst> -f   "
echo "╚══════════════════════════════════════════════════════════════╝"
