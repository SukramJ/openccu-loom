<!--
  Mirrors HA frontend's hui-target-temperature-card-feature
  (frontend/src/panels/lovelace/card-features/hui-target-temperature-card-feature.ts,
  Apache-2.0). Large stepper for the SETPOINT slot.
-->
<script lang="ts">
  import ControlNumberButtons from "../controls/ControlNumberButtons.svelte";

  type Props = {
    value: number;
    color: string;
    min?: number;
    max?: number;
    step?: number;
    unit?: string;
    disabled?: boolean;
    onChange: (value: number) => void;
  };

  let {
    value,
    color,
    min = 4.5,
    max = 30.5,
    step = 0.5,
    unit = "°C",
    disabled = false,
    onChange,
  }: Props = $props();
</script>

<ControlNumberButtons {value} {color} {min} {max} {step} {disabled} {onChange}>
  {#snippet display(v)}
    <span>{v.toFixed(1)}</span>
    <span class="text-sm text-[var(--ha-secondary-text-color)]">{unit}</span>
  {/snippet}
</ControlNumberButtons>
