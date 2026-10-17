#!/usr/bin/env bash
# ============================================================================
# install.sh – Installation bzw. Aktualisierung der Online-Anzeige
# (schiessstand-online) auf einem Ubuntu/Debian-Server, OHNE Go.
#
# Aus dem entpackten Release-Paket aufrufen (build-release.sh):
#
#   sudo ./install.sh
#
# Erstinstallation:
#   - PostgreSQL (falls nicht vorhanden, aus den Paketquellen), Datenbank
#     "schiessstand_online" mit eigenem Benutzer und Zufallspasswort
#   - Systembenutzer schiessstand-online, Programm nach /opt/schiessstand-online
#   - Konfiguration /etc/schiessstand/online.json
#   - Zertifikat: vorhandenes Let's-Encrypt-Zertifikat (/etc/letsencrypt/live/
#     <domain>/), per systemd LoadCredential an den Dienst uebergeben (die
#     Schluesseldatei bleibt nur fuer root lesbar); certbot-Hook startet den
#     Dienst nach jeder Erneuerung neu.
#     Ist Port 443 bereits belegt (nginx/Apache), wird der Dienst stattdessen
#     lokal auf 127.0.0.1:8095 hinter diesem Webserver eingerichtet.
#   - erster Plattform-Administrator
#
# Aktualisierung (Dienst schon installiert): nur Programm austauschen und
# neu starten; Konfiguration und Daten bleiben unveraendert. Die Datenbank-
# Migrationen fuehrt das Programm beim Start selbst aus.
# ============================================================================
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP=schiessstand-online
USER_NAME=schiessstand-online
OPT=/opt/schiessstand-online
CONF=/etc/schiessstand/online.json
STATE=/var/lib/schiessstand-online
UNIT=/etc/systemd/system/${APP}.service
HOOK=/etc/letsencrypt/renewal-hooks/deploy/${APP}.sh
PROXY_PORT=8095

if [[ $EUID -ne 0 ]]; then
  echo "Bitte mit sudo ausführen: sudo $0" >&2
  exit 1
fi

ask() { # ask <Frage> <Vorgabe>
  local reply
  read -r -p "$1 [$2] " reply </dev/tty || reply=""
  echo "${reply:-$2}"
}

say() { echo; echo "==> $*"; }

[[ -x "$DIR/online" ]] || { echo "Programm 'online' fehlt im Paket ($DIR)" >&2; exit 1; }
echo "Paket: $("$DIR/online" -version)"

# ── Aktualisierung ──────────────────────────────────────────────────────────
if [[ -f "$UNIT" && -f "$CONF" ]]; then
  say "Aktualisierung einer bestehenden Installation"
  install -m 755 "$DIR/online" "$OPT/online.new"
  mv "$OPT/online.new" "$OPT/online"
  systemctl restart "$APP"
  sleep 2
  systemctl --no-pager --lines=5 status "$APP" || true
  echo
  echo "Aktualisiert auf $("$OPT/online" -version)."
  exit 0
fi

# ── Erstinstallation ────────────────────────────────────────────────────────
say "Erstinstallation"

# Zertifikat / Domain
DOMAINS=()
if [[ -d /etc/letsencrypt/live ]]; then
  for d in /etc/letsencrypt/live/*/; do
    [[ -f "$d/fullchain.pem" ]] && DOMAINS+=("$(basename "$d")")
  done
fi
if [[ ${#DOMAINS[@]} -eq 0 ]]; then
  echo "Kein Let's-Encrypt-Zertifikat unter /etc/letsencrypt/live gefunden." >&2
  echo "Bitte zuerst eines ausstellen (z. B. 'certbot certonly --standalone -d <domain>')." >&2
  exit 1
fi
echo "Gefundene Zertifikate: ${DOMAINS[*]}"
DOMAIN="$(ask "Domain der Online-Anzeige" "${DOMAINS[0]}")"
[[ -f "/etc/letsencrypt/live/$DOMAIN/fullchain.pem" ]] || { echo "Kein Zertifikat für $DOMAIN" >&2; exit 1; }

# Port 443 frei?
BEHIND_PROXY=false
if ss -Hltn 'sport = :443' | grep -q .; then
  echo
  echo "Port 443 ist bereits belegt:"
  ss -Hltnp 'sport = :443' || true
  echo "Die Online-Anzeige wird lokal auf 127.0.0.1:$PROXY_PORT eingerichtet; der vorhandene"
  echo "Webserver muss die Domain $DOMAIN dorthin weiterleiten (Beispiele am Ende)."
  [[ "$(ask "So fortfahren? (j/n)" "j")" == "j" ]] || exit 1
  BEHIND_PROXY=true
fi

SITE_TITLE="$(ask "Titel der Startseite" "Schießstand online")"
BASE_PATH=""
if $BEHIND_PROXY; then
  echo "Die Anzeige kann unter einem Unterverzeichnis der Domain laufen (z. B. /befreeshoot),"
  echo "die übrigen Seiten der Domain bleiben dann unverändert. Leer = ganze Domain."
  BASE_PATH="$(ask "Unterverzeichnis" "/befreeshoot")"
  BASE_PATH="/${BASE_PATH#/}"; BASE_PATH="${BASE_PATH%/}"
fi

# PostgreSQL
if ! dpkg -s postgresql >/dev/null 2>&1; then
  say "PostgreSQL installieren"
  apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq postgresql
fi
systemctl enable --now postgresql >/dev/null

say "Datenbank einrichten"
DB_PASS="$(head -c 24 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 32)"
if sudo -u postgres psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='schiessstand_online'" | grep -q 1; then
  sudo -u postgres psql -qc "ALTER ROLE schiessstand_online WITH LOGIN PASSWORD '$DB_PASS'"
else
  sudo -u postgres psql -qc "CREATE ROLE schiessstand_online WITH LOGIN PASSWORD '$DB_PASS'"
fi
if ! sudo -u postgres psql -tAc "SELECT 1 FROM pg_database WHERE datname='schiessstand_online'" | grep -q 1; then
  sudo -u postgres createdb -O schiessstand_online schiessstand_online
fi

say "Programm und Benutzer"
id "$USER_NAME" >/dev/null 2>&1 || useradd --system --home "$STATE" --shell /usr/sbin/nologin "$USER_NAME"
install -d -m 755 "$OPT"
install -d -m 750 -o "$USER_NAME" -g "$USER_NAME" "$STATE"
install -m 755 "$DIR/online" "$OPT/online"

say "Konfiguration $CONF"
install -d -m 755 /etc/schiessstand
if $BEHIND_PROXY; then
  TLS_JSON="\"listen_addr\": \"127.0.0.1:$PROXY_PORT\", \"behind_proxy\": true"
else
  TLS_JSON="\"listen_addr\": \":443\", \"tls_cert\": \"\${CREDENTIALS_DIRECTORY}/cert.pem\", \"tls_key\": \"\${CREDENTIALS_DIRECTORY}/key.pem\""
fi
umask 027
cat > "$CONF" <<EOF
{
  "postgres_dsn": "postgres://schiessstand_online:$DB_PASS@127.0.0.1/schiessstand_online",
  $TLS_JSON,
  "site_title": "$SITE_TITLE",
  "base_path": "$BASE_PATH"
}
EOF
chown root:"$USER_NAME" "$CONF"
chmod 640 "$CONF"
umask 022

say "systemd-Dienst"
{
  cat <<EOF
[Unit]
Description=Schiessstand Online-Anzeige
After=network-online.target postgresql.service
Wants=network-online.target

[Service]
User=$USER_NAME
ExecStart=$OPT/online -config $CONF
Restart=always
RestartSec=3
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=$STATE
EOF
  if ! $BEHIND_PROXY; then
    cat <<EOF
# Port 443 ohne root; Zertifikat von Let's Encrypt (bleibt nur fuer root lesbar,
# systemd reicht eine Kopie an den Dienst durch)
AmbientCapabilities=CAP_NET_BIND_SERVICE
LoadCredential=cert.pem:/etc/letsencrypt/live/$DOMAIN/fullchain.pem
LoadCredential=key.pem:/etc/letsencrypt/live/$DOMAIN/privkey.pem
EOF
  fi
  cat <<EOF

[Install]
WantedBy=multi-user.target
EOF
} > "$UNIT"

if ! $BEHIND_PROXY; then
  install -d /etc/letsencrypt/renewal-hooks/deploy
  cat > "$HOOK" <<EOF
#!/bin/sh
# nach jeder Zertifikatserneuerung: Online-Anzeige mit neuem Zertifikat starten
systemctl restart $APP
EOF
  chmod 755 "$HOOK"
fi

say "Datenbank-Schema"
sudo -u "$USER_NAME" "$OPT/online" -config "$CONF" -migrate-only

say "Erster Plattform-Administrator"
ADMIN="$(ask "Benutzername" "admin")"
echo "Passwort für $ADMIN (mindestens 10 Zeichen, wird nicht angezeigt):"
until sudo -u "$USER_NAME" "$OPT/online" -config "$CONF" -create-admin "$ADMIN" < <(read -r -s PW </dev/tty; echo "$PW"); do
  echo "Bitte erneut:"
done

systemctl daemon-reload
systemctl enable --now "$APP"
sleep 2
systemctl --no-pager --lines=5 status "$APP" || true

if command -v ufw >/dev/null && ufw status | grep -q "Status: active" && ! $BEHIND_PROXY; then
  echo
  echo "Hinweis: Firewall (ufw) ist aktiv – ggf. freigeben: sudo ufw allow 443/tcp"
fi

echo
echo "╔══════════════════════════════════════════════════════════════════╗"
echo "  Online-Anzeige installiert: https://$DOMAIN$BASE_PATH/"
echo "  Verwaltung:                 https://$DOMAIN$BASE_PATH/admin/  (Benutzer $ADMIN)"
echo "  Adresse für die Schießstand-Server: https://$DOMAIN$BASE_PATH"
echo "  Logs:      sudo journalctl -u $APP -f"
echo "  Update:    neues Paket entpacken, sudo ./install.sh"
echo "╚══════════════════════════════════════════════════════════════════╝"

if $BEHIND_PROXY; then
  cat <<EOF

Der vorhandene Webserver muss https://$DOMAIN${BASE_PATH:-/} an den Dienst weiterleiten
(erst danach ist die Anzeige erreichbar; die übrigen Seiten der Domain bleiben unberührt):

  Apache – in den <VirtualHost ...:443> der Domain $DOMAIN eintragen
  (Module proxy und proxy_http), dann: apache2ctl configtest && systemctl reload apache2

      ProxyPreserveHost On
      ProxyPass        ${BASE_PATH:-/} http://127.0.0.1:$PROXY_PORT${BASE_PATH:-/}
      ProxyPassReverse ${BASE_PATH:-/} http://127.0.0.1:$PROXY_PORT${BASE_PATH:-/}

  nginx – im server-Block der Domain (listen 443 ssl):

      location ${BASE_PATH:-/} {
          proxy_pass http://127.0.0.1:$PROXY_PORT;
          proxy_set_header Host \$host;
          proxy_set_header X-Forwarded-For \$remote_addr;
          client_max_body_size 20m;
      }
EOF
fi
