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
      toastStore.success(t("common.remove") + " OK");
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
    <p class="text-sm" style="color: var(--ha-secondary-text-color);">{t("common.loading")}</p>
  {:else if matterStore.fabricsError}
    <p class="text-sm" style="color: var(--ha-error-color, #ef4444);">{matterStore.fabricsError}</p>
  {:else if matterStore.fabrics.length === 0}
    <p class="text-sm" style="color: var(--ha-secondary-text-color);">No fabrics paired yet.</p>
  {:else}
    <div class="rounded-lg border overflow-x-auto" style="border-color: var(--ha-divider-color);">
      <table class="w-full text-sm">
        <thead>
          <tr style="border-bottom: 1px solid var(--ha-divider-color); background-color: var(--ha-secondary-background-color);">
            <th class="px-3 py-2 text-left">Vendor</th>
            <th class="px-3 py-2 text-left">Label</th>
            <th class="px-3 py-2 text-left hidden md:table-cell">Fabric #</th>
            <th class="px-3 py-2 text-left hidden lg:table-cell">Node ID</th>
            <th class="px-3 py-2 text-left"></th>
          </tr>
        </thead>
        <tbody>
          {#each matterStore.fabrics as fabric (fabric.fabric_index)}
            <tr style="border-bottom: 1px solid var(--ha-divider-color);">
              <td class="px-3 py-2" style="color: var(--ha-primary-text-color);">
                {vendorName(fabric.vendor_id)}
              </td>
              <td class="px-3 py-2" style="color: {fabric.label ? 'var(--ha-primary-text-color)' : 'var(--ha-secondary-text-color)'};">
                {fabric.label || t("matter.fabric.label_unknown")}
              </td>
              <td class="px-3 py-2 hidden md:table-cell" style="color: var(--ha-secondary-text-color);">
                {fabric.fabric_index}
              </td>
              <td class="px-3 py-2 hidden lg:table-cell font-mono text-xs" style="color: var(--ha-secondary-text-color);">
                0x{fabric.node_id}
              </td>
              <td class="px-3 py-2 text-right">
                <Button
                  size="sm"
                  variant="destructive"
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
  <div
    class="rounded-lg border p-4"
    style="border-color: var(--ha-divider-color); background-color: var(--ha-secondary-background-color);"
  >
    <h3 class="font-medium mb-2" style="color: var(--ha-primary-text-color);">
      {t("matter.fabric.share_bridge")}
    </h3>
    <div class="flex flex-wrap items-center gap-2">
      <select
        class="h-9 rounded-md border px-2 text-sm"
        style="border-color: var(--ha-divider-color); background-color: var(--ha-card-background-color); color: var(--ha-primary-text-color);"
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
        <p class="text-sm font-medium" style="color: var(--ha-primary-text-color);">
          {t("matter.pair.manual_code")}:
          <span class="font-mono">{shareWindow.manual_code}</span>
        </p>
        <p class="text-xs" style="color: var(--ha-secondary-text-color);">
          QR payload: <span class="font-mono">{shareWindow.qr_code}</span>
        </p>
      </div>
    {/if}
  </div>
</div>
