<!--
  Eight-colour picker for CCU fixed-colour lights (HmIP-BSL,
  HmIPW-WGC, HM-LC-RGBW-WM). The CCU's COLOR enum carries the eight
  named colours BLACK / BLUE / GREEN / TURQUOISE / RED / PURPLE /
  YELLOW / WHITE plus the two non-user-facing sentinels OLD_VALUE +
  DO_NOT_CARE — those are filtered out before render.

  Visual idiom from HA frontend's hui-light-color-picker
  (frontend/src/dialogs/more-info/components/lights/light-color-picker.ts,
  Apache-2.0): row of round colour chips with an active outline.
-->
<script lang="ts">
  type ColorOption = { name: string; css: string };

  type Props = {
    /** Current CCU COLOR enum label (e.g. "RED"). */
    value: string | number | undefined;
    /** Full VALUE_LIST as published by the descriptor — the widget
     *  filters OLD_VALUE / DO_NOT_CARE and any non-known label. */
    options: string[];
    disabled?: boolean;
    label?: string;
    onChange: (next: string) => void;
  };

  let { value, options, disabled = false, label, onChange }: Props = $props();

  const PALETTE: Record<string, string> = {
    BLACK: "#1f1f1f",
    BLUE: "#1e63ff",
    GREEN: "#1ec96f",
    TURQUOISE: "#1eb8c9",
    RED: "#ff3b3b",
    PURPLE: "#a155ff",
    YELLOW: "#ffd23a",
    WHITE: "#ffffff",
  };
  const SKIP = new Set(["OLD_VALUE", "DO_NOT_CARE"]);

  const palette = $derived<ColorOption[]>(
    options
      .filter((o) => !SKIP.has(o) && o in PALETTE)
      .map((o) => ({ name: o, css: PALETTE[o] })),
  );

  // When the descriptor publishes the value as an int (enum index),
  // resolve back to the label via the VALUE_LIST.
  const currentLabel = $derived<string>(
    typeof value === "number"
      ? (options[value] ?? "")
      : ((value as string | undefined) ?? ""),
  );
</script>

<div class="flex flex-col gap-1">
  {#if label}
    <span class="text-xs text-[var(--ha-secondary-text-color)]">{label}</span>
  {/if}
  <div class="flex flex-wrap items-center gap-2" role="radiogroup" aria-label={label}>
    {#each palette as opt (opt.name)}
      <button
        type="button"
        class="h-11 w-11 rounded-full border-2 transition-transform focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-offset-1"
        class:scale-110={opt.name === currentLabel}
        style="background-color: {opt.css}; border-color: {opt.name === currentLabel ? 'var(--ha-primary-color)' : 'rgba(0,0,0,0.15)'};"
        role="radio"
        aria-checked={opt.name === currentLabel}
        aria-label={opt.name}
        {disabled}
        onclick={() => onChange(opt.name)}
      ></button>
    {/each}
  </div>
</div>
