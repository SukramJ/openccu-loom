<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { CentralLinksReport, CentralLinksStatus } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import { t } from "$lib/i18n";

  // Central-Links toggle. Mirrors aiohomematic's
  // `Device.create_central_links` / `Device.remove_central_links` —
  // the CCU forwards PRESS_SHORT/PRESS_LONG events to the central
  // only after the per-channel reportValueUsage counter is > 0.
  // Surfaces the touched/skipped/failed counters so the user sees
  // exactly which channels participated. Besides the device-wide
  // switch it offers a per-channel switch for each eligible channel,
  // mirroring the CCU channel-config dialog that scopes the switch to
  // the single opened channel.

  type Props = { address: string };
  let { address }: Props = $props();

  let status = $state<CentralLinksStatus | null>(null);
  let loading = $state(true);
  // Device-wide busy state, plus the address of the channel whose
  // per-channel switch is currently in flight.
  let busy = $state<"create" | "remove" | null>(null);
  let busyChannel = $state<string | null>(null);
  let banner = $state<string | null>(null);
  let lastReport = $state<CentralLinksReport | null>(null);
  let loadError = $state<string | null>(null);

  async function load() {
    loading = true;
    loadError = null;
    try {
      status = await api.centralLinksStatus(address);
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  function errorText(err: unknown): string {
    return err instanceof ApiError
      ? `${err.status}: ${err.message}`
      : err instanceof Error
        ? err.message
        : String(err);
  }

  function bannerFor(enabled: boolean, report: CentralLinksReport): string {
    return t(enabled ? "central.report.enabled" : "central.report.disabled", {
      touched: report.touched,
      skipped: report.skipped,
      failed: report.failed,
    });
  }

  async function create() {
    busy = "create";
    banner = null;
    try {
      lastReport = await api.createCentralLinks(address);
      banner = bannerFor(true, lastReport);
      await load();
    } catch (err) {
      banner = errorText(err);
    } finally {
      busy = null;
    }
  }

  async function remove() {
    busy = "remove";
    banner = null;
    try {
      lastReport = await api.removeCentralLinks(address);
      banner = bannerFor(false, lastReport);
      await load();
    } catch (err) {
      banner = errorText(err);
    } finally {
      busy = null;
    }
  }

  async function toggleChannel(channelAddress: string, enable: boolean) {
    busyChannel = channelAddress;
    banner = null;
    try {
      lastReport = enable
        ? await api.createCentralLinks(address, channelAddress)
        : await api.removeCentralLinks(address, channelAddress);
      banner = bannerFor(enable, lastReport);
      await load();
    } catch (err) {
      banner = errorText(err);
    } finally {
      busyChannel = null;
    }
  }

  const anyBusy = $derived(busy !== null || busyChannel !== null);

  onMount(load);
</script>

<Card class="p-4">
  <header class="mb-2 flex items-center justify-between gap-2">
    <h3 class="text-sm font-semibold">{t("central.title")}</h3>
    {#if status}
      {#if status.supported}
        <Badge variant="muted">
          {status.eligible_channels ?? 0}
          {t("central.eligible")}
        </Badge>
      {:else}
        <Badge variant="muted">{t("central.unsupported_badge")}</Badge>
      {/if}
    {/if}
  </header>
  <p class="mb-3 text-xs text-[var(--ha-secondary-text-color)]">{t("central.subtitle")}</p>

  {#if loading}
    <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
  {:else if loadError}
    <p class="text-sm text-red-600 dark:text-red-400">
      {t("common.error")} {loadError}
    </p>
  {:else if status && !status.supported}
    <p class="text-xs italic text-[var(--ha-secondary-text-color)]">
      {t("central.unsupported")}
      {#if status.reason}
        <span class="ml-1 font-mono">({status.reason})</span>
      {/if}
    </p>
  {:else if status}
    <div class="space-y-3">
      <div>
        <p class="mb-1 text-xs font-medium text-[var(--ha-secondary-text-color)]">
          {t("central.device_wide")}
        </p>
        <div class="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            size="sm"
            onclick={() => void create()}
            disabled={anyBusy || (status.eligible_channels ?? 0) === 0}
          >
            {busy === "create" ? "…" : t("central.enable")}
          </Button>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onclick={() => void remove()}
            disabled={anyBusy || (status.eligible_channels ?? 0) === 0}
          >
            {busy === "remove" ? "…" : t("central.disable")}
          </Button>
        </div>
      </div>

      {#if status.channels && status.channels.length > 0}
        <div>
          <p class="mb-1 text-xs font-medium text-[var(--ha-secondary-text-color)]">
            {t("central.per_channel")}
          </p>
          <ul class="space-y-1.5">
            {#each status.channels as ch (ch.address)}
              <li class="flex flex-wrap items-center gap-2">
                <span class="min-w-0 flex-1 text-xs">
                  {t("central.channel_label", { number: ch.number })}
                  <span class="ml-1 font-mono text-[var(--ha-secondary-text-color)]">{ch.address}</span>
                </span>
                <Button
                  type="button"
                  size="sm"
                  onclick={() => void toggleChannel(ch.address, true)}
                  disabled={anyBusy}
                >
                  {busyChannel === ch.address ? "…" : t("central.enable")}
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onclick={() => void toggleChannel(ch.address, false)}
                  disabled={anyBusy}
                >
                  {busyChannel === ch.address ? "…" : t("central.disable")}
                </Button>
              </li>
            {/each}
          </ul>
        </div>
      {/if}

      {#if banner}
        <p class="text-xs text-[var(--ha-secondary-text-color)]">{banner}</p>
      {/if}
    </div>
  {/if}
</Card>
