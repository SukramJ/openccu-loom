<!--
  Mirrors HA frontend's hui-numeric-input-card-feature
  (frontend/src/panels/lovelace/card-features/hui-numeric-input-card-feature.ts,
  Apache-2.0). Continuous slider for a writable numeric data point.
  Range is reported as 0..1 for CCU LEVEL semantics; callers can
  override via min/max props.
-->
<script lang="ts">
  import ControlSlider from "../controls/ControlSlider.svelte";

  type Props = {
    value: number;
    min?: number;
    max?: number;
    step?: number;
    /** Multiplier for display only. e.g. 100 to show LEVEL=0.6 as 60 %. */
    displayScale?: number;
    /** Unit suffix in the label (e.g. "%"). */
    unit?: string;
    color: string;
    disabled?: boolean;
    label?: string;
    onChange: (value: number) => void;
  };

  let {
    value,
    min = 0,
    max = 1,
    step = 0.01,
    displayScale = 100,
    unit = "%",
    color,
    disabled = false,
    label,
    onChange,
  }: Props = $props();

  const displayValue = $derived(Math.round((value ?? 0) * displayScale));
</script>

<div class="space-y-1">
  <div class="flex items-baseline justify-between text-xs">
    <span class="text-[var(--ha-secondary-text-color)]">{label ?? ""}</span>
    <span class="font-medium tabular-nums text-[var(--ha-primary-text-color)]">
      {displayValue}{unit}
    </span>
  </div>
  <ControlSlider
    {value}
    {min}
    {max}
    {step}
    {color}
    {disabled}
    {label}
    onChange={(v) => onChange(v)}
  />
</div>
