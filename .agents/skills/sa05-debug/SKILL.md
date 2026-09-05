---
name: sa05-debug
description: Debug SA05 core without the GUI via sa05ctl (subscription, profiles, tunnel up/check/ping, Telegram proxy, TUN status). Use when reproducing connection issues or testing engine logic headlessly.
---

# SA05 Debug via sa05ctl

`sa05ctl` drives the exact same controller (`internal/app`) as the GUI, with no
desktop session needed. Prefer it over building the Wails GUI for logic issues.
Build once: `go build -o /tmp/sa05ctl ./cmd/sa05ctl` (or `go run ./cmd/sa05ctl`).

## Workflow

```bash
/tmp/sa05ctl import 'https://…'   # fetch subscription; failures leave cache untouched
/tmp/sa05ctl profiles             # list servers (id / number)
/tmp/sa05ctl use 1                # set active profile
/tmp/sa05ctl up 1 --socks 21808   # run core in foreground until Ctrl-C
/tmp/sa05ctl check 21808          # request through the tunnel (proves traffic flows)
/tmp/sa05ctl ping                 # latency of all profiles
/tmp/sa05ctl status               # persisted state
/tmp/sa05ctl tun status           # tunnel via sa05-helper (needs installed helper)
/tmp/sa05ctl tg-link              # tg:// link (generates secret once, persists it)
/tmp/sa05ctl tg-run auto          # run MTProto proxy until Ctrl-C (auto|cf|ws|tcp)
```

## Reading results

- `up` failing with `порт SOCKS ... занят` → another proxy client holds the port;
  the desktop GUI would AutoPort-rebind, `sa05ctl up --socks <free>` does it manually.
- `up` succeeds but `check` fails → port open, tunnel dead (server unreachable).
  Same distinction as `Engine.Healthy` (port dial) vs `probeThroughTunnel` (traffic).
- `ping` errors per profile are cached into `ProfileView.error` — trust them, they
  come from the same `ping.Measurer` the servers screen shows.
- State file: `~/.config/sa05/state.json` (0600, contains tokens) — read, never paste
  into logs or commits.

## Where the code lives

- CLI plumbing: `cmd/sa05ctl/main.go`
- Start/stop/health: `internal/core/engine/engine.go`
- Subscription fetch: `internal/core/subscription/`
- Latency probes: `internal/core/ping/`
- Reconnect policy: `internal/core/recovery/` (bounded attempts — keep it that way)
- Full diagnostics verdict: `App.Diagnose` in `internal/app/diagnostics.go`
