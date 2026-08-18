<script lang="ts">
  import { getHistory, HistoryDisabledError } from "$lib/api/client";
  import type { HistoryBucket } from "$lib/api/client";
  import { t } from "$lib/i18n";
  import Icon from "$lib/components/ui/Icon.svelte";

  // Props mirror the required /history query parameters.
  // hoursBack controls the default time range shown on first render.
  type Props = {
    central: string;
    interfaceId: string;
    channel: string;
    parameter: string;
    /** Localised caption for the parameter; falls back to the raw key. */
    parameterLabel?: string;
    unit?: string;
    /**
     * `value -> unit` conversion factor (DataPointSummary.multiplier).
     * History buckets come back from `/history` as raw CCU wire values —
     * that endpoint carries no multiplier of its own — so the chart scales
     * min/avg/max with this factor before plotting/labelling. Default 1
     * (no projection), matching `multiplier` being absent for a trivial
     * conversion everywhere else in the SPA.
     */
    multiplier?: number;
    /** How many hours of history to show by default. Default 24. */
    hoursBack?: number;
  };

  let {
    central,
    interfaceId,
    channel,
    parameter,
    parameterLabel,
    unit = "",
    multiplier = 1,
    // hoursBack is intentionally not wired into $state — the range selector
    // below owns the mutable state. The prop serves as a hint for callers but
    // is not expected to change after mount (same pattern as other one-time
    // initialisation props in this codebase).
    hoursBack: _hoursBack = 24,
  }: Props = $props();

  // Project a raw history sample into the displayed unit. See the
  // `multiplier` prop doc above for why this happens here instead of on
  // the daemon side.
  function scale(v: number): number {
    return multiplier === 1 ? v : v * multiplier;
  }

  type ChartStatus =
    | { kind: "idle" }
    | { kind: "loading" }
    | { kind: "disabled" }
    | { kind: "empty" }
    | { kind: "error"; message: string }
    | { kind: "data"; buckets: HistoryBucket[] };

  let chartStatus = $state<ChartStatus>({ kind: "idle" });

  // Range selector options (hours back from now).
  const RANGE_OPTIONS: { label: string; value: number }[] = [
    { label: "1 h", value: 1 },
    { label: "6 h", value: 6 },
    { label: "24 h", value: 24 },
    { label: "7 d", value: 168 },
    { label: "30 d", value: 720 },
  ];
  let selectedHours = $state(24);

  // Monotonic generation guarding the async fetch: a slow response for the
  // range or data point the chart has since moved on from must never
  // overwrite the one it now shows.
  let loadGeneration = 0;

  async function load() {
    const generation = ++loadGeneration;
    chartStatus = { kind: "loading" };
    const to = new Date();
    const from = new Date(to.getTime() - selectedHours * 60 * 60 * 1000);
    try {
      const buckets = await getHistory({
        central,
        interfaceId,
        channel,
        parameter,
        from: from.toISOString(),
        to: to.toISOString(),
        buckets: 200,
      });
      if (generation !== loadGeneration) return;
      if (buckets.length === 0) {
        chartStatus = { kind: "empty" };
      } else {
        chartStatus = { kind: "data", buckets };
      }
    } catch (err) {
      if (generation !== loadGeneration) return;
      if (err instanceof HistoryDisabledError) {
        chartStatus = { kind: "disabled" };
      } else {
        chartStatus = {
          kind: "error",
          message: err instanceof Error ? err.message : String(err),
        };
      }
    }
  }

  // Re-fetch whenever the key props or the selected range changes. The
  // effect's first run covers the initial fetch, so there is no separate
  // onMount load (which would issue the same request twice).
  $effect(() => {
    void central;
    void interfaceId;
    void channel;
    void parameter;
    void selectedHours;
    void load();
  });

  // --- SVG layout constants ---
  const SVG_W = 560;
  const SVG_H = 180;
  const PAD_LEFT = 46;
  const PAD_RIGHT = 8;
  const PAD_TOP = 10;
  const PAD_BOTTOM = 28;
  const PLOT_W = SVG_W - PAD_LEFT - PAD_RIGHT;
  const PLOT_H = SVG_H - PAD_TOP - PAD_BOTTOM;

  function xOf(ts: number, tMin: number, tRange: number): number {
    return PAD_LEFT + ((ts - tMin) / tRange) * PLOT_W;
  }
  function yOf(v: number, vMin: number, vRange: number): number {
    return PAD_TOP + PLOT_H - ((v - vMin) / vRange) * PLOT_H;
  }

  function formatTick(d: Date, hours: number): string {
    if (hours <= 24) {
      return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
    }
    return d.toLocaleDateString([], { month: "short", day: "numeric" });
  }

  // Derived chart geometry computed from the current bucket array.
  const chart = $derived.by(() => {
    if (chartStatus.kind !== "data") return null;
    const { buckets } = chartStatus;

    const times = buckets.map((b: HistoryBucket) => new Date(b.ts).getTime());
    const tMin = Math.min(...times);
    const tMax = Math.max(...times);
    const tRange = tMax - tMin || 1;

    const allMin = Math.min(...buckets.map((b: HistoryBucket) => scale(b.min)));
    const allMax = Math.max(...buckets.map((b: HistoryBucket) => scale(b.max)));
    const vPad = (allMax - allMin) * 0.1 || 0.5;
    const vMin = allMin - vPad;
    const vMax = allMax + vPad;
    const vRange = vMax - vMin || 1;

    // Avg line polyline points.
    const avgPts = buckets
      .map((b: HistoryBucket) =>
        `${xOf(new Date(b.ts).getTime(), tMin, tRange).toFixed(1)},${yOf(scale(b.avg), vMin, vRange).toFixed(1)}`,
      )
      .join(" ");

    // Min/max band: trace min left→right, then max right→left (polygon).
    const minPts = buckets.map((b: HistoryBucket) =>
      `${xOf(new Date(b.ts).getTime(), tMin, tRange).toFixed(1)},${yOf(scale(b.min), vMin, vRange).toFixed(1)}`,
    );
    const maxPts = [...buckets].reverse().map((b: HistoryBucket) =>
      `${xOf(new Date(b.ts).getTime(), tMin, tRange).toFixed(1)},${yOf(scale(b.max), vMin, vRange).toFixed(1)}`,
    );
    const bandPolygon = [...minPts, ...maxPts].join(" ");

    // Y-axis ticks: 4 evenly spaced value labels.
    const yTicks = Array.from({ length: 4 }, (_, i) => {
      const v = vMin + (vRange * i) / 3;
      return { v, y: yOf(v, vMin, vRange), label: v.toFixed(1) };
    });

    // X-axis time ticks: 4 representative bucket timestamps.
    const xTickCount = 4;
    const xTicks = Array.from({ length: xTickCount }, (_, i) => {
      const idx = Math.round((i / (xTickCount - 1)) * (buckets.length - 1));
      const ts = new Date(buckets[idx].ts).getTime();
      return {
        x: xOf(ts, tMin, tRange),
        label: formatTick(new Date(buckets[idx].ts), selectedHours),
      };
    });

    return { avgPts, bandPolygon, yTicks, xTicks };
  });
</script>

<div class="rounded-lg border border-slate-200 bg-white p-3 dark:border-slate-800 dark:bg-slate-900">
  <div class="mb-2 flex items-center justify-between gap-2">
    <h4 class="text-sm font-semibold" style="color: var(--ha-primary-text-color);">
      {t("history.chart_title", { name: parameterLabel || parameter })}{unit ? ` (${unit})` : ""}
    </h4>
    <!-- Range + reload toolbar is meaningless when recording is off, so
         hide it in the disabled state (which carries its own CTA). -->
    {#if chartStatus.kind !== "disabled"}
      <div class="flex gap-1">
        {#each RANGE_OPTIONS as opt (opt.value)}
          <button
            type="button"
            class="rounded px-2 py-0.5 text-xs transition"
            style="
              background-color: {selectedHours === opt.value ? 'var(--ha-primary-color)' : 'transparent'};
              color: {selectedHours === opt.value ? 'white' : 'var(--ha-secondary-text-color)'};
              border: 1px solid {selectedHours === opt.value ? 'var(--ha-primary-color)' : 'var(--ha-divider-color)'};
            "
            onclick={() => { selectedHours = opt.value; }}
          >
            {opt.label}
          </button>
        {/each}
        <button
          type="button"
          class="ml-1 rounded px-2 py-0.5 text-xs"
          style="color: var(--ha-secondary-text-color); border: 1px solid var(--ha-divider-color);"
          onclick={() => void load()}
          aria-label={t("history.reload")}
        >
          ↺
        </button>
      </div>
    {/if}
  </div>

  {#if chartStatus.kind === "loading" || chartStatus.kind === "idle"}
    <div class="flex h-[180px] items-center justify-center text-sm" style="color: var(--ha-secondary-text-color);">
      {t("common.loading")}
    </div>
  {:else if chartStatus.kind === "disabled"}
    <!-- Feature off: distinct from "no data" — explain in plain language
         and link straight to the setting instead of naming a YAML key. -->
    <div class="flex h-[180px] flex-col items-center justify-center gap-1.5 px-4 text-center" style="color: var(--ha-secondary-text-color);">
      <Icon name="mdi:history" size={28} />
      <span class="text-sm font-medium" style="color: var(--ha-primary-text-color);">{t("history.disabled_title")}</span>
      <span class="text-xs">{t("history.disabled_hint")}</span>
      <a href="#/settings" class="mt-0.5 text-xs font-medium underline" style="color: var(--ha-primary-color);">
        {t("history.enable_link")}
      </a>
    </div>
  {:else if chartStatus.kind === "empty"}
    <!-- Enabled but no samples in the selected window. -->
    <div class="flex h-[180px] flex-col items-center justify-center gap-1.5 px-4 text-center" style="color: var(--ha-secondary-text-color);">
      <Icon name="mdi:information-outline" size={28} />
      <span class="text-sm">{t("history.empty")}</span>
    </div>
  {:else if chartStatus.kind === "error"}
    <div class="flex h-[180px] flex-col items-center justify-center gap-1.5 px-4 text-center" style="color: var(--ha-error-color);">
      <Icon name="mdi:alert-circle" size={28} />
      <span class="text-sm">{chartStatus.message}</span>
    </div>
  {:else if chartStatus.kind === "data" && chart}
    <!-- SVG line chart: min/max band + avg polyline + labelled axes -->
    <svg
      viewBox="0 0 {SVG_W} {SVG_H}"
      class="w-full"
      aria-label={t("history.chart_title", { name: parameterLabel || parameter })}
      role="img"
    >
      <!-- Plot area background -->
      <rect
        x={PAD_LEFT}
        y={PAD_TOP}
        width={PLOT_W}
        height={PLOT_H}
        fill="var(--ha-card-background-color, #f8fafc)"
        rx="2"
      />

      <!-- Horizontal grid lines at y-tick positions -->
      {#each chart.yTicks as tick (tick.v)}
        <line
          x1={PAD_LEFT}
          y1={tick.y}
          x2={PAD_LEFT + PLOT_W}
          y2={tick.y}
          stroke="var(--ha-divider-color, #e2e8f0)"
          stroke-width="0.5"
        />
      {/each}

      <!-- Min/max band polygon -->
      <polygon
        points={chart.bandPolygon}
        fill="var(--ha-primary-color, #03a9f4)"
        fill-opacity="0.12"
        stroke="none"
      />

      <!-- Avg polyline -->
      <polyline
        points={chart.avgPts}
        fill="none"
        stroke="var(--ha-primary-color, #03a9f4)"
        stroke-width="1.5"
        stroke-linejoin="round"
        stroke-linecap="round"
      />

      <!-- Y-axis value labels -->
      {#each chart.yTicks as tick (tick.v)}
        <text
          x={PAD_LEFT - 4}
          y={tick.y + 3.5}
          text-anchor="end"
          font-size="9"
          fill="var(--ha-secondary-text-color, #6b7280)"
        >{tick.label}</text>
      {/each}

      <!-- X-axis time labels -->
      {#each chart.xTicks as tick, i (i)}
        <text
          x={tick.x}
          y={SVG_H - 6}
          text-anchor="middle"
          font-size="9"
          fill="var(--ha-secondary-text-color, #6b7280)"
        >{tick.label}</text>
      {/each}

      <!-- Y axis -->
      <line
        x1={PAD_LEFT}
        y1={PAD_TOP}
        x2={PAD_LEFT}
        y2={PAD_TOP + PLOT_H}
        stroke="var(--ha-divider-color, #cbd5e1)"
        stroke-width="1"
      />
      <!-- X axis -->
      <line
        x1={PAD_LEFT}
        y1={PAD_TOP + PLOT_H}
        x2={PAD_LEFT + PLOT_W}
        y2={PAD_TOP + PLOT_H}
        stroke="var(--ha-divider-color, #cbd5e1)"
        stroke-width="1"
      />
    </svg>
  {/if}
</div>
