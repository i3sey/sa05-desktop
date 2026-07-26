#!/usr/bin/env bash
# Applies the Beeline short-session-ID patch to a checked-out Xray-core tree,
# tolerant of file path / line moves across versions. Locates the XHTTP dialer by
# content (the UUID session-ID generation), not by a fixed path.
#
# Ported verbatim from the Android client's ci/patch-xray.sh so both clients speak
# the same XHTTP session-ID format to Beeline CDN.
#
# Usage: build/patch-xray.sh <xray-src-dir> <patch-dir>
set -euo pipefail

XRAY_DIR="$1"
PATCH_DIR="$2"
cd "$XRAY_DIR"

if grep -rq 'newBeelineSessionID' transport/internet; then
  echo "Already patched"
  exit 0
fi

# Find the file that builds the XHTTP session ID from a UUID.
f="$(grep -rl 'sessionIdUuid := uuid.New()' transport/internet | head -1)"
test -n "$f"
echo "Patching dialer: $f"

# 1) import encoding/base64 (after the first "context" import) if missing.
grep -q '"encoding/base64"' "$f" || \
  sed -i '0,/^\t"context"$/s##\t"context"\n\t"encoding/base64"#' "$f"

# 2) replace the 36-char UUID string with the short helper call.
perl -0pi -e 's/sessionIdUuid := uuid\.New\(\)\s*\n\s*sessionId = sessionIdUuid\.String\(\)/sessionId = newBeelineSessionID()/' "$f"
grep -q 'sessionId = newBeelineSessionID()' "$f"

# 3) append the helper (9 random bytes -> 12-char Base64URL).
cat >> "$f" <<'GO'

// newBeelineSessionID generates the XHTTP session ID used in the request URL.
// Stock Xray uses the 36-char UUID string, which Beeline CDN rejects with 403.
// Encoding 9 random bytes as Base64URL yields exactly 12 chars from the
// [A-Za-z0-9_-] set, which Beeline passes through. The server treats the
// session ID as opaque, so no server-side change is required.
func newBeelineSessionID() string {
	u := uuid.New()
	return base64.RawURLEncoding.EncodeToString(u[:9])
}
GO

# 4) drop the regression test next to the dialer, matching its package name.
pkg="$(sed -n 's/^package //p' "$f" | head -1)"
dst="$(dirname "$f")/dialer_beeline_test.go"
sed "s/^package .*/package $pkg/" "$PATCH_DIR/patches/dialer_beeline_test.go.in" > "$dst"

gofmt -w "$f" "$dst"
echo "Patched package '$pkg'"
