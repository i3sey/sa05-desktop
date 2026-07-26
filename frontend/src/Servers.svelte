<script lang="ts">
  import type { ProfileView } from './api'

  interface Props {
    profiles: ProfileView[]
    busy: boolean
    onback: () => void
    onselect: (id: string) => void
    onping: () => void
    onfastest: () => void
  }

  let { profiles, busy, onback, onselect, onping, onfastest }: Props = $props()
</script>

<div class="head">
  <button class="ghost" onclick={onback} aria-label="Назад">‹</button>
  <h1>Серверы</h1>
  <div class="spacer"></div>
  <button class="ghost" disabled={busy} onclick={onping}>Пинг</button>
  <button class="ghost" disabled={busy} onclick={onfastest}>Лучший</button>
</div>

<div class="screen">
  {#if profiles.length === 0}
    <div class="empty">Подписка не импортирована</div>
  {:else}
    <div class="rows">
      {#each profiles as profile (profile.id)}
        <button class="row" disabled={busy} onclick={() => onselect(profile.id)}>
          <span>{profile.flag || (profile.active ? '●' : '○')}</span>
          <div>
            <div class="title">{profile.name || profile.remarks}</div>
            {#if profile.active}<div class="hint">Активен</div>{/if}
          </div>
          <div class="spacer"></div>
          {#if profile.error}
            <span class="pill bad" title={profile.error}>нет ответа</span>
          {:else if profile.latencyMs > 0}
            <span class="pill good">{profile.latencyMs} мс</span>
          {/if}
        </button>
      {/each}
    </div>
    <p class="hint">
      Пинг поднимает каждый профиль на временных портах и измеряет время до первого байта
      ответа — так проверяется весь маршрут, а не только доступность адреса.
    </p>
  {/if}
</div>
