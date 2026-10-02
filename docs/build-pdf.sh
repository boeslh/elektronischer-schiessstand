#!/usr/bin/env bash
# ============================================================================
# build-pdf.sh – erzeugt PDF-Dateien aus der Dokumentation (docs/*.md).
#
#   docs/build-pdf.sh            Gesamt-PDF:  docs/pdf/Schiessstand-Funktionsuebersicht.pdf
#   docs/build-pdf.sh --einzeln  zusaetzlich je Kapitel eine PDF (docs/pdf/NN-*.pdf)
#
# Beim ersten Aufruf wird docs/.venv mit Python-Markdown und einem Chromium
# zum Drucken angelegt (ca. 300 MB, Internet noetig; Paket python3-venv).
# ============================================================================
set -euo pipefail
DOCS="$(cd "$(dirname "$0")" && pwd)"
"$DOCS/tools/venv.sh"
"$DOCS/.venv/bin/python" "$DOCS/tools/build_pdf.py" "$@"
