#!/usr/bin/env bash
#
# Builds the frontend bundle and both binaries into build/out.
#
# Wails needs its own build tags; webkit2_41 selects the WebKitGTK 4.1 API, which is what
# current distributions ship.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/build/out"
TAGS="desktop,production,webkit2_41"

cd "$ROOT"

if [ ! -d third_party/xray-core ]; then
  echo ">> Vendoring Xray-core"
  build/vendor-xray.sh
fi

echo ">> Building frontend"
(cd frontend && npm ci --silent && npm run build)

echo ">> Building binaries"
mkdir -p "$OUT"
go build -tags "$TAGS" -ldflags "-w -s" -o "$OUT/sa05" ./cmd/sa05
go build -ldflags "-w -s" -o "$OUT/sa05ctl" ./cmd/sa05ctl

echo ">> Done"
ls -lh "$OUT"
