<!--
  EventReadout — momentary event DPs (ACTION-typed, e.g. a remote's
  PRESS_SHORT / PRESS_LONG). These carry no meaningful steady-state
  value — the wire boolean is just the last edge — so rendering it as
  "false" / "Aus" is misleading. Show the event's name with the time
  it last fired instead.
-->
<script lang="ts">
  import type { DataPointSummary } from "$lib/api/types";
  import { dpLabel } from "../classify";
  import { resolveIconLoose } from "$lib/icons";
  import { t } from "$lib/i18n";

  type Props = {
    dp: DataPointSummary;
    density?: "comfortable" | "compact";
    showAge?: boolean;
  };

  let { dp, density = "comfortable", showAge = true }: Props = $props();

  const Icon = $derived(resolveIconLoose(dp.ui_hint?.icon));

  function formatAge(seconds?: number): string {
    if (seconds == null || !Number.isFinite(seconds)) return "";
    if (seconds < 60) return `vor ${Math.floor(seconds)} s`;
    if (seconds < 3600) return `vor ${Math.floor(seconds / 60)} min`;
    if (seconds < 86400) return `vor ${Math.floor(seconds / 3600)} h`;
    return `vor ${Math.floor(seconds / 86400)} d`;
  }

  const label = $derived(dpLabel(dp));
  const age = $derived(showAge ? formatAge(dp.value_age_seconds) : "");
  // Secondary line: when the event last fired, else an idle hint so the
  // row never reads as a stale boolean state.
  const sub = $derived(age ? t("sensor_actor.event_last", { age }) : t("sensor_actor.event_idle"));
</script>

{#if density === "comfortable"}
  <div class="flex items-center gap-2">
    <Icon size={20} color="var(--ha-secondary-text-color)" />
    <div class="flex min-w-0 flex-col">
      <span class="truncate text-base font-medium text-[var(--ha-primary-text-color)]">{label}</span>
      <span class="text-[11px] text-[var(--ha-secondary-text-color)]">{sub}</span>
    </div>
  </div>
{:else}
  <span class="inline-flex items-baseline gap-1 text-xs">
    <Icon size={12} color="var(--ha-secondary-text-color)" />
    <span class="font-medium text-[var(--ha-primary-text-color)]">{label}</span>
    <span class="text-[var(--ha-secondary-text-color)]">{sub}</span>
  </span>
{/if}
