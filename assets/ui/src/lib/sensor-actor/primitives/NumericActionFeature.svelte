<!--
  NumericActionFeature — duration / value-typed write-only DPs (ON_TIME,
  BOOST_TIME_PERIOD, …). Wizard decision Q4: inline expand to a free
  number input + Send button (min/max from the DP descriptor). No
  preset pills.

  Render-states:
    collapsed: a single pill "[⏱ <label>]" that expands on click
    expanded:  "[__value__] <unit> [Send]" + min/max hint
  After Send: success pulse on the Send button (same pattern as
  ActionButton), then collapse back to the pill.
-->
<script lang="ts">
  import { api, friendlyError } from "$lib/api/client";
  import { t } from "$lib/i18n";
  import { toastStore } from "$lib/stores/toast.svelte";
  import type { DataPointSummary } from "$lib/api/types";

  type Props = {
    address: string;
    channel: number;
    dp: DataPointSummary;
    label: string;
    icon?: string;
    disabled?: boolean;
  };

  let { address, channel, dp, label, icon = "✎", disabled = false }: Props = $props();

  type Phase = "idle" | "pending" | "ok" | "err";
  let expanded = $state(false);
  let phase = $state<Phase>("idle");
  let entered = $state("");

  // Descriptor min/max — surfaced through a future API extension; for
  // now the input accepts any number and the daemon validates on
  // write. Display "min/max" hint when known.
  const min = $derived<number | undefined>(undefined);
  const max = $derived<number | undefined>(undefined);
  const unit = $derived(dp.unit ?? "");

  function open() {
    if (disabled) return;
    expanded = true;
    phase = "idle";
    if (entered === "") {
      const v = dp.value;
      entered = typeof v === "number" ? String(v) : "";
    }
  }

  function close() {
    expanded = false;
    phase = "idle";
  }

  async function send() {
    if (phase !== "idle") return;
    const num = parseFloat(entered);
    if (Number.isNaN(num)) {
      toastStore.error(
        t("sensor_actor.numeric_invalid", { name: label }),
        t("sensor_actor.numeric_invalid_detail"),
      );
      return;
    }
    phase = "pending";
    try {
      await api.setValue(address, channel, dp.parameter, num);
      phase = "ok";
      setTimeout(() => {
        close();
      }, 700);
    } catch (err) {
      phase = "err";
      toastStore.error(t("sensor_actor.action_failed", { name: label }), friendlyError(err, t));
      setTimeout(() => {
        phase = "idle";
      }, 600);
    }
  }

  function onKey(ev: KeyboardEvent) {
    if (ev.key === "Enter") {
      ev.preventDefault();
      send();
    } else if (ev.key === "Escape") {
      ev.preventDefault();
      close();
    }
  }
</script>

{#if !expanded}
  <button
    type="button"
    class="inline-flex min-h-10 items-center gap-1 rounded-full border px-3 py-2 text-sm font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50"
    disabled={disabled}
    onclick={open}
    aria-label={label}
    style:border-color="var(--ha-divider-color)"
    style:color="var(--ha-primary-text-color)"
  >
    <span aria-hidden="true">{icon}</span>
    <span>{label}</span>
  </button>
{:else}
  <div
    class="flex w-full items-center gap-2 rounded-lg border px-2"
    style:border-color="var(--ha-primary-color)"
    style:background-color="var(--ha-card-background-color)"
  >
    <span class="text-[var(--ha-secondary-text-color)]" aria-hidden="true">{icon}</span>
    <input
      type="number"
      class="h-10 w-16 bg-transparent text-right outline-none"
      style:color="var(--ha-primary-text-color)"
      bind:value={entered}
      onkeydown={onKey}
      aria-label={label}
    />
    {#if unit}
      <span class="text-sm text-[var(--ha-secondary-text-color)]">{unit}</span>
    {/if}
    {#if min !== undefined || max !== undefined}
      <span class="text-[10px] text-[var(--ha-secondary-text-color)]">
        {#if min !== undefined && max !== undefined}
          {min}–{max}
        {:else if min !== undefined}
          ≥ {min}
        {:else}
          ≤ {max}
        {/if}
      </span>
    {/if}
    <button
      type="button"
      class="ml-auto min-h-10 rounded-full px-3 font-medium transition-colors"
      class:phase-pending={phase === "pending"}
      class:phase-ok={phase === "ok"}
      class:phase-err={phase === "err"}
      disabled={phase !== "idle"}
      onclick={send}
      aria-label={t("sensor_actor.send")}
      style:background-color="var(--ha-primary-color)"
      style:color="white"
    >
      {phase === "pending" ? "⏳" : phase === "ok" ? "✔" : phase === "err" ? "✕" : "Send"}
    </button>
    <button
      type="button"
      class="min-h-10 rounded-full px-3 text-[var(--ha-secondary-text-color)] transition-colors hover:text-[var(--ha-primary-text-color)]"
      onclick={close}
      aria-label={t("sensor_actor.cancel")}
    >
      ✕
    </button>
  </div>
{/if}

<style>
  .phase-ok {
    background-color: var(--ha-success-color) !important;
  }
  .phase-err {
    background-color: var(--ha-error-color) !important;
  }
  .phase-pending {
    opacity: 0.7;
  }
</style>
