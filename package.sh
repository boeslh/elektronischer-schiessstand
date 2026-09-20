#!/usr/bin/env bash
# ============================================================================
# package.sh – packt das Entwicklungssystem (server/, standpc/, preisanzeige/,
# wertmaschine/, install.sh, Demo-Datenbank) in ein einzelnes Tar-Archiv, das
# auf einem anderen Rechner mit install.sh installiert werden kann.
#
# Verwendung (auf DIESEM Entwicklungsrechner, kein sudo noetig):
#   ./package.sh [Zielverzeichnis]
#
# Enthalten sind alle git-verfolgten Dateien (Arbeitsstand inkl. noch nicht
# committeter Aenderungen, aber OHNE Build-Artefakte/Secrets - siehe
# .gitignore) sowie zusaetzlich db-backups/demodb1.dump als Demo-Datenbestand
# und install.sh/package.sh selbst (auch wenn diese noch nicht committet
# sind). Es wird NICHT auf einen sauberen Arbeitsstand bestanden - das Archiv
# spiegelt exakt das, was gerade auf der Platte liegt.
# ============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT_DIR="${1:-${SCRIPT_DIR}}"
STAMP="$(date +%Y%m%d-%H%M)"
ARCHIVE_NAME="schiessstand-deploy-${STAMP}.tar.gz"
ARCHIVE_PATH="${OUT_DIR}/${ARCHIVE_NAME}"

cd "${SCRIPT_DIR}"

if [[ ! -d .git ]]; then
  echo "FEHLER: ${SCRIPT_DIR} ist kein Git-Repository - package.sh verlaesst" >&2
  echo "        sich auf 'git ls-files', um Build-Artefakte und Secrets" >&2
  echo "        (siehe .gitignore) automatisch auszuschliessen." >&2
  exit 1
fi

echo "==> Sammle Dateiliste (git-verfolgte Dateien, aktueller Arbeitsstand)..."
FILELIST="$(mktemp)"
trap 'rm -f "${FILELIST}"' EXIT

git ls-files -z > "${FILELIST}"

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

ARCHIVE_BASE="schiessstand-deploy-${STAMP}"
echo "==> Erstelle ${ARCHIVE_PATH}..."
tar --null -T "${FILELIST}" --transform "s,^,${ARCHIVE_BASE}/," -czf "${ARCHIVE_PATH}"

echo ""
echo "==> Fertig: ${ARCHIVE_PATH} ($(du -h "${ARCHIVE_PATH}" | cut -f1))"
echo ""
echo "    Auf dem Zielsystem entpacken und installieren:"
echo "      tar -xzf ${ARCHIVE_NAME}"
echo "      cd ${ARCHIVE_BASE}"
echo "      sudo ./install.sh"
