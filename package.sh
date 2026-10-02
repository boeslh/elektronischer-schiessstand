#!/usr/bin/env bash
# ============================================================================
# package.sh – packt das Entwicklungssystem (server/, standpc/, preisanzeige/,
# wertmaschine/, install.sh, Demo-Datenbank) in ein einzelnes Tar-Archiv, das
# auf einem anderen Rechner mit install.sh installiert werden kann.
#
# Verwendung (auf DIESEM Entwicklungsrechner, kein sudo noetig):
#   ./package.sh [Zielverzeichnis] [--with-deps] [--with-app]
#
# Enthalten sind alle git-verfolgten Dateien (Arbeitsstand inkl. noch nicht
# committeter Aenderungen, aber OHNE Build-Artefakte/Secrets - siehe
# .gitignore) sowie zusaetzlich db-backups/demodb1.dump als Demo-Datenbestand
# und install.sh/package.sh selbst (auch wenn diese noch nicht committet
# sind). Es wird NICHT auf einen sauberen Arbeitsstand bestanden - das Archiv
# spiegelt exakt das, was gerade auf der Platte liegt.
#
# --with-deps (optional, NICHT der Standard): buendelt zusaetzlich alle
# Abhaengigkeiten, die install.sh sonst aus dem Internet laedt (Go-Toolchain,
# Go-Modul-Cache aller vier Komponenten, apt-Pakete inkl. deren rekursiver
# Abhaengigkeiten fuer sudo/curl/ca-certificates/openssl/postgresql-client/
# postgresql) - damit kann install.sh auf einem Zielrechner OHNE Internet-
# zugang laufen. Deutlich groesseres Archiv (Go-Toolchain + Modul-Cache allein
# schon typischerweise 250-350 MB) und deshalb bewusst optional: WIRD NUR AUF
# EXPLIZITE ANFRAGE GEBAUT, nicht bei jedem normalen Deployment-Package.
# Voraussetzung: dieser Rechner braucht selbst Internetzugang (zum Laden der
# apt-Pakete) sowie eine bereits installierte Go-Toolchain (/usr/local/go)
# und einen bereits befuellten Go-Modul-Cache (durch die uebliche Entwicklung
# hier ohnehin vorhanden). Die apt-Pakete werden fuer DIESEN Rechner (Debian-
# Version/Architektur) geladen - auf einem Zielrechner mit abweichender
# Debian-Version/Architektur koennen sie inkompatibel sein; install.sh faellt
# in dem Fall automatisch auf den normalen Online-Weg zurueck (siehe dortigen
# apt_ensure()-Kommentar), was ohne Internet dann fehlschlaegt.
#
# --with-app (optional, NICHT der Standard): nimmt zusaetzlich den Quelltext
# der Android-App standanzeige/ mit. Fuer die Installation der Anlage wird
# sie nicht gebraucht (die App wird als APK direkt auf die Geraete gespielt)
# und bleibt deshalb standardmaessig draussen.
# ============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

OUT_DIR="${SCRIPT_DIR}"
WITH_DEPS=false
WITH_APP=false
for arg in "$@"; do
  case "${arg}" in
    --with-deps) WITH_DEPS=true ;;
    --with-app)  WITH_APP=true ;;
    *)           OUT_DIR="${arg}" ;;
  esac
done

STAMP="$(date +%Y%m%d-%H%M)"
ARCHIVE_NAME="schiessstand-deploy-${STAMP}.tar.gz"
ARCHIVE_PATH="${OUT_DIR}/${ARCHIVE_NAME}"
ARCHIVE_BASE="schiessstand-deploy-${STAMP}"

cd "${SCRIPT_DIR}"

if [[ ! -d .git ]]; then
  echo "FEHLER: ${SCRIPT_DIR} ist kein Git-Repository - package.sh verlaesst" >&2
  echo "        sich auf 'git ls-files', um Build-Artefakte und Secrets" >&2
  echo "        (siehe .gitignore) automatisch auszuschliessen." >&2
  exit 1
fi

echo "==> Sammle Dateiliste (git-verfolgte + neue, noch nicht committete Dateien)..."
FILELIST="$(mktemp)"
OFFLINE_STAGE=""
cleanup() {
  rm -f "${FILELIST}"
  [[ -n "${OFFLINE_STAGE}" ]] && rm -rf "${OFFLINE_STAGE}"
}
trap cleanup EXIT

# --cached: bereits getrackte Dateien (unabhaengig davon, ob committet).
# --others --exclude-standard: neue Dateien, die "git add" noch nicht gesehen
# hat, aber NICHT von .gitignore ausgeschlossen sind - ohne diese zweite
# Gruppe wuerden frisch angelegte, noch nicht committete Dateien (z.B. eine
# neue .go-Datei) im Archiv fehlen, obwohl sie auf der Platte liegen.
git ls-files -z --cached --others --exclude-standard > "${FILELIST}"

# Android-App (standanzeige/) nur mit --with-app; die Dokumentation (docs/,
# u.a. ~6 MB Screenshots) wird fuer die Installation nicht gebraucht
if ! $WITH_APP; then
  grep -zv '^standanzeige/' "${FILELIST}" > "${FILELIST}.tmp" || true
  mv "${FILELIST}.tmp" "${FILELIST}"
fi
grep -zv '^docs/' "${FILELIST}" > "${FILELIST}.tmp" || true
mv "${FILELIST}.tmp" "${FILELIST}"

# install.sh/package.sh selbst mitnehmen, auch falls (noch) nicht committet -
# ohne sie waere ein frisch gepacktes Archiv nicht selbst installierbar.
for extra in install.sh package.sh; do
  if [[ -f "${extra}" ]] && ! grep -zqx "${extra}" "${FILELIST}"; then
    printf '%s\0' "${extra}" >> "${FILELIST}"
  fi
done

# Demo-Datenbank ist bewusst NICHT in Git (siehe db-backups/, keine
# Nutzdaten im Repo) - fuer die Installation aber explizit gewuenscht.
DEMO_DUMP="db-backups/demodb1.dump"
if [[ -f "${DEMO_DUMP}" ]]; then
  printf '%s\0' "${DEMO_DUMP}" >> "${FILELIST}"
  echo "    + ${DEMO_DUMP} ($(du -h "${DEMO_DUMP}" | cut -f1))"
else
  echo "    WARNUNG: ${DEMO_DUMP} nicht gefunden - Archiv wird ohne Demo-DB erstellt."
fi

COUNT="$(tr -cd '\0' < "${FILELIST}" | wc -c)"
echo "    ${COUNT} Dateien werden gepackt."

# ── Offline-Abhaengigkeiten (nur mit --with-deps) ───────────────────────────
TAR_EXTRA_ARGS=()
if $WITH_DEPS; then
  echo "==> --with-deps: sammle Offline-Abhaengigkeiten..."
  OFFLINE_STAGE="$(mktemp -d)"
  DEPS_DIR="${OFFLINE_STAGE}/offline-deps"
  mkdir -p "${DEPS_DIR}/apt"

  # 1. Go-Toolchain (das bereits auf DIESEM Rechner installierte /usr/local/go,
  #    statt es fuer das Buendel erneut von go.dev herunterzuladen).
  if [[ -d /usr/local/go ]]; then
    echo "    Go-Toolchain packen (/usr/local/go)..."
    tar -C /usr/local -czf "${DEPS_DIR}/go-toolchain.tar.gz" go
    echo "        OK ($(du -h "${DEPS_DIR}/go-toolchain.tar.gz" | cut -f1))"
  else
    echo "    WARNUNG: /usr/local/go nicht gefunden - Go-Toolchain wird NICHT gebuendelt."
  fi

  # 2. Go-Modul-Cache (alle vier go.mod-Module teilen sich einen GOPATH auf
  #    diesem Rechner) - macht "go build" auf dem Zielrechner offline moeglich,
  #    ohne dass server/standpc/wertmaschine/preisanzeige einzeln vendort
  #    werden muessen.
  GOPATH_DIR="$(command -v go &>/dev/null && go env GOPATH || echo "${HOME}/go")"
  if [[ -d "${GOPATH_DIR}/pkg/mod" ]]; then
    echo "    Go-Modul-Cache packen (${GOPATH_DIR}/pkg/mod, $(du -sh "${GOPATH_DIR}/pkg/mod" | cut -f1))..."
    tar -C "$(dirname "${GOPATH_DIR}")" -czf "${DEPS_DIR}/go-mod-cache.tar.gz" "$(basename "${GOPATH_DIR}")"
    echo "        OK ($(du -h "${DEPS_DIR}/go-mod-cache.tar.gz" | cut -f1))"
  else
    echo "    WARNUNG: Go-Modul-Cache (${GOPATH_DIR}/pkg/mod) nicht gefunden - wird NICHT gebuendelt."
    echo "             ('go build' fuer server/standpc/wertmaschine/preisanzeige einmal"
    echo "             lokal ausfuehren, damit der Cache befuellt wird, dann erneut packen.)"
  fi

  # 3. apt-Pakete: rekursive Abhaengigkeiten der von install.sh benoetigten
  #    Basispakete ermitteln (apt-cache depends --recurse) und als .deb laden
  #    (apt-get download, kein root noetig) - deckt alle vier Komponenten ab,
  #    unabhaengig davon, welche der Nutzer auf dem Zielrechner spaeter waehlt.
  APT_BASE_PKGS="sudo curl ca-certificates openssl postgresql-client postgresql"
  echo "    Ermittle apt-Abhaengigkeiten (${APT_BASE_PKGS})..."
  APT_DEPS="$(apt-cache depends --recurse --no-recommends --no-suggests \
                --no-conflicts --no-breaks --no-replaces --no-enhances \
                ${APT_BASE_PKGS} 2>/dev/null | grep -E '^\w' | sort -u)"
  if [[ -n "${APT_DEPS}" ]]; then
    echo "    Lade $(wc -w <<< "${APT_DEPS}") .deb-Pakete (apt-get download)..."
    ( cd "${DEPS_DIR}/apt" && apt-get download ${APT_DEPS} >/dev/null 2>&1 ) \
      || echo "    WARNUNG: einzelne Pakete konnten nicht geladen werden (siehe apt-get-Ausgabe oben)."
    N_DEB="$(find "${DEPS_DIR}/apt" -name '*.deb' | wc -l)"
    echo "        OK: ${N_DEB} .deb-Dateien ($(du -sh "${DEPS_DIR}/apt" | cut -f1))"
  else
    echo "    WARNUNG: apt-cache depends lieferte keine Pakete - apt-Bundle wird leer sein."
  fi
  rmdir "${DEPS_DIR}/apt" 2>/dev/null || true  # falls download komplett leer blieb

  TAR_EXTRA_ARGS=(-C "${OFFLINE_STAGE}" offline-deps)
fi

echo "==> Erstelle ${ARCHIVE_PATH}..."
tar --null -T "${FILELIST}" \
  "${TAR_EXTRA_ARGS[@]}" \
  --transform "s,^,${ARCHIVE_BASE}/," -czf "${ARCHIVE_PATH}"

echo ""
echo "==> Fertig: ${ARCHIVE_PATH} ($(du -h "${ARCHIVE_PATH}" | cut -f1))"
echo ""
echo "    Auf dem Zielsystem entpacken und installieren:"
echo "      tar -xzf ${ARCHIVE_NAME}"
echo "      cd ${ARCHIVE_BASE}"
echo "      sudo ./install.sh"
$WITH_DEPS && echo "    (enthaelt offline-deps/ - install.sh nutzt das automatisch, kein Internet noetig)"
