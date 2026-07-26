#!/usr/bin/env bash
#
# Downloads geoip.dat / geosite.dat and verifies them against the checksums published
# next to the release assets.
#
# The files are not vendored: they are ~30 MB, change weekly, and every packaging format
# ships them as data files. This script is what CI and the packaging scripts call.
#
# Usage: build/fetch-geoassets.sh [target-dir]
set -euo pipefail

# Pin the release, not "latest": a build must be reproducible, and a surprise rule change
# can break a working profile.
GEOIP_RELEASE="${GEOIP_RELEASE:-202607171233}"
GEOSITE_RELEASE="${GEOSITE_RELEASE:-20260726062913}"
GEOIP_URL="${GEOIP_URL:-https://github.com/v2fly/geoip/releases/download/$GEOIP_RELEASE/geoip.dat}"
GEOSITE_URL="${GEOSITE_URL:-https://github.com/v2fly/domain-list-community/releases/download/$GEOSITE_RELEASE/dlc.dat}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TARGET="${1:-$ROOT/build/out/assets}"

mkdir -p "$TARGET"

fetch() {
  local url="$1"
  local name="$2"
  local destination="$TARGET/$name"
  local work
  work="$(mktemp -d)"
  trap 'rm -rf "$work"' RETURN

  echo ">> $name <- $url"
  curl -fsSL --retry 3 -o "$work/$name" "$url"
  # Every v2fly release publishes a .sha256sum next to the asset.
  if curl -fsSL --retry 3 -o "$work/$name.sha256sum" "$url.sha256sum"; then
    local expected actual
    expected="$(awk '{print $1}' "$work/$name.sha256sum")"
    actual="$(sha256sum "$work/$name" | awk '{print $1}')"
    if [ "$expected" != "$actual" ]; then
      echo "SHA-256 mismatch for $name: expected $expected, got $actual" >&2
      exit 1
    fi
    echo "   sha256 ok: $actual"
  else
    echo "   WARNING: no published checksum for $name" >&2
  fi
  mv "$work/$name" "$destination"
}

fetch "$GEOIP_URL" geoip.dat
fetch "$GEOSITE_URL" geosite.dat

echo ">> Installed into $TARGET"
ls -lh "$TARGET"
echo
echo "Run the client with SA05_ASSET_SOURCE=$TARGET, or install these files into"
echo "/usr/share/sa05 when packaging."
