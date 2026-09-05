---
name: sa05-xray
description: Work with SA05 subscription contracts and Xray runtime config (Beeline XHTTP padding, HTTP inbound, port handling, outbound mark, geo assets, traffic stats). Use when touching internal/core/subscription, internal/core/xrayconf, or internal/core/engine.
---

# SA05 Xray / Subscription

## Data flow

Subscription URL → `subscription.Client.Update` → cached `subscription.State`
(profiles with `Remarks` + raw `JSON`) → `engine.Start(profileJSON)` →
`xrayconf` runtime patches (stored profile never mutated) → Xray in-process.

Contracts are shared with the Android client — do not change the subscription
parsing or validation semantics unilaterally.

## Runtime patches (internal/core/xrayconf, applied in this order)

1. `ApplyBeelinePadding` — XHTTP session ID as 12-char Base64URL
   (`newBeelineSessionID`). Stock UUID form gets 403 from Beeline CDN.
2. `OverrideSocksPort` — only when an explicit port is requested.
3. `ApplyOutboundMark` — `netbypass.Mark` on core sockets so TUN does not
   loop core traffic into itself. Zero = leave provider value.
4. `EnableTrafficStats` — enables counters; traffic reads zero (not error)
   when stats are absent.
5. `EnsureHTTPInbound` — adds loopback HTTP inbound (default 10809) for the
   system proxy when the provider config has none; `ForceHTTPPort` rebinds it.
6. `Validate` — final gate before `core.New`.

## Port rules (do not "simplify")

- Xray binds with `SO_REUSEPORT`: a busy port would silently split traffic
  between two clients. `ensurePortFree` refuses instead; desktop sets
  `Engine.AutoPort = true` to rebind and show actual ports in UI.
- `Ephemeral` puts both inbounds on kernel-assigned ports (latency probes).
- MTProto port **1443 is fixed** — Telegram is configured once and survives
  transport changes. `telegramPort` override exists for tests only.
- Defaults: SOCKS 10808, HTTP 10809 (`engine.DefaultHTTPPort`).

## Geo assets

`xrayconf.UsesGeoAssets(profileJSON)` decides. Only then `assets.Ensure`
requires `geoip.dat`/`geosite.dat` in `SA05_ASSET_DIR` or
`$XDG_CACHE_HOME/sa05/assets`. Do not require them unconditionally.

## Tests that guard this

```bash
go test -count=1 ./internal/core/...
(cd third_party/xray-core && go test ./transport/internet/splithttp/ -run TestNewBeelineSessionID -count=1)
```

`internal/core/xrayconf/*_test.go` (incl. `ports_test.go`) cover the patch
pipeline — extend them when adding a new runtime mutation.
