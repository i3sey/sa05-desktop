<script lang="ts">
  // Detail sheet for the three main-screen modes. Each mode gets a short animated
  // scheme of how traffic flows plus a plain-language explanation, so tapping a row
  // teaches what the switch actually does.
  import Switch from './Switch.svelte'
  import type { View } from './api'

  export type Mode = 'systemProxy' | 'tun' | 'telegram'

  interface Props {
    mode: Mode
    view: View
    busy: boolean
    onclose: () => void
    ontoggle: (name: string, next: boolean) => void
  }

  let { mode, view, busy, onclose, ontoggle }: Props = $props()

  const enabled = $derived(
    mode === 'systemProxy'
      ? view.toggles.systemProxy
      : mode === 'tun'
        ? view.toggles.tun
        : view.toggles.telegram,
  )
  const toggleDisabled = $derived(
    busy || (mode === 'tun' && !view.helperAvailable),
  )

  const title = $derived(
    mode === 'systemProxy' ? 'Системный прокси' : mode === 'tun' ? 'TUN-режим' : 'Telegram',
  )
  const lede = $derived(
    mode === 'systemProxy'
      ? 'SA05 прописывает себя в настройки системы, и приложения начинают ходить в интернет через зашифрованный туннель.'
      : mode === 'tun'
        ? 'Виртуальный сетевой интерфейс забирает весь трафик устройства — даже тот, что не знает про прокси.'
        : `Встроенный MTProto-прокси на 127.0.0.1:${view.telegram.port}: Telegram ходит через SA05, остальные приложения — как обычно.`,
  )
  const facts = $derived<string[]>(
    mode === 'systemProxy'
      ? [
          'Браузеры, мессенджеры и консольные утилиты идут через туннель',
          'При включении сам поднимает подключение, если его нет',
          'Приложения без поддержки системного прокси идут напрямую — им нужен TUN',
        ]
      : mode === 'tun'
        ? [
            'Покрывает все приложения, игры и службы, включая DNS',
            'При включении сам поднимает подключение, если его нет',
            'Требует системный компонент (helper)',
            'С включённым kill-switch обрыв туннеля блокирует сеть, а не выпускает трафик наружу',
          ]
        : [
            'Обычно быстрее и стабильнее основного туннеля: лёгкий протокол MTProto меньше страдает от потерь пакетов и блокировок',
            'Настраивается один раз ссылкой tg://proxy, дальше работает сам',
            'Транспорт меняется в Настройках без перенастройки Telegram',
            'Независим от основного подключения к серверу',
          ],
  )
  const toggleHint = $derived(
    mode === 'systemProxy'
      ? 'Трафик приложений — через туннель'
      : mode === 'tun'
        ? view.helperAvailable
          ? 'Весь трафик устройства — через туннель'
          : 'Нужен системный компонент: sudo build/install-linux.sh'
        : `MTProto на порту ${view.telegram.port}`,
  )
</script>

<svelte:window
  onkeydown={(event) => {
    if (event.key === 'Escape') onclose()
  }}
/>

<div class="overlay" onclick={onclose} role="presentation">
  <!-- to stop the overlay click from closing the sheet when the sheet itself is clicked -->
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div
    class="sheet"
    role="dialog"
    aria-modal="true"
    aria-label={title}
    tabindex="-1"
    onclick={(event) => event.stopPropagation()}
  >
    <div class="sheet-head">
      <h2>{title}</h2>
      <div class="spacer"></div>
      <button class="ghost" onclick={onclose} aria-label="Закрыть">✕</button>
    </div>

    <div class="screen-flat">
      {#if mode === 'systemProxy'}
        <div class="scene" class:off={!enabled}>
          <div class="lane">
            <div class="sources">
              <div class="node">
                <span class="icon">
                  <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="14" rx="2" /><path d="M3 9h18" /><path d="M6 6.5h.01M9 6.5h.01" /></svg>
                </span>
                <span class="cap">Браузер</span>
              </div>
              <div class="node">
                <span class="icon">
                  <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="14" rx="2" /><path d="m7 9 3 3-3 3" /><path d="M12 15h5" /></svg>
                </span>
                <span class="cap">Терминал</span>
              </div>
            </div>
            <div class="track"><span class="dot"></span><span class="dot d2"></span></div>
            <div class="node center">
              <span class="icon">S<span class="ring"></span></span>
              <span class="cap">SA05</span>
            </div>
            <div class="track"><span class="dot"></span><span class="dot d2"></span></div>
            <div class="node">
              <span class="icon">
                <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9" /><path d="M3 12h18" /><path d="M12 3a15.3 15.3 0 0 1 4 9 15.3 15.3 0 0 1-4 9 15.3 15.3 0 0 1-4-9 15.3 15.3 0 0 1 4-9z" /></svg>
              </span>
              <span class="cap">Интернет</span>
            </div>
          </div>
        </div>
      {:else if mode === 'tun'}
        <div class="scene" class:off={!enabled}>
          <div class="lane">
            <div class="node">
              <span class="icon">
                <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="4" width="16" height="11" rx="1" /><path d="M2 19h20" /></svg>
              </span>
              <span class="cap">Устройство</span>
            </div>
            <div class="tube" title="Зашифрованный туннель"></div>
            <div class="node">
              <span class="icon">
                <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9" /><path d="M3 12h18" /><path d="M12 3a15.3 15.3 0 0 1 4 9 15.3 15.3 0 0 1-4 9 15.3 15.3 0 0 1-4-9 15.3 15.3 0 0 1 4-9z" /></svg>
              </span>
              <span class="cap">Интернет</span>
            </div>
          </div>
          <div class="badges">
            <span class="badge" class:warn={!view.toggles.killSwitch}>
              <svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" /></svg>
              {view.toggles.killSwitch ? 'kill-switch включён' : 'kill-switch выключен'}
            </span>
            <span class="badge">весь трафик + DNS</span>
          </div>
        </div>
      {:else}
        <div class="scene" class:off={!enabled}>
          <div class="lane">
            <div class="node">
              <span class="icon">
                <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="7" y="2" width="10" height="20" rx="2" /><path d="M11 18.5h2" /></svg>
              </span>
              <span class="cap">Telegram</span>
            </div>
            <div class="track">
              <span class="plane">
                <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 2 11 13" /><path d="M22 2l-7 20-4-9-9-4 20-7z" /></svg>
              </span>
            </div>
            <div class="node center">
              <span class="icon">S<span class="ring"></span></span>
              <span class="cap">SA05</span>
            </div>
            <div class="track"><span class="dot"></span><span class="dot d2"></span></div>
            <div class="node">
              <span class="icon">
                <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 10h-1.26A8 8 0 1 0 9 20h9a5 5 0 0 0 0-10z" /></svg>
              </span>
              <span class="cap">Серверы TG</span>
            </div>
          </div>
        </div>
      {/if}

      <p class="desc">{lede}</p>
      <ul class="facts">
        {#each facts as fact (fact)}
          <li><span class="tick"></span>{fact}</li>
        {/each}
      </ul>

      <div class="rows">
        <div class="row">
          <div>
            <div class="title">{enabled ? 'Включено' : 'Выключено'}</div>
            <div class="hint">{toggleHint}</div>
          </div>
          <div class="spacer"></div>
          <Switch
            label={title}
            checked={enabled}
            disabled={toggleDisabled}
            onchange={(next) => ontoggle(mode, next)}
          />
        </div>
      </div>
    </div>
  </div>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 50;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 16px;
    background: rgb(0 0 0 / 0.45);
    animation: fade-in 0.15s ease-out;
  }

  .sheet {
    width: 100%;
    max-width: 420px;
    max-height: calc(100vh - 32px);
    overflow-y: auto;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 18px;
    padding: 14px 14px 16px;
    animation: sheet-in 0.18s ease-out;
  }

  @keyframes fade-in {
    from {
      opacity: 0;
    }
  }

  @keyframes sheet-in {
    from {
      transform: translateY(12px) scale(0.98);
      opacity: 0;
    }
  }

  .sheet-head {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 12px;
  }

  .sheet-head h2 {
    font-size: 16px;
    font-weight: 600;
    margin: 0;
  }

  .screen-flat {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .desc {
    color: var(--text-dim);
    font-size: 13px;
    margin: 0;
  }

  .facts {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .facts li {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    font-size: 13px;
    color: var(--text-dim);
  }

  .tick {
    flex: none;
    width: 7px;
    height: 7px;
    margin-top: 5px;
    border-radius: 50%;
    background: var(--accent);
  }

  /* --- Animated scenes --- */

  .scene {
    background: var(--surface-alt);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 14px 12px;
    overflow: hidden;
  }

  .lane {
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 118px;
  }

  .sources {
    display: flex;
    flex-direction: column;
    gap: 8px;
    flex: none;
  }

  .node {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
    width: 62px;
    flex: none;
  }

  .node .icon {
    position: relative;
    width: 44px;
    height: 44px;
    border-radius: 12px;
    background: var(--surface);
    border: 1px solid var(--border);
    color: var(--text-dim);
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .node .cap {
    font-size: 10.5px;
    line-height: 1.2;
    color: var(--text-dim);
    text-align: center;
  }

  .node.center .icon {
    background: var(--accent);
    border-color: transparent;
    color: var(--accent-text);
    font-weight: 700;
    font-size: 17px;
  }

  .ring {
    position: absolute;
    inset: -6px;
    border: 2px dashed var(--accent);
    border-radius: 16px;
    opacity: 0.55;
    animation: spin 9s linear infinite;
  }

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  .track {
    position: relative;
    flex: 1;
    min-width: 24px;
    height: 2px;
    border-radius: 2px;
    background-image: repeating-linear-gradient(
      90deg,
      var(--accent) 0 6px,
      transparent 6px 12px
    );
    animation: dash-slide 0.6s linear infinite;
  }

  @keyframes dash-slide {
    to {
      background-position-x: -12px;
    }
  }

  .dot {
    position: absolute;
    top: 50%;
    left: 0;
    width: 8px;
    height: 8px;
    margin-top: -4px;
    border-radius: 50%;
    background: var(--accent);
    box-shadow: 0 0 6px var(--accent);
    animation: traverse 1.8s linear infinite;
  }

  .dot.d2 {
    animation-delay: 0.9s;
  }

  @keyframes traverse {
    from {
      left: 0;
      opacity: 0;
    }
    12% {
      opacity: 1;
    }
    85% {
      opacity: 1;
    }
    to {
      left: calc(100% - 8px);
      opacity: 0;
    }
  }

  .plane {
    position: absolute;
    top: 50%;
    left: 0;
    margin-top: -9px;
    color: var(--accent);
    animation: traverse 2.4s linear infinite;
  }

  .tube {
    flex: 1;
    min-width: 24px;
    height: 26px;
    border-radius: 999px;
    border: 1px solid var(--border);
    background-color: var(--surface);
    background-image: repeating-linear-gradient(
      -55deg,
      var(--accent) 0 6px,
      transparent 6px 14px
    );
    animation: tube-slide 0.9s linear infinite;
  }

  @keyframes tube-slide {
    to {
      background-position: 20px 0;
    }
  }

  .badges {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 10px;
  }

  .badge {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    font-size: 11px;
    padding: 2px 8px;
    border-radius: 999px;
    color: var(--accent);
    background: color-mix(in srgb, var(--accent) 14%, transparent);
  }

  .badge.warn {
    color: var(--danger);
    background: color-mix(in srgb, var(--danger) 14%, transparent);
  }

  /* Paused state: the scheme freezes and fades when the mode is off. */
  .scene.off .track {
    background-image: repeating-linear-gradient(
      90deg,
      var(--border) 0 6px,
      transparent 6px 12px
    );
    animation-play-state: paused;
  }

  .scene.off .tube {
    background-image: repeating-linear-gradient(
      -55deg,
      var(--border) 0 6px,
      transparent 6px 14px
    );
    animation-play-state: paused;
  }

  .scene.off .ring,
  .scene.off .dot,
  .scene.off .plane {
    display: none;
  }

  @media (prefers-reduced-motion: reduce) {
    .overlay,
    .sheet,
    .track,
    .dot,
    .plane,
    .tube,
    .ring {
      animation: none;
    }
  }
</style>
