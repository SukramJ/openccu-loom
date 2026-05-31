<!--
  Mirrors HA frontend's ha-control-slider
  (frontend/src/components/ha-control-slider.ts, Apache-2.0). Same
  visual rhythm: 40 px-thick track, rounded-md radius, 20 % opacity
  background, fill driven by a brand/state colour. Reimplemented in
  Svelte 5 — no Lit code reproduced.
-->
<script lang="ts">
  import { cn } from "$lib/utils";

  type Props = {
    value: number;
    min?: number;
    max?: number;
    step?: number;
    /** CSS colour expression for the fill (e.g. `var(--state-light-active-color)`). */
    color?: string;
    disabled?: boolean;
    /** Used for aria-label so screen readers announce the slider purpose. */
    label?: string;
    onChange: (value: number) => void;
    /** Fires continuously while the user drags. Optional. */
    onInput?: (value: number) => void;
  };

  let {
    value,
    min = 0,
    max = 100,
    step = 1,
    color = "var(--ha-primary-color)",
    disabled = false,
    label,
    onChange,
    onInput,
  }: Props = $props();

  let track: HTMLDivElement;
  let dragging = $state(false);
  let pendingValue = $state<number | null>(null);

  const display = $derived(pendingValue ?? value);
  const percent = $derived(((display - min) / (max - min)) * 100);

  function clamp(v: number): number {
    if (v < min) return min;
    if (v > max) return max;
    return v;
  }

  function quantize(v: number): number {
    return Math.round(clamp(v) / step) * step;
  }

  function valueFromPointer(event: PointerEvent): number {
    const rect = track.getBoundingClientRect();
    const ratio = (event.clientX - rect.left) / rect.width;
    return quantize(min + ratio * (max - min));
  }

  function onPointerDown(event: PointerEvent) {
    if (disabled) return;
    track.setPointerCapture(event.pointerId);
    dragging = true;
    const v = valueFromPointer(event);
    pendingValue = v;
    onInput?.(v);
  }

  function onPointerMove(event: PointerEvent) {
    if (!dragging) return;
    const v = valueFromPointer(event);
    pendingValue = v;
    onInput?.(v);
  }

  function onPointerUp(event: PointerEvent) {
    if (!dragging) return;
    track.releasePointerCapture(event.pointerId);
    const v = valueFromPointer(event);
    dragging = false;
    pendingValue = null;
    onChange(v);
  }

  function onKey(event: KeyboardEvent) {
    if (disabled) return;
    let next = value;
    switch (event.key) {
      case "ArrowRight":
      case "ArrowUp":
        next = quantize(value + step);
        break;
      case "ArrowLeft":
      case "ArrowDown":
        next = quantize(value - step);
        break;
      case "PageUp":
        next = quantize(value + step * 10);
        break;
      case "PageDown":
        next = quantize(value - step * 10);
        break;
      case "Home":
        next = min;
        break;
      case "End":
        next = max;
        break;
      default:
        return;
    }
    event.preventDefault();
    if (next !== value) onChange(next);
  }
</script>

<div
  bind:this={track}
  class={cn(
    "relative h-10 w-full select-none rounded-md outline-none focus-visible:ring-2 focus-visible:ring-offset-1 transition-shadow",
    disabled ? "opacity-50 cursor-not-allowed" : "cursor-pointer",
  )}
  style="background-color: color-mix(in srgb, {color} 20%, transparent);"
  role="slider"
  tabindex={disabled ? -1 : 0}
  aria-label={label}
  aria-valuemin={min}
  aria-valuemax={max}
  aria-valuenow={Math.round(display)}
  aria-disabled={disabled}
  onpointerdown={onPointerDown}
  onpointermove={onPointerMove}
  onpointerup={onPointerUp}
  onpointercancel={onPointerUp}
  onkeydown={onKey}
>
  <div
    class="absolute inset-y-0 left-0 rounded-md transition-[width] duration-100"
    style="width: {percent}%; background-color: {color};"
  ></div>
</div>
