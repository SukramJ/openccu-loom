<!--
  EnumReadout — ENUM-typed value, displayed as the value_list entry
  at the numeric index. value_list tokens are localised via
  enumValueText (i18n `enum.<TOKEN>`, then the data point's
  server-resolved `value_translations`), falling back to a title-cased
  form so `INTRUSION_ALARM` still surfaces as "Intrusion Alarm".
-->
<script lang="ts">
  import type { DataPointSummary } from "$lib/api/types";
  import { dpLabel, enumValueText } from "../classify";
  import { formatValueAge } from "../age";
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

  function enumText(v: unknown): string {
    if (v === null || v === undefined) return "—";
    if (!dp.value_list || dp.value_list.length === 0) return String(v);
    let idx: number;
    if (typeof v === "number") idx = Math.round(v);
    else if (typeof v === "boolean") idx = v ? 1 : 0;
    else return String(v);
    if (idx < 0 || idx >= dp.value_list.length) return String(v);
    return enumValueText(dp.value_list[idx], dp.value_translations);
  }

  const display = $derived(enumText(dp.value));
  const label = $derived(dpLabel(dp));
  const age = $derived(showAge ? formatValueAge(dp.value_age_seconds) : "");
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
