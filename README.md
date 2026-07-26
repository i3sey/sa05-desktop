# SA05 Desktop

Десктопный клиент SA05 для Linux и Windows: Xray-профили из HTTPS-подписки, системный прокси,
TUN-режим и Telegram через встроенный MTProto-прокси (tg-ws-proxy).

Порт Android-клиента `com.fife.sa05` на Go. Контракты подписки, валидации конфигов и политик
восстановления совместимы с Android-версией.

## Состав

| Компонент | Что делает |
|---|---|
| `cmd/sa05` | GUI (Wails), tray, автозапуск, обработка `sa05://` |
| `cmd/sa05-helper` | Привилегированный демон: TUN, маршруты, DNS, kill-switch |
| `cmd/sa05ctl` | CLI для отладки ядра без GUI |
| `internal/core` | Оркестрация ядра, подписка, состояние, диагностика, пинг |
| `internal/tgws` | Вендор tg-ws-proxy 1.2.0 (GPLv3) как библиотека |

## Архитектура

```
пользовательский процесс                     root / SYSTEM
┌────────────────────────────────┐   ┌──────────────────────────┐
│ sa05 (GUI)                     │   │ sa05-helper              │
│  ├ xray-core (in-process)      │IPC│  ├ TUN + tun2socks       │
│  │   SOCKS 127.0.0.1:10808     │◄─►│  ├ маршруты, DNS         │
│  │   HTTP  127.0.0.1:10809     │   │  └ kill-switch           │
│  ├ tgws  MTProto 127.0.0.1:1443│   └──────────────────────────┘
│  └ системный прокси (per-user) │
└────────────────────────────────┘
```

Апстрим-сокеты ядра и tgws помечаются (`SO_MARK` на Linux, bind к физическому интерфейсу на
Windows), поэтому они не заворачиваются в собственный TUN.

## Сборка

```bash
build/build-linux.sh     # вендоринг ядра + фронтенд + оба бинаря в build/out
go test ./...
```

Отдельные шаги:

```bash
build/vendor-xray.sh                     # xray-core на закреплённом коммите + патч Beeline
build/vendor-tgws.sh                     # исходник tg-ws-proxy (GPLv3)
(cd frontend && npm install && npm run build)
go build -tags "desktop,production,webkit2_41" -o build/out/sa05 ./cmd/sa05
```

Требуется `webkit2gtk-4.1` (Wails), Go 1.26, Node 20+.
Гео-базы `geoip.dat` / `geosite.dat` ищутся в `$XDG_CACHE_HOME/sa05/assets`
(переопределяется `SA05_ASSET_DIR`) — без них профили с правилами `geosite:` не стартуют.

## Установка

```bash
build/fetch-geoassets.sh build/out/assets   # geoip.dat / geosite.dat (нужны профилям с geosite:)
sudo build/install-linux.sh                 # клиент + системный компонент (systemd)
sa05                                        # запуск
sudo build/install-linux.sh --uninstall
```

Системный компонент `sa05-helper` — единственная часть, работающая от root: он создаёт
TUN-устройство и правит маршрутизацию. GUI работает от пользователя и общается с ним через
unix-сокет с проверкой uid; без установленного хелпера тумблер TUN недоступен, остальное
(ядро, системный прокси, Telegram) работает как обычно.

## Локальные адреса

Пока ядро работает, клиент слушает на loopback и на эти адреса можно направлять любые
приложения. Главный экран показывает их списком — строка копируется по клику.

| Что | Адрес по умолчанию | Кому отдавать |
|---|---|---|
| SOCKS5 | `127.0.0.1:10808` | браузеры, торренты, `curl --socks5-hostname` |
| HTTP | `127.0.0.1:10809` | `http_proxy` / `https_proxy` |
| MTProto | `127.0.0.1:1443` | Telegram (секрет — в настройках клиента) |

Порты SOCKS/HTTP берутся из профиля подписки; если они уже заняты другим клиентом, SA05
поднимается на свободных и показывает фактические — Xray биндится с `SO_REUSEPORT`, поэтому
занятый порт молча делил бы трафик между двумя клиентами. Порт MTProto фиксирован: Telegram
настраивается один раз и переживает смену транспорта.

```bash
curl --socks5-hostname 127.0.0.1:10808 https://api.ipify.org
http_proxy=http://127.0.0.1:10809 https_proxy=http://127.0.0.1:10809 curl https://api.ipify.org
```

## Отладка без GUI

```bash
build/out/sa05ctl import 'https://…'        # импорт подписки
build/out/sa05ctl profiles                  # список серверов
build/out/sa05ctl up 1 --socks 21808        # поднять ядро на своих портах
build/out/sa05ctl check 21808               # запрос через туннель
```

## Лицензия

GPLv3 — `internal/tgws` содержит производную работу от tg-ws-proxy (GPLv3), линкуемую в бинарь.
См. `LICENSE` и `THIRD_PARTY_NOTICES.md`.
