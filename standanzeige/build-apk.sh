#!/usr/bin/env bash
# ============================================================================
# build-apk.sh – baut die Standanzeige-App (Release-APK) und legt sie mit
# Datum/Uhrzeit im Namen unter standanzeige/Versionen/ ab.
#
#   ./build-apk.sh            eine APK fuer alle Prozessorarten
#   ./build-apk.sh --split    je Prozessorart eine kleinere APK
#
# Signierung mit ~/.android/debug.keystore (derselbe Schluessel wie bei der
# App "My Shootings") - Updates lassen sich so ueber eine installierte
# Version installieren. Voraussetzung: Shooting-App/setup-flutter-android.sh.
# ============================================================================
set -euo pipefail

BASE="$(cd "$(dirname "$0")" && pwd)"
OUT="$BASE/Versionen"
export ANDROID_HOME="${ANDROID_HOME:-$HOME/Android/Sdk}"
export PATH="$HOME/development/flutter/bin:$ANDROID_HOME/cmdline-tools/latest/bin:$ANDROID_HOME/platform-tools:$PATH"

command -v flutter >/dev/null || { echo "flutter nicht gefunden - zuerst Shooting-App/setup-flutter-android.sh ausfuehren" >&2; exit 1; }

cd "$BASE"
"$BASE/build-standpc.sh"   # eingebauter Stand-PC (Standalone-Betrieb)
flutter pub get

stamp="$(date +%Y%m%d-%H%M)"
version="$(awk '/^version:/ {print $2}' pubspec.yaml)"
mkdir -p "$OUT"

if [[ "${1:-}" == "--split" ]]; then
  flutter build apk --release --split-per-abi
  for f in build/app/outputs/flutter-apk/app-*-release.apk; do
    abi="$(basename "$f" | sed 's/^app-\(.*\)-release\.apk$/\1/')"
    cp "$f" "$OUT/Standanzeige_${version%%+*}_${stamp}_${abi}.apk"
  done
else
  flutter build apk --release
  cp build/app/outputs/flutter-apk/app-release.apk "$OUT/Standanzeige_${version%%+*}_${stamp}.apk"
fi

echo
echo "APK(s) abgelegt in $OUT:"
ls -lh "$OUT"/*"${stamp}"*.apk
