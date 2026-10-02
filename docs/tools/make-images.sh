#!/usr/bin/env bash
# ============================================================================
# make-images.sh – erzeugt alle Screenshots der Dokumentation neu
# (docs/bilder/). Voraussetzungen: laufende Dienste auf diesem Rechner
# (Server :8090, Stand-PC Stand 4 :9004, Preisanzeige :8091), Flutter-SDK
# (fuer die App-Bilder), sudo (Datenbankzugang aus /etc/schiessstand).
#
# Personenbezogene Daten werden dabei pseudonymisiert (siehe anon.py).
# ============================================================================
set -euo pipefail
DOCS="$(cd "$(dirname "$0")/.." && pwd)"
REPO="$(dirname "$DOCS")"
"$DOCS/tools/venv.sh"

echo "==> Web-Oberflaechen"
"$DOCS/.venv/bin/python" "$DOCS/tools/screenshots.py"

echo "==> Beispiel-Einzelergebnis (PDF, erfundene Daten)"
TMP="$(mktemp -d)"
( cd "$REPO/server" && DOKU_PDF_OUT="$TMP/beispiel.pdf" /usr/local/go/bin/go test -run TestDokuBeispielPDF -count=1 . >/dev/null )
pdftoppm -r 80 -png -singlefile "$TMP/beispiel.pdf" "$DOCS/bilder/pdf-einzelergebnis"
rm -rf "$TMP"

echo "==> Android-App Standanzeige"
( cd "$REPO/standanzeige" && DOKU=1 "$HOME/development/flutter/bin/flutter" test test/doku_screenshots_test.dart --update-goldens >/dev/null )

echo "Fertig: $(ls "$DOCS/bilder" | wc -l) Bilder in $DOCS/bilder"
