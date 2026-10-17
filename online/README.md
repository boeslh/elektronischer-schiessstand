# Schießstand online – Einrichtung des Internet-Servers

Der Dienst `schiessstand-online` zeigt Preisschießen und Rundenwettkämpfe
mehrerer Vereine im Internet an. Die Daten schickt der Schießstand-Server
jedes Vereins selbst (ausgehende HTTPS-Verbindung, API-Schlüssel des
Vereins). Bedienung und Ablauf: `docs/11-online.md`.

Auf dem Internet-Server wird **keine Entwicklungsumgebung** gebraucht: Das
Programm ist eine einzelne, eigenständige Datei ohne Abhaengigkeiten. Nötig
sind nur PostgreSQL (installiert der Installer bei Bedarf aus den
Paketquellen) und ein Let's-Encrypt-Zertifikat für die Domain.

## 1. Paket bauen (auf dem Entwicklungsrechner)

```bash
online/build-release.sh            # -> online/dist/schiessstand-online-<version>-linux-amd64.tar.gz
                                   #    und ...-linux-arm64.tar.gz (für ARM-Server)
```

## 2. Installieren (auf dem Internet-Server, z. B. Ubuntu 24.04)

```bash
scp online/dist/schiessstand-online-<version>-linux-amd64.tar.gz benutzer@server:
ssh benutzer@server
tar -xzf schiessstand-online-<version>-linux-amd64.tar.gz
cd schiessstand-online-<version>-linux-amd64
sudo ./install.sh
```

Der Installer fragt nach Domain (aus `/etc/letsencrypt/live/` vorgeschlagen),
Seitentitel und dem ersten Administrator und richtet ein:

| Was | Wo |
|---|---|
| Programm | `/opt/schiessstand-online/online` |
| Konfiguration | `/etc/schiessstand/online.json` (enthält das Datenbank-Passwort, nur root/Dienst lesbar) |
| Datenbank | `schiessstand_online`, Benutzer `schiessstand_online` mit Zufallspasswort |
| Dienst | `schiessstand-online.service` unter dem Systembenutzer `schiessstand-online` |
| Zertifikat | `/etc/letsencrypt/live/<domain>/` – systemd übergibt es beim Start an den Dienst (`LoadCredential=`), der private Schlüssel bleibt nur für root lesbar |
| Zertifikatserneuerung | `/etc/letsencrypt/renewal-hooks/deploy/schiessstand-online.sh` startet den Dienst nach jeder Erneuerung neu |

Danach unter `https://<domain>/admin/` anmelden, Vereine anlegen und für
jeden Verein einen Benutzer und einen API-Schlüssel erzeugen.

**Port 443 schon belegt** (nginx oder Apache mit mehreren Webseiten auf
demselben Server): Der Installer erkennt das, richtet den Dienst auf
`127.0.0.1:8095` ein und fragt nach einem **Unterverzeichnis** (Vorgabe
`/befreeshoot`). Die Anzeige ist dann z. B. unter
`https://gs.schuetzengau-massenhausen.de/befreeshoot/` erreichbar, die übrigen
Seiten der Domain bleiben unberührt. Am Ende zeigt der Installer den Eintrag
für den vorhandenen Webserver, für Apache im `<VirtualHost …:443>` der Domain:

```apache
ProxyPreserveHost On
ProxyPass        /befreeshoot http://127.0.0.1:8095/befreeshoot
ProxyPassReverse /befreeshoot http://127.0.0.1:8095/befreeshoot
```

(Module `proxy` und `proxy_http`; danach `apache2ctl configtest && systemctl
reload apache2`.) `ProxyPreserveHost On` ist nötig, sonst lehnt die
Verwaltung Formulare ab. Auf den Schießstand-Servern wird als Adresse die
volle Adresse mit Unterverzeichnis eingetragen
(`https://gs.schuetzengau-massenhausen.de/befreeshoot`).

**Firewall:** Port 443 muss aus dem Internet erreichbar sein
(`sudo ufw allow 443/tcp`, falls ufw aktiv ist). Port 80 belegt die
Online-Anzeige nicht – certbot kann ihn weiter für die Erneuerung nutzen.

## 3. Aktualisieren

Neues Paket kopieren, entpacken, `sudo ./install.sh` – erkennt die bestehende
Installation, tauscht nur das Programm und startet den Dienst neu.
Konfiguration und Daten bleiben unverändert; Datenbankänderungen führt das
Programm beim Start selbst aus.

## Betrieb

```bash
sudo systemctl status schiessstand-online
sudo journalctl -u schiessstand-online -f
/opt/schiessstand-online/online -version
# weiteren Plattform-Administrator anlegen oder Passwort zurücksetzen:
sudo -u schiessstand-online /opt/schiessstand-online/online -config /etc/schiessstand/online.json -create-admin <name>
# Sicherung:
sudo -u postgres pg_dump -Fc schiessstand_online > online.dump
```

## Konfiguration (Referenz)

`online.json` – vom Installer angelegt; `online.example.json` zeigt die
Variante mit automatischem Let's Encrypt durch das Programm selbst.

| Schlüssel | Bedeutung |
|---|---|
| `postgres_dsn` | Datenbankverbindung |
| `listen_addr` | z. B. `:443`, hinter Proxy `127.0.0.1:8095` |
| `tls_cert`, `tls_key` | Zertifikatsdateien (PEM); `${CREDENTIALS_DIRECTORY}` wird ersetzt |
| `acme_domains`, `acme_email`, `acme_cache_dir` | alternativ: Zertifikat holt das Programm selbst (Port 80 nötig) |
| `behind_proxy` | `true`: HTTPS stellt ein vorgeschalteter Webserver bereit |
| `site_title` | Titel der Startseite |
| `base_path` | Unterverzeichnis, z. B. `/befreeshoot` (leer = ganze Domain) |
