#!/usr/bin/env bash
#
# Fetches wintun.dll for the Windows helper.
#
# The Wintun driver is not in the Go module: wireguard/tun loads wintun.dll at
# runtime, looking next to the executable first. Without it the helper builds
# fine but TunUp fails with "wintun.dll not found". This script stages the DLL
# into build/out so release zips carry it next to sa05-helper.exe.
#
# Usage: build/fetch-wintun.sh [out-dir]   (default: build/out)
set -euo pipefail

VERSION="${WINTUN_VERSION:-0.14.1}"
# Pinned to the upstream build; amneziawg-windows-client verifies the same digest.
SHA256="${WINTUN_SHA256:-07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51}"
OUT="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/out}"

base="https://www.wintun.net/builds"
archive="$OUT/wintun-$VERSION.zip"

mkdir -p "$OUT"
if [ ! -f "$archive" ]; then
  echo ">> Downloading wintun $VERSION"
  curl -fL --retry 3 -o "$archive" "$base/wintun-$VERSION.zip"
fi

echo "$SHA256  $archive" | sha256sum -c -

# The zip carries arm64/x86/amd64; the desktop client ships amd64 only.
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
unzip -q -o "$archive" "wintun/bin/amd64/wintun.dll" -d "$tmp"
install -m 0644 "$tmp/wintun/bin/amd64/wintun.dll" "$OUT/wintun.dll"

echo ">> Staged $OUT/wintun.dll"
ls -lh "$OUT/wintun.dll"
