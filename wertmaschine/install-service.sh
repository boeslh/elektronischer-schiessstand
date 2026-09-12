#!/usr/bin/env bash
# ============================================================================
# install-service.sh – wertmaschine als systemd-Service einrichten
#
# Verwendung:
#   sudo ./install-service.sh [Optionen]
#
# Optionen:
#   --server-url URL   Zentraler Server         (Standard: http://localhost:8090)
#   --psk-hex HEX       Wertmaschine-PSK (Hex)   (Standard: aus server.env gelesen, falls vorhanden)
#   --com-port PORT     Serieller Port/COM-Port  (Standard: /dev/ttyUSB0)
#   --protocol PROTO    rmiii oder rmiv          (Standard: rmiii)
#   --listen ADDR        HTTP-Adresse (Bedienoberflaeche) (Standard: :8092)
#   --user USER          Systembenutzer          (Standard: Aufrufer vor sudo)
#   --uninstall           Service stoppen, deaktivieren und entfernen
#
# Läuft als eigener Dienst auf dem Rechner mit dem COM-Port (Server selbst
# oder ein separater Büro-PC) - siehe Konzept .claude/plans/wise-scribbling-abelson.md.
# Der Systembenutzer braucht Zugriff auf den seriellen Port (Gruppe "dialout" -
# die Unit setzt SupplementaryGroups=dialout, ein manuelles usermod ist nicht
# noetig).
# ============================================================================
set -euo pipefail

# ── Standardwerte ──────────────────────────────────────────────────────────
SERVICE_NAME="schiessstand-wertmaschine"
UNIT_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
CONFIG_FILE="/etc/schiessstand/wertmaschine.json"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BINARY="${SCRIPT_DIR}/wertmaschine"

SERVER_URL="http://localhost:8090"
PSK_HEX=""
COM_PORT="/dev/ttyUSB0"
PROTOCOL="rmiii"
LISTEN=":8092"
RUN_USER="${SUDO_USER:-${USER:-myshoot}}"
UNINSTALL=false

# ── Argumente parsen ───────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --server-url) SERVER_URL="$2"; shift 2 ;;
    --psk-hex)    PSK_HEX="$2";    shift 2 ;;
    --com-port)   COM_PORT="$2";   shift 2 ;;
    --protocol)   PROTOCOL="$2";   shift 2 ;;
    --listen)     LISTEN="$2";     shift 2 ;;
    --user)       RUN_USER="$2";   shift 2 ;;
    --uninstall)  UNINSTALL=true;  shift ;;
    *) echo "Unbekannte Option: $1" >&2; exit 1 ;;
  esac
done

# ── Root prüfen ─────────────────────────────────────────────────────────────
if [[ $EUID -ne 0 ]]; then
  echo "Bitte mit sudo ausführen: sudo $0" >&2
  exit 1
fi

# ── Deinstallation ──────────────────────────────────────────────────────────
if $UNINSTALL; then
  echo "==> Entferne ${SERVICE_NAME}..."
  systemctl stop    "${SERVICE_NAME}" 2>/dev/null || true
  systemctl disable "${SERVICE_NAME}" 2>/dev/null || true
  rm -f "${UNIT_FILE}"
  rm -f "${CONFIG_FILE}"
  systemctl daemon-reload
  echo "    Fertig – Service entfernt."
  exit 0
fi

# ── PSK ggf. aus server.env uebernehmen (gleicher Rechner) ──────────────────
if [[ -z "${PSK_HEX}" && -f /etc/schiessstand/server.env ]]; then
  PSK_HEX="$(grep -oP '(?<=SCHIESSSTAND_WERTMASCHINE_PSK=).*' /etc/schiessstand/server.env || true)"
fi
if [[ -z "${PSK_HEX}" ]]; then
  echo "FEHLER: kein --psk-hex angegeben und keiner in /etc/schiessstand/server.env gefunden." >&2
  echo "        Muss mit server.wertmaschine_psk_hex uebereinstimmen." >&2
  exit 1
fi

echo "==> wertmaschine Service-Installation"
echo "    Verzeichnis : ${SCRIPT_DIR}"
echo "    Benutzer    : ${RUN_USER}"
echo "    Server-URL  : ${SERVER_URL}"
echo "    COM-Port    : ${COM_PORT}"
echo "    Protokoll   : ${PROTOCOL}"
echo "    Adresse     : ${LISTEN}"
echo ""

# ── 1. Binary bauen ──────────────────────────────────────────────────────────
echo "==> Baue wertmaschine..."
cd "${SCRIPT_DIR}"
export HOME="/home/${RUN_USER}"
sudo -u "${RUN_USER}" /usr/local/go/bin/go build -o wertmaschine . \
  || { echo "FEHLER: go build fehlgeschlagen."; exit 1; }
echo "    OK: ${BINARY}"

# ── 2. Konfigurationsdatei anlegen (PSK nicht in der Unit-Datei) ────────────
echo "==> Lege Konfiguration an: ${CONFIG_FILE}"
mkdir -p "$(dirname "${CONFIG_FILE}")"
cat > "${CONFIG_FILE}" << EOF
{
  "server_url": "${SERVER_URL}",
  "server_psk_hex": "${PSK_HEX}",
  "com_port": "${COM_PORT}",
  "protocol": "${PROTOCOL}",
  "http_listen": "${LISTEN}"
}
EOF
chmod 640 "${CONFIG_FILE}"
chown "root:${RUN_USER}" "${CONFIG_FILE}"
echo "    OK (Berechtigungen: root:${RUN_USER} 640)"

# ── 3. systemd Unit-Datei schreiben ─────────────────────────────────────────
echo "==> Schreibe ${UNIT_FILE}..."
cat > "${UNIT_FILE}" << EOF
[Unit]
Description=Schiessstand Disag-Wertmaschine
After=network.target schiessstand-server.service

[Service]
Type=simple
User=${RUN_USER}
Group=${RUN_USER}
SupplementaryGroups=dialout
WorkingDirectory=${SCRIPT_DIR}
ExecStart=${BINARY} -config ${CONFIG_FILE}
Restart=on-failure
RestartSec=5s
StartLimitBurst=5
StartLimitIntervalSec=60s

# Logging
StandardOutput=journal
StandardError=journal
SyslogIdentifier=${SERVICE_NAME}

# Sicherheit (minimale Einschränkungen - PrivateDevices=no, da /dev/ttyUSB*
# bzw. native COM-Ports erreichbar sein muessen)
NoNewPrivileges=yes

[Install]
WantedBy=multi-user.target
EOF
chmod 644 "${UNIT_FILE}"
echo "    OK"

# ── 4. Service aktivieren und starten ────────────────────────────────────────
echo "==> Aktiviere und starte ${SERVICE_NAME}..."
systemctl daemon-reload
systemctl enable "${SERVICE_NAME}"
systemctl restart "${SERVICE_NAME}"

sleep 2
echo ""
echo "==> Status:"
systemctl status "${SERVICE_NAME}" --no-pager -l | head -20

echo ""
echo "==> Installation abgeschlossen."
echo ""
echo "    Bedienoberflaeche: http://localhost${LISTEN}"
echo ""
echo "    Nützliche Befehle:"
echo "    sudo systemctl status  ${SERVICE_NAME}"
echo "    sudo systemctl restart ${SERVICE_NAME}"
echo "    sudo systemctl stop    ${SERVICE_NAME}"
echo "    sudo journalctl -u ${SERVICE_NAME} -f"
