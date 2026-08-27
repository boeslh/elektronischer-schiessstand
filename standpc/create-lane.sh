#!/usr/bin/env bash
# ============================================================================
# create-lane.sh – Neue StandPC-Instanz (Lane) einrichten
#
# Jede Lane läuft als eigener Prozess mit eigener config.json und eigenem
# systemd-Service. Ports werden nach folgendem Schema vergeben:
#
#   Lane N  →  HTTP-Anzeige :90{NN}  /  ESP32-TCP :92{NN}
#   Lane 1  →  :9001  /  :9201
#   Lane 2  →  :9002  /  :9202
#   ...
#   Lane 99 →  :9099  /  :9299
#
# :8090 bleibt dem Verwaltungsserver vorbehalten.
#
# Verwendung:
#   ./create-lane.sh --lane N [Optionen]
#
# Optionen:
#   --lane N           Stand-Nummer (Pflicht, 1–99)
#   --transport MODE   serial | tcp | both  (Standard: tcp)
#   --serial-port DEV  Serieller Port, z.B. /dev/ttyUSB1  (nur bei serial/both)
#   --http-port PORT   Überschreibt auto-berechneten HTTP-Port
#   --tcp-port  PORT   Überschreibt auto-berechneten ESP32-TCP-Port
#   --install          Service direkt mit sudo installieren
#   --list             Vorhandene Lane-Konfigurationen anzeigen
#
# Beispiele:
#   ./create-lane.sh --lane 2
#   ./create-lane.sh --lane 3 --transport serial --serial-port /dev/ttyUSB2
#   ./create-lane.sh --lane 4 --install
# ============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

LANE_NO=""
TRANSPORT="tcp"
SERIAL_PORT=""
HTTP_PORT=""
TCP_PORT=""
DO_INSTALL=false
DO_LIST=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --lane)         LANE_NO="$2";      shift 2 ;;
    --transport)    TRANSPORT="$2";    shift 2 ;;
    --serial-port)  SERIAL_PORT="$2";  shift 2 ;;
    --http-port)    HTTP_PORT="$2";    shift 2 ;;
    --tcp-port)     TCP_PORT="$2";     shift 2 ;;
    --install)      DO_INSTALL=true;   shift ;;
    --list)         DO_LIST=true;      shift ;;
    *) echo "Unbekannte Option: $1" >&2; exit 1 ;;
  esac
done

# ── Liste ────────────────────────────────────────────────────────────────────
if $DO_LIST; then
  echo "Vorhandene Lane-Konfigurationen:"
  found=0
  for cfg in "${SCRIPT_DIR}"/config-lane*.json; do
    [[ -f "$cfg" ]] || continue
    lane=$(python3 -c "import json,sys; c=json.load(open('$cfg')); print(c.get('lane_no','?'))" 2>/dev/null || echo "?")
    http=$(python3 -c "import json,sys; c=json.load(open('$cfg')); print(c.get('http_listen','?'))" 2>/dev/null || echo "?")
    tcp=$(python3  -c "import json,sys; c=json.load(open('$cfg')); print(c.get('tcp_listen','?'))" 2>/dev/null || echo "?")
    svc="schiessstand-standpc-lane${lane}"
    active=$(systemctl is-active "$svc" 2>/dev/null || echo "not-installed")
    printf "  Lane %-3s  HTTP %-7s  TCP %-7s  %s  [%s]\n" \
      "$lane" "$http" "$tcp" "$(basename "$cfg")" "$active"
    found=$((found+1))
  done
  # config.json (Lane 1 Standard)
  if [[ -f "${SCRIPT_DIR}/config.json" ]]; then
    lane=$(python3 -c "import json; c=json.load(open('${SCRIPT_DIR}/config.json')); print(c.get('lane_no','?'))" 2>/dev/null || echo "?")
    http=$(python3 -c "import json; c=json.load(open('${SCRIPT_DIR}/config.json')); print(c.get('http_listen','?'))" 2>/dev/null || echo "?")
    tcp=$(python3  -c "import json; c=json.load(open('${SCRIPT_DIR}/config.json')); print(c.get('tcp_listen','?'))" 2>/dev/null || echo "?")
    active=$(systemctl is-active "schiessstand-standpc" 2>/dev/null || echo "not-installed")
    printf "  Lane %-3s  HTTP %-7s  TCP %-7s  config.json  [%s]\n" \
      "$lane" "$http" "$tcp" "$active"
    found=$((found+1))
  fi
  [[ $found -eq 0 ]] && echo "  (keine gefunden)"
  exit 0
fi

# ── Lane-Nummer prüfen ────────────────────────────────────────────────────────
if [[ -z "$LANE_NO" ]]; then
  echo "FEHLER: --lane N ist Pflicht." >&2
  echo "Verwendung: $0 --lane N [--transport tcp|serial|both] [--install]" >&2
  exit 1
fi
if ! [[ "$LANE_NO" =~ ^[0-9]+$ ]] || [[ $LANE_NO -lt 1 ]] || [[ $LANE_NO -gt 99 ]]; then
  echo "FEHLER: Lane-Nummer muss zwischen 1 und 99 liegen." >&2
  exit 1
fi

# ── Ports automatisch berechnen ───────────────────────────────────────────────
# Anzeige (Browser): 9001–9099  (Lane 1 = :9001, Lane 2 = :9002, ...)
# ESP32-TCP:         9201–9299  (Lane 1 = :9201, Lane 2 = :9202, ...)
if [[ -z "$HTTP_PORT" ]]; then
  HTTP_PORT=":$((9000 + LANE_NO))"
fi
if [[ -z "$TCP_PORT" ]]; then
  TCP_PORT=":$((9200 + LANE_NO))"
fi

# ── Serial-Port aus Vorlage oder Standard ─────────────────────────────────────
if [[ -z "$SERIAL_PORT" ]]; then
  SERIAL_PORT="/dev/ttyUSB$((LANE_NO - 1))"  # 0-basiert: Lane 1 → ttyUSB0
fi

# ── Zieldatei ────────────────────────────────────────────────────────────────
LANE_PADDED=$(printf "%02d" "$LANE_NO")
CONFIG_FILE="${SCRIPT_DIR}/config-lane${LANE_PADDED}.json"
LOG_DIR="${SCRIPT_DIR}/shotlog"  # gemeinsam – Dateien heissen lane02_DATUM.jsonl

if [[ -f "$CONFIG_FILE" ]]; then
  echo "WARNUNG: $CONFIG_FILE existiert bereits und wird überschrieben."
fi

# ── Vorlage aus bestehender config.json lesen ─────────────────────────────────
TEMPLATE="${SCRIPT_DIR}/config.json"
if [[ ! -f "$TEMPLATE" ]]; then
  echo "FEHLER: Keine config.json als Vorlage gefunden." >&2
  exit 1
fi

echo "==> Erstelle Lane-$LANE_NO Konfiguration: $CONFIG_FILE"
echo "    HTTP-Port (Anzeige)  : $HTTP_PORT"
echo "    TCP-Port  (ESP32)    : $TCP_PORT"
echo "    Transport            : $TRANSPORT"
[[ "$TRANSPORT" != "tcp" ]] && echo "    Serieller Port       : $SERIAL_PORT"
echo ""

# ── Config-JSON generieren ────────────────────────────────────────────────────
python3 - << PYEOF
import json, copy, sys

with open("${TEMPLATE}") as f:
    cfg = json.load(f)

cfg["lane_no"]     = ${LANE_NO}
cfg["http_listen"] = "${HTTP_PORT}"
cfg["transport"]   = "${TRANSPORT}"
cfg["tcp_listen"]  = "${TCP_PORT}"
cfg["serial_port"] = "${SERIAL_PORT}"
cfg["shot_log_dir"] = "${LOG_DIR}"

with open("${CONFIG_FILE}", "w") as f:
    json.dump(cfg, f, indent=2, ensure_ascii=False)
    f.write("\n")

print(f"    Konfiguration geschrieben: ${CONFIG_FILE}")
PYEOF

echo ""
echo "==> Nächste Schritte:"
echo ""
echo "    1. Kalibrierung in $CONFIG_FILE prüfen/anpassen"
echo "       (sensors, sound_speed_mps, plate_angle_deg)"
echo ""
echo "    2. Service installieren:"
echo "       sudo ${SCRIPT_DIR}/install-service.sh \\"
echo "         --config ${CONFIG_FILE} \\"
echo "         --name schiessstand-standpc-lane${LANE_PADDED}"
echo ""
echo "    3. Browser-Anzeige für Stand $LANE_NO:"
echo "       http://localhost${HTTP_PORT}"
echo ""

# ── Direkt installieren wenn --install ───────────────────────────────────────
if $DO_INSTALL; then
  if [[ $EUID -ne 0 ]]; then
    echo "==> Installiere Service (sudo erforderlich)..."
    sudo "${SCRIPT_DIR}/install-service.sh" \
      --config "${CONFIG_FILE}" \
      --name "schiessstand-standpc-lane${LANE_PADDED}"
  else
    "${SCRIPT_DIR}/install-service.sh" \
      --config "${CONFIG_FILE}" \
      --name "schiessstand-standpc-lane${LANE_PADDED}"
  fi
fi
