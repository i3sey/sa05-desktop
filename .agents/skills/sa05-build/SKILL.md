---
name: sa05-build
description: Build, test and lint SA05 Desktop (Go + Wails + Svelte). Use when building binaries, running go tests, checking gofmt/vet, building the frontend, or reproducing CI.
---

# SA05 Build

## 0. Fresh checkout first

`third_party/xray-core/` is gitignored. Without it nothing compiles:

```bash
build/vendor-xray.sh          # fetch pinned Xray-core + apply Beeline patch + regression test
build/vendor-xray.sh --force  # re-fetch if the tree looks broken
```

Never edit `third_party/xray-core/` by hand — fix `build/patch-xray.sh` /
`build/patches/` instead.

## 1. Tests (fast feedback)

```bash
go test -count=1 ./internal/... ./cmd/sa05ctl ./cmd/sa05-helper
```

`cmd/sa05` is excluded: it embeds the built frontend and is compile-only in CI.
Single package: `go test -count=1 ./internal/core/engine/`.

## 2. Lint

```bash
gofmt -l ./cmd ./internal | grep -v 'internal/tgws/upstream/proxy.go'
go vet ./internal/... ./cmd/sa05ctl ./cmd/sa05-helper
```

`internal/tgws/upstream/proxy.go` is generated — excluded from gofmt by design.

## 3. Frontend

```bash
(cd frontend && npm ci && npm run build)   # required before building cmd/sa05
(cd frontend && npm run dev)               # UI on mock data from src/api.ts, no Go needed
(cd frontend && npm run check)             # svelte-check
```

The browser calls `window.go.main.App` (injected by Wails). Missing in dev
browser → `api.ts` mock takes over. New Go binding → add it to the mock too.

## 4. Binaries

```bash
build/build-linux.sh   # frontend + sa05 + sa05ctl into build/out/
```

Manual equivalents:

```bash
# Linux GUI (webkit2_41 = WebKitGTK 4.1 on current distros):
go build -tags "desktop,production,webkit2_41" -ldflags "-w -s" -o build/out/sa05 ./cmd/sa05
# Windows GUI (-H windowsgui hides the console window):
go build -tags "desktop,production" -ldflags "-H windowsgui" -o build/out/sa05.exe ./cmd/sa05
go build -ldflags "-w -s" -o build/out/sa05ctl ./cmd/sa05ctl
go build -ldflags "-w -s" -o build/out/sa05-helper ./cmd/sa05-helper
```

System deps (Linux): `libwebkit2gtk-4.1-dev libgtk-3-dev`. Go 1.26, Node 20+.

## 5. Geo assets

Only needed for profiles with `geosite:`/`geoip:` rules:

```bash
build/fetch-geoassets.sh build/out/assets
```

Lookup order: `SA05_ASSET_DIR` → `$XDG_CACHE_HOME/sa05/assets`.

## 6. CI parity (.github/workflows/ci.yml)

test job: vendor → gofmt → vet → tests. build-linux: + frontend + 3 binaries +
`sa05ctl status` smoke test. Match this order when something fails in CI but not locally.
