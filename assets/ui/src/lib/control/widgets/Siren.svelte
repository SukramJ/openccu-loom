<!--
  CONTROL widget for the ACOUSTIC_SIGNAL_* / OPTICAL_SIGNAL_* /
  ALARM_SWITCH_VIRTUAL_RECEIVER families. Tile with a pulsing
  alarm-coloured icon + a Test/Off button-group on the dominant
  write slot. Lacking full VALUE_LIST awareness (waiting on the
  REST DTO extension), the toggle flips the first writable
  acoustic/optical-alarm selection between 0 (silent) and 1
  (test alarm).
-->
<script lang="ts">
  import type { DataPointSummary } from "$lib/api/types";
  import type { ResolvedChannel } from "../resolver";
  import ControlTile from "../tile/ControlTile.svelte";
  import ControlTileIcon from "../tile/ControlTileIcon.svelte";
  import ControlTileInfo from "../tile/ControlTileInfo.svelte";
  import ControlButtonGroup from "../controls/ControlButtonGroup.svelte";
  import ControlButton from "../controls/ControlButton.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import { resolveTileColor } from "../state-color";

  type Props = {
    resolved: ResolvedChannel;
    title: string;
    secondary?: string;
    onSetSlot: (slot: string, value: unknown) => void;
  };

  let { resolved, title, secondary, onSetSlot }: Props = $props();

  const dominantSlot = $derived.by(() => {
    const candidates = [
      "ACOUSTIC_ALARM_SELECTION",
      "OPTICAL_ALARM_SELECTION",
      "LEVEL",
    ];
    for (const k of candidates) {
      const dp: DataPointSummary | undefined = resolved.slots[k];
      if (dp && dp.operations.write) return { key: k, dp };
    }
    return null;
  });

  const isActive = $derived(
    dominantSlot ? Boolean(Number(dominantSlot.dp.value)) : false,
  );
  const observed = $derived(dominantSlot?.dp.observed ?? false);
  const tileColor = $derived(
    resolveTileColor("DANGER", isActive, observed),
  );

  const computedSecondary = $derived(
    secondary ?? (isActive ? "Alarm aktiv" : "Ruhe"),
  );
</script>

<ControlTile {tileColor} focused={isActive}>
  {#snippet icon()}
    <ControlTileIcon active={isActive} label={title}>
      <span class:pulse={isActive}>
        <Icon name="mdi:alert-triangle" size={22} />
      </span>
    </ControlTileIcon>
  {/snippet}
  {#snippet info()}
    <ControlTileInfo primary={title} secondary={computedSecondary} />
  {/snippet}
  {#snippet features()}
    {#if dominantSlot}
      <ControlButtonGroup>
        <ControlButton
          active={!isActive}
          color={tileColor}
          label="Aus"
          onClick={() => onSetSlot(dominantSlot.key, 0)}
        >
          Aus
        </ControlButton>
        <ControlButton
          active={isActive}
          color={tileColor}
          label="Test"
          onClick={() => onSetSlot(dominantSlot.key, 1)}
        >
          Test
        </ControlButton>
      </ControlButtonGroup>
    {/if}
  {/snippet}
</ControlTile>

<style>
  .pulse {
    display: inline-block;
    animation: pulse 1s ease-in-out infinite;
  }
  @keyframes pulse {
    0%, 100% { transform: scale(1); }
    50% { transform: scale(1.2); }
  }
</style>
