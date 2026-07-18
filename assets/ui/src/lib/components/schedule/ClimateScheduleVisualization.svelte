<script lang="ts">
  import type { ClimatePeriod, ClimateProfile } from "$lib/api/types";
  import { t } from "$lib/i18n";

  // Vertical 24h temperature grid — one column per weekday with the
  // periods stacked top-to-bottom and coloured by HA's climate state
  // colour scale. Mirrors homematicip-local-frontend's
  // hmip-schedule-grid (climate-schedule-card) so HmIP-BWTH /
  // HmIP-eTRV-2 schedules look familiar.

  type Props = {
    profile: ClimateProfile | null | undefined;
    onWeekdayClick?: (day: string) => void;
    showTemperature?: boolean;
    showGradient?: boolean;
  };

  let {
    profile,
    onWeekdayClick,
    showTemperature = true,
    showGradient = false,
  }: Props = $props();

  const weekdays = [
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

  // HA climate state color scale, ported 1:1 from
  // packages/schedule-core/src/utils/colors.ts.
  function tempColor(temp: number): string {
    if (temp < 10) return "#2b9af9";
    if (temp < 14) return "#40c4ff";
    if (temp < 17) return "#26c6da";
    if (temp < 19) return "#66bb6a";
    if (temp < 21) return "#9ccc65";
    if (temp < 23) return "#ffb74d";
    if (temp < 25) return "#ff8100";
    return "#f4511e";
  }

  function timeToMinutes(t: string): number {
    const [h, m] = t.split(":").map(Number);
    if (!Number.isFinite(h) || !Number.isFinite(m)) return 0;
    return h * 60 + m;
  }

  function minutesToTime(m: number): string {
    const h = Math.floor(m / 60);
    const mm = m % 60;
    return `${String(h).padStart(2, "0")}:${String(mm).padStart(2, "0")}`;
  }

  type Block = {
    startMinutes: number;
    endMinutes: number;
    startTime: string;
    endTime: string;
    temperature: number;
    isBase: boolean;
  };

  // Port of fillGapsWithBaseTemperature() from schedule-core. Periods
  // sorted by start time, gaps filled with `baseTemp` chunks marked
  // isBase=true so the renderer can paint them in a neutral colour.
  // Special case: when there are no explicit periods, the day is a
  // single coloured block at base temperature (isBase=false). This
  // diverges from the reference card which would show grey — a flat
  // schedule should still look like a real schedule, not unconfigured.
  function fillGaps(periods: ClimatePeriod[], baseTemp: number): Block[] {
    if (!periods.length) {
      return [
        {
          startMinutes: 0,
          endMinutes: 1440,
          startTime: "00:00",
          endTime: "24:00",
          temperature: baseTemp,
          isBase: false,
        },
      ];
    }
    const sorted: Block[] = [...periods]
      .map((p) => ({
        startMinutes: timeToMinutes(p.start_time),
        endMinutes: timeToMinutes(p.end_time),
        startTime: p.start_time,
        endTime: p.end_time,
        temperature: p.temperature,
        isBase: false,
      }))
      .sort((a, b) => a.startMinutes - b.startMinutes);

    const result: Block[] = [];
    let cursor = 0;
    for (const p of sorted) {
      if (p.startMinutes > cursor) {
        result.push({
          startMinutes: cursor,
          endMinutes: p.startMinutes,
          startTime: minutesToTime(cursor),
          endTime: p.startTime,
          temperature: baseTemp,
          isBase: true,
        });
      }
      result.push(p);
      cursor = p.endMinutes;
    }
    if (cursor < 1440) {
      result.push({
        startMinutes: cursor,
        endMinutes: 1440,
        startTime: minutesToTime(cursor),
        endTime: "24:00",
        temperature: baseTemp,
        isBase: true,
      });
    }
    return result;
  }

  // Background style per block — flat colour or top→bottom gradient
  // blending into adjacent blocks, mirroring getTemperatureGradient().
  function blockBackground(blocks: Block[], i: number): string {
    const b = blocks[i];
    if (b.isBase) return "background-color: rgba(148,163,184,0.22);";
    if (!showGradient) return `background-color: ${tempColor(b.temperature)};`;
    const prev = i > 0 ? blocks[i - 1].temperature : null;
    const next = i < blocks.length - 1 ? blocks[i + 1].temperature : null;
    const cur = tempColor(b.temperature);
    if (prev === null && next === null) return `background-color: ${cur};`;
    if (prev !== null && next === null) {
      return `background: linear-gradient(to bottom, ${tempColor(prev)}, ${cur});`;
    }
    if (prev === null && next !== null) {
      return `background: linear-gradient(to bottom, ${cur}, ${tempColor(next)});`;
    }
    return `background: linear-gradient(to bottom, ${tempColor(prev as number)}, ${cur} 50%, ${tempColor(next as number)});`;
  }

  // Time-axis ticks every 3h (00, 03, 06, ..., 24). The 24:00 tick is
  // drawn as a closing line only, no label.
  const timeLabels = Array.from({ length: 9 }, (_, i) => {
    const h = i * 3;
    return { hour: h, top: (h / 24) * 100 };
  });

  function handleClick(day: string) {
    onWeekdayClick?.(day);
  }

  function tooltipText(b: Block): string {
    const text = `${b.startTime} – ${b.endTime} · ${b.temperature.toFixed(1)} °C`;
    if (b.isBase) {
      return `${text} · ${t("schedule.base_temperature")}`;
    }
    return text;
  }

  const heading = $derived(t("schedule.weekday_overview"));
  const hint = $derived(t("schedule.click_to_edit"));

  // Pre-compute per-day blocks so the template stays clean and the
  // reactive dependency on `profile` is explicit.
  const dayBlocks = $derived.by(() => {
    return weekdays.map((day) => {
      const wd = profile?.weekdays?.[day];
      const base = wd?.base_temperature ?? 19;
      return {
        day,
        base,
        blocks: fillGaps(wd?.periods ?? [], base),
      };
    });
  });
</script>

<section class="rounded-md border border-slate-200 bg-slate-50 p-3 dark:border-slate-800 dark:bg-[color-mix(in_srgb,var(--color-slate-900)_40%,transparent)]">
  <header class="mb-2 flex items-center justify-between gap-2">
    <h3 class="text-sm font-semibold text-slate-700 dark:text-slate-200">{heading}</h3>
    <span class="text-[11px] text-[var(--ha-secondary-text-color)]">{hint}</span>
  </header>

  <div class="overflow-x-auto">
    <div class="flex gap-1 sm:min-w-[520px] sm:gap-2">
      <div class="flex w-9 shrink-0 flex-col">
        <div class="mb-1 h-[14px]"></div>
        <div class="relative h-[280px]">
          {#each timeLabels as tl (tl.hour)}
            {#if tl.hour < 24}
              <div
                class="absolute right-1 -translate-y-1/2 text-[10px] tabular-nums text-[var(--ha-secondary-text-color)]"
                style:top="{tl.top}%"
              >
                {String(tl.hour).padStart(2, "0")}
              </div>
            {/if}
          {/each}
        </div>
      </div>

      {#each dayBlocks as { day, blocks } (day)}
        <div class="flex min-w-[40px] flex-1 flex-col sm:min-w-[56px]">
          <div class="mb-1 text-center text-[11px] font-semibold text-slate-700 dark:text-slate-200">
            {shortDay(day)}
          </div>
          <button
            type="button"
            class="relative h-[280px] w-full overflow-hidden rounded border border-slate-300 bg-white text-left transition hover:ring-2 hover:ring-brand-400 focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-500 dark:border-slate-700 dark:bg-slate-950"
            onclick={() => handleClick(day)}
            aria-label={shortDay(day)}
          >
            {#each blocks as block, i (`${block.startMinutes}-${block.endMinutes}-${i}`)}
              {@const heightPct = ((block.endMinutes - block.startMinutes) / 1440) * 100}
              <div
                class="absolute left-0 right-0 flex items-center justify-center text-[11px] font-medium {block.isBase
                  ? 'text-slate-500 dark:text-slate-400'
                  : 'text-white drop-shadow-sm'}"
                style="top: {(block.startMinutes / 1440) * 100}%; height: {heightPct}%; {blockBackground(blocks, i)}"
                title={tooltipText(block)}
              >
                {#if showTemperature && heightPct >= 6}
                  {block.temperature.toFixed(1)}°
                {/if}
              </div>
            {/each}

            {#each timeLabels as tl (`grid-${day}-${tl.hour}`)}
              {#if tl.hour > 0 && tl.hour < 24}
                <div
                  class="pointer-events-none absolute inset-x-0 border-t border-[color-mix(in_srgb,var(--color-slate-200)_60%,transparent)] dark:border-[color-mix(in_srgb,var(--color-slate-700)_40%,transparent)]"
                  style:top="{tl.top}%"
                ></div>
              {/if}
            {/each}
          </button>
        </div>
      {/each}
    </div>
  </div>
</section>
