<!--
  Saturation slider for HSV colour lights. The track shows a
  white-to-fully-saturated gradient at the current hue so the user
  picks how vibrant the colour is rather than which hue. CCU
  SATURATION is a FLOAT in [0, 1].

  Mirrors HA frontend's saturation lane inside ha-color-picker
  (frontend/src/components/ha-color-picker.ts, Apache-2.0).
-->
<script lang="ts">
  type Props = {
    /** Current value in [0, 1]. */
    value: number;
    /** Hue degrees (0-360) so the gradient previews the current colour. */
    hue?: number;
    disabled?: boolean;
    label?: string;
    /** Fires once, on release — every call is a CCU write. */
    onChange: (value: number) => void;
    /** Fires continuously while the user drags. Optional. */
    onInput?: (value: number) => void;
  };

  let {
    value,
    hue = 0,
    disabled = false,
    label,
    onChange,
    onInput,
  }: Props = $props();

  let track: HTMLDivElement;
  let dragging = $state(false);
  // The thumb follows the pointer locally while the drag runs; `onChange` —
  // and with it the CCU write — happens once, on release. Same contract as
  // ControlSlider.
  let pendingValue = $state<number | null>(null);

  function clamp(v: number): number {
    if (v < 0) return 0;
    if (v > 1) return 1;
    return v;
  }
  function quantize(v: number): number {
    return Math.round(clamp(v) * 100) / 100;
  }
  function valueFromPointer(event: PointerEvent): number {
    const rect = track.getBoundingClientRect();
    const ratio = (event.clientX - rect.left) / rect.width;
    return quantize(ratio);
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

  // An ARIA slider that takes focus has to be operable from the keyboard —
  // the track has no native input behind it, so a focused thumb would
  // otherwise be a dead end for keyboard and switch users. Saturation is a
  // 0..1 fraction quantized to 1 %, so that is the arrow-key increment.
  const KEY_STEP = 0.01;

  function onKey(event: KeyboardEvent) {
    if (disabled) return;
    let next = value;
    switch (event.key) {
      case "ArrowRight":
      case "ArrowUp":
        next = quantize(value + KEY_STEP);
        break;
      case "ArrowLeft":
      case "ArrowDown":
        next = quantize(value - KEY_STEP);
        break;
      case "PageUp":
        next = quantize(value + KEY_STEP * 10);
        break;
      case "PageDown":
        next = quantize(value - KEY_STEP * 10);
        break;
      case "Home":
        next = 0;
        break;
      case "End":
        next = 1;
        break;
      default:
        return;
    }
    event.preventDefault();
    if (next !== value) onChange(next);
  }

  const display = $derived(pendingValue ?? value);
  const percent = $derived(display * 100);
</script>

<div
  bind:this={track}
  class="relative h-10 w-full select-none cursor-pointer rounded-md outline-none focus-visible:ring-2 focus-visible:ring-offset-1"
  style="background: linear-gradient(to right, hsl({hue} 0% 100%) 0%, hsl({hue} 100% 50%) 100%);"
  role="slider"
  tabindex={disabled ? -1 : 0}
  aria-label={label}
  aria-valuemin="0"
  aria-valuemax="1"
  aria-valuenow={display}
  aria-disabled={disabled}
  onpointerdown={onPointerDown}
  onpointermove={onPointerMove}
  onpointerup={onPointerUp}
  onpointercancel={onPointerUp}
  onkeydown={onKey}
>
  <div
    class="pointer-events-none absolute top-1/2 h-12 w-1.5 -translate-x-1/2 -translate-y-1/2 rounded-full bg-white shadow-md ring-1 ring-black/20 dark:bg-slate-200 dark:ring-white/30"
    style="left: {percent}%;"
  ></div>
</div>
