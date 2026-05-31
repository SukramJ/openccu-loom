<!--
  StringReadout — text-typed DPs (IP_ADDRESS, FIRMWARE_VERSION, …)
  rendered as a mono-styled label-value pair.
-->
<script lang="ts">
  import type { DataPointSummary } from "$lib/api/types";
  import { resolveIconLoose } from "$lib/icons";

  type Props = {
    dp: DataPointSummary;
    density?: "comfortable" | "compact";
    showAge?: boolean;
  };

  let { dp, density = "comfortable", showAge = true }: Props = $props();

  const Icon = $derived(resolveIconLoose(dp.ui_hint?.icon));

  function asString(v: unknown): string {
    if (v === null || v === undefined) return "—";
    return String(v);
  }

  function formatAge(seconds?: number): string {
    if (seconds == null || !Number.isFinite(seconds)) return "";
    if (seconds < 60) return `vor ${Math.floor(seconds)} s`;
    if (seconds < 3600) return `vor ${Math.floor(seconds / 60)} min`;
    if (seconds < 86400) return `vor ${Math.floor(seconds / 3600)} h`;
    return `vor ${Math.floor(seconds / 86400)} d`;
  }

  const display = $derived(asString(dp.value));
  const label = $derived(dp.parameter_label || dp.parameter);
  const age = $derived(showAge ? formatAge(dp.value_age_seconds) : "");
</script>

{#if density === "comfortable"}
  <div class="flex items-center gap-2">
    <Icon size={20} color="var(--ha-secondary-text-color)" />
    <div class="flex min-w-0 flex-col">
      <span class="truncate font-mono text-base text-[var(--ha-primary-text-color)]">{display}</span>
      <span class="text-[11px] text-[var(--ha-secondary-text-color)]">
        {label}{#if age} · {age}{/if}
      </span>
    </div>
  </div>
{:else}
  <span class="inline-flex items-baseline gap-1 text-xs">
    <Icon size={12} color="var(--ha-secondary-text-color)" />
    <span class="font-mono font-medium text-[var(--ha-primary-text-color)]">{display}</span>
    <span class="text-[var(--ha-secondary-text-color)]">{label}</span>
  </span>
{/if}
