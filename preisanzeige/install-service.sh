#!/usr/bin/env bash
# ============================================================================
# install-service.sh – preisanzeige als systemd-Service einrichten
#
# Verwendung:
#   sudo ./install-service.sh [Optionen]
#
# Optionen:
#   --dsn DSN     PostgreSQL-DSN (Standard: postgres://schiessstand:CHANGEME@127.0.0.1/schiessstand)
#   --listen ADDR HTTP-Adresse   (Standard: :8091)
#   --user USER   Systembenutzer (Standard: Aufrufer vor sudo)
#   --uninstall   Service stoppen, deaktivieren und entfernen
#
# Ein laufender Dienst bedient ALLE Preisschießen der DB gleichzeitig -
# Auswahl erfolgt über den Pfad (/ps/<id>/...), die Startseite "/" listet sie
# zur Auswahl auf (siehe site.go). Kein --preisschiessen-Parameter mehr nötig.
# ============================================================================
set -euo pipefail

# ── Standardwerte ──────────────────────────────────────────────────────────
SERVICE_NAME="schiessstand-preisanzeige"
UNIT_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
CONFIG_FILE="/etc/schiessstand/preisanzeige.json"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BINARY="${SCRIPT_DIR}/preisanzeige"

DSN="postgres://schiessstand:CHANGEME@127.0.0.1/schiessstand"
LISTEN=":8091"
RUN_USER="${SUDO_USER:-${USER:-myshoot}}"
UNINSTALL=false

# ── Argumente parsen ───────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dsn)      DSN="$2";     shift 2 ;;
    --listen)   LISTEN="$2";  shift 2 ;;
    --user)     RUN_USER="$2";shift 2 ;;
    --uninstall) UNINSTALL=true; shift ;;
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

echo "==> preisanzeige Service-Installation"
echo "    Verzeichnis : ${SCRIPT_DIR}"
echo "    Benutzer    : ${RUN_USER}"
echo "    Adresse     : ${LISTEN}"
echo "    DSN         : $(echo "$DSN" | sed 's|:[^:@]*@|:***@|')"
echo ""

# ── 1. Binary bauen ──────────────────────────────────────────────────────────
echo "==> Baue preisanzeige..."
cd "${SCRIPT_DIR}"
export HOME="/home/${RUN_USER}"
sudo -u "${RUN_USER}" /usr/local/go/bin/go build -o preisanzeige . \
  || { echo "FEHLER: go build fehlgeschlagen."; exit 1; }
echo "    OK: ${BINARY}"

# ── 2. Konfigurationsdatei anlegen (Credentials nicht in der Unit-Datei) ────
echo "==> Lege Konfiguration an: ${CONFIG_FILE}"
mkdir -p "$(dirname "${CONFIG_FILE}")"
cat > "${CONFIG_FILE}" << EOF
{
  "postgres_dsn": "${DSN}",
  "listen_addr": "${LISTEN}"
}
EOF
chmod 640 "${CONFIG_FILE}"
chown "root:${RUN_USER}" "${CONFIG_FILE}"
echo "    OK (Berechtigungen: root:${RUN_USER} 640)"

# ── 3. systemd Unit-Datei schreiben ─────────────────────────────────────────
echo "==> Schreibe ${UNIT_FILE}..."
cat > "${UNIT_FILE}" << EOF
[Unit]
Description=Schiessstand Preisschiessen-Anzeige
After=network.target postgresql.service postgresql@15-main.service schiessstand-server.service
Wants=postgresql.service

[Service]
Type=simple
User=${RUN_USER}
Group=${RUN_USER}
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

# Sicherheit (minimale Einschränkungen)
NoNewPrivileges=yes
PrivateTmp=yes

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
echo "    Nützliche Befehle:"
echo "    sudo systemctl status  ${SERVICE_NAME}"
echo "    sudo systemctl restart ${SERVICE_NAME}"
echo "    sudo systemctl stop    ${SERVICE_NAME}"
echo "    sudo journalctl -u ${SERVICE_NAME} -f"
