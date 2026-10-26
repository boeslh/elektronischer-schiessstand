#!/usr/bin/env bash
# ============================================================================
# build-standpc.sh – uebersetzt den Stand-PC (../standpc) fuer Android und
# legt ihn als android/app/src/main/jniLibs/<abi>/libstandpc.so ab. Die App
# startet ihn im Standalone-Betrieb als eigenen Prozess (StandPcService).
#
#   arm64-v8a     reines Go (CGO aus)
#   armeabi-v7a   Go verlangt fuer android/arm den NDK-Linker
#
# Wird von build-apk.sh aufgerufen.
# ============================================================================
set -euo pipefail

BASE="$(cd "$(dirname "$0")" && pwd)"
SRC="$BASE/../standpc"
LIBS="$BASE/android/app/src/main/jniLibs"
export ANDROID_HOME="${ANDROID_HOME:-$HOME/Android/Sdk}"

mkdir -p "$LIBS/arm64-v8a" "$LIBS/armeabi-v7a"
cd "$SRC"

echo "Stand-PC fuer Android (arm64-v8a) ..."
CGO_ENABLED=0 GOOS=android GOARCH=arm64 \
  go build -trimpath -ldflags="-s -w" -o "$LIBS/arm64-v8a/libstandpc.so" .

NDK="$(ls -d "$ANDROID_HOME"/ndk/* 2>/dev/null | sort -V | tail -1 || true)"
CC="$NDK/toolchains/llvm/prebuilt/linux-x86_64/bin/armv7a-linux-androideabi26-clang"
if [[ -n "$NDK" && -x "$CC" ]]; then
  echo "Stand-PC fuer Android (armeabi-v7a) ..."
  CGO_ENABLED=1 CC="$CC" GOOS=android GOARCH=arm GOARM=7 \
    go build -trimpath -ldflags="-s -w" -o "$LIBS/armeabi-v7a/libstandpc.so" .
else
  echo "WARNUNG: kein Android-NDK gefunden - armeabi-v7a (32-Bit-Geraete) ohne eingebauten Stand-PC" >&2
  rm -f "$LIBS/armeabi-v7a/libstandpc.so"
fi
ls -la "$LIBS"/*/libstandpc.so
