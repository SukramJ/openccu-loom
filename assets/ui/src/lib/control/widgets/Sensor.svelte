<!--
  Generic read-only widget for sensor-style CONTROL families
  (BRIGHTNESS_TRANSMITTER, FLOW_METER_TRANSMITTER, TEMP,
  WEATHER_TRANSMIT, RAIN_DETECTION_TRANSMITTER, …). Renders a tile
  with a state-coloured icon and a StatReadout grid covering each
  slot the channel exposes. No write controls.
-->
<script lang="ts">
  import type { ResolvedChannel } from "../resolver";
  import ControlTile from "../tile/ControlTile.svelte";
  import ControlTileIcon from "../tile/ControlTileIcon.svelte";
  import ControlTileInfo from "../tile/ControlTileInfo.svelte";
  import StatReadoutFeature from "../features/StatReadoutFeature.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import { resolveTileColor } from "../state-color";

  type Props = {
    resolved: ResolvedChannel;
    title: string;
    secondary?: string;
    /** Glyph rendered in the tile icon. Family-specific (e.g. ☀ for
     *  brightness, ☔ for rain). Defaults to "📊"; widgets can pass
     *  a more specific Unicode glyph or wrap the snippet in an
     *  Icon component when an mdi mapping fits. */
    glyph?: string;
    /** Optional per-slot unit overrides; falls back to "". */
    units?: Record<string, string>;
    /** Optional per-slot label overrides; falls back to the slot suffix. */
    labels?: Record<string, string>;
  };

  let { resolved, title, secondary, glyph = "📊", units, labels }: Props = $props();

  const entries = $derived(
    Object.entries(resolved.slots).map(([slot, dp]) => ({
      slot,
      dp,
      unit: units?.[slot] ?? "",
      label: labels?.[slot] ?? slot.replace(/_/g, " ").toLowerCase(),
    })),
  );

  const firstNumeric = $derived(
    entries.find((e) => typeof e.dp.value === "number"),
  );
  const tileColor = $derived(
    resolveTileColor(
      resolved.family,
      firstNumeric?.dp.value ?? null,
      firstNumeric?.dp.observed ?? false,
    ),
  );

  const computedSecondary = $derived.by(() => {
    if (secondary) return secondary;
    if (firstNumeric && typeof firstNumeric.dp.value === "number") {
      return `${firstNumeric.dp.value.toFixed(1)}${firstNumeric.unit ? " " + firstNumeric.unit : ""}`;
    }
    return "—";
  });
</script>

<ControlTile {tileColor}>
  {#snippet icon()}
    <ControlTileIcon active={firstNumeric?.dp.observed ?? false} label={title}>
      {#if glyph === "📊"}
        <Icon name="mdi:gauge" size={22} />
      {:else}
        <span aria-hidden="true">{glyph}</span>
      {/if}
    </ControlTileIcon>
  {/snippet}
  {#snippet info()}
    <ControlTileInfo primary={title} secondary={computedSecondary} />
  {/snippet}
  {#snippet features()}
    {#if entries.length > 1}
      <div class="grid grid-cols-2 gap-2">
        {#each entries as e (e.slot)}
          <StatReadoutFeature label={e.label} value={e.dp.value} unit={e.unit} />
        {/each}
      </div>
    {/if}
  {/snippet}
</ControlTile>
