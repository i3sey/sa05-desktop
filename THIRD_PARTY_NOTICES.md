# Third-party components

## Xray-core

- Project: https://github.com/XTLS/Xray-core
- Revision: `d2758a023cd7f4174a5a5fa4ff66e487d4342ba0`
- Vendored by: `build/vendor-xray.sh` into `third_party/xray-core`, linked in-process
- Local modification: `build/patch-xray.sh` shortens the XHTTP session ID to a 12-char
  Base64URL string (`newBeelineSessionID`) because Beeline CDN answers HTTP 403 to the
  stock 36-char UUID. The same patch ships in the SA05 Android client.
- License: MPL-2.0

## tg-ws-proxy (Telegram WS Proxy Android)

- Version: 1.2.0
- Source archive SHA-256:
  `328409ea4dfcbc50eb3b9dbc24dec9535442a69d926b91e8fb2578fb7f71abba`
- Vendored by: `build/vendor-tgws.sh` into `internal/tgws/upstream`
- Local modification: the cgo `//export` wrappers and `main()` are replaced by a Go
  library API (`tgws.Start`), and every upstream dial goes through an injectable dialer
  so the proxy's own sockets bypass the SA05 TUN.
- License: GPL-3.0

Because tg-ws-proxy is GPLv3 and is linked into the SA05 binary, the whole client is
distributed under GPLv3. See `LICENSE`.

## Go dependencies

Everything else is a normal Go module dependency; see `go.mod` / `go.sum` for exact
versions and their licenses.
