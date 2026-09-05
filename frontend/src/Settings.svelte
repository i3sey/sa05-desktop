<script lang="ts">
  import { untrack } from 'svelte'
  import Switch from './Switch.svelte'
  import { backend, copyText, errorText, type UpdateView, type View } from './api'

  interface Props {
    view: View
    busy: boolean
    error: string
    onback: () => void
    onimport: (url: string) => void
    ontoggle: (name: string, next: boolean) => void
    ontheme: (value: string) => void
    ontransport: (value: string) => void
  }

  let { view, busy, error, onback, onimport, ontoggle, ontheme, ontransport }: Props = $props()
  // Seeded once: the field is editable, so later refreshes must not overwrite typing.
  let url = $state(untrack(() => view.subscription.url))

  const themes = [
    { value: 'auto', label: 'Как в системе' },
    { value: 'light', label: 'Светлая' },
    { value: 'dark', label: 'Тёмная' },
  ]

  const transports = [
    { value: 'auto', label: 'Автоматически' },
    { value: 'cf', label: 'Через Cloudflare' },
    { value: 'ws', label: 'Прямой WebSocket' },
    { value: 'tcp', label: 'Прямой TCP' },
  ]

  let update = $state<UpdateView | null>(null)
  let checking = $state(false)
  let installing = $state(false)
  let updateError = $state('')
  let copied = $state('')
  let copiedTimer: ReturnType<typeof setTimeout> | undefined

  async function copy(value: string) {
    try {
      await copyText(value)
      copied = value
      clearTimeout(copiedTimer)
      copiedTimer = setTimeout(() => (copied = ''), 1500)
    } catch (cause) {
      updateError = errorText(cause)
    }
  }

  const ports = $derived.by(() => {
    const snapshot = view.snapshot
    const list: { label: string; value: string; hint: string }[] = []
    if (snapshot.status !== 'CONNECTED') return list
    if (snapshot.socksPort > 0) {
      list.push({
        label: 'SOCKS5',
        value: `127.0.0.1:${snapshot.socksPort}`,
        hint: 'браузеры, торренты, curl --socks5-hostname',
      })
    }
    if (snapshot.httpPort > 0) {
      list.push({
        label: 'HTTP',
        value: `127.0.0.1:${snapshot.httpPort}`,
        hint: 'http_proxy / https_proxy',
      })
    }
    return list
  })

  async function checkUpdate() {
    checking = true
    updateError = ''
    try {
      update = await backend.CheckUpdate()
      if (update.error) updateError = update.error
    } catch (cause) {
      updateError = errorText(cause)
    } finally {
      checking = false
    }
  }

  async function installUpdate() {
    installing = true
    updateError = ''
    try {
      update = await backend.InstallUpdate()
    } catch (cause) {
      updateError = errorText(cause)
    } finally {
      installing = false
    }
  }

  const updated = $derived.by(() => {
    if (!view.subscription.updatedAt) return ''
    return new Date(view.subscription.updatedAt).toLocaleString('ru-RU')
  })
</script>

<div class="head">
  <button class="ghost" onclick={onback} aria-label="Назад">‹</button>
  <h1>Настройки</h1>
</div>

<div class="screen">
  <div class="card status">
    <h2>Подписка</h2>
    <input type="url" bind:value={url} placeholder="https://…" spellcheck="false" />
    <button class="primary" disabled={busy || url.trim() === ''} onclick={() => onimport(url.trim())}>
      {busy ? 'Обновляем…' : 'Обновить'}
    </button>
    <div class="meta">
      <span>{view.subscription.count} профилей</span>
      {#if updated}<span>обновлена {updated}</span>{/if}
    </div>
    {#if view.subscription.userInfo}
      <p class="hint mono">{view.subscription.userInfo}</p>
    {/if}
    {#if error}<p class="error">{error}</p>{/if}
  </div>

  <div class="card status">
    <h2>Оформление</h2>
    <select
      value={view.theme ?? 'auto'}
      disabled={busy}
      onchange={(event) => ontheme((event.currentTarget as HTMLSelectElement).value)}
    >
      {#each themes as theme (theme.value)}
        <option value={theme.value}>{theme.label}</option>
      {/each}
    </select>
  </div>

  <div class="card status">
    <h2>Telegram</h2>
    <p class="desc">
      Telegram подключается к SA05 на порт {view.telegram.port}. Транспорт меняется здесь и
      не требует перенастройки Telegram.
    </p>
    <select
      value={view.telegram.transport}
      disabled={busy}
      onchange={(event) => ontransport((event.currentTarget as HTMLSelectElement).value)}
    >
      {#each transports as transport (transport.value)}
        <option value={transport.value}>{transport.label}</option>
      {/each}
    </select>
    {#if view.toggles.telegram && view.telegram.link}
      <button class="primary" onclick={() => backend.OpenURL(view.telegram.link)}>
        Добавить в Telegram
      </button>
      <p class="hint">Откроется Telegram с предложением добавить прокси — ничего копировать не нужно.</p>
    {:else if !view.toggles.telegram}
      <p class="hint">Включите Telegram на главном экране — здесь появится кнопка добавления.</p>
    {/if}
  </div>

  {#if ports.length > 0}
    <div class="card status">
      <h2>Локальные порты</h2>
      <p class="desc">Для приложений, настроенных вручную. Работают, пока подключение активно.</p>
      <div class="rows">
        {#each ports as port (port.label)}
          <button class="row" onclick={() => copy(port.value)}>
            <div>
              <div class="title">{port.label} <span class="mono">{port.value}</span></div>
              <div class="hint">{port.hint}</div>
            </div>
            <div class="spacer"></div>
            <span class="pill" class:good={copied === port.value}>
              {#key copied}<span class="pop"
                  >{copied === port.value ? 'скопировано' : 'копировать'}</span
                >{/key}
            </span>
          </button>
        {/each}
      </div>
    </div>
  {/if}

  <div class="card status">
    <h2>Обновления</h2>
    {#if update?.installed}
      <p class="desc">
        Версия {update.available} установлена. Перезапустите клиент, чтобы она заработала.
      </p>
    {:else if update?.available}
      <p class="desc">Доступна версия {update.available}</p>
      {#if update.notes}<p class="hint">{update.notes.slice(0, 400)}</p>{/if}
      <button class="primary" disabled={installing} onclick={installUpdate}>
        {installing ? 'Устанавливаем…' : `Обновить до ${update.available}`}
      </button>
    {:else}
      <p class="desc">
        Версия {update?.current ?? '—'}{update ? ': обновлений нет' : ''}
      </p>
      <button disabled={checking} onclick={checkUpdate}>
        {checking ? 'Проверяем…' : 'Проверить обновления'}
      </button>
    {/if}
    {#if updateError}<p class="error">{updateError}</p>{/if}
    <p class="hint">
      Обновление скачивается с GitHub и проверяется подписью разработчика: архив без
      подписи или с чужой подписью не устанавливается.
    </p>
  </div>

  <div class="rows">
    <div class="row">
      <div>
        <div class="title">Kill-switch</div>
        <div class="hint">Блокировать трафик, если ядро упало при включённом TUN</div>
      </div>
      <div class="spacer"></div>
      <Switch
        label="Kill-switch"
        checked={view.toggles.killSwitch}
        disabled={busy}
        onchange={(next) => ontoggle('killSwitch', next)}
      />
    </div>
    <div class="row">
      <div>
        <div class="title">Отдать IPv6 системе</div>
        <div class="hint">Только для IPv6-only сетей: снова открывает утечку AAAA</div>
      </div>
      <div class="spacer"></div>
      <Switch
        label="Отдать IPv6 системе"
        checked={view.toggles.allowIpv6Bypass}
        disabled={busy}
        onchange={(next) => ontoggle('allowIpv6Bypass', next)}
      />
    </div>
    <div class="row">
      <div>
        <div class="title">Подключаться при запуске</div>
      </div>
      <div class="spacer"></div>
      <Switch
        label="Подключаться при запуске"
        checked={view.toggles.autoConnect}
        disabled={busy}
        onchange={(next) => ontoggle('autoConnect', next)}
      />
    </div>
    <div class="row">
      <div>
        <div class="title">Запускать вместе с системой</div>
      </div>
      <div class="spacer"></div>
      <Switch
        label="Запускать вместе с системой"
        checked={view.toggles.autostart}
        disabled={busy}
        onchange={(next) => ontoggle('autostart', next)}
      />
    </div>
    <div class="row">
      <div>
        <div class="title">Проверять обновления</div>
      </div>
      <div class="spacer"></div>
      <Switch
        label="Проверять обновления"
        checked={view.toggles.autoUpdate}
        disabled={busy}
        onchange={(next) => ontoggle('autoUpdate', next)}
      />
    </div>
    <div class="row">
      <div>
        <div class="title">Уведомления</div>
        <div class="hint">Подключения, обрывы и обновления</div>
      </div>
      <div class="spacer"></div>
      <Switch
        label="Уведомления"
        checked={!view.toggles.muteNotifications}
        disabled={busy}
        onchange={(next) => ontoggle('notifications', next)}
      />
    </div>
  </div>
</div>
