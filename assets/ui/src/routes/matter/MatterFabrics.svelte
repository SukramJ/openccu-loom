<script lang="ts">
  import { onMount } from "svelte";
  import { matterStore } from "$lib/stores/matter.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { api, ApiError } from "$lib/api/client";
  import { t } from "$lib/i18n";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import type { MatterCommissioningWindow, MatterFabric } from "$lib/api/matter-types";

  onMount(async () => {
    await matterStore.loadFabrics();
  });

  let sharing = $state(false);
  let shareWindow = $state<MatterCommissioningWindow | null>(null);
  let shareDuration = $state(300); // 5 min default

  // Vendor display name lookup table
  // The vendor name comes from the daemon: it owns the vendor table so
  // one list cannot drift from the other. It did — this component
  // carried its own table in which two ids were wrong and two others
  // disagreed with the daemon's, and a fabric could be labelled one
  // vendor here and classified as another ecosystem on the
  // compatibility tab. A fabric served before that field existed falls
  // back to the raw id.
  function vendorName(fabric: MatterFabric): string {
    return (
      fabric.vendor_name ||
      `0x${fabric.vendor_id.toString(16).toUpperCase().padStart(4, "0")}`
    );
  }

  // The fabric list serves node ids as JSON numbers; controllers and
  // chip-tool print them as 16-digit hex, so a `0x` in front of the
  // decimal value matches nothing the operator can compare against.
  function nodeIdHex(nodeId: number): string {
    return `0x${nodeId.toString(16).toUpperCase().padStart(16, "0")}`;
  }

  async function unpair(fabricIndex: number, label: string) {
    const confirmed = await confirmStore.ask({
      title: t("matter.fabric.unpair_confirm"),
      body: label || t("matter.fabric.label_unknown"),
      destructive: true,
      confirmLabel: t("common.remove"),
    });
    if (!confirmed) return;
    try {
      await api.deleteMatterFabric(fabricIndex);
      toastStore.success(t("matter.fabric.unpaired"));
      await matterStore.loadFabrics();
      await matterStore.loadStatus();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    }
  }

  async function shareBridge() {
    sharing = true;
    shareWindow = null;
    try {
      const win = await api.matterShareBridge(shareDuration);
      shareWindow = win;
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      sharing = false;
    }
  }

  const columns: DataColumn<MatterFabric>[] = $derived([
    { key: "vendor", label: t("matter.fabrics.col_vendor"), sortable: true, title: true, get: (f) => vendorName(f) },
    { key: "label", label: t("matter.fabrics.col_label"), sortable: true, get: (f) => f.label || t("matter.fabric.label_unknown") },
    { key: "fabric", label: t("matter.fabrics.col_fabric"), sortable: true, get: (f) => f.fabric_index },
    { key: "node_id", label: t("matter.fabrics.col_node_id"), sortable: true, get: (f) => f.node_id },
    { key: "action", label: "", align: "right", cellClass: "reflow-actions" },
  ]);
</script>

<div class="space-y-6">
  {#if matterStore.fabricsLoading}
    <LoadingState message={t("common.loading")} />
  {:else if matterStore.fabricsError}
    <ErrorState message={matterStore.fabricsError} onRetry={() => void matterStore.loadFabrics()} />
  {:else}
    <Card class="p-4">
      <DataTable
        rows={matterStore.fabrics}
        {columns}
        rowKey={(f) => String(f.fabric_index)}
        emptyMessage={t("matter.fabrics.empty")}
        emptyIcon="mdi:link"
        initialSort={{ key: "vendor", asc: true }}
      >
        {#snippet cell(fabric, col)}
          {#if col.key === "vendor"}
            <span class="text-slate-900 dark:text-slate-100">{vendorName(fabric)}</span>
          {:else if col.key === "label"}
            <span class="{fabric.label ? 'text-slate-900 dark:text-slate-100' : 'text-slate-500 dark:text-slate-400'}">
              {fabric.label || t("matter.fabric.label_unknown")}
            </span>
          {:else if col.key === "fabric"}
            <span class="text-slate-500 dark:text-slate-400">{fabric.fabric_index}</span>
          {:else if col.key === "node_id"}
            <span class="font-mono text-xs text-slate-500 dark:text-slate-400">{nodeIdHex(fabric.node_id)}</span>
          {:else if col.key === "action"}
            <Button
              size="sm"
              variant="ghost"
              class="text-red-600 hover:text-red-700 dark:text-red-400"
              onclick={() => void unpair(fabric.fabric_index, fabric.label)}
            >
              {t("common.remove")}
            </Button>
          {/if}
        {/snippet}
      </DataTable>
    </Card>
  {/if}

  <!-- Share bridge section -->
  <div class="rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-[color-mix(in_srgb,var(--color-slate-800)_50%,transparent)] p-4">
    <h3 class="font-medium mb-2 text-slate-900 dark:text-slate-100">
      {t("matter.fabric.share_bridge")}
    </h3>
    <div class="flex flex-wrap items-center gap-2">
      <select
        class="h-9 rounded-md border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 px-2 text-sm"
        bind:value={shareDuration}
      >
        <option value={300}>5 {t("matter.pair.minutes")}</option>
        <option value={600}>10 {t("matter.pair.minutes")}</option>
        <option value={900}>15 {t("matter.pair.minutes")}</option>
      </select>
      <Button size="sm" disabled={sharing} onclick={() => void shareBridge()}>
        {sharing ? t("common.saving") : t("matter.fabric.share_bridge")}
      </Button>
    </div>

    {#if shareWindow}
      <div class="mt-4 space-y-2">
        <p class="text-sm font-medium text-slate-900 dark:text-slate-100">
          {t("matter.pair.manual_code")}:
          <span class="font-mono break-all">{shareWindow.manual_code}</span>
        </p>
        <p class="text-xs text-slate-500 dark:text-slate-400">
          {t("matter.pair.qr_payload")}: <span class="font-mono break-all">{shareWindow.qr_code}</span>
        </p>
      </div>
    {/if}
  </div>
</div>
