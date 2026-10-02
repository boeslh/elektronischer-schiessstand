#!/usr/bin/env bash
# Legt docs/.venv mit Playwright (Chromium), segno und markdown an (einmalig,
# ca. 300 MB inkl. Browser). Benoetigt python3-venv.
set -euo pipefail
DOCS="$(cd "$(dirname "$0")/.." && pwd)"
if [[ ! -x "$DOCS/.venv/bin/python" ]]; then
  python3 -m venv "$DOCS/.venv"
fi
if ! "$DOCS/.venv/bin/python" -c "import playwright, segno, markdown" 2>/dev/null; then
  "$DOCS/.venv/bin/pip" install -q playwright segno markdown
fi
"$DOCS/.venv/bin/python" -m playwright install chromium >/dev/null
