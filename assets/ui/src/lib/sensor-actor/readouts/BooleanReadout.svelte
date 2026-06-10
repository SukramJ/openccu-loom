<!--
  BooleanReadout — true/false value as icon + localised label.
  ENUMs with value_list of length 2 (e.g. SHUTTER_CONTACT
  STATE → ["CLOSED", "OPEN"]) flow through here too.
-->
<script lang="ts">
  import type { DataPointSummary } from "$lib/api/types";
  import { dpLabel } from "../classify";
  import { resolveIconLoose } from "$lib/icons";
  import { stateColorFor } from "../state-color";

  type Props = {
    dp: DataPointSummary;
    density?: "comfortable" | "compact";
    showAge?: boolean;
  };

  let { dp, density = "comfortable", showAge = true }: Props = $props();

  const Icon = $derived(resolveIconLoose(dp.ui_hint?.icon));
  const stateColor = $derived(stateColorFor(dp));

  function boolText(v: unknown): string {
    if (v === null || v === undefined) return "—";
    // ENUM with value_list — map bool / index to label
    if (dp.value_list && dp.value_list.length >= 2) {
      const idx = typeof v === "boolean" ? (v ? 1 : 0) : typeof v === "number" ? Math.round(v) : -1;
      if (idx >= 0 && idx < dp.value_list.length) {
        return prettify(dp.value_list[idx]);
      }
    }
    if (typeof v === "boolean") return v ? "An" : "Aus";
    return String(v);
  }

  function prettify(s: string): string {
    return s
      .toLowerCase()
      .split("_")
      .filter(Boolean)
      .map((p) => p[0].toUpperCase() + p.slice(1))
      .join(" ");
  }

  function formatAge(seconds?: number): string {
    if (seconds == null || !Number.isFinite(seconds)) return "";
    if (seconds < 60) return `vor ${Math.floor(seconds)} s`;
    if (seconds < 3600) return `vor ${Math.floor(seconds / 60)} min`;
    if (seconds < 86400) return `vor ${Math.floor(seconds / 3600)} h`;
    return `vor ${Math.floor(seconds / 86400)} d`;
  }

  const display = $derived(boolText(dp.value));
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
    <span class="font-medium" style:color={stateColor || "var(--ha-primary-text-color)"}>{display}</span>
    <span class="text-[var(--ha-secondary-text-color)]">{label}</span>
  </span>
{/if}
