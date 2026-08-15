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
  import type { MatterFabric } from "$lib/api/matter-types";

  onMount(async () => {
    await matterStore.loadFabrics();
  });

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

  let busy = $state(false);

  async function forceSync() {
    busy = true;
    try {
      await api.matterForceSync();
      toastStore.success(t("matter.maint.force_sync_done"));
      await matterStore.loadStatus();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      busy = false;
    }
  }

  async function factoryReset() {
    const confirmed = await confirmStore.ask({
      title: t("matter.maint.reset_confirm"),
      body: t("matter.maint.reset_confirm_body"),
      destructive: true,
      confirmLabel: t("matter.maint.reset_confirm_label"),
    });
    if (!confirmed) return;
    busy = true;
    try {
      await api.matterFactoryReset();
      toastStore.success(t("matter.maint.reset_done"));
      await matterStore.loadFabrics();
      await matterStore.loadStatus();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      busy = false;
    }
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

  <!-- Maintenance: one non-destructive repair, one irreversible reset -->
  <div class="rounded-lg border border-slate-200 dark:border-slate-700 p-4">
    <h3 class="font-medium mb-2 text-slate-900 dark:text-slate-100">
      {t("matter.maint.title")}
    </h3>
    <div class="flex flex-wrap items-center gap-3">
      <Button variant="outline" onclick={forceSync} disabled={busy}>
        {t("matter.maint.force_sync")}
      </Button>
      <span class="text-xs text-slate-500 dark:text-slate-400">
        {t("matter.maint.force_sync_hint")}
      </span>
    </div>
    <div class="mt-3 flex flex-wrap items-center gap-3">
      <Button variant="outline-destructive" onclick={factoryReset} disabled={busy}>
        {t("matter.maint.reset")}
      </Button>
      <span class="text-xs text-slate-500 dark:text-slate-400">
        {t("matter.maint.reset_hint")}
      </span>
    </div>
  </div>

  <!-- Adding a controller runs on the pairing tab -->
  <div class="rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-[color-mix(in_srgb,var(--color-slate-800)_50%,transparent)] p-4">
    <h3 class="font-medium mb-2 text-slate-900 dark:text-slate-100">
      {t("matter.fabric.share_bridge")}
    </h3>
    <p class="text-sm text-slate-500 dark:text-slate-400">
      {t("matter.fabric.share_bridge_hint")}
    </p>
    <a
      href="#/matter/pair"
      data-testid="share-bridge-link"
      class="mt-3 inline-flex items-center text-sm font-medium text-brand-600 dark:text-brand-400 hover:underline"
    >
      {t("matter.fabric.share_bridge_go")}
    </a>
  </div>
</div>
