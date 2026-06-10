<!--
  NumericReadout — number value + unit, optional icon, optional age
  stamp + state-color tint. Used by AutoTile for both the headline
  (large variant) and secondary readouts (compact variant).
-->
<script lang="ts">
  import type { DataPointSummary } from "$lib/api/types";
  import { dpLabel } from "../classify";
  import { resolveIconLoose } from "$lib/icons";
  import { stateColorFor } from "../state-color";

  type Props = {
    dp: DataPointSummary;
    /** comfortable = big value + label, compact = inline */
    density?: "comfortable" | "compact";
    /** show "vor 3 min" timestamp */
    showAge?: boolean;
  };

  let { dp, density = "comfortable", showAge = true }: Props = $props();

  const Icon = $derived(resolveIconLoose(dp.ui_hint?.icon));
  const stateColor = $derived(stateColorFor(dp));

  function formatNumber(v: unknown, unit?: string): string {
    if (typeof v !== "number" || !Number.isFinite(v)) return "—";
    const t = (dp.type ?? "").toUpperCase();
    let s: string;
    if (t === "INTEGER" || Number.isInteger(v)) {
      s = String(Math.round(v));
    } else {
      const abs = Math.abs(v);
      const frac = abs >= 100 ? 0 : abs >= 10 ? 1 : 2;
      s = v.toLocaleString(undefined, {
        minimumFractionDigits: 0,
        maximumFractionDigits: frac,
      });
    }
    return unit ? `${s} ${unit}` : s;
  }

  function formatAge(seconds?: number): string {
    if (seconds == null || !Number.isFinite(seconds)) return "";
    if (seconds < 60) return `vor ${Math.floor(seconds)} s`;
    if (seconds < 3600) return `vor ${Math.floor(seconds / 60)} min`;
    if (seconds < 86400) return `vor ${Math.floor(seconds / 3600)} h`;
    return `vor ${Math.floor(seconds / 86400)} d`;
  }

  const display = $derived(formatNumber(dp.value, dp.unit));
  const label = $derived(dpLabel(dp));
  const age = $derived(showAge ? formatAge(dp.value_age_seconds) : "");
</script>

{#if density === "comfortable"}
  <div class="flex items-center gap-2">
    <Icon size={20} color={stateColor || "var(--ha-secondary-text-color)"} />
    <div class="flex min-w-0 flex-col">
      <span class="truncate text-lg font-medium" style:color={stateColor || "var(--ha-primary-text-color)"}>
        {display}
      </span>
      <span class="text-[11px] text-[var(--ha-secondary-text-color)]">
        {label}{#if age} · {age}{/if}
      </span>
    </div>
  </div>
{:else}
  <span class="inline-flex items-baseline gap-1 text-xs">
    <Icon size={12} color={stateColor || "var(--ha-secondary-text-color)"} />
    <span class="font-medium" style:color={stateColor || "var(--ha-primary-text-color)"}>
      {display}
    </span>
    <span class="text-[var(--ha-secondary-text-color)]">{label}</span>
  </span>
{/if}
