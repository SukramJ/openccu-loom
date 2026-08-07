<script lang="ts">
  import { onMount } from "svelte";
  import { api, getEnergy, HistoryDisabledError } from "$lib/api/client";
  import type { CentralRow } from "$lib/api/client";
  import type { EnergyResponse } from "$lib/api/types";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import { t } from "$lib/i18n";
  import { prefs } from "$lib/stores/preferences.svelte";

  // GET /api/v1/energy view — per-device consumption/feed-in breakdown
  // over a time range, grouped by hour/day/month. See
  // docs/plans/A2-timeseries-energy.md "Energy aggregation API" for the
  // response shape. Values are Wh on the wire; every rendered figure
  // here divides by 1000 to show kWh.

  type Group = "hour" | "day" | "month";

  let centrals = $state<CentralRow[]>([]);
  let centralsLoading = $state(true);
  let centralsError = $state<string | null>(null);
  let selectedCentral = $state("");

  let group = $state<Group>("day");

  // Preset time ranges (hours back from now), mirroring the toolbar
  // pattern in HistoryChart.svelte.
  const RANGE_PRESETS: { label: string; hours: number }[] = [
    { label: t("energy.preset.24h"), hours: 24 },
    { label: t("energy.preset.7d"), hours: 24 * 7 },
    { label: t("energy.preset.30d"), hours: 24 * 30 },
    { label: t("energy.preset.12mo"), hours: 24 * 365 },
  ];
  let selectedRangeHours = $state(24 * 30);

  // "" focuses the chart on the summed total across every device;
  // a specific address focuses it on that one device's series.
  let chartDevice = $state("");

  type Status =
    | { kind: "idle" }
    | { kind: "loading" }
    | { kind: "disabled" }
    | { kind: "empty" }
    | { kind: "error"; message: string }
    | { kind: "data"; data: EnergyResponse };

  let status = $state<Status>({ kind: "idle" });

  async function loadCentrals() {
    centralsLoading = true;
    centralsError = null;
    try {
      centrals = await api.listCentralsV2();
      if (!selectedCentral && centrals.length > 0) {
        selectedCentral = centrals[0].name;
      }
    } catch (err) {
      centralsError = err instanceof Error ? err.message : String(err);
    } finally {
      centralsLoading = false;
    }
  }

  async function load() {
    if (!selectedCentral) return;
    status = { kind: "loading" };
    const to = new Date();
    const from = new Date(to.getTime() - selectedRangeHours * 60 * 60 * 1000);
    try {
      const data = await getEnergy({
        central: selectedCentral,
        from: from.toISOString(),
        to: to.toISOString(),
        group,
      });
      // Reset the chart focus when the device set changes (e.g. after
      // switching central) so a stale address never silently empties
      // the chart.
      if (chartDevice && !data.devices.some((d) => d.address === chartDevice)) {
        chartDevice = "";
      }
      status = data.devices.length === 0 ? { kind: "empty" } : { kind: "data", data };
    } catch (err) {
      if (err instanceof HistoryDisabledError) {
        status = { kind: "disabled" };
      } else {
        status = { kind: "error", message: err instanceof Error ? err.message : String(err) };
      }
    }
  }

  onMount(() => {
    void loadCentrals();
  });

  // Re-fetch whenever the central, group or range preset changes.
  $effect(() => {
    void selectedCentral;
    void group;
    void selectedRangeHours;
    if (selectedCentral) void load();
  });

  const centralOptions = $derived(
    centrals.map((c) => ({ value: c.name, label: c.name })),
  );

  const groupOptions = $derived([
    { value: "hour", label: t("energy.group.hour") },
    { value: "day", label: t("energy.group.day") },
    { value: "month", label: t("energy.group.month") },
  ]);

  const deviceFocusOptions = $derived.by(() => {
    if (status.kind !== "data") return [];
    return [
      { value: "", label: t("energy.chart.all_devices") },
      ...status.data.devices.map((d) => ({ value: d.address, label: d.name })),
    ];
  });

  // --- Per-device breakdown table -------------------------------------

  type DeviceRow = {
    address: string;
    name: string;
    consumedKwh: number;
    feedInKwh: number;
    avgPowerW: number;
    peakPowerW: number;
    anyReset: boolean;
  };

  const deviceRows = $derived.by<DeviceRow[]>(() => {
    if (status.kind !== "data") return [];
    return status.data.devices.map((d) => ({
      address: d.address,
      name: d.name,
      consumedKwh: d.total_consumed_wh / 1000,
      feedInKwh: d.total_feed_in_wh / 1000,
      avgPowerW: d.buckets.length
        ? d.buckets.reduce((s, b) => s + b.avg_power_w, 0) / d.buckets.length
        : 0,
      peakPowerW: d.buckets.length ? Math.max(...d.buckets.map((b) => b.peak_power_w)) : 0,
      anyReset: d.buckets.some((b) => b.reset),
    }));
  });

  const anyDeviceReset = $derived(deviceRows.some((r) => r.anyReset));

  // The tariff rides on the response; zero (or absent) means none is
  // configured, in which case no cost is shown anywhere rather than a
  // row of 0.00 that reads as "free".
  const tariff = $derived(status.kind === "data" ? (status.data.price_per_kwh ?? 0) : 0);
  const currency = $derived(status.kind === "data" ? (status.data.currency ?? "") : "");
  const hasTariff = $derived(tariff > 0);

  // Amounts are formatted here, not by the daemon: the locale decides the
  // decimal separator and grouping, and the SPA already knows it.
  function formatCost(kwh: number): string {
    return `${(kwh * tariff).toLocaleString(prefs.locale === "de" ? "de-DE" : "en-US", {
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    })} ${currency}`;
  }

  const deviceColumns: DataColumn<DeviceRow>[] = $derived([
    { key: "name", label: t("energy.col.device"), sortable: true, title: true, get: (r) => r.name },
    { key: "consumed", label: t("energy.col.consumed"), sortable: true, align: "right", get: (r) => r.consumedKwh },
    { key: "feed_in", label: t("energy.col.feed_in"), sortable: true, align: "right", get: (r) => r.feedInKwh },
    { key: "avg_power", label: t("energy.col.avg_power"), sortable: true, align: "right", get: (r) => r.avgPowerW },
    { key: "peak_power", label: t("energy.col.peak_power"), sortable: true, align: "right", get: (r) => r.peakPowerW },
    ...(hasTariff
      ? [
          {
            key: "cost",
            label: t("energy.col.cost"),
            sortable: true,
            align: "right" as const,
            get: (r: DeviceRow) => r.consumedKwh * tariff,
          },
        ]
      : []),
  ]);

  function formatKwh(v: number): string {
    return `${v.toFixed(2)} kWh`;
  }

  function formatW(v: number): string {
    return `${v.toFixed(0)} W`;
  }

  // --- Consumption-over-time chart -------------------------------------
  // Single-series SVG line chart mirroring HistoryChart.svelte's layout
  // constants; the energy series has no min/max band (one value per
  // bucket, not avg/min/max), so it is a dedicated inline chart rather
  // than a literal reuse of that component's per-parameter fetch cycle.

  const seriesBuckets = $derived.by(() => {
    if (status.kind !== "data") return [];
    const { data } = status;
    if (chartDevice) {
      const dev = data.devices.find((d) => d.address === chartDevice);
      return dev ? dev.buckets.map((b) => ({ ts: b.ts, kwh: b.consumed_wh / 1000 })) : [];
    }
    const sums = new Map<string, number>();
    for (const dev of data.devices) {
      for (const b of dev.buckets) {
        sums.set(b.ts, (sums.get(b.ts) ?? 0) + b.consumed_wh / 1000);
      }
    }
    return Array.from(sums.entries())
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([ts, kwh]) => ({ ts, kwh }));
  });

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
  // Bucket starts arrive as local calendar boundaries — the daemon folds day
  // and month buckets on its own zone, which is the household's — so a local
  // date format names exactly the day or month the bucket covers. Formatting
  // in UTC here would undo that and print the previous day for every bucket
  // east of Greenwich.
  function formatTick(d: Date, g: Group): string {
    if (g === "hour") return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
    if (g === "month") return d.toLocaleDateString([], { month: "short", year: "2-digit" });
    return d.toLocaleDateString([], { month: "short", day: "numeric" });
  }

  const chart = $derived.by(() => {
    const buckets = seriesBuckets;
    if (buckets.length === 0) return null;

    const times = buckets.map((b) => new Date(b.ts).getTime());
    const tMin = Math.min(...times);
    const tMax = Math.max(...times);
    const tRange = tMax - tMin || 1;

    const values = buckets.map((b) => b.kwh);
    const vMin = 0;
    const vMax = Math.max(...values, 0.001);
    const vRange = vMax - vMin || 1;

    const linePts = buckets
      .map((b) => `${xOf(new Date(b.ts).getTime(), tMin, tRange).toFixed(1)},${yOf(b.kwh, vMin, vRange).toFixed(1)}`)
      .join(" ");

    const yTicks = Array.from({ length: 4 }, (_, i) => {
      const v = vMin + (vRange * i) / 3;
      return { v, y: yOf(v, vMin, vRange), label: v.toFixed(2) };
    });

    const xTickCount = Math.min(4, buckets.length);
    const xTicks = Array.from({ length: xTickCount }, (_, i) => {
      const idx = Math.round((i / (xTickCount - 1 || 1)) * (buckets.length - 1));
      const ts = new Date(buckets[idx].ts).getTime();
      return { x: xOf(ts, tMin, tRange), label: formatTick(new Date(buckets[idx].ts), group) };
    });

    return { linePts, yTicks, xTicks };
  });
</script>

<svelte:head>
  <title>{t("page.title.energy")}</title>
</svelte:head>

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6 space-y-6">
  <PageHeader title={t("energy.title")} subtitle={t("energy.subtitle")} />

  {#if centralsLoading}
    <LoadingState />
  {:else if centralsError}
    <ErrorState message={centralsError} onRetry={() => void loadCentrals()} />
  {:else if centrals.length === 0}
    <EmptyState message={t("energy.no_centrals")} icon="mdi:server" />
  {:else}
    <Card class="p-4">
      <div class="flex flex-wrap items-end gap-4">
        <label class="flex flex-col gap-1 text-xs">
          <span class="text-[var(--ha-secondary-text-color)]">{t("energy.central")}</span>
          <Select options={centralOptions} bind:value={selectedCentral} class="w-48" />
        </label>

        <label class="flex flex-col gap-1 text-xs">
          <span class="text-[var(--ha-secondary-text-color)]">{t("energy.group")}</span>
          <Select
            options={groupOptions}
            value={group}
            onValueChange={(v) => { group = v as Group; }}
            class="w-40"
          />
        </label>

        <div class="flex flex-col gap-1 text-xs">
          <span class="text-[var(--ha-secondary-text-color)]">{t("energy.range")}</span>
          <div class="flex gap-1">
            {#each RANGE_PRESETS as preset (preset.hours)}
              <button
                type="button"
                class="rounded px-2 py-1 text-xs transition"
                style="
                  background-color: {selectedRangeHours === preset.hours ? 'var(--ha-primary-color)' : 'transparent'};
                  color: {selectedRangeHours === preset.hours ? 'white' : 'var(--ha-secondary-text-color)'};
                  border: 1px solid {selectedRangeHours === preset.hours ? 'var(--ha-primary-color)' : 'var(--ha-divider-color)'};
                "
                onclick={() => { selectedRangeHours = preset.hours; }}
              >
                {preset.label}
              </button>
            {/each}
          </div>
        </div>
      </div>
    </Card>

    {#if status.kind === "loading" || status.kind === "idle"}
      <LoadingState />
    {:else if status.kind === "disabled"}
      <EmptyState message={t("energy.disabled_title")} icon="mdi:history">
        {#snippet action()}
          <div class="text-center">
            <p class="mb-2 text-xs text-[var(--ha-secondary-text-color)]">{t("energy.disabled_hint")}</p>
            <a href="#/settings" class="text-xs font-medium underline" style="color: var(--ha-primary-color);">
              {t("energy.enable_link")}
            </a>
          </div>
        {/snippet}
      </EmptyState>
    {:else if status.kind === "error"}
      <ErrorState message={status.message} onRetry={() => void load()} />
    {:else if status.kind === "empty"}
      <EmptyState message={t("energy.empty")} icon="mdi:information-outline" />
    {:else if status.kind === "data"}
      <!-- Totals -->
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Card class="p-4">
          <div class="text-xs uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
            {t("energy.total_consumed")}
          </div>
          <div class="mt-1 text-2xl font-bold tabular-nums">{formatKwh(status.data.total_consumed_wh / 1000)}</div>
          {#if hasTariff}
            <div class="mt-0.5 text-sm tabular-nums text-[var(--ha-secondary-text-color)]">
              {formatCost(status.data.total_consumed_wh / 1000)}
            </div>
          {/if}
        </Card>
        <Card class="p-4">
          <div class="text-xs uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
            {t("energy.total_feed_in")}
          </div>
          <div class="mt-1 text-2xl font-bold tabular-nums">{formatKwh(status.data.total_feed_in_wh / 1000)}</div>
        </Card>
      </div>

      <!-- Consumption-over-time chart -->
      <Card class="p-3">
        <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
          <h4 class="text-sm font-semibold" style="color: var(--ha-primary-text-color);">
            {t("energy.chart_title")}
          </h4>
          {#if deviceFocusOptions.length > 2}
            <Select options={deviceFocusOptions} bind:value={chartDevice} class="w-48" />
          {/if}
        </div>

        {#if chart}
          <svg
            viewBox="0 0 {SVG_W} {SVG_H}"
            class="w-full"
            aria-label={t("energy.chart_title")}
            role="img"
          >
            <rect
              x={PAD_LEFT}
              y={PAD_TOP}
              width={PLOT_W}
              height={PLOT_H}
              fill="var(--ha-card-background-color, #f8fafc)"
              rx="2"
            />
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
            <polyline
              points={chart.linePts}
              fill="none"
              stroke="var(--ha-primary-color, #03a9f4)"
              stroke-width="1.5"
              stroke-linejoin="round"
              stroke-linecap="round"
            />
            {#each chart.yTicks as tick (tick.v)}
              <text
                x={PAD_LEFT - 4}
                y={tick.y + 3.5}
                text-anchor="end"
                font-size="9"
                fill="var(--ha-secondary-text-color, #6b7280)"
              >{tick.label}</text>
            {/each}
            {#each chart.xTicks as tick, i (i)}
              <text
                x={tick.x}
                y={SVG_H - 6}
                text-anchor="middle"
                font-size="9"
                fill="var(--ha-secondary-text-color, #6b7280)"
              >{tick.label}</text>
            {/each}
            <line
              x1={PAD_LEFT}
              y1={PAD_TOP}
              x2={PAD_LEFT}
              y2={PAD_TOP + PLOT_H}
              stroke="var(--ha-divider-color, #cbd5e1)"
              stroke-width="1"
            />
            <line
              x1={PAD_LEFT}
              y1={PAD_TOP + PLOT_H}
              x2={PAD_LEFT + PLOT_W}
              y2={PAD_TOP + PLOT_H}
              stroke="var(--ha-divider-color, #cbd5e1)"
              stroke-width="1"
            />
          </svg>
        {:else}
          <div class="flex h-[180px] items-center justify-center text-sm" style="color: var(--ha-secondary-text-color);">
            {t("energy.empty")}
          </div>
        {/if}
      </Card>

      <!-- Per-device breakdown -->
      <Card class="p-4">
        <header class="mb-3 flex items-center justify-between gap-2">
          <h2 class="text-lg font-semibold">{t("energy.breakdown_title")}</h2>
          <span class="text-xs text-[var(--ha-secondary-text-color)]">{deviceRows.length}</span>
        </header>
        <DataTable
          rows={deviceRows}
          columns={deviceColumns}
          rowKey={(r) => r.address}
          initialSort={{ key: "consumed", asc: false }}
          emptyMessage={t("energy.empty")}
          emptyIcon="mdi:zap"
        >
          {#snippet cell(row, col)}
            {#if col.key === "name"}
              <span class="font-medium">{row.name}</span>
              {#if row.anyReset}
                <Badge variant="warning" class="ml-1.5">{t("energy.col.reset")}</Badge>
              {/if}
            {:else if col.key === "consumed"}
              <span class="tabular-nums">{formatKwh(row.consumedKwh)}</span>
            {:else if col.key === "feed_in"}
              <span class="tabular-nums">{formatKwh(row.feedInKwh)}</span>
            {:else if col.key === "avg_power"}
              <span class="tabular-nums">{formatW(row.avgPowerW)}</span>
            {:else if col.key === "peak_power"}
              <span class="tabular-nums">{formatW(row.peakPowerW)}</span>
            {:else if col.key === "cost"}
              <span class="tabular-nums">{formatCost(row.consumedKwh)}</span>
            {/if}
          {/snippet}
        </DataTable>
        {#if anyDeviceReset}
          <p class="mt-3 flex items-center gap-1 text-xs text-[var(--ha-secondary-text-color)]">
            <Icon name="mdi:alert-triangle" size={14} aria-label="" />
            {t("energy.reset_note")}
          </p>
        {/if}
      </Card>
    {/if}
  {/if}
</section>
