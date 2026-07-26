<script lang="ts">
  import { untrack } from 'svelte'
  import Switch from './Switch.svelte'
  import { copyText, type View } from './api'

  interface Props {
    view: View
    busy: boolean
    error: string
    onback: () => void
    onimport: (url: string) => void
    ontoggle: (name: string, next: boolean) => void
    ontransport: (value: string) => void
  }

  let { view, busy, error, onback, onimport, ontoggle, ontransport }: Props = $props()
  // Seeded once: the field is editable, so later refreshes must not overwrite typing.
  let url = $state(untrack(() => view.subscription.url))

  const transports = [
    { value: 'auto', label: 'Автоматически' },
    { value: 'cf', label: 'Через Cloudflare' },
    { value: 'ws', label: 'Прямой WebSocket' },
    { value: 'tcp', label: 'Прямой TCP' },
  ]

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
    {#if view.telegram.link}
      <p class="hint mono">{view.telegram.link}</p>
      <button onclick={() => copyText(view.telegram.link)}>Скопировать ссылку</button>
    {/if}
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
  </div>
</div>
