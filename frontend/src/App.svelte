<script lang="ts">
  import { onMount } from 'svelte'
  import Switch from './Switch.svelte'
  import Servers from './Servers.svelte'
  import Settings from './Settings.svelte'
  import Subscribe from './Subscribe.svelte'
  import Diagnostics from './Diagnostics.svelte'
  import { backend, copyText, errorText, formatBytes, onSnapshot, type View } from './api'

  type Screen = 'main' | 'servers' | 'settings' | 'diagnostics'

  let view = $state<View | null>(null)
  let screen = $state<Screen>('main')
  let error = $state('')
  let busy = $state(false)
  let now = $state(Date.now())

  async function refresh() {
    try {
      view = await backend.View()
    } catch (cause) {
      error = errorText(cause)
    }
  }

  onMount(() => {
    refresh()
    // The backend pushes every transition; the poll is only a safety net for events lost
    // while the window was hidden.
    const off = onSnapshot(() => refresh())
    const tick = setInterval(() => (now = Date.now()), 1000)
    const poll = setInterval(refresh, 5000)
    return () => {
      off()
      clearInterval(tick)
      clearInterval(poll)
    }
  })

  async function guard(action: () => Promise<unknown>) {
    busy = true
    error = ''
    try {
      await action()
    } catch (cause) {
      error = errorText(cause)
    } finally {
      busy = false
      await refresh()
    }
  }

  function primary() {
    const action = view?.presentation.primaryAction
    if (action === 'OPEN_SUBSCRIPTION') {
      screen = 'settings'
      return
    }
    if (action === 'STOP') {
      guard(() => backend.Disconnect())
      return
    }
    guard(() => backend.Connect())
  }

  const primaryLabel = $derived.by(() => {
    switch (view?.presentation.primaryAction) {
      case 'STOP':
        return 'Отключить'
      case 'RETRY':
        return 'Повторить'
      case 'OPEN_SUBSCRIPTION':
        return 'Настроить подписку'
      default:
        return 'Подключить'
    }
  })

  const dotClass = $derived.by(() => {
    switch (view?.snapshot.status) {
      case 'CONNECTED':
        return 'dot on'
      case 'CONNECTING':
      case 'RECOVERING':
        return 'dot busy'
      case 'ERROR':
      case 'WAITING_FOR_NETWORK':
        return 'dot bad'
      default:
        return 'dot'
    }
  })

  const uptime = $derived.by(() => {
    const startedAt = view?.snapshot.connectedAt ?? 0
    if (view?.snapshot.status !== 'CONNECTED' || startedAt === 0) return ''
    const seconds = Math.max(0, Math.floor((now - startedAt) / 1000))
    const hours = Math.floor(seconds / 3600)
    const minutes = Math.floor((seconds % 3600) / 60)
    if (hours > 0) return `${hours} ч ${minutes} мин`
    if (minutes > 0) return `${minutes} мин`
    return `${seconds} с`
  })

  const activeProfile = $derived((view?.profiles ?? []).find((profile) => profile.active))

  // The local endpoints other applications can be pointed at. They are listed only while
  // something is actually listening: an address shown for a stopped core would send the
  // user to configure a dead port.
  const endpoints = $derived.by(() => {
    const snapshot = view?.snapshot
    const list: { label: string; value: string; hint: string }[] = []
    if (!snapshot) return list
    if (snapshot.status === 'CONNECTED' && snapshot.socksPort > 0) {
      list.push({
        label: 'SOCKS5',
        value: `127.0.0.1:${snapshot.socksPort}`,
        hint: 'браузеры, торренты, curl --socks5-hostname',
      })
    }
    if (snapshot.status === 'CONNECTED' && snapshot.httpPort > 0) {
      list.push({
        label: 'HTTP',
        value: `127.0.0.1:${snapshot.httpPort}`,
        hint: 'http_proxy / https_proxy',
      })
    }
    if (snapshot.telegramOn) {
      list.push({
        label: 'MTProto',
        value: `127.0.0.1:${view?.telegram.port ?? 1443}`,
        hint: 'Telegram: прокси MTProto с секретом ниже',
      })
    }
    return list
  })

  let copied = $state('')
  let copiedTimer: ReturnType<typeof setTimeout> | undefined

  async function copy(value: string) {
    try {
      await copyText(value)
      copied = value
      clearTimeout(copiedTimer)
      copiedTimer = setTimeout(() => (copied = ''), 1500)
    } catch (cause) {
      error = errorText(cause)
    }
  }

  async function toggle(name: string, next: boolean) {
    await guard(() => backend.Toggle(name, next))
  }

  // The theme lives in state.json and arrives with every View; the DOM attribute is
  // just its projection so CSS can switch palettes. A localStorage mirror keeps the
  // first paint correct before the first View arrives.
  $effect(() => {
    const theme = view?.theme ?? 'auto'
    document.documentElement.dataset.theme = theme
    try {
      localStorage.setItem('sa05-theme', theme)
    } catch {
      /* private mode: the next View will set it again */
    }
  })
</script>

{#if !view}
  <div class="empty">Загрузка…</div>
{:else if !view.subscription.authorized && screen === 'main'}
  <Subscribe
    {busy}
    {error}
    onimport={(url) => guard(() => backend.Import(url))}
  />
{:else if screen === 'servers'}
  <Servers
    profiles={view.profiles ?? []}
    {busy}
    onback={() => (screen = 'main')}
    onselect={(id) => guard(() => backend.SelectProfile(id))}
    onping={() => guard(() => backend.PingProfiles())}
    onfastest={() => guard(() => backend.SelectFastest())}
  />
{:else if screen === 'diagnostics'}
  <Diagnostics onback={() => (screen = 'main')} />
{:else if screen === 'settings'}
  <Settings
    {view}
    {busy}
    {error}
    onback={() => (screen = 'main')}
    onimport={(url) => guard(() => backend.Import(url))}
    ontoggle={toggle}
    ontheme={(value) => guard(() => backend.SetTheme(value))}
    ontransport={(value) => guard(() => backend.SetTelegramTransport(value))}
  />
{:else}
  <div class="head">
    <h1>SA05</h1>
    {#if view.subscription.title}
      <span class="sub">{view.subscription.title}</span>
    {/if}
    <div class="spacer"></div>
    <button class="ghost" onclick={() => (screen = 'settings')} aria-label="Настройки">⚙</button>
  </div>

  <div class="screen">
    <div class="card status">
      <div class="status-line">
        <span class={dotClass}></span>
        <div>
          <h2>{view.presentation.title}</h2>
          <p class="desc">{view.presentation.description}</p>
        </div>
      </div>

      {#if view.snapshot.status === 'CONNECTED'}
        <div class="meta">
          {#if uptime}<span>{uptime}</span>{/if}
          {#if view.snapshot.latencyMs > 0}<span>{view.snapshot.latencyMs} мс</span>{/if}
          <span title="Скорость сейчас">
            ↓ {formatBytes(view.snapshot.rateDown)}/с · ↑ {formatBytes(view.snapshot.rateUp)}/с
          </span>
        </div>
        <div class="meta">
          <span title="За сессию">
            всего ↓ {formatBytes(view.snapshot.trafficDown)} · ↑ {formatBytes(view.snapshot.trafficUp)}
          </span>
        </div>
      {/if}

      <button class="primary" disabled={busy} onclick={primary}>{primaryLabel}</button>
      {#if error}<p class="error">{error}</p>{/if}
    </div>

    <div class="rows">
      <div class="row">
        <div>
          <div class="title">Системный прокси</div>
          <div class="hint">Трафик приложений, уважающих настройки системы</div>
        </div>
        <div class="spacer"></div>
        <Switch
          label="Системный прокси"
          checked={view.toggles.systemProxy}
          disabled={busy}
          onchange={(next) => toggle('systemProxy', next)}
        />
      </div>
      <div class="row">
        <div>
          <div class="title">TUN</div>
          <div class="hint">
            {view.helperAvailable
              ? 'Весь трафик системы через туннель'
              : 'Нужен системный компонент: sudo build/install-linux.sh'}
          </div>
        </div>
        <div class="spacer"></div>
        <Switch
          label="TUN"
          checked={view.toggles.tun}
          disabled={busy || !view.helperAvailable}
          onchange={(next) => toggle('tun', next)}
        />
      </div>
      <div class="row">
        <div>
          <div class="title">Telegram</div>
          <div class="hint">MTProto-прокси на порту {view.telegram.port}</div>
        </div>
        <div class="spacer"></div>
        <Switch
          label="Telegram"
          checked={view.toggles.telegram}
          disabled={busy}
          onchange={(next) => toggle('telegram', next)}
        />
      </div>
    </div>

    {#if endpoints.length > 0}
      <div class="rows">
        {#each endpoints as endpoint (endpoint.label)}
          <button class="row" onclick={() => copy(endpoint.value)}>
            <div>
              <div class="title">{endpoint.label} <span class="mono">{endpoint.value}</span></div>
              <div class="hint">{endpoint.hint}</div>
            </div>
            <div class="spacer"></div>
            <span class="pill" class:good={copied === endpoint.value}>
              {copied === endpoint.value ? 'скопировано' : 'копировать'}
            </span>
          </button>
        {/each}
      </div>
    {/if}

    <div class="rows">
      <button class="row" onclick={() => (screen = 'servers')}>
        <div>
          <div class="title">Серверы</div>
          <div class="hint">
            {activeProfile
              ? `${activeProfile.flag} ${activeProfile.name}`.trim()
              : 'Сервер не выбран'}
          </div>
        </div>
        <div class="spacer"></div>
        <span class="chev">›</span>
      </button>
      <button class="row" onclick={() => (screen = 'diagnostics')}>
        <div>
          <div class="title">Диагностика</div>
          <div class="hint">Проверить, что именно не открывается</div>
        </div>
        <div class="spacer"></div>
        <span class="chev">›</span>
      </button>
      {#if view.telegram.link}
        <button class="row" onclick={() => guard(() => copyText(view.telegram.link))}>
          <div>
            <div class="title">Ссылка для Telegram</div>
            <div class="hint">Скопировать tg://proxy и вставить в Telegram</div>
          </div>
          <div class="spacer"></div>
          <span class="chev">⧉</span>
        </button>
      {/if}
    </div>
  </div>
{/if}
