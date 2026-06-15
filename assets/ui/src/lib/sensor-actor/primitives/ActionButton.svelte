<!--
  ActionButton — write-only action with the pulse + toast pattern from
  the sensor-actor wizard (Q3):
    success: button transitions ⏳ → ✔ → idle (~1.2 s), no toast.
    failure: button returns to idle with a red flash + toast surfacing
             the error.

  The DP is fire-and-forget — the daemon does not return a value; we
  show the pulse on the HTTP request completion.
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
    /** Value to write. Defaults to `true` for typeless / ACTION DPs. */
    value?: unknown;
    icon?: string;
    disabled?: boolean;
  };

  let {
    address,
    channel,
    parameter,
    label,
    value = true,
    icon = "▸",
    disabled = false,
  }: Props = $props();

  type Phase = "idle" | "pending" | "ok" | "err";
  let phase = $state<Phase>("idle");

  async function fire() {
    if (phase !== "idle" || disabled) return;
    phase = "pending";
    try {
      await api.setValue(address, channel, parameter, value);
      phase = "ok";
      setTimeout(() => {
        phase = "idle";
      }, 800);
    } catch (err) {
      phase = "err";
      toastStore.error(t("sensor_actor.action_failed", { name: label }), friendlyError(err, t));
      setTimeout(() => {
        phase = "idle";
      }, 600);
    }
  }

  const visibleIcon = $derived(
    phase === "pending" ? "⏳" : phase === "ok" ? "✔" : phase === "err" ? "✕" : icon,
  );
</script>

<button
  type="button"
  class="inline-flex min-h-10 items-center gap-1 rounded-full border px-3 py-2 text-sm font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50"
  class:phase-pending={phase === "pending"}
  class:phase-ok={phase === "ok"}
  class:phase-err={phase === "err"}
  disabled={disabled || phase !== "idle"}
  onclick={fire}
  aria-label={label}
  style:border-color="var(--ha-divider-color)"
  style:color="var(--ha-primary-text-color)"
>
  <span aria-hidden="true">{visibleIcon}</span>
  <span>{label}</span>
</button>

<style>
  button:not(:disabled):hover {
    background-color: var(--ha-secondary-background-color);
  }
  .phase-ok {
    background-color: var(--ha-success-color);
    color: white !important;
    border-color: var(--ha-success-color) !important;
  }
  .phase-err {
    background-color: var(--ha-error-color);
    color: white !important;
    border-color: var(--ha-error-color) !important;
  }
  .phase-pending {
    opacity: 0.7;
  }
</style>
