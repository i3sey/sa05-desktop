<script lang="ts">
  import { backend, copyText, errorText, type DiagnosticsReport, type IPInfo } from './api'

  interface Props {
    onback: () => void
  }

  let { onback }: Props = $props()

  let report = $state<DiagnosticsReport | null>(null)
  let running = $state(false)
  let error = $state('')
  let ipinfo = $state<IPInfo | null>(null)
  let ipLoading = $state(false)
  let ipError = $state('')
  let bundleState = $state('')
  let bundleBusy = $state(false)

  async function copyReport() {
    bundleBusy = true
    bundleState = ''
    try {
      await copyText(await backend.LogBundle())
      bundleState = 'copied'
    } catch (cause) {
      bundleState = errorText(cause)
    } finally {
      bundleBusy = false
    }
  }

  async function checkIP() {
    ipLoading = true
    ipError = ''
    try {
      ipinfo = await backend.CheckIP()
    } catch (cause) {
      ipError = errorText(cause)
    } finally {
      ipLoading = false
    }
  }

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
  <button class="ghost" disabled={running || ipLoading} onclick={checkIP}>
    {ipLoading ? 'Узнаём…' : 'Мой IP'}
  </button>
  <button class="ghost" disabled={running} onclick={run}>
    {running ? 'Проверяем…' : 'Проверить'}
  </button>
</div>

<div class="screen">
  {#if error}
    <p class="error">{error}</p>
  {/if}

  {#if ipinfo}
    <div class="card status">
      <h2 class="mono">{ipinfo.ip}</h2>
      <div class="meta">
        {#if ipinfo.country}<span>{[ipinfo.city, ipinfo.country].filter(Boolean).join(', ')}</span>{/if}
        <span>{ipinfo.throughTunnel ? 'через туннель' : 'напрямую, без туннеля'}</span>
      </div>
    </div>
  {/if}
  {#if ipError}
    <p class="error">{ipError}</p>
  {/if}

  <div class="card status">
    <h2>Как ходят сайты</h2>
    <ul class="facts">
      <li><span class="tick"></span>Российские сайты и сервисы — банки, госуслуги, маркетплейсы — идут напрямую, без туннеля: так быстрее и не срабатывает антифрод.</li>
      <li><span class="tick"></span>YouTube идёт через туннель с российским выходом: без замедления и без рекламы.</li>
      <li><span class="tick"></span>Всё остальное — через туннель.</li>
    </ul>
  </div>

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

  <button disabled={bundleBusy} onclick={copyReport}>
    {bundleBusy ? 'Готовим…' : bundleState === 'copied' ? 'Отчёт скопирован' : 'Скопировать отчёт для разработчика'}
  </button>
  {#if bundleState !== '' && bundleState !== 'copied'}
    <p class="error">{bundleState}</p>
  {:else}
    <p class="hint">Состояние и хвост журнала — в буфере обмена, отправьте его разработчику.</p>
  {/if}
</div>

<style>
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
</style>
