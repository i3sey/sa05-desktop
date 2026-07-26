<script lang="ts">
  // A toggle that reflects backend state only after the backend confirms it: an optimistic
  // switch would show "TUN on" while the tunnel actually failed to come up.
  interface Props {
    checked: boolean
    disabled?: boolean
    label: string
    onchange: (next: boolean) => void
  }

  let { checked, disabled = false, label, onchange }: Props = $props()
</script>

<button
  class="switch"
  role="switch"
  aria-checked={checked}
  aria-label={label}
  {disabled}
  onclick={() => onchange(!checked)}
></button>
