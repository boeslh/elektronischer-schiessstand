#!/usr/bin/env bash
# ============================================================================
# install-service.sh – Schiessstand-Server als systemd-Service einrichten
#
# Verwendung:
#   sudo ./install-service.sh [Optionen]
#
# Optionen:
#   --dsn DSN          PostgreSQL-DSN (Standard: postgres://schiessstand:CHANGEME@127.0.0.1/schiessstand)
#   --listen ADDR      HTTP-Adresse   (Standard: :8090)
#   --user USER        Systembenutzer (Standard: Aufrufer vor sudo)
#   --no-migrate       Migrationen NICHT automatisch anwenden
#   --uninstall        Service stoppen, deaktivieren und entfernen
# ============================================================================
set -euo pipefail

# ── Standardwerte ──────────────────────────────────────────────────────────
SERVICE_NAME="schiessstand-server"
UNIT_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
ENV_FILE="/etc/schiessstand/server.env"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BINARY="${SCRIPT_DIR}/server_bin"

DSN="postgres://schiessstand:CHANGEME@127.0.0.1/schiessstand"
LISTEN=":8090"
RUN_USER="${SUDO_USER:-${USER:-myshoot}}"
RUN_MIGRATE=true
UNINSTALL=false

# ── Argumente parsen ───────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dsn)      DSN="$2";     shift 2 ;;
    --listen)   LISTEN="$2";  shift 2 ;;
    --user)     RUN_USER="$2";shift 2 ;;
    --no-migrate) RUN_MIGRATE=false; shift ;;
    --uninstall)  UNINSTALL=true;    shift ;;
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
  rm -f "${ENV_FILE}"
  systemctl daemon-reload
  echo "    Fertig – Service entfernt."
  exit 0
fi

echo "==> Schiessstand-Server Service-Installation"
echo "    Verzeichnis : ${SCRIPT_DIR}"
echo "    Benutzer    : ${RUN_USER}"
echo "    Adresse     : ${LISTEN}"
echo "    DSN         : $(echo "$DSN" | sed 's|:[^:@]*@|:***@|')"
echo ""

# ── 1. Binary bauen ──────────────────────────────────────────────────────────
echo "==> Baue server_bin..."
cd "${SCRIPT_DIR}"
export HOME="/home/${RUN_USER}"
sudo -u "${RUN_USER}" /usr/local/go/bin/go build -o server_bin . \
  || { echo "FEHLER: go build fehlgeschlagen."; exit 1; }
echo "    OK: ${BINARY}"

# ── 2. Migrationen anwenden ─────────────────────────────────────────────────
if $RUN_MIGRATE; then
  echo "==> Wende Migrationen an..."
  MIGRATION_DIR="${SCRIPT_DIR}/migrations"
  if [[ -d "${MIGRATION_DIR}" ]]; then
    for sql in "${MIGRATION_DIR}"/*.sql; do
      [[ -f "$sql" ]] || continue
      echo "    $(basename "$sql")..."
      sudo -u "${RUN_USER}" psql "${DSN}" -f "${sql}" -q \
        || { echo "WARNUNG: $(basename "$sql") fehlgeschlagen (evtl. bereits angewandt)"; }
    done
    echo "    Migrationen abgeschlossen."
  else
    echo "    Kein migrations/-Verzeichnis gefunden – übersprungen."
  fi
fi

# ── 3. Umgebungsdatei anlegen (Credentials nicht in der Unit-Datei) ──────────
echo "==> Lege Umgebungsdatei an: ${ENV_FILE}"
mkdir -p "$(dirname "${ENV_FILE}")"
cat > "${ENV_FILE}" << EOF
SCHIESSSTAND_DSN=${DSN}
SCHIESSSTAND_LISTEN=${LISTEN}
EOF
chmod 640 "${ENV_FILE}"
chown "root:${RUN_USER}" "${ENV_FILE}"
echo "    OK (Berechtigungen: root:${RUN_USER} 640)"

# ── 4. systemd Unit-Datei schreiben ─────────────────────────────────────────
echo "==> Schreibe ${UNIT_FILE}..."
cat > "${UNIT_FILE}" << EOF
[Unit]
Description=Schiessstand Verwaltungsserver
Documentation=file://${SCRIPT_DIR}/README.md
After=network.target postgresql.service postgresql@15-main.service
Wants=postgresql.service

[Service]
Type=simple
User=${RUN_USER}
Group=${RUN_USER}
WorkingDirectory=${SCRIPT_DIR}
EnvironmentFile=${ENV_FILE}
ExecStart=${BINARY} -dsn \${SCHIESSSTAND_DSN} -listen \${SCHIESSSTAND_LISTEN}
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

# ── 5. Service aktivieren und starten ────────────────────────────────────────
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
