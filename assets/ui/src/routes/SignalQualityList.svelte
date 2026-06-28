<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { RSSIDevice } from "$lib/api/types";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import { t } from "$lib/i18n";
  import { loadLS, saveLS } from "$lib/utils";

  // Signal-quality overview — per-device RF reception strength (RSSI_DEVICE /
  // RSSI_PEER), battery state, and reachability, served by
  // GET /api/v1/diagnostics/rssi. Read from the device model (no CCU radio
  // hit), so it covers HmIP and BidCos alike.

  let devices = $state<RSSIDevice[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let centralFilter = $state(loadLS("signal:central"));
  $effect(() => saveLS("signal:central", centralFilter));

  async function load() {
    loading = true;
    loadError = null;
    try {
      const matrix = await api.rssiInfo();
      devices = matrix.devices;
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  const centrals = $derived(
    [...new Set(devices.map((d) => d.central).filter(Boolean))].sort(),
  );

  const filtered = $derived(
    centralFilter ? devices.filter((d) => d.central === centralFilter) : devices,
  );

  // RSSI quality buckets (dBm, closer to 0 is better) → badge colour.
  function rssiVariant(v: number | null): "success" | "warning" | "danger" | "muted" {
    if (v == null) return "muted";
    if (v >= -60) return "success";
    if (v >= -80) return "warning";
    return "danger";
  }

  // Battery colour thresholds follow the common battery-state-card
  // convention: ≤20% red, ≤55% amber, otherwise green; a LOW_BAT flag is
  // always red regardless of the reported level.
  function batteryVariant(d: RSSIDevice): "success" | "warning" | "danger" | "muted" {
    if (d.low_battery) return "danger";
    if (d.battery_level == null) return "muted";
    if (d.battery_level <= 20) return "danger";
    if (d.battery_level <= 55) return "warning";
    return "success";
  }

  const columns: DataColumn<RSSIDevice>[] = $derived([
    { key: "name", label: t("diagnostics.rssi.device"), sortable: true, title: true, get: (d) => d.name || d.address },
    { key: "interface_id", label: t("diagnostics.interfaces"), sortable: true, get: (d) => d.interface_id },
    { key: "rssi_device", label: t("diagnostics.rssi.device_dbm"), sortable: true, align: "right", get: (d) => d.rssi_device },
    { key: "rssi_peer", label: t("diagnostics.rssi.peer_dbm"), sortable: true, align: "right", get: (d) => d.rssi_peer },
    { key: "battery", label: t("diagnostics.rssi.battery"), sortable: true, align: "right", get: (d) => d.battery_level },
    { key: "reachable", label: t("diagnostics.rssi.reachable"), sortable: true, get: (d) => (d.reachable ? 1 : 0) },
  ]);
</script>

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  <PageHeader
    title={t("signal.title")}
    subtitle={loading ? t("common.loading") : t("signal.count", { count: filtered.length })}
  >
    {#snippet actions()}
      {#if centrals.length > 1}
        <select
          bind:value={centralFilter}
          class="rounded-md border border-slate-300 bg-white px-2 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
          title="CCU"
        >
          <option value="">{t("common.all_ccus")}</option>
          {#each centrals as c (c)}
            <option value={c}>{c}</option>
          {/each}
        </select>
      {/if}
      <Button type="button" variant="outline" size="sm" onclick={() => void load()} disabled={loading}>
        {t("common.reload")}
      </Button>
    {/snippet}
  </PageHeader>

  <p class="mb-3 text-sm text-[var(--ha-secondary-text-color)]">{t("signal.hint")}</p>

  {#if loadError}
    <ErrorState message={loadError} onRetry={load} class="mb-4" />
  {/if}

  {#if loading}
    <LoadingState />
  {:else}
    <DataTable
      rows={filtered}
      {columns}
      rowKey={(d) => d.central + "/" + d.address}
      search
      searchPlaceholder={t("common.search")}
      persistKey="signal-quality"
      initialSort={{ key: "rssi_device", asc: true }}
      emptyMessage={t("signal.empty")}
      emptyIcon="mdi:signal"
    >
      {#snippet cell(d, col)}
        {#if col.key === "name"}
          <span class="font-medium">{d.name || d.address}</span>
          <span class="block font-mono text-xs text-[var(--ha-secondary-text-color)]">{d.address}</span>
        {:else if col.key === "interface_id"}
          <span class="font-mono text-xs">{d.interface_id}</span>
        {:else if col.key === "rssi_device"}
          {#if d.rssi_device != null}
            <Badge variant={rssiVariant(d.rssi_device)}>{d.rssi_device} dBm</Badge>
          {:else}<span class="text-[var(--ha-secondary-text-color)]">—</span>{/if}
        {:else if col.key === "rssi_peer"}
          {#if d.rssi_peer != null}
            <Badge variant={rssiVariant(d.rssi_peer)}>{d.rssi_peer} dBm</Badge>
          {:else}<span class="text-[var(--ha-secondary-text-color)]">—</span>{/if}
        {:else if col.key === "battery"}
          {#if d.battery_level != null}
            <Badge variant={batteryVariant(d)}>{d.battery_level}%</Badge>
          {:else if d.low_battery}
            <Badge variant="danger">{t("signal.low_battery")}</Badge>
          {:else}<span class="text-[var(--ha-secondary-text-color)]">—</span>{/if}
        {:else if col.key === "reachable"}
          {#if d.reachable}
            <Badge variant="success">{t("diagnostics.connected")}</Badge>
          {:else}
            <Badge variant="danger">{t("diagnostics.disconnected")}</Badge>
          {/if}
        {/if}
      {/snippet}
    </DataTable>
  {/if}
</section>
