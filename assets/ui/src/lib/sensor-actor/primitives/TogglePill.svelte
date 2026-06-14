<!--
  TogglePill — single-button toggle for a read+write bool DP.
  On tap, optimistically flips state and writes via api.setValue.
  On failure, reverts and bubbles the error to the parent.
-->
<script lang="ts">
  import { api, friendlyError } from "$lib/api/client";
  import { t } from "$lib/i18n";
  import { toastStore } from "$lib/stores/toast.svelte";

  type Props = {
    address: string;
    channel: number;
    parameter: string;
    label: string;
    value: boolean;
    disabled?: boolean;
  };

  let { address, channel, parameter, label, value, disabled = false }: Props = $props();

  let busy = $state(false);
  let optimistic = $state<boolean | null>(null);

  const displayValue = $derived(optimistic ?? value);

  async function toggle() {
    if (busy || disabled) return;
    const next = !displayValue;
    busy = true;
    optimistic = next;
    try {
      await api.setValue(address, channel, parameter, next);
      // Wait for the WS echo to confirm; the parent listens and
      // updates the underlying `value` prop, at which point we drop
      // the optimistic override.
      setTimeout(() => {
        if (optimistic === next) optimistic = null;
      }, 1500);
    } catch (err) {
      optimistic = null;
      toastStore.error(t("sensor_actor.toggle_failed", { name: label }), friendlyError(err, t));
    } finally {
      busy = false;
    }
  }
</script>

<button
  type="button"
  class="inline-flex min-h-10 items-center gap-1 rounded-full border px-3 py-2 text-sm font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50"
  class:active={displayValue}
  disabled={disabled || busy}
  onclick={toggle}
  aria-pressed={displayValue}
  aria-label={label}
  style:background-color={displayValue ? "var(--ha-primary-color)" : "transparent"}
  style:color={displayValue ? "white" : "var(--ha-primary-text-color)"}
  style:border-color="var(--ha-divider-color)"
>
  <span aria-hidden="true">{displayValue ? "⏻" : "○"}</span>
  <span>{label}</span>
</button>

<style>
  button:not(.active):not(:disabled):hover {
    background-color: var(--ha-secondary-background-color);
  }
</style>
