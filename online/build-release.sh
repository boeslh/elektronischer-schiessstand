#!/usr/bin/env bash
# ============================================================================
# build-release.sh – baut die Online-Anzeige als eigenstaendiges Programm
# (statisch, ohne Abhaengigkeiten) und packt es mit dem Installer zu je
# einem Archiv fuer Linux amd64 und arm64:
#
#   online/dist/schiessstand-online-<version>-linux-<arch>.tar.gz
#
# Auf dem Internet-Server wird damit KEIN Go gebraucht: Archiv kopieren,
# entpacken, sudo ./install.sh (siehe online/README.md).
#
#   ./build-release.sh [version]     (Vorgabe: Datum + git-Stand)
# ============================================================================
set -euo pipefail
cd "$(dirname "$0")"
GO="${GO:-/usr/local/go/bin/go}"
VERSION="${1:-$(date +%Y%m%d)-$(git rev-parse --short HEAD 2>/dev/null || echo lokal)}"
OUT=dist
mkdir -p "$OUT"
for ARCH in amd64 arm64; do
  NAME="schiessstand-online-$VERSION-linux-$ARCH"
  STAGE="$(mktemp -d)"
  mkdir "$STAGE/$NAME"
  CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" "$GO" build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION" -o "$STAGE/$NAME/online" .
  install -m 755 deploy/install.sh "$STAGE/$NAME/install.sh"
  install -m 644 README.md "$STAGE/$NAME/README.md"
  install -m 644 online.example.json "$STAGE/$NAME/online.example.json"
  tar -C "$STAGE" -czf "$OUT/$NAME.tar.gz" "$NAME"
  rm -r "${STAGE:?}"
  echo "  $OUT/$NAME.tar.gz ($(du -h "$OUT/$NAME.tar.gz" | cut -f1))"
done
