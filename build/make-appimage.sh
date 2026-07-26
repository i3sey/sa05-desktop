#!/usr/bin/env bash
#
# Packs the built client into an AppImage for distributions without a native package.
#
# Only the GUI goes inside: the helper is a system service and must be installed by the
# distribution's own mechanism, so the AppImage ships everything that works unprivileged
# (core, system proxy, Telegram) and says so when TUN is requested.
#
# Usage: build/make-appimage.sh [version]
set -euo pipefail

VERSION="${1:-dev}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/build/out"
WORK="$ROOT/build/appimage"
APPDIR="$WORK/SA05.AppDir"

TOOL="${APPIMAGETOOL:-}"
if [ -z "$TOOL" ]; then
  TOOL="$WORK/appimagetool"
  if [ ! -x "$TOOL" ]; then
    mkdir -p "$WORK"
    echo ">> Скачиваю appimagetool"
    curl -fsSL -o "$TOOL" \
      https://github.com/AppImage/appimagetool/releases/download/continuous/appimagetool-x86_64.AppImage
    chmod +x "$TOOL"
  fi
fi

[ -x "$OUT/sa05" ] || { echo "Нет $OUT/sa05 — сначала build/build-linux.sh" >&2; exit 1; }

rm -rf "$APPDIR"
install -d "$APPDIR/usr/bin" "$APPDIR/usr/share/sa05" "$APPDIR/usr/share/applications"

install -m 0755 "$OUT/sa05" "$APPDIR/usr/bin/sa05"
install -m 0755 "$OUT/sa05ctl" "$APPDIR/usr/bin/sa05ctl"

if [ -f "$OUT/assets/geoip.dat" ]; then
  install -m 0644 "$OUT/assets/geoip.dat" "$OUT/assets/geosite.dat" "$APPDIR/usr/share/sa05/"
else
  echo ">> Гео-базы не найдены: профили с geosite: не запустятся (build/fetch-geoassets.sh)" >&2
fi

install -m 0644 "$ROOT/packaging/sa05.desktop" "$APPDIR/usr/share/applications/sa05.desktop"
install -m 0644 "$ROOT/packaging/sa05.desktop" "$APPDIR/sa05.desktop"
install -m 0644 "$ROOT/packaging/icons/sa05-256.png" "$APPDIR/sa05.png"
for size in 16 24 32 48 64 128 256; do
  install -D -m 0644 "$ROOT/packaging/icons/sa05-$size.png" \
    "$APPDIR/usr/share/icons/hicolor/${size}x${size}/apps/sa05.png"
done

cat > "$APPDIR/AppRun" <<'RUN'
#!/usr/bin/env bash
# The geo databases travel inside the image; SA05_ASSET_SOURCE is where the client looks
# for installed copies before falling back to its per-user cache.
HERE="$(dirname "$(readlink -f "$0")")"
export SA05_ASSET_SOURCE="${SA05_ASSET_SOURCE:-$HERE/usr/share/sa05}"
exec "$HERE/usr/bin/sa05" "$@"
RUN
chmod +x "$APPDIR/AppRun"

echo ">> Собираю AppImage"
ARCH=x86_64 "$TOOL" "$APPDIR" "$WORK/SA05-$VERSION-x86_64.AppImage"

echo ">> Готово"
ls -lh "$WORK"/*.AppImage
