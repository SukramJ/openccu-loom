<script lang="ts">
  // A small SVG progress ring for the exit/entry-delay countdown on the
  // alarm Overview (docs/alarm-concept.md §12.1). Purely presentational:
  // the parent feeds `remaining`/`total` seconds from the alarm store's
  // client-decayed countdown; this component draws the arc + the seconds
  // label. Colours come from the theme tokens so it inverts correctly in
  // all four skin×scheme combos.

  type Tone = "neutral" | "danger";

  type Props = {
    remaining: number;
    total: number;
    /** Outer diameter in px. */
    size?: number;
    /** neutral (exit delay, primary colour) or danger (entry delay, red). */
    tone?: Tone;
  };

  let { remaining, total, size = 72, tone = "neutral" }: Props = $props();

  const stroke = $derived(Math.max(4, Math.round(size / 12)));
  const radius = $derived((size - stroke) / 2);
  const circumference = $derived(2 * Math.PI * radius);

  // Clamp so a late/negative tick or an over-run cannot draw a broken arc.
  const fraction = $derived(
    total > 0 ? Math.min(1, Math.max(0, remaining / total)) : 0,
  );
  const dashOffset = $derived(circumference * (1 - fraction));
  const seconds = $derived(Math.max(0, Math.ceil(remaining)));

  const arcColor = $derived(
    tone === "danger" ? "var(--ha-error-color)" : "var(--ha-primary-color)",
  );
</script>

<div
  class="relative inline-flex items-center justify-center"
  style="width: {size}px; height: {size}px;"
  role="timer"
  aria-label={`${seconds}s`}
>
  <svg
    width={size}
    height={size}
    viewBox={`0 0 ${size} ${size}`}
    class="-rotate-90"
    aria-hidden="true"
  >
    <circle
      cx={size / 2}
      cy={size / 2}
      r={radius}
      fill="none"
      stroke="var(--ha-divider-color)"
      stroke-width={stroke}
    />
    <circle
      cx={size / 2}
      cy={size / 2}
      r={radius}
      fill="none"
      stroke={arcColor}
      stroke-width={stroke}
      stroke-linecap="round"
      stroke-dasharray={circumference}
      stroke-dashoffset={dashOffset}
      style="transition: stroke-dashoffset 1s linear;"
    />
  </svg>
  <span
    class="absolute font-semibold tabular-nums"
    style="font-size: {Math.round(size / 3.2)}px; color: var(--ha-primary-text-color);"
  >
    {seconds}
  </span>
</div>
