<script lang="ts">
  import { onMount } from 'svelte'
  import Switch from './Switch.svelte'
  import ModeDetails from './ModeDetails.svelte'
  import Sparkline from './Sparkline.svelte'
  import Onboarding from './Onboarding.svelte'
  import Servers from './Servers.svelte'
  import Settings from './Settings.svelte'
  import Subscribe from './Subscribe.svelte'
  import Diagnostics from './Diagnostics.svelte'
  import { backend, copyText, errorText, onSnapshot, type View } from './api'

  type Screen = 'main' | 'servers' | 'settings' | 'diagnostics'

  let view = $state<View | null>(null)
  let screen = $state<Screen>('main')
  let details = $state<'systemProxy' | 'tun' | 'telegram' | null>(null)
  let helpOpen = $state(false)
  let error = $state('')
  let busy = $state(false)
  // Rate history for the sparkline: one sample per backend refresh while connected.
  let rateHist = $state<{ down: number[]; up: number[] }>({ down: [], up: [] })
  const HIST_MAX = 60

  async function refresh() {
    try {
      view = await backend.View()
      const snapshot = view.snapshot
      if (snapshot.status === 'CONNECTED') {
        rateHist = {
          down: [...rateHist.down, snapshot.rateDown].slice(-HIST_MAX),
          up: [...rateHist.up, snapshot.rateUp].slice(-HIST_MAX),
        }
      } else if (rateHist.down.length > 0 || rateHist.up.length > 0) {
        rateHist = { down: [], up: [] }
      }
    } catch (cause) {
      error = errorText(cause)
    }
  }

  onMount(() => {
    refresh()
    // The backend pushes every transition; the poll is only a safety net for events lost
    // while the window was hidden.
    const off = onSnapshot(() => refresh())
    const poll = setInterval(refresh, 5000)
    return () => {
      off()
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

  const activeProfile = $derived((view?.profiles ?? []).find((profile) => profile.active))

  // The local MTProto endpoint is listed only while the proxy is actually running:
  // an address shown for a stopped proxy would send the user to a dead port.
  // SOCKS5/HTTP live in Settings — they are for manual app configuration, not daily use.
  const endpoints = $derived.by(() => {
    const snapshot = view?.snapshot
    const list: { label: string; value: string; hint: string }[] = []
    if (!snapshot) return list
    if (snapshot.telegramOn) {
      list.push({
        label: 'MTProto',
        value: `127.0.0.1:${view?.telegram.port ?? 1443}`,
        hint: 'Telegram: прокси MTProto с секретом ниже',
      })
    }
    return list
  })

  const heroBusy = $derived(
    view?.snapshot.status === 'CONNECTING' || view?.snapshot.status === 'RECOVERING',
  )
  const heroStop = $derived(view?.presentation.primaryAction === 'STOP')

  // One line answering what is routed right now: users confuse "connected" with
  // "everything goes through the tunnel". Shown only while the core is up.
  const routeLine = $derived.by(() => {
    const snapshot = view?.snapshot
    if (!snapshot || snapshot.status !== 'CONNECTED') return ''
    const parts: string[] = []
    if (snapshot.tunOn) parts.push('весь трафик — через туннель')
    else if (snapshot.systemProxyOn) parts.push('приложения — через прокси')
    if (snapshot.telegramOn) parts.push('Telegram — отдельно')
    return parts.join(' · ')
  })
  const heroSub = $derived.by(() => {
    const snapshot = view?.snapshot
    if (!snapshot) return ''
    if (snapshot.status === 'CONNECTING' || snapshot.status === 'RECOVERING') {
      return 'Это может занять несколько секунд'
    }
    return ''
  })

  const isDark = $derived.by(() => {
    const theme = view?.theme ?? 'auto'
    if (theme === 'dark') return true
    if (theme === 'light') return false
    return window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false
  })

  async function cycleTheme() {
    await guard(() => backend.SetTheme(isDark ? 'light' : 'dark'))
  }

  async function closeHelp() {
    helpOpen = false
    await guard(() => backend.MarkOnboarded())
  }

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
    <button class="ghost" onclick={() => (helpOpen = true)} aria-label="Обучение">?</button>
    <button class="ghost" onclick={cycleTheme} aria-label={isDark ? 'Светлая тема' : 'Тёмная тема'}>
      {isDark ? '☀' : '☾'}
    </button>
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
      {#if routeLine}<p class="route" title="Что сейчас идёт через туннель">⇄ {routeLine}</p>{/if}

      <button
        class="hero"
        class:busy={heroBusy}
        class:stop={heroStop}
        disabled={busy}
        onclick={primary}
      >
        <span class="hero-label">{primaryLabel}</span>
        {#if heroSub}<span class="hero-sub">{heroSub}</span>{/if}
      </button>
      {#if error}<p class="error">{error}</p>{/if}
    </div>

    {#if view.snapshot.status === 'CONNECTED'}
      <div class="card">
        <Sparkline down={rateHist.down} up={rateHist.up} />
      </div>
    {/if}

    <div class="rows">
      <div class="row">
        <button class="rowmain" onclick={() => (details = 'systemProxy')} aria-label="Как работает системный прокси">
          <div class="title">Системный прокси</div>
          <div class="hint">Трафик приложений, уважающих настройки системы</div>
        </button>
        <div class="spacer"></div>
        <span class="chev">›</span>
        <Switch
          label="Системный прокси"
          checked={view.toggles.systemProxy}
          disabled={busy}
          onchange={(next) => toggle('systemProxy', next)}
        />
      </div>
      <div class="row">
        <button class="rowmain" onclick={() => (details = 'tun')} aria-label="Как работает TUN">
          <div class="title">TUN</div>
          <div class="hint">
            {view.helperAvailable
              ? 'Весь трафик системы через туннель'
              : 'Нужен системный компонент: sudo build/install-linux.sh'}
          </div>
        </button>
        <div class="spacer"></div>
        <span class="chev">›</span>
        <Switch
          label="TUN"
          checked={view.toggles.tun}
          disabled={busy || !view.helperAvailable}
          onchange={(next) => toggle('tun', next)}
        />
      </div>
      <div class="row">
        <button class="rowmain" onclick={() => (details = 'telegram')} aria-label="Как работает Telegram-прокси">
          <div class="title">Telegram</div>
          <div class="hint">MTProto-прокси на порту {view.telegram.port}</div>
        </button>
        <div class="spacer"></div>
        <span class="chev">›</span>
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
              {#key copied}<span class="pop"
                  >{copied === endpoint.value ? 'скопировано' : 'копировать'}</span
                >{/key}
            </span>
          </button>
        {/each}
      </div>
    {/if}

    <div class="rows">
      <div class="row">
        <button class="rowmain" onclick={() => (screen = 'servers')} aria-label="Список серверов">
          <div class="title">Серверы</div>
          <div class="hint">
            {activeProfile
              ? `${activeProfile.flag} ${activeProfile.name}`.trim()
              : 'Сервер не выбран'}
          </div>
        </button>
        <div class="spacer"></div>
        {#if (view.profiles ?? []).length > 1}
          <button
            class="ghost tiny"
            disabled={busy}
            onclick={() => guard(() => backend.CycleProfile(-1))}
            aria-label="Предыдущий сервер"
          >‹</button>
          <button
            class="ghost tiny"
            disabled={busy}
            onclick={() => guard(() => backend.CycleProfile(1))}
            aria-label="Следующий сервер"
          >›</button>
        {/if}
      </div>
      <button class="row" onclick={() => (screen = 'diagnostics')}>
        <div>
          <div class="title">Диагностика</div>
          <div class="hint">Проверить, что именно не открывается</div>
        </div>
        <div class="spacer"></div>
        <span class="chev">›</span>
      </button>
    </div>
  </div>
  {#if details}
    <ModeDetails
      mode={details}
      {view}
      {busy}
      onclose={() => (details = null)}
      ontoggle={toggle}
    />
  {/if}
{/if}
{#if view && (!view.onboarded || helpOpen)}
  <Onboarding onclose={closeHelp} />
{/if}
