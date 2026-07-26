#!/usr/bin/env bash
#
# Fetches Xray-core at the pinned commit into third_party/xray-core and applies the
# Beeline short-session-ID patch, then verifies it with the regression test.
#
# The SA05 desktop binary links this tree in-process through a `replace` directive
# in go.mod, so the patch must be reapplied on every fresh checkout.
#
# Usage: build/vendor-xray.sh [--force]
set -euo pipefail

XRAY_REPO="${XRAY_REPO:-https://github.com/XTLS/Xray-core.git}"
# Same revision the Android client ships: modern transports (Reality, XHTTP, Hysteria).
XRAY_COMMIT="${XRAY_COMMIT:-d2758a023cd7f4174a5a5fa4ff66e487d4342ba0}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEST="$ROOT/third_party/xray-core"

if [ "${1:-}" = "--force" ]; then
  rm -rf "$DEST"
fi

if [ -d "$DEST/.git" ]; then
  echo ">> $DEST already present; run with --force to re-fetch"
else
  echo ">> Cloning Xray-core at $XRAY_COMMIT"
  mkdir -p "$(dirname "$DEST")"
  git init -q "$DEST"
  git -C "$DEST" remote add origin "$XRAY_REPO"
  git -C "$DEST" fetch -q --depth 1 origin "$XRAY_COMMIT"
  git -C "$DEST" checkout -q FETCH_HEAD
fi

"$ROOT/build/patch-xray.sh" "$DEST" "$ROOT/build"

echo ">> Verifying patch"
( cd "$DEST" && go test ./transport/internet/splithttp/ -run TestNewBeelineSessionID -count=1 )

echo ">> Vendored: $DEST"
