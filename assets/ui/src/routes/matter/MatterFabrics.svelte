<script lang="ts">
  import { onMount } from "svelte";
  import { matterStore } from "$lib/stores/matter.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { api, ApiError } from "$lib/api/client";
  import { t } from "$lib/i18n";
  import Button from "$lib/components/ui/Button.svelte";
  import type { MatterCommissioningWindow } from "$lib/api/matter-types";

  onMount(async () => {
    await matterStore.loadFabrics();
  });

  let sharing = $state(false);
  let shareWindow = $state<MatterCommissioningWindow | null>(null);
  let shareDuration = $state(300); // 5 min default

  // Vendor display name lookup table
  const VENDOR_NAMES: Record<number, string> = {
    0x1349: "Apple",
    0x6006: "Google",
    0x1391: "Amazon",
    0x04e5: "Signify (Philips)",
    0x1321: "SmartThings",
    0xfff1: "Test Vendor",
  };

  function vendorName(vendorId: number): string {
    return VENDOR_NAMES[vendorId] ?? `0x${vendorId.toString(16).toUpperCase().padStart(4, "0")}`;
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
</script>

<div class="space-y-6">
  {#if matterStore.fabricsLoading}
    <p class="text-sm text-slate-500 dark:text-slate-400">{t("common.loading")}</p>
  {:else if matterStore.fabricsError}
    <p class="text-sm text-red-600 dark:text-red-400">{matterStore.fabricsError}</p>
  {:else if matterStore.fabrics.length === 0}
    <p class="text-sm text-slate-500 dark:text-slate-400">{t("matter.fabrics.empty")}</p>
  {:else}
    <div class="rounded-lg border border-slate-200 dark:border-slate-700 overflow-x-auto">
      <table class="table-reflow w-full text-sm">
        <thead>
          <tr class="border-b border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50">
            <th class="px-3 py-3 text-left">{t("matter.fabrics.col_vendor")}</th>
            <th class="px-3 py-3 text-left">{t("matter.fabrics.col_label")}</th>
            <th class="px-3 py-3 text-left hidden md:table-cell">{t("matter.fabrics.col_fabric")}</th>
            <th class="px-3 py-3 text-left hidden lg:table-cell">{t("matter.fabrics.col_node_id")}</th>
            <th class="px-3 py-3 text-left"></th>
          </tr>
        </thead>
        <tbody>
          {#each matterStore.fabrics as fabric (fabric.fabric_index)}
            <tr class="border-b border-slate-200 dark:border-slate-700">
              <td class="reflow-title px-3 py-3 text-slate-900 dark:text-slate-100">
                {vendorName(fabric.vendor_id)}
              </td>
              <td class="px-3 py-3 {fabric.label ? 'text-slate-900 dark:text-slate-100' : 'text-slate-500 dark:text-slate-400'}" data-label={t("matter.fabrics.col_label")}>
                {fabric.label || t("matter.fabric.label_unknown")}
              </td>
              <td class="px-3 py-3 hidden md:table-cell text-slate-500 dark:text-slate-400" data-label={t("matter.fabrics.col_fabric")}>
                {fabric.fabric_index}
              </td>
              <td class="px-3 py-3 hidden lg:table-cell font-mono text-xs text-slate-500 dark:text-slate-400" data-label={t("matter.fabrics.col_node_id")}>
                0x{fabric.node_id}
              </td>
              <td class="reflow-actions px-3 py-3 text-right">
                <Button
                  size="sm"
                  variant="ghost"
                  class="text-red-600 hover:text-red-700 dark:text-red-400"
                  onclick={() => void unpair(fabric.fabric_index, fabric.label)}
                >
                  {t("common.remove")}
                </Button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}

  <!-- Share bridge section -->
  <div class="rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 p-4">
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
