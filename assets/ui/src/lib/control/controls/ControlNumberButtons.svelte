<!--
  Mirrors HA frontend's ha-control-number-buttons
  (frontend/src/components/ha-control-number-buttons.ts, Apache-2.0).
  −  [VALUE]  +  layout, large tap targets. The middle slot displays
  the formatted value passed via the snippet so unit handling stays
  with the caller (°C, %, etc.).
-->
<script lang="ts">
  import type { Snippet } from "svelte";
  import ControlButton from "./ControlButton.svelte";

  type Props = {
    value: number;
    color: string;
    min?: number;
    max?: number;
    step?: number;
    disabled?: boolean;
    onChange: (value: number) => void;
    /** Display slot. Receives the current value; render the formatted
     *  representation. */
    display: Snippet<[number]>;
  };

  let {
    value,
    color,
    min = -Infinity,
    max = Infinity,
    step = 1,
    disabled = false,
    onChange,
    display,
  }: Props = $props();

  function clamp(v: number): number {
    if (v < min) return min;
    if (v > max) return max;
    return v;
  }

  function quantize(v: number): number {
    return Math.round(clamp(v) / step) * step;
  }
</script>

<div class="flex h-10 w-full items-center gap-3">
  <div class="w-14">
    <ControlButton
      {color}
      {disabled}
      label="Verringern"
      onClick={() => onChange(quantize(value - step))}
    >
      <span aria-hidden="true" class="text-lg leading-none">−</span>
    </ControlButton>
  </div>
  <div class="flex flex-1 items-baseline justify-center gap-1 text-2xl font-medium tabular-nums text-[var(--ha-primary-text-color)]">
    {@render display(value)}
  </div>
  <div class="w-14">
    <ControlButton
      {color}
      {disabled}
      label="Erhöhen"
      onClick={() => onChange(quantize(value + step))}
    >
      <span aria-hidden="true" class="text-lg leading-none">+</span>
    </ControlButton>
  </div>
</div>
