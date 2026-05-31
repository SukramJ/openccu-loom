<!--
  Dense status-only channel badge. Lives in the Übersicht panel for
  channels classified by `isStatusOnlyChannelType` (transmitters,
  transceivers, maintenance pseudo-channels) — i.e. channels that
  never accept a setValue write. The badge renders in a single row:

      [type icon]  channel name / type label    last observed value

  Click expands the badge into a small panel showing every data point
  on the channel; collapsed it occupies one tile-row, far less than a
  full ChannelControl card.
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { api, friendlyError } from "$lib/api/client";
  import type { DataPointSummary } from "$lib/api/types";
  import { subscribe } from "$lib/stores/events.svelte";
  import { STATUS_HEADLINE_KEYS } from "$lib/quickcontrol/domain";
  import { t } from "$lib/i18n";

  type Props = {
    address: string;
    channel: number;
    type?: string;
    name?: string;
    typeLabel?: string;
  };

  let { address, channel, type, name, typeLabel }: Props = $props();

  let dataPoints = $state<DataPointSummary[]>([]);
  let expanded = $state(false);
  let loaded = $state(false);
  let error = $state<string | null>(null);

  const channelAddress = $derived(`${address}:${channel}`);

  async function load() {
    try {
      dataPoints = await api.listDataPoints(address, channel);
      loaded = true;
    } catch (err) {
      error = friendlyError(err, t);
    }
  }

  onMount(() => {
    load();
    const unsub = subscribe((ev) => {
      if (ev.type !== "data_point") return;
      const e = ev.payload as { channel_address: string; parameter: string; value: unknown };
      if (e.channel_address !== channelAddress) return;
      const idx = dataPoints.findIndex((dp) => dp.parameter === e.parameter);
      if (idx < 0) return;
      dataPoints[idx] = { ...dataPoints[idx], value: e.value, observed: true };
    });
    return () => unsub();
  });

  // Active alerts win the collapsed slot — when LOW_BAT / SABOTAGE / …
  // is truthy, the badge highlights it in error colour. Otherwise the
  // collapsed view shows up to two headline values (TEMPERATURE +
  // HUMIDITY on a climate sensor; STATE on a contact; …) so the user
  // gets the live reading without having to expand.
  const ALERT_PARAMS = [
    "LOW_BAT",
    "SABOTAGE",
    "ERROR",
    "ERROR_CODE",
    "UNREACH",
    "STICKY_UNREACH",
    "CONFIG_PENDING",
  ];

  type Summary =
    | { kind: "alert"; value: string }
    | { kind: "values"; items: { key: string; value: string }[] }
    | null;

  const summary = $derived.by<Summary>(() => {
    for (const p of ALERT_PARAMS) {
      const dp = dataPoints.find((d) => d.parameter === p);
      if (dp?.observed && dp.value) {
        return { kind: "alert", value: String(dp.value) };
      }
    }
    const observed = dataPoints.filter(
      (d) => d.observed && !ALERT_PARAMS.includes(d.parameter),
    );
    if (observed.length === 0) return null;
    // Headline-order pass: pick up to two values that match the curated
    // headline list (TEMPERATURE / HUMIDITY / STATE / LEVEL / …).
    const items: { key: string; value: string }[] = [];
    for (const key of STATUS_HEADLINE_KEYS) {
      if (items.length >= 2) break;
      const dp = observed.find((d) => d.parameter === key);
      if (!dp) continue;
      items.push({ key, value: formatDP(dp) });
    }
    // Fallback when no headline parameter is observed: surface the first
    // observed value so the badge isn't blank.
    if (items.length === 0) {
      items.push({ key: observed[0].parameter, value: formatDP(observed[0]) });
    }
    return { kind: "values", items };
  });

  // Format a DP value for the badge: numbers carry the descriptor unit
  // when present (and the explicit LEVEL → percent conversion stays).
  function formatDP(dp: DataPointSummary): string {
    return formatValue(dp.value, dp.parameter, dp.unit);
  }

  // Coarse human-readable age formatter for the freshness tooltip.
  // < 60 s → "Xs", < 60 min → "Xm", < 24 h → "Xh", else "Xd".
  function formatAge(seconds: number): string {
    if (seconds < 60) return `${seconds}s`;
    if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
    if (seconds < 86400) return `${Math.round(seconds / 3600)}h`;
    return `${Math.round(seconds / 86400)}d`;
  }

  function formatValue(v: unknown, key?: string, unit?: string): string {
    if (v === null || v === undefined) return "—";
    if (typeof v === "boolean") {
      // Domain-aware bool rendering: a SHUTTER_CONTACT / WINDOW_SENSOR
      // true means "open", a SMOKE_DETECTOR true means an alarm. Keep
      // it terse — the badge row is short.
      if (key === "STATE" && (type ?? "").toUpperCase().includes("SHUTTER_CONTACT")) {
        return v ? "offen" : "zu";
      }
      return v ? "an" : "aus";
    }
    if (typeof v === "number") {
      // LEVEL is stored as 0..1 — render as percent regardless of unit.
      if (key === "LEVEL") return `${Math.round(v * 100)} %`;
      const formatted = Number.isInteger(v) ? String(v) : v.toFixed(1);
      if (unit) return `${formatted} ${unit}`;
      return formatted;
    }
    return String(v);
  }

  // Channel-type icon — minimal lucide-style SVG inline; one glyph per
  // class. Falls back to a small circle when the type is unknown.
  function iconPath(t: string | undefined): string {
    const s = (t ?? "").toUpperCase();
    if (s === "MAINTENANCE") return "M12 2v6m0 8v6m-10-10h6m8 0h6"; // wrench-ish cross
    if (s.includes("KEY_") || s.endsWith("_TRANSCEIVER"))
      return "M9 9h6v6H9zm0-5h6m-6 16h6"; // button
    if (s.endsWith("_TRANSMITTER")) return "M3 12h18M3 6h18M3 18h18"; // signal bars
    return "M12 2a10 10 0 100 20 10 10 0 000-20z"; // circle
  }
  const displayName = $derived(name ?? typeLabel ?? `Kanal ${channel}`);
</script>

<div
  class="rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)]/60 px-2 py-1 text-xs text-[var(--ha-secondary-text-color)] transition hover:bg-[var(--ha-card-background-color)]"
  class:opacity-60={!loaded && !error}
>
  <button
    type="button"
    class="flex w-full items-center gap-2 text-left"
    onclick={() => (expanded = !expanded)}
  >
    <svg
      viewBox="0 0 24 24"
      class="h-3.5 w-3.5 flex-shrink-0 text-[var(--ha-secondary-text-color)]"
      fill="none"
      stroke="currentColor"
      stroke-width="1.6"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <path d={iconPath(type)} />
    </svg>
    <span class="flex-shrink-0 font-medium text-[var(--ha-primary-text-color)]">
      {displayName}
    </span>
    {#if summary?.kind === "alert"}
      <span class="ml-auto truncate text-[var(--ha-error-color)]">
        {summary.value}
      </span>
    {:else if summary?.kind === "values"}
      <span class="ml-auto flex items-center gap-1.5 truncate">
        {#each summary.items as item, i (item.key)}
          {#if i > 0}<span class="opacity-40">·</span>{/if}
          <span class="font-mono tabular-nums text-[var(--ha-primary-text-color)]">
            {item.value}
          </span>
        {/each}
      </span>
    {:else}
      <span class="ml-auto opacity-50">—</span>
    {/if}
    <svg
      viewBox="0 0 24 24"
      class="h-3 w-3 flex-shrink-0 opacity-60 transition-transform"
      class:rotate-180={expanded}
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <path d="M6 9l6 6 6-6" />
    </svg>
  </button>
  {#if expanded}
    {#if error}
      <p class="mt-1 text-[var(--ha-error-color)]">{error}</p>
    {:else if dataPoints.length === 0}
      <p class="mt-1 opacity-60">Keine Datenpunkte beobachtet.</p>
    {:else}
      <ul class="mt-1 space-y-0.5 border-t border-[var(--ha-divider-color)] pt-1">
        {#each dataPoints as dp (dp.parameter)}
          {@const isCached = dp.source === "cache"}
          {@const isStale = dp.source === "stale"}
          {@const ageTitle = dp.value_age_seconds != null
            ? ` · zuletzt vor ${formatAge(dp.value_age_seconds)}`
            : ""}
          <li class="flex items-center justify-between gap-2">
            <span class="truncate">{dp.parameter_label ?? dp.parameter}</span>
            <span
              class="text-[var(--ha-primary-text-color)]"
              class:opacity-40={!dp.observed}
              class:italic={isCached || isStale}
              title={isCached ? `Aus Cache wiederhergestellt${ageTitle}` : isStale ? `Verbindung verloren${ageTitle}` : undefined}
            >
              {#if isCached || isStale}
                <span aria-hidden="true" class="mr-1 opacity-70">⏱</span>
              {/if}
              {formatDP(dp)}
            </span>
          </li>
        {/each}
      </ul>
    {/if}
  {/if}
</div>
