<script lang="ts">
  import { backend, errorText, type DiagnosticsReport } from './api'

  interface Props {
    onback: () => void
  }

  let { onback }: Props = $props()

  let report = $state<DiagnosticsReport | null>(null)
  let running = $state(false)
  let error = $state('')

  async function run() {
    running = true
    error = ''
    try {
      report = await backend.Diagnose()
    } catch (cause) {
      error = errorText(cause)
    } finally {
      running = false
    }
  }

  function badge(status: string): string {
    switch (status) {
      case 'OK':
        return 'pill good'
      case 'INCONCLUSIVE':
        return 'pill'
      default:
        return 'pill bad'
    }
  }

  function label(status: string): string {
    switch (status) {
      case 'OK':
        return 'открылся'
      case 'INCONCLUSIVE':
        return 'неясно'
      default:
        return 'не открылся'
    }
  }
</script>

<div class="head">
  <button class="ghost" onclick={onback} aria-label="Назад">‹</button>
  <h1>Диагностика</h1>
  <div class="spacer"></div>
  <button class="ghost" disabled={running} onclick={run}>
    {running ? 'Проверяем…' : 'Проверить'}
  </button>
</div>

<div class="screen">
  {#if error}
    <p class="error">{error}</p>
  {/if}

  {#if !report && !running}
    <div class="card status">
      <h2>Что это</h2>
      <p class="desc">
        Клиент откроет несколько сайтов и проверит не только ответ, но и его содержимое:
        подмена и заглушка провайдера выглядят как успешный запрос. Проверка идёт через
        туннель, если он поднят, и напрямую, если нет.
      </p>
      <button class="primary" onclick={run}>Проверить</button>
    </div>
  {/if}

  {#if running}
    <div class="empty">Проверяем сайты…</div>
  {/if}

  {#if report}
    <div class="card status">
      <h2>{report.verdict.headline}</h2>
      <p class="desc">{report.verdict.detail}</p>
      <div class="meta">
        <span>{report.throughTunnel ? 'через туннель' : 'напрямую, без туннеля'}</span>
      </div>
    </div>

    <div class="rows">
      {#each report.results as result (result.target.id)}
        <div class="row">
          <div>
            <div class="title">
              {result.target.label}
              {#if result.target.informational}<span class="hint">— справочно</span>{/if}
            </div>
            <div class="hint">
              {#if result.error}
                {result.error}
              {:else}
                HTTP {result.statusCode} · {result.bodyBytes} байт · {result.delayMs} мс
              {/if}
            </div>
          </div>
          <div class="spacer"></div>
          <span class={badge(result.status)}>{label(result.status)}</span>
        </div>
      {/each}
    </div>
  {/if}
</div>
