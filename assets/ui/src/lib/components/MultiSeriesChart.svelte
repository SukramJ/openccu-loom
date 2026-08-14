<!--
  Multi-series measurement-history chart (SV03). Overlays the average
  line of N data-point series on a shared time + value axis, each in a
  distinct palette colour with a legend. Each series is fetched
  independently (Promise.allSettled) so one failing/empty series never
  blanks the whole chart. Read-only; the single-series HistoryChart is
  unchanged.
-->
<script lang="ts">
  import { untrack } from "svelte";
  import { getHistory, HistoryDisabledError } from "$lib/api/client";
  import type { HistoryBucket } from "$lib/api/client";
  import type { DiagramSeries } from "$lib/api/types";
  import { t } from "$lib/i18n";

  type Props = {
    series: DiagramSeries[];
    rangeHours?: number;
  };
  let { series, rangeHours = 24 }: Props = $props();

  const SVG_W = 560;
  const SVG_H = 180;
  const PAD = 8;

  // Categorical palette — distinct hues that read on both light and dark.
  const PALETTE = [
    "#2563eb",
    "#dc2626",
    "#16a34a",
    "#d97706",
    "#7c3aed",
    "#0891b2",
    "#db2777",
    "#65a30d",
  ];

  type SeriesResult = {
    def: DiagramSeries;
    color: string;
    buckets: HistoryBucket[];
    error?: string;
    disabled?: boolean;
  };

  let results = $state<SeriesResult[]>([]);
  let loading = $state(true);
  // Initial range comes from the diagram's default; the toolbar then owns
  // the mutable selection (untrack keeps this a one-time read).
  let selectedHours = $state(untrack(() => rangeHours));

  const RANGE_OPTIONS = [
    { label: "1 h", value: 1 },
    { label: "6 h", value: 6 },
    { label: "24 h", value: 24 },
    { label: "7 d", value: 168 },
    { label: "30 d", value: 720 },
  ];

  function seriesLabel(s: DiagramSeries): string {
    return s.label || s.parameter || s.channel_address || s.central;
  }

  // Monotonic generation guarding the async batch: a slower earlier batch
  // must never overwrite the results of the range or series list the chart
  // has since moved on to. The series list is snapshotted with it so the
  // zip below cannot pair a response with a definition from a newer prop.
  let loadGeneration = 0;

  async function load() {
    const generation = ++loadGeneration;
    const defs = [...series];
    loading = true;
    const to = new Date();
    const from = new Date(to.getTime() - selectedHours * 60 * 60 * 1000);
    const settled = await Promise.allSettled(
      defs.map((s) =>
        getHistory({
          central: s.central,
          interfaceId: s.interface_id ?? "",
          channel: s.channel_address ?? "",
          parameter: s.parameter ?? "",
          from: from.toISOString(),
          to: to.toISOString(),
          buckets: 200,
        }),
      ),
    );
    if (generation !== loadGeneration) return;
    results = settled.map((r, i) => {
      const def = defs[i];
      const color = PALETTE[i % PALETTE.length];
      if (r.status === "fulfilled") {
        return { def, color, buckets: r.value };
      }
      if (r.reason instanceof HistoryDisabledError) {
        return { def, color, buckets: [], disabled: true };
      }
      return {
        def,
        color,
        buckets: [],
        error: r.reason instanceof Error ? r.reason.message : String(r.reason),
      };
    });
    loading = false;
  }

  $effect(() => {
    void series;
    void selectedHours;
    void load();
  });

  // Shared axes across every successful series.
  const bounds = $derived.by(() => {
    let tMin = Infinity;
    let tMax = -Infinity;
    let vMin = Infinity;
    let vMax = -Infinity;
    for (const r of results) {
      for (const b of r.buckets) {
        const ts = new Date(b.ts).getTime();
        tMin = Math.min(tMin, ts);
        tMax = Math.max(tMax, ts);
        vMin = Math.min(vMin, b.avg);
        vMax = Math.max(vMax, b.avg);
      }
    }
    if (!Number.isFinite(tMin)) return null;
    if (vMin === vMax) {
      vMin -= 1;
      vMax += 1;
    }
    return { tMin, tRange: tMax - tMin || 1, vMin, vRange: vMax - vMin || 1 };
  });

  function xOf(ts: number, tMin: number, tRange: number): number {
    return PAD + ((ts - tMin) / tRange) * (SVG_W - 2 * PAD);
  }
  function yOf(v: number, vMin: number, vRange: number): number {
    return SVG_H - PAD - ((v - vMin) / vRange) * (SVG_H - 2 * PAD);
  }

  function polyline(r: SeriesResult): string {
    if (!bounds) return "";
    return r.buckets
      .map((b) => {
        const x = xOf(new Date(b.ts).getTime(), bounds.tMin, bounds.tRange);
        const y = yOf(b.avg, bounds.vMin, bounds.vRange);
        return `${x.toFixed(1)},${y.toFixed(1)}`;
      })
      .join(" ");
  }

  const hasData = $derived(results.some((r) => r.buckets.length > 0));
</script>

<div class="flex flex-col gap-2">
  <div class="flex flex-wrap items-center gap-1">
    {#each RANGE_OPTIONS as opt (opt.value)}
      <button
        type="button"
        class="rounded px-2 py-0.5 text-xs {selectedHours === opt.value
          ? 'bg-brand-500 text-white'
          : 'text-[var(--ha-secondary-text-color)] hover:bg-slate-100 dark:hover:bg-slate-800'}"
        onclick={() => (selectedHours = opt.value)}
      >
        {opt.label}
      </button>
    {/each}
  </div>

  {#if loading}
    <div class="flex h-[180px] items-center justify-center text-sm text-[var(--ha-secondary-text-color)]">
      {t("common.loading")}
    </div>
  {:else if !hasData}
    <div class="flex h-[180px] items-center justify-center text-sm text-[var(--ha-secondary-text-color)]">
      {t("diagrams.chart.empty")}
    </div>
  {:else}
    <svg
      viewBox="0 0 {SVG_W} {SVG_H}"
      class="w-full"
      style="height: {SVG_H}px;"
      role="img"
      aria-label={t("diagrams.chart.aria")}
    >
      {#each results as r (r.def.central + (r.def.channel_address ?? '') + (r.def.parameter ?? ''))}
        {#if r.buckets.length > 0}
          <polyline points={polyline(r)} fill="none" stroke={r.color} stroke-width="1.5" />
        {/if}
      {/each}
    </svg>
  {/if}

  <ul class="flex flex-wrap gap-x-4 gap-y-1 text-xs">
    {#each results as r (r.def.central + (r.def.channel_address ?? '') + (r.def.parameter ?? ''))}
      <li class="flex items-center gap-1.5">
        <span class="inline-block h-2.5 w-2.5 rounded-full" style="background:{r.color}"></span>
        <span class="text-[var(--ha-primary-text-color)]">{seriesLabel(r.def)}</span>
        {#if r.disabled}
          <span class="text-[var(--ha-secondary-text-color)]">({t("diagrams.chart.history_off")})</span>
        {:else if r.error}
          <span class="text-red-600 dark:text-red-400">({t("diagrams.chart.series_error")})</span>
        {:else if r.buckets.length === 0}
          <span class="text-[var(--ha-secondary-text-color)]">({t("diagrams.chart.no_samples")})</span>
        {/if}
      </li>
    {/each}
  </ul>
</div>
