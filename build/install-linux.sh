#!/usr/bin/env bash
#
# Installs the SA05 desktop client and its privileged helper.
#
# The GUI is a normal user program; only the helper is installed as a system service, and
# it is the only part that ever runs as root.
#
# Usage: sudo build/install-linux.sh [--uninstall]
set -euo pipefail

PREFIX="${PREFIX:-/usr/local}"
LIBDIR="$PREFIX/lib/sa05"
BINDIR="$PREFIX/bin"
SHAREDIR="$PREFIX/share/sa05"
UNIT=/etc/systemd/system/sa05-helper.service

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Two layouts are supported: a source checkout (binaries in build/out) and an unpacked
# release archive (binaries next to this script).
if [ -x "$HERE/sa05-helper" ]; then
  ROOT="$HERE"
  OUT="$HERE"
else
  ROOT="$(cd "$HERE/.." && pwd)"
  OUT="$ROOT/build/out"
fi

if [ "$(id -u)" != "0" ]; then
  echo "Запустите с правами root: sudo $0" >&2
  exit 1
fi

uninstall() {
  echo ">> Останавливаю службу"
  systemctl disable --now sa05-helper.service 2>/dev/null || true
  rm -f "$UNIT" "$BINDIR/sa05" "$BINDIR/sa05ctl"
  rm -rf "$LIBDIR"
  systemctl daemon-reload
  echo ">> Удалено. Каталог $SHAREDIR с гео-базами оставлен."
}

if [ "${1:-}" = "--uninstall" ]; then
  uninstall
  exit 0
fi

for binary in sa05 sa05ctl sa05-helper; do
  if [ ! -x "$OUT/$binary" ]; then
    echo "Не найден $OUT/$binary — сначала выполните build/build-linux.sh" >&2
    exit 1
  fi
done

echo ">> Устанавливаю бинарники"
install -d "$LIBDIR" "$BINDIR" "$SHAREDIR"
install -m 0755 "$OUT/sa05-helper" "$LIBDIR/sa05-helper"
install -m 0755 "$OUT/sa05" "$BINDIR/sa05"
install -m 0755 "$OUT/sa05ctl" "$BINDIR/sa05ctl"

if [ -f "$OUT/assets/geoip.dat" ]; then
  echo ">> Устанавливаю гео-базы"
  install -m 0644 "$OUT/assets/geoip.dat" "$OUT/assets/geosite.dat" "$SHAREDIR/"
else
  echo ">> Гео-базы не найдены: запустите build/fetch-geoassets.sh (нужны профилям с geosite:)"
fi

echo ">> Устанавливаю службу"
install -m 0644 "$ROOT/packaging/systemd/sa05-helper.service" "$UNIT"
systemctl daemon-reload
systemctl enable --now sa05-helper.service

echo ">> Готово"
systemctl --no-pager --lines=3 status sa05-helper.service || true
echo
echo "Запуск клиента:      sa05"
echo "Удаление:            sudo $0 --uninstall"
