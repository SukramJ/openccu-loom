<script lang="ts">
  import type { SimpleScheduleEntry } from "$lib/api/types";
  import { t } from "$lib/i18n";

  // 24-hour visualisation strip — one row per weekday with each
  // slot's trigger time plotted on a horizontal time axis. Mirrors
  // the schedule-card preview from homematicip-local-frontend, scaled
  // down for inline display above the editor list.
  //
  // The strip is read-only: clicking a slot scrolls the editor to
  // its detail row (the parent supplies the click handler so the
  // visualisation stays decoupled from the edit state).

  type Props = {
    entries: SimpleScheduleEntry[];
    domain: string;
    onSlotClick?: (slotNo: number) => void;
  };

  let { entries, domain, onSlotClick }: Props = $props();

  const weekdayKeys = [
    "MONDAY",
    "TUESDAY",
    "WEDNESDAY",
    "THURSDAY",
    "FRIDAY",
    "SATURDAY",
    "SUNDAY",
  ] as const;

  function shortDay(d: string): string {
    return t(`weekday.short.${d}`);
  }

  // Layout constants. Pure SVG without external deps so the strip
  // scales nicely regardless of dark mode + responsive breakpoints.
  const ROW_HEIGHT = 28;
  const LABEL_WIDTH = 40;
  const HOUR_WIDTH = 24; // → 24h * 24px = 576px content width
  const PADDING_X = 8;
  const HEADER_HEIGHT = 18;
  const stripWidth = LABEL_WIDTH + 24 * HOUR_WIDTH + PADDING_X * 2;
  const stripHeight = HEADER_HEIGHT + weekdayKeys.length * ROW_HEIGHT + 4;

  function timeToX(time: string): number {
    const [h, m] = time.split(":").map(Number);
    if (!Number.isFinite(h) || !Number.isFinite(m)) return LABEL_WIDTH + PADDING_X;
    const minutes = h * 60 + m;
    return LABEL_WIDTH + PADDING_X + (minutes / 60) * HOUR_WIDTH;
  }

  // Slot color: switch / lock → on/off binary, others → gradient
  // 0..1 mapped to brand-200 .. brand-700.
  function slotColor(level: number): string {
    if (domain === "switch") {
      return level >= 0.5 ? "var(--brand-500, #2563eb)" : "rgba(148,163,184,0.7)";
    }
    if (domain === "lock") {
      return level >= 0.5 ? "rgba(16,185,129,0.85)" : "rgba(239,68,68,0.85)";
    }
    // light/cover/valve gradient
    const pct = Math.max(0, Math.min(1, level));
    const lightness = 30 + (1 - pct) * 50; // 30..80%
    return `hsl(217 91% ${lightness}%)`;
  }

  // Slot symbol — astro markers get sun/moon glyphs, fixed-time gets
  // a vertical bar. Shapes drawn directly into the SVG group.
  function isAstro(entry: SimpleScheduleEntry): boolean {
    const c = entry.condition ?? "fixed_time";
    return c !== "fixed_time";
  }

  function entriesForDay(day: string): SimpleScheduleEntry[] {
    return entries
      .filter((e) => e.weekdays.includes(day))
      .sort((a, b) => a.time.localeCompare(b.time));
  }

  function entryTitle(e: SimpleScheduleEntry): string {
    const parts: string[] = [
      `#${e.slot_no}`,
      e.time,
      `${t("schedule.entry.level")} ${(e.level * 100).toFixed(0)}%`,
    ];
    if (isAstro(e)) {
      const astro =
        e.astro_type === "sunset"
          ? t("schedule.astro.sunset")
          : t("schedule.astro.sunrise");
      const off = e.astro_offset_minutes ?? 0;
      parts.push(`${astro}${off ? ` ${off > 0 ? "+" : ""}${off} min` : ""}`);
    }
    if (e.duration) parts.push(`⏱ ${e.duration}`);
    return parts.join(" · ");
  }
</script>

<div class="overflow-x-auto rounded-md border border-slate-200 bg-slate-50 p-2 dark:border-slate-800 dark:bg-[color-mix(in_srgb,var(--color-slate-900)_40%,transparent)]">
  <!-- Fluid: fills the container up to its natural width, then scales
       down to fit a phone so the whole 24h day stays visible at a glance
       (slots remain tappable for detail) instead of forcing a sideways
       scroll. The viewBox preserves the aspect ratio. -->
  <svg
    viewBox="0 0 {stripWidth} {stripHeight}"
    width="100%"
    style="max-width: {stripWidth}px; height: auto;"
    class="block"
    role="img"
    aria-label={t("schedule.viz.aria")}
  >
    <!-- Hour axis at the top — every 6h labelled, every 3h ticked. -->
    {#each Array.from({ length: 25 }, (_, i) => i) as h (h)}
      {@const x = LABEL_WIDTH + PADDING_X + h * HOUR_WIDTH}
      {#if h % 6 === 0}
        <text
          x={x}
          y={HEADER_HEIGHT - 4}
          text-anchor="middle"
          class="fill-slate-500 text-[9px]"
        >
          {String(h).padStart(2, "0")}
        </text>
      {/if}
      {#if h % 3 === 0}
        <line
          x1={x}
          y1={HEADER_HEIGHT}
          x2={x}
          y2={HEADER_HEIGHT + weekdayKeys.length * ROW_HEIGHT}
          stroke="currentColor"
          class="text-slate-200 dark:text-slate-700"
          stroke-width="1"
        />
      {/if}
    {/each}

    <!-- Weekday rows -->
    {#each weekdayKeys as day, i (day)}
      {@const y = HEADER_HEIGHT + i * ROW_HEIGHT}
      {@const cy = y + ROW_HEIGHT / 2}
      <text
        x={4}
        y={cy + 3}
        class="fill-slate-700 text-[10px] font-semibold dark:fill-slate-200"
      >
        {shortDay(day)}
      </text>
      <!-- Track background -->
      <rect
        x={LABEL_WIDTH + PADDING_X}
        y={y + ROW_HEIGHT / 2 - 6}
        width={24 * HOUR_WIDTH}
        height={12}
        rx={6}
        fill="currentColor"
        class="text-slate-200 dark:text-slate-800"
      />
      <!-- Slot markers -->
      {#each entriesForDay(day) as entry (entry.slot_no)}
        {@const cx = timeToX(entry.time)}
        {@const astro = isAstro(entry)}
        <g
          transform="translate({cx} {cy})"
          class="cursor-pointer focus:outline-none"
          onclick={() => onSlotClick?.(entry.slot_no)}
          onkeydown={(e) => {
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault();
              onSlotClick?.(entry.slot_no);
            }
          }}
          role="button"
          tabindex="0"
          aria-label={entryTitle(entry)}
        >
          <title>{entryTitle(entry)}</title>
          {#if astro && entry.astro_type === "sunset"}
            <!-- Moon-ish glyph for sunset-relative slots -->
            <circle r="8" fill="rgba(245,158,11,0.18)" />
            <text
              text-anchor="middle"
              dominant-baseline="central"
              class="fill-amber-700 text-[10px]"
            >
              ☾
            </text>
          {:else if astro}
            <circle r="8" fill="rgba(245,158,11,0.18)" />
            <text
              text-anchor="middle"
              dominant-baseline="central"
              class="fill-amber-700 text-[10px]"
            >
              ☼
            </text>
          {:else}
            <rect
              x="-3"
              y="-9"
              width="6"
              height="18"
              rx="2"
              fill={slotColor(entry.level)}
              stroke="white"
              stroke-width="1"
            />
          {/if}
        </g>
      {/each}
    {/each}
  </svg>
</div>
