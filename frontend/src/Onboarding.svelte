<script lang="ts">
  // First-run tour: one short card per main-screen element. Shown automatically until
  // marked as seen, reopenable any time from the header "?" button.
  interface Props {
    onclose: () => void
  }

  let { onclose }: Props = $props()

  const steps = [
    {
      art: 'pulse',
      title: 'Подключение',
      text: 'Большая кнопка подключает и отключает. Под ней — скорость, время сессии и график трафика за последние минуты.',
    },
    {
      art: 'flow',
      title: 'Системный прокси',
      text: 'Направляет браузеры и программы через туннель. При включении сам поднимает подключение. Нажмите на строку — откроется схема работы.',
    },
    {
      art: 'tube',
      title: 'TUN-режим',
      text: 'Забирает вообще весь трафик устройства, включая игры и службы. Требует системный компонент, дружит с kill-switch.',
    },
    {
      art: 'plane',
      title: 'Telegram',
      text: 'Встроенный MTProto-прокси: обычно быстрее и стабильнее основного туннеля. Ссылка для добавления — в Настройках.',
    },
    {
      art: 'bars',
      title: 'Серверы',
      text: 'Список серверов подписки. «Пинг» замеряет все сразу, «Лучший» ставит самый быстрый.',
    },
    {
      art: 'gear',
      title: 'Диагностика и настройки',
      text: 'Диагностика показывает, что именно не открывается. В настройках — тема, порты для ручной настройки и обновления.',
    },
  ]

  let index = $state(0)
  const last = $derived(index === steps.length - 1)
</script>

<svelte:window
  onkeydown={(event) => {
    if (event.key === 'Escape') onclose()
  }}
/>

<div class="overlay" role="presentation">
  <div class="sheet" role="dialog" aria-modal="true" aria-label="Обучение" tabindex="-1">
    {#key index}
      <div class="step">
        <div class="art" aria-hidden="true">
          <span class="halo"></span>
          <span class="halo h2"></span>
          {#if steps[index].art === 'flow'}
            <div class="aglyph">⇄</div>
            <div class="mini-track"><span class="mdot"></span><span class="mdot m2"></span></div>
          {:else if steps[index].art === 'tube'}
            <div class="aglyph">▼</div>
            <div class="mini-tube"></div>
          {:else if steps[index].art === 'plane'}
            <div class="aglyph fly">✈</div>
          {:else if steps[index].art === 'bars'}
            <div class="bars"><span></span><span></span><span></span></div>
          {:else if steps[index].art === 'gear'}
            <div class="aglyph spin">⚙</div>
          {:else}
            <div class="aglyph ping">●</div>
          {/if}
        </div>
        <h2>{steps[index].title}</h2>
        <p class="desc">{steps[index].text}</p>
      </div>
    {/key}

    <div class="dots" aria-hidden="true">
      {#each steps as _, dot (dot)}
        <span class="dot" class:on={dot === index}></span>
      {/each}
    </div>

    <div class="nav">
      {#if index > 0}
        <button onclick={() => (index -= 1)}>Назад</button>
      {:else}
        <button class="ghost" onclick={onclose}>Пропустить</button>
      {/if}
      <div class="spacer"></div>
      {#if last}
        <button class="primary inline" onclick={onclose}>Понятно</button>
      {:else}
        <button class="primary inline" onclick={() => (index += 1)}>Далее</button>
      {/if}
    </div>
  </div>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 60;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 16px;
    background: rgb(0 0 0 / 0.45);
    animation: fade-in 0.15s ease-out;
  }

  .sheet {
    width: 100%;
    max-width: 360px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 18px;
    padding: 22px 18px 16px;
    text-align: center;
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

  .step {
    animation: step-in 0.22s ease-out;
  }

  @keyframes step-in {
    from {
      opacity: 0;
      transform: translateX(14px);
    }
  }

  .art {
    position: relative;
    height: 122px;
    margin-bottom: 12px;
    border-radius: var(--radius);
    border: 1px solid var(--border);
    background: var(--surface-alt);
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 10px;
    overflow: hidden;
  }

  .halo {
    position: absolute;
    top: 50%;
    left: 50%;
    width: 64px;
    height: 64px;
    margin: -32px 0 0 -32px;
    border-radius: 50%;
    border: 2px solid var(--accent);
    opacity: 0;
    animation: halo 2.6s ease-out infinite;
  }

  .halo.h2 {
    animation-delay: 1.3s;
  }

  @keyframes halo {
    0% {
      transform: scale(0.5);
      opacity: 0.45;
    }
    100% {
      transform: scale(1.5);
      opacity: 0;
    }
  }

  .aglyph {
    width: 46px;
    height: 46px;
    border-radius: 14px;
    background: var(--accent);
    color: var(--accent-text);
    font-size: 22px;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .aglyph.ping {
    animation: ping-soft 2s ease-in-out infinite;
  }

  @keyframes ping-soft {
    0%,
    100% {
      transform: scale(1);
    }
    50% {
      transform: scale(1.08);
    }
  }

  .aglyph.fly {
    animation: drift 3s ease-in-out infinite;
  }

  @keyframes drift {
    0%,
    100% {
      transform: translate(0, 0);
    }
    50% {
      transform: translate(10px, -8px);
    }
  }

  .aglyph.spin {
    animation: spin-slow 12s linear infinite;
  }

  @keyframes spin-slow {
    to {
      transform: rotate(360deg);
    }
  }

  .mini-track {
    position: relative;
    width: 70%;
    height: 2px;
    border-radius: 2px;
    background-image: repeating-linear-gradient(
      90deg,
      var(--accent) 0 6px,
      transparent 6px 12px
    );
    animation: dash-slide 0.7s linear infinite;
  }

  @keyframes dash-slide {
    to {
      background-position-x: -12px;
    }
  }

  .mdot {
    position: absolute;
    top: 50%;
    left: 0;
    width: 7px;
    height: 7px;
    margin-top: -3.5px;
    border-radius: 50%;
    background: var(--accent);
    animation: traverse 1.8s linear infinite;
  }

  .mdot.m2 {
    animation-delay: 0.9s;
  }

  @keyframes traverse {
    from {
      left: 0;
      opacity: 0;
    }
    15% {
      opacity: 1;
    }
    85% {
      opacity: 1;
    }
    to {
      left: calc(100% - 7px);
      opacity: 0;
    }
  }

  .mini-tube {
    width: 70%;
    height: 20px;
    border-radius: 999px;
    border: 1px solid var(--border);
    background-color: var(--surface);
    background-image: repeating-linear-gradient(
      -55deg,
      var(--accent) 0 6px,
      transparent 6px 14px
    );
    animation: tube-slide 1s linear infinite;
  }

  @keyframes tube-slide {
    to {
      background-position: 20px 0;
    }
  }

  .bars {
    display: flex;
    align-items: flex-end;
    gap: 7px;
    height: 44px;
  }

  .bars span {
    width: 12px;
    border-radius: 6px;
    background: var(--accent);
    animation: bar-pulse 1.6s ease-in-out infinite;
  }

  .bars span:nth-child(1) {
    height: 20px;
  }

  .bars span:nth-child(2) {
    height: 34px;
    animation-delay: 0.25s;
  }

  .bars span:nth-child(3) {
    height: 44px;
    animation-delay: 0.5s;
  }

  @keyframes bar-pulse {
    0%,
    100% {
      opacity: 0.45;
    }
    50% {
      opacity: 1;
    }
  }

  .sheet h2 {
    font-size: 17px;
    margin: 0 0 8px;
  }

  .desc {
    color: var(--text-dim);
    font-size: 13.5px;
    margin: 0;
    min-height: 66px;
  }

  .dots {
    display: flex;
    justify-content: center;
    gap: 6px;
    margin: 14px 0;
  }

  .dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--border);
  }

  .dot.on {
    background: var(--accent);
  }

  .nav {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  button.primary.inline {
    width: auto;
  }

  @media (prefers-reduced-motion: reduce) {
    .overlay,
    .sheet,
    .step,
    .halo,
    .aglyph,
    .mini-track,
    .mdot,
    .mini-tube,
    .bars span {
      animation: none;
    }
  }
</style>
