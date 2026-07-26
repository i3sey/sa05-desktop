#!/usr/bin/env bash
#
# Extracts the tg-ws-proxy source (GPLv3) into internal/tgws/upstream and applies the
# desktop library patch (drop cgo exports, expose a Go API, injectable dialer).
#
# The upstream archive is the same one the Android client ships under
# third_party/tg-ws-proxy-android/. Point TGWS_ARCHIVE at another copy if needed.
#
# Usage: build/vendor-tgws.sh
set -euo pipefail

TGWS_VERSION="${TGWS_VERSION:-1.2.0}"
TGWS_ARCHIVE="${TGWS_ARCHIVE:-/home/sa05/AndroidStudioProjects/sa05/third_party/tg-ws-proxy-android/tg-ws-proxy-android-$TGWS_VERSION.tar.gz}"
TGWS_SHA256="${TGWS_SHA256:-328409ea4dfcbc50eb3b9dbc24dec9535442a69d926b91e8fb2578fb7f71abba}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEST="$ROOT/internal/tgws/upstream"

[ -f "$TGWS_ARCHIVE" ] || { echo "Archive not found: $TGWS_ARCHIVE" >&2; exit 1; }

actual="$(sha256sum "$TGWS_ARCHIVE" | awk '{print $1}')"
if [ "$actual" != "$TGWS_SHA256" ]; then
  echo "SHA-256 mismatch for $TGWS_ARCHIVE" >&2
  echo "  expected $TGWS_SHA256" >&2
  echo "  actual   $actual" >&2
  exit 1
fi

mkdir -p "$DEST"
tar -xzOf "$TGWS_ARCHIVE" "tg-ws-proxy-android-$TGWS_VERSION/tg-ws-proxy.go" > "$DEST/tg-ws-proxy.go.orig"
tar -xzOf "$TGWS_ARCHIVE" "tg-ws-proxy-android-$TGWS_VERSION/LICENSE" > "$DEST/LICENSE"

echo ">> Generating the library form"
python3 "$ROOT/build/patches/tgws-library.py" \
  "$DEST/tg-ws-proxy.go.orig" "$DEST/proxy.go"
gofmt -w "$DEST/proxy.go"

echo ">> Vendored tg-ws-proxy $TGWS_VERSION into $DEST"
