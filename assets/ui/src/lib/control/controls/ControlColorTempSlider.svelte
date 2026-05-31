<!--
  Mirrors HA frontend's color-temp mode of ha-color-picker
  (frontend/src/components/ha-color-picker.ts, Apache-2.0). The
  track shows a warm-to-cool kelvin gradient; the thumb picks the
  colour temperature.
-->
<script lang="ts">
  type Props = {
    value: number;
    /** Kelvin range bounds. CCU DUAL_WHITE devices typically
     *  expose 2000–6500 K. */
    min?: number;
    max?: number;
    step?: number;
    disabled?: boolean;
    label?: string;
    onChange: (value: number) => void;
  };

  let {
    value,
    min = 2000,
    max = 6500,
    step = 100,
    disabled = false,
    label,
    onChange,
  }: Props = $props();

  let track: HTMLDivElement;
  let dragging = $state(false);

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
    onChange(valueFromPointer(event));
  }
  function onPointerMove(event: PointerEvent) {
    if (!dragging) return;
    onChange(valueFromPointer(event));
  }
  function onPointerUp(event: PointerEvent) {
    if (!dragging) return;
    track.releasePointerCapture(event.pointerId);
    dragging = false;
    onChange(valueFromPointer(event));
  }

  const percent = $derived(((value - min) / (max - min)) * 100);
</script>

<div
  bind:this={track}
  class="relative h-10 w-full select-none cursor-pointer rounded-md outline-none focus-visible:ring-2 focus-visible:ring-offset-1"
  style="background: linear-gradient(to right, #ff8a17 0%, #ffd680 30%, #fff5e2 55%, #d9e6ff 80%, #80b3ff 100%);"
  role="slider"
  tabindex={disabled ? -1 : 0}
  aria-label={label}
  aria-valuemin={min}
  aria-valuemax={max}
  aria-valuenow={value}
  aria-disabled={disabled}
  onpointerdown={onPointerDown}
  onpointermove={onPointerMove}
  onpointerup={onPointerUp}
  onpointercancel={onPointerUp}
>
  <div
    class="pointer-events-none absolute top-1/2 h-12 w-1.5 -translate-x-1/2 -translate-y-1/2 rounded-full bg-white shadow-md ring-1 ring-black/20 dark:bg-slate-200 dark:ring-white/30"
    style="left: {percent}%;"
  ></div>
</div>
