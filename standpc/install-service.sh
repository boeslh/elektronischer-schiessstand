#!/usr/bin/env bash
# ============================================================================
# install-service.sh – StandPC als systemd-Service einrichten
#
# Verwendung:
#   sudo ./install-service.sh [Optionen]
#
# Optionen:
#   --config PATH      Pfad zur config.json  (Standard: <Skript-Verzeichnis>/config.json)
#   --user USER        Systembenutzer        (Standard: Aufrufer vor sudo)
#   --uninstall        Service stoppen, deaktivieren und entfernen
#
# Hinweise:
#   - Pro Stand-PC eine eigene Instanz: Standardname ist "schiessstand-standpc".
#     Für mehrere Instanzen auf demselben Rechner --name verwenden:
#       sudo ./install-service.sh --name schiessstand-standpc-1
#   - Der Benutzer muss Mitglied der Gruppe "dialout" sein (serieller Zugriff):
#       sudo usermod -aG dialout <user>
# ============================================================================
set -euo pipefail

# ── Standardwerte ──────────────────────────────────────────────────────────
SERVICE_NAME="schiessstand-standpc"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BINARY="${SCRIPT_DIR}/standpc_bin"
CONFIG="${SCRIPT_DIR}/config.json"
RUN_USER="${SUDO_USER:-${USER:-myshoot}}"
UNINSTALL=false

# ── Argumente parsen ───────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --config)     CONFIG="$2";       shift 2 ;;
    --user)       RUN_USER="$2";     shift 2 ;;
    --name)       SERVICE_NAME="$2"; shift 2 ;;
    --uninstall)  UNINSTALL=true;    shift ;;
    *) echo "Unbekannte Option: $1" >&2; exit 1 ;;
  esac
done

UNIT_FILE="/etc/systemd/system/${SERVICE_NAME}.service"

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
  systemctl daemon-reload
  echo "    Fertig – Service entfernt."
  exit 0
fi

echo "==> StandPC Service-Installation"
echo "    Verzeichnis : ${SCRIPT_DIR}"
echo "    Benutzer    : ${RUN_USER}"
echo "    Config      : ${CONFIG}"
echo "    Service     : ${SERVICE_NAME}"
echo ""

# ── Dialout-Gruppe prüfen ────────────────────────────────────────────────────
if ! id -nG "${RUN_USER}" 2>/dev/null | grep -qw dialout; then
  echo "WARNUNG: Benutzer '${RUN_USER}' ist nicht in der Gruppe 'dialout'."
  echo "         Serieller Zugriff auf den ESP32 wird fehlschlagen."
  echo "         Beheben: sudo usermod -aG dialout ${RUN_USER}"
  echo "         Danach neu einloggen oder: sudo systemctl restart ${SERVICE_NAME}"
  echo ""
fi

# ── Config prüfen ─────────────────────────────────────────────────────────
if [[ ! -f "${CONFIG}" ]]; then
  echo "FEHLER: Konfigurationsdatei nicht gefunden: ${CONFIG}" >&2
  echo "        Bitte config.json anlegen (Vorlage: config.json.example)" >&2
  exit 1
fi
echo "==> Konfiguration geprüft: ${CONFIG}"

# ── 1. Binary bauen ──────────────────────────────────────────────────────────
echo "==> Baue standpc_bin..."
cd "${SCRIPT_DIR}"
export HOME="/home/${RUN_USER}"
sudo -u "${RUN_USER}" /usr/local/go/bin/go build -o standpc_bin . \
  || { echo "FEHLER: go build fehlgeschlagen."; exit 1; }
echo "    OK: ${BINARY}"

# ── 2. systemd Unit-Datei schreiben ─────────────────────────────────────────
echo "==> Schreibe ${UNIT_FILE}..."
cat > "${UNIT_FILE}" << EOF
[Unit]
Description=Schiessstand StandPC (Stand ${SERVICE_NAME})
Documentation=file://${SCRIPT_DIR}/README.md
After=network.target schiessstand-server.service
# Ohne lokale DB kann der StandPC auch ohne Server laufen (Offline-Modus)
# Wants=schiessstand-server.service

[Service]
Type=simple
User=${RUN_USER}
Group=${RUN_USER}
WorkingDirectory=${SCRIPT_DIR}
ExecStart=${BINARY} -config ${CONFIG}
Restart=on-failure
RestartSec=10s
StartLimitBurst=5
StartLimitIntervalSec=120s

# Logging
StandardOutput=journal
StandardError=journal
SyslogIdentifier=${SERVICE_NAME}

# Zugriff auf serielle Schnittstelle (ESP32 via USB)
# Gruppe dialout wird über User-Gruppenmitgliedschaft vererbt
SupplementaryGroups=dialout

# Sicherheit
NoNewPrivileges=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF
chmod 644 "${UNIT_FILE}"
echo "    OK"

# ── 3. Service aktivieren und starten ────────────────────────────────────────
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
