<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { DataPointChangedEvent } from "$lib/api/types";
  import { onResync, subscribe } from "$lib/stores/events.svelte";
  import { t } from "$lib/i18n";
  import Icon from "$lib/components/ui/Icon.svelte";
  import type { IconName } from "$lib/icons";
  import Card from "$lib/components/ui/Card.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";

  // Maintenance / health status grid for a device's `:0` channel.
  // Mirrors homematicip-local-frontend `_renderStatusSummary`
  // (views/device-detail.ts:355-413). Renders a compact grid of icons
  // + values for the most-asked questions:
  //   • Is the device reachable (UNREACH)?
  //   • RSSI device / peer
  //   • Battery (LOW_BAT, OPERATING_VOLTAGE)
  //   • Duty-cycle blocked?
  //   • Config-pending?
  //
  // The component refreshes once on mount and patches values in place
  // when `data_point` events arrive on the WS bus.

  type Props = {
    address: string;
  };

  let { address }: Props = $props();

  type DPMap = Record<string, unknown>;

  let values = $state<DPMap>({});
  let loading = $state(true);
  let error = $state<string | null>(null);

  async function load() {
    error = null;
    try {
      const dps = await api.listDataPoints(address, 0);
      const next: DPMap = {};
      for (const dp of dps) {
        next[dp.parameter] = dp.value;
      }
      values = next;
    } catch (err) {
      // 404 means "no :0 channel" — skip the grid silently.
      if (err instanceof ApiError && err.status === 404) {
        values = {};
      } else {
        error =
          err instanceof Error ? err.message : String(err);
      }
    } finally {
      loading = false;
    }
  }

  let unsubEvents: (() => void) | null = null;
  // The boot snapshot signals a resync instead of replaying the model
  // into the stream, so the view reloads what it read over REST.
  let unsubResync: (() => void) | null = null;

  onMount(() => {
    void load();
    unsubResync = onResync(() => void load());
    unsubEvents = subscribe((env) => {
      if (env.type !== "data_point") return;
      // Discriminated-union narrowing falls back to `unknown` for the
      // generic branch; the explicit cast keeps the consuming code
      // typed without weakening the public envelope.
      const p = env.payload as DataPointChangedEvent;
      // Match against the device's :0 channel only — we never want
      // to surface, e.g., a level event from channel 3 here.
      if (!p.channel_address?.endsWith(":0")) return;
      if (!p.channel_address.startsWith(address)) return;
      values = { ...values, [p.parameter]: p.value };
    });
  });

  onDestroy(() => {
    unsubEvents?.();
    unsubResync?.();
  });

  type StatusItem = {
    icon: IconName;
    label: string;
    value: string;
    tone?: "ok" | "warn" | "error";
  };

  function fmtBool(v: unknown, yes: string, no: string): string {
    if (v === undefined || v === null || v === "") return "—";
    return Boolean(v) ? yes : no;
  }

  function fmtNumber(v: unknown, suffix = ""): string {
    if (v === undefined || v === null || v === "") return "—";
    const n = Number(v);
    if (!Number.isFinite(n)) return String(v);
    return `${n}${suffix}`;
  }

  // HmIP radio modules report an out-of-band sentinel (commonly 0, or a
  // value outside any real radio's dynamic range) on RSSI_DEVICE/
  // RSSI_PEER before the first real measurement arrives. The daemon now
  // filters these at the source, but the SPA renders defensively too:
  // real Homematic RSSI readings are negative and sit roughly between
  // -30 dBm (very strong) and -120 dBm (very weak) — anything missing
  // or outside that band renders as "—" rather than a misleading number.
  function fmtRssi(v: unknown): string {
    if (v === undefined || v === null || v === "") return "—";
    const n = Number(v);
    if (!Number.isFinite(n) || n > -30 || n < -120) return "—";
    return `${n} dBm`;
  }

  const items = $derived.by<StatusItem[]>(() => {
    const out: StatusItem[] = [];
    if (values.UNREACH !== undefined) {
      const unreach = Boolean(values.UNREACH);
      out.push({
        icon: unreach ? "mdi:wifi-off" : "mdi:wifi",
        label: t("device.maintenance.reachable"),
        value: fmtBool(values.UNREACH, t("common.no"), t("common.yes")),
        tone: unreach ? "error" : "ok",
      });
    }
    if (values.RSSI_DEVICE !== undefined) {
      out.push({
        icon: "mdi:signal",
        label: t("device.maintenance.rssi_device"),
        value: fmtRssi(values.RSSI_DEVICE),
      });
    }
    if (values.RSSI_PEER !== undefined) {
      out.push({
        icon: "mdi:signal",
        label: t("device.maintenance.rssi_peer"),
        value: fmtRssi(values.RSSI_PEER),
      });
    }
    if (values.LOW_BAT !== undefined) {
      const low = Boolean(values.LOW_BAT);
      out.push({
        icon: "mdi:battery-alert",
        label: t("device.maintenance.battery"),
        value: low ? t("device.maintenance.bat_low") : t("device.maintenance.status_ok"),
        tone: low ? "warn" : "ok",
      });
    }
    if (values.OPERATING_VOLTAGE !== undefined) {
      out.push({
        icon: "mdi:battery",
        label: t("device.maintenance.operating_voltage"),
        value: fmtNumber(values.OPERATING_VOLTAGE, " V"),
      });
    }
    if (values.DUTY_CYCLE !== undefined) {
      const blocked = Boolean(values.DUTY_CYCLE);
      out.push({
        icon: blocked ? "mdi:alert-triangle" : "mdi:check-circle",
        label: t("device.maintenance.duty_cycle_level"),
        value: blocked ? t("device.maintenance.blocked") : t("device.maintenance.status_ok"),
        tone: blocked ? "warn" : "ok",
      });
    }
    if (values.DUTY_CYCLE_LEVEL !== undefined) {
      const level = Number(values.DUTY_CYCLE_LEVEL);
      // HmIP radio modules expose the running duty-cycle percentage on
      // their maintenance channel. The legal limit is 1 % per hour, but
      // the value is reported as a 0–100 % scale of that budget; warn
      // above 80 %, alert above 95 %.
      const tone: StatusItem["tone"] = level >= 95 ? "error" : level >= 80 ? "warn" : "ok";
      out.push({
        icon: "mdi:radio-tower",
        label: t("device.maintenance.duty_cycle_level"),
        value: fmtNumber(level, " %"),
        tone,
      });
    }
    if (values.CARRIER_SENSE_LEVEL !== undefined) {
      const level = Number(values.CARRIER_SENSE_LEVEL);
      // Carrier-sense level is the fraction of the listening window
      // during which the radio detected another transmitter on the
      // band. Persistently high values mean external interference.
      const tone: StatusItem["tone"] = level >= 80 ? "warn" : "ok";
      out.push({
        icon: "mdi:waveform",
        label: t("device.maintenance.carrier_sense_level"),
        value: fmtNumber(level, " %"),
        tone,
      });
    }
    if (values.CONFIG_PENDING !== undefined) {
      const pending = Boolean(values.CONFIG_PENDING);
      out.push({
        icon: "mdi:information-outline",
        label: t("device.maintenance.config_pending"),
        value: fmtBool(values.CONFIG_PENDING, t("common.yes"), t("common.no")),
        tone: pending ? "warn" : "ok",
      });
    }
    if (values.UPDATE_PENDING !== undefined) {
      const pending = Boolean(values.UPDATE_PENDING);
      out.push({
        icon: "mdi:download",
        label: t("device.maintenance.update_pending"),
        value: fmtBool(values.UPDATE_PENDING, t("common.yes"), t("common.no")),
        tone: pending ? "warn" : "ok",
      });
    }
    return out;
  });

  function toneClass(tone: StatusItem["tone"]): string {
    switch (tone) {
      case "error":
        return "text-red-600 dark:text-red-400";
      case "warn":
        return "text-amber-600 dark:text-amber-400";
      case "ok":
        return "text-green-600 dark:text-green-400";
      default:
        return "text-slate-500 dark:text-slate-400";
    }
  }
</script>

{#if !loading && items.length > 0}
  <Card class="p-4">
    <h2 class="mb-3 text-sm font-semibold text-slate-700 dark:text-slate-300">{t("device.maintenance.title")}</h2>
    <div class="grid grid-cols-2 gap-x-6 gap-y-2 sm:grid-cols-3 lg:grid-cols-4">
      {#each items as item, i (i)}
        <div class="flex items-center gap-2 text-sm">
          <span class={toneClass(item.tone)}>
            <Icon name={item.icon} size={18} aria-label={item.label} />
          </span>
          <span class="text-slate-500 dark:text-slate-400">{item.label}:</span>
          <span class="font-medium text-slate-900 dark:text-white">{item.value}</span>
        </div>
      {/each}
    </div>
  </Card>
{:else if error}
  <ErrorState message={error} onRetry={load} />
{/if}
