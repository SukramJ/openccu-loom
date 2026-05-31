<!--
  Mirrors HA frontend's hui-climate-preset-modes-card-feature
  (frontend/src/panels/lovelace/card-features/hui-climate-preset-modes-card-feature.ts,
  Apache-2.0). Action buttons for boolean preset slots (BOOST_MODE,
  PARTY_MODE, FROST_PROTECTION, …). Single click toggles the slot.
-->
<script lang="ts">
  import ControlButtonGroup from "../controls/ControlButtonGroup.svelte";
  import ControlButton from "../controls/ControlButton.svelte";

  type Preset = {
    /** Slot suffix or any unique key. */
    key: string;
    /** Display label. */
    label: string;
    /** Current value (true = active). */
    value: boolean;
    /** Writable? false → render disabled. */
    writable: boolean;
  };

  type Props = {
    presets: Preset[];
    color: string;
    onToggle: (key: string, next: boolean) => void;
  };

  let { presets, color, onToggle }: Props = $props();
</script>

{#if presets.length > 0}
  <ControlButtonGroup>
    {#each presets as preset (preset.key)}
      <ControlButton
        active={preset.value}
        {color}
        disabled={!preset.writable}
        label={preset.label}
        onClick={() => onToggle(preset.key, !preset.value)}
      >
        {preset.label}
      </ControlButton>
    {/each}
  </ControlButtonGroup>
{/if}
