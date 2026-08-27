#!/usr/bin/env bash
# ============================================================================
# install-services.sh – Server UND StandPC gemeinsam einrichten
#
# Verwendung:
#   sudo ./install-services.sh [Optionen]
#
# Optionen:
#   --dsn DSN          PostgreSQL-DSN für den Server
#   --listen ADDR      HTTP-Adresse des Servers  (Standard: :8090)
#   --user USER        Systembenutzer            (Standard: Aufrufer vor sudo)
#   --no-standpc       Nur den Server installieren
#   --no-server        Nur den StandPC installieren
#   --no-migrate       Migrationen NICHT anwenden
#   --uninstall        Beide Services entfernen
# ============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVER_DIR="${SCRIPT_DIR}/server"
STANDPC_DIR="${SCRIPT_DIR}/standpc"

INSTALL_SERVER=true
INSTALL_STANDPC=true
EXTRA_SERVER_ARGS=()
EXTRA_STANDPC_ARGS=()
UNINSTALL=false

if [[ $EUID -ne 0 ]]; then
  echo "Bitte mit sudo ausführen: sudo $0" >&2
  exit 1
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    --no-server)    INSTALL_SERVER=false;  shift ;;
    --no-standpc)   INSTALL_STANDPC=false; shift ;;
    --uninstall)    UNINSTALL=true;        shift ;;
    --dsn)          EXTRA_SERVER_ARGS+=(--dsn "$2");    shift 2 ;;
    --listen)       EXTRA_SERVER_ARGS+=(--listen "$2"); shift 2 ;;
    --user)
      EXTRA_SERVER_ARGS+=(--user "$2")
      EXTRA_STANDPC_ARGS+=(--user "$2")
      shift 2 ;;
    --no-migrate)   EXTRA_SERVER_ARGS+=(--no-migrate); shift ;;
    *) echo "Unbekannte Option: $1" >&2; exit 1 ;;
  esac
done

if $UNINSTALL; then
  $INSTALL_SERVER  && bash "${SERVER_DIR}/install-service.sh"  --uninstall
  $INSTALL_STANDPC && bash "${STANDPC_DIR}/install-service.sh" --uninstall
  echo "==> Alle Services entfernt."
  exit 0
fi

echo "╔══════════════════════════════════════════════════════════════╗"
echo "║         Schiessstand – Service-Installation                  ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

if $INSTALL_SERVER; then
  echo "━━━ 1/2 Server ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  bash "${SERVER_DIR}/install-service.sh" "${EXTRA_SERVER_ARGS[@]+"${EXTRA_SERVER_ARGS[@]}"}"
  echo ""
fi

if $INSTALL_STANDPC; then
  echo "━━━ 2/2 StandPC ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  bash "${STANDPC_DIR}/install-service.sh" "${EXTRA_STANDPC_ARGS[@]+"${EXTRA_STANDPC_ARGS[@]}"}"
  echo ""
fi

echo "╔══════════════════════════════════════════════════════════════╗"
echo "║  Alle Services installiert und gestartet.                    ║"
echo "╠══════════════════════════════════════════════════════════════╣"
$INSTALL_SERVER  && echo "║  Server :  http://localhost:8090                             ║"
$INSTALL_STANDPC && echo "║  StandPC:  http://localhost:8080                             ║"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Logs anzeigen:                                              ║"
$INSTALL_SERVER  && echo "║    sudo journalctl -u schiessstand-server  -f                ║"
$INSTALL_STANDPC && echo "║    sudo journalctl -u schiessstand-standpc -f                ║"
echo "╚══════════════════════════════════════════════════════════════╝"
