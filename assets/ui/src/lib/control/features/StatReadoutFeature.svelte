<!--
  Compact read-only stat row for a single measurement slot
  (TEMPERATURE, HUMIDITY, POWER, VOLTAGE, CURRENT, …). Two-column
  grid: label / formatted-value-with-unit. Multiple StatReadouts
  inside a tile align via the parent grid.

  Inspired by HA's badges-row pattern in
  frontend/src/panels/lovelace/cards/tile/badges/tile-badge*.ts
  (Apache-2.0).
-->
<script lang="ts">
  import type { Snippet } from "svelte";

  type Props = {
    label: string;
    value: unknown;
    unit?: string;
    /** Number-formatting fn; defaults to toString. */
    format?: (v: unknown) => string;
    icon?: Snippet;
  };

  let { label, value, unit, format, icon }: Props = $props();

  const formatted = $derived.by(() => {
    if (value == null) return "—";
    if (format) return format(value);
    if (typeof value === "number") return value.toFixed(1);
    return String(value);
  });
</script>

<div class="flex items-center gap-2 rounded-md bg-[color:color-mix(in_srgb,var(--ha-secondary-text-color)_8%,transparent)] px-3 py-2">
  {#if icon}
    <span class="text-[var(--ha-secondary-text-color)]">{@render icon()}</span>
  {/if}
  <div class="min-w-0 flex-1">
    <div class="text-xs text-[var(--ha-secondary-text-color)]">{label}</div>
    <div class="truncate text-sm font-medium tabular-nums text-[var(--ha-primary-text-color)]">
      {formatted}{#if unit}<span class="ml-0.5 text-[var(--ha-secondary-text-color)]">{unit}</span>{/if}
    </div>
  </div>
</div>
