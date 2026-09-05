# SA05 Desktop — инструкции для LLM-агентов

Десктопный VPN-клиент (Linux + Windows) на Go. Xray-профили из HTTPS-подписки,
системный прокси, TUN-режим и Telegram через встроенный MTProto-прокси (tg-ws-proxy).
Порт Android-клиента `com.fife.sa05`; контракты подписки и политики восстановления
совместимы с ним.

## Стек

- Go 1.26, Wails v2 (GUI), Svelte 5 + Vite + TypeScript (фронтенд)
- Xray-core in-process через `replace` в `go.mod` → `third_party/xray-core`
- Node 20+, системно для Linux нужен `webkit2gtk-4.1` + `libgtk-3-dev`

## Структура

| Путь | Что там |
|---|---|
| `cmd/sa05` | GUI: тонкая Wails-обёртка, tray, `sa05://`, биндинги к фронтенду |
| `cmd/sa05-helper` | Привилегированный демон (root/SYSTEM): TUN, маршруты, DNS, kill-switch |
| `cmd/sa05ctl` | CLI для отладки ядра без GUI — предпочитать его при проверке логики |
| `internal/app` | Контроллер — вся бизнес-логика. Не знает про Wails/терминал |
| `internal/core/engine` | Запуск Xray in-process, SOCKS/HTTP инбаунды, счётчики трафика |
| `internal/core/subscription` | Импорт/парсинг подписки, контракт совместим с Android |
| `internal/core/xrayconf` | Патчи рантайм-конфига: Beeline-padding, HTTP-inbound, порты, mark |
| `internal/core/state` | Единый снапшот статуса — читают GUI, tray и CLI |
| `internal/core/ping`, `recovery`, `diag` | Замеры задержки, политика переподключений, диагностика |
| `internal/tgws` | MTProto-прокси (порт 1443 фиксирован). `upstream/` — вендор, руками не править |
| `internal/ipc` | Связь GUI ↔ helper (unix-socket с проверкой uid на Linux) |
| `internal/sysproxy`, `tun`, `netbypass`, `netmon`, `storage`, `update`, `desktop`, `notify`, `trayicon` | Системный прокси, TUN, mark байпаса, мониторинг сети, хранилище, автообновление, автозапуск, уведомления, иконка трея |
| `frontend/src` | Svelte UI. Мост к Go — `window.go.main.App`, мок для `npm run dev` — в `api.ts` |
| `build/` | `vendor-xray.sh`, `patch-xray.sh`, `vendor-tgws.sh`, `build-linux.sh`, `fetch-geoassets.sh`, `install-linux.sh`, `make-appimage.sh` |
| `packaging/`, `.github/workflows` | desktop-файл, systemd-юнит, AUR, CI (`ci.yml`) и релиз (`release.yml`) |

Архитектура: GUI (`sa05`) держит xray-core in-process (SOCKS `127.0.0.1:10808`,
HTTP `127.0.0.1:10809`) + tgws (MTProto `127.0.0.1:1443`) и говорит с `sa05-helper`
по IPC. Сокеты ядра и tgws помечены (`SO_MARK` / bind к физическому интерфейсу),
чтобы не заворачиваться в собственный TUN.

## Команды

```bash
build/vendor-xray.sh              # ОБЯЗАТЕЛЬНО на свежем checkout: third_party/ в .gitignore
go test -count=1 ./internal/... ./cmd/sa05ctl ./cmd/sa05-helper
gofmt -l ./cmd ./internal | grep -v 'internal/tgws/upstream/proxy.go'
go vet ./internal/... ./cmd/sa05ctl ./cmd/sa05-helper
(cd frontend && npm ci && npm run build)

# Полная сборка Linux:
build/build-linux.sh
# Ручная сборка GUI (нужен собранный frontend/dist):
go build -tags "desktop,production,webkit2_41" -o build/out/sa05 ./cmd/sa05
# Windows GUI (без консоли):
go build -tags "desktop,production" -ldflags "-H windowsgui" -o build/out/sa05.exe ./cmd/sa05

# Гео-базы (нужны только профилям с правилами geosite:/geoip:):
build/fetch-geoassets.sh build/out/assets
```

CI делает то же самое: вендоринг → gofmt → vet → тесты → сборка обеих платформ.
`cmd/sa05` в CI не тестируется (нужен собранный фронтенд), только компилируется.

## Отладка без GUI (предпочитать GUI)

```bash
go run ./cmd/sa05ctl import 'https://…'   # импорт подписки
go run ./cmd/sa05ctl profiles             # список серверов
go run ./cmd/sa05ctl up 1 --socks 21808   # поднять ядро до Ctrl-C
go run ./cmd/sa05ctl check 21808          # запрос через туннель
go run ./cmd/sa05ctl ping                 # замер всех профилей
go run ./cmd/sa05ctl tun status           # туннель через helper
```

Фронтенд отдельно: `(cd frontend && npm run dev)` — работает на моке из `api.ts`.

## Конвенции

- Пользовательские ошибки — **на русском** (`fmt.Errorf("Профиль не найден")`).
  Комментарии и имена — на английском.
- `internal/app` — UI-агностик: новую фичу делать там, а в `cmd/sa05` только
  тонкий биндинг + `EventsEmit(ctx, "state", snapshot)`.
- Фронтенд не склеивает состояние из кусков: `View()` отдаёт всё одним payload.
  Новый тоггл → `storage.Toggles` + `App.Toggle` + `Settings.svelte`.
- Тесты лежат рядом (`*_test.go`), запускать с `-count=1`.
- `gofmt` обязателен; единственное исключение — `internal/tgws/upstream/proxy.go`
  (сгенерирован, в CI исключён из проверки).

## Инварианты — не ломать

1. **SO_REUSEPORT**: Xray тихо делит занятый порт с другим клиентом. Поэтому
   `ensurePortFree` + `AutoPort=true` в десктопе; занятый порт → перебинд
   на свободный и показ фактического в UI, а не ошибка.
2. **Порт MTProto 1443 фиксирован** — Telegram настраивается один раз.
   SOCKS/HTTP — из профиля, могут плавать.
3. **OutboundMark** (`netbypass.Mark`) на сокетах ядра обязателен, иначе TUN
   завернёт трафик ядра в самого себя.
4. **Beeline-патч XHTTP** (`newBeelineSessionID`, 12-char Base64URL) применяется
   скриптом `build/patch-xray.sh`, а не коммитом. `third_party/xray-core`
   руками не править — чинить скрипт/патч в `build/patches/`.
5. **Гео-ассеты** требуются только если `xrayconf.UsesGeoAssets(profile)` true.
   Каталог: `$XDG_CACHE_HOME/sa05/assets` или `SA05_ASSET_DIR`.
6. **`state.json`** — один файл, `0600`, атомарная запись. Там токены подписки
   и секрет Telegram — никогда не логировать и не коммитить.
7. **Kill-switch и восстановление**: решения о реконнекте — только через
   `internal/core/recovery`, счётчик попыток сохранять (не зацикливать).
8. **Helper — единственное, что работает от root.** GUI общается по IPC
   с проверкой uid; без хелпера TUN-тумблер недоступен, остальное работает.

## Скиллы агента (`.agents/skills/`)

Загружать по мере нужды (`/skill:name` в pi), тексты — в `SKILL.md` каждого:

- `sa05-build` — сборка, тесты, gofmt/vet, фронтенд, CI-матрица
- `sa05-debug` — отладка ядра через `sa05ctl` без GUI
- `sa05-xray` — подписка, рантайм-патчи конфига, порты, Beeline, гео-ассеты

## Запреты

- Не коммитить: `state.json`, `*.log`, гео-базы, секреты, `build/out/`,
  `third_party/xray-core/`, `frontend/node_modules/`, `frontend/dist/`.
- Не править `internal/tgws/upstream/` руками — регенерировать через
  `build/vendor-tgws.sh` + `build/patches/tgws-library.py`.
- Не добавлять GUI-логику в обход `internal/app` и `state.Store`.
- Лицензия — GPLv3 (вендор tg-ws-proxy линкуется в бинарь). Новый вендорный
  код отмечать в `THIRD_PARTY_NOTICES.md`.
