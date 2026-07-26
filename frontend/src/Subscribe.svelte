<script lang="ts">
  // First-run gate: without a subscription that returns at least one valid profile the
  // client refuses to connect, exactly like the Android version.
  interface Props {
    busy: boolean
    error: string
    onimport: (url: string) => void
  }

  let { busy, error, onimport }: Props = $props()
  let url = $state('')
</script>

<div class="head">
  <h1>SA05</h1>
</div>

<div class="screen">
  <div class="card status">
    <h2>Добавьте подписку</h2>
    <p class="desc">
      Вставьте HTTPS-ссылку подписки или ссылку вида <span class="mono">sa05://add/…</span>
    </p>
    <input
      type="url"
      bind:value={url}
      placeholder="https://…"
      spellcheck="false"
      autocomplete="off"
    />
    <button class="primary" disabled={busy || url.trim() === ''} onclick={() => onimport(url.trim())}>
      {busy ? 'Проверяем…' : 'Импортировать'}
    </button>
    {#if error}<p class="error">{error}</p>{/if}
  </div>
  <p class="hint">
    Ссылка хранится только на этом компьютере, в файле настроек с правами 0600.
  </p>
</div>
