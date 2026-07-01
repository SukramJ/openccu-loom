<!--
  Read-only cross-CCU overview (roadmap "Operations & multi-CCU").
  Additive lens over data the SPA already fetches elsewhere:

    - GET /api/v1/system/ccu (`api.getSystemCCUs()`) — per-central
      name/host/availability/model/version/config-URL/configured
      interfaces. Already reflects runtime-added CCUs live.
    - `deviceStore` (the same store DeviceList/Overview use) — device
      counts are derived client-side by filtering `DeviceSummary.central`
      against each CCU's name; no new endpoint needed.

  Rooms/functions stay per-CCU by design (see docs/roadmap.md) — this
  route only surfaces fleet-level identity + reachability + device
  counts, never mutates anything.
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { api } from "$lib/api/client";
  import type { SystemCCUEntry } from "$lib/api/types";
  import { deviceStore } from "$lib/stores/devices.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { t } from "$lib/i18n";

  let ccus = $state<SystemCCUEntry[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);

  async function load() {
    loading = true;
    error = null;
    try {
      const [entries] = await Promise.all([
        api.getSystemCCUs(),
        deviceStore.refresh(),
      ]);
      ccus = entries;
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
    deviceStore.ensureStream();
  });

  // Deterministic ordering — a fleet's card grid should not reshuffle
  // between renders just because the REST response happened to change
  // entry order.
  const sortedCcus = $derived(
    [...ccus].sort((a, b) => a.name.localeCompare(b.name)),
  );

  function deviceCount(central: string): number {
    return deviceStore.items.filter((d) => d.central === central).length;
  }
</script>

<svelte:head>
  <title>{t("page.title.fleet")}</title>
</svelte:head>

<section class="w-full px-4 py-8 sm:px-6">
  <PageHeader title={t("fleet.title")} subtitle={t("fleet.subtitle")} />

  {#if loading}
    <LoadingState />
  {:else if error}
    <ErrorState message={t("fleet.load_error", { error })} onRetry={() => void load()} />
  {:else if sortedCcus.length === 0}
    <EmptyState message={t("fleet.empty")} icon="mdi:server-network" />
  {:else}
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
      {#each sortedCcus as ccu (ccu.name)}
        <Card class="flex flex-col gap-3 p-4">
          <div class="flex items-start justify-between gap-2">
            <h2 class="min-w-0 truncate text-base font-semibold text-slate-900 dark:text-white">
              {ccu.name}
            </h2>
            <Badge variant={ccu.available ? "success" : "danger"}>
              {ccu.available ? t("fleet.status.online") : t("fleet.status.offline")}
            </Badge>
          </div>

          <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm">
            <dt class="text-slate-500 dark:text-slate-400">{t("fleet.field.host")}</dt>
            <dd class="min-w-0 truncate font-mono text-xs text-slate-700 dark:text-slate-300">
              {ccu.host}{ccu.hostname && ccu.hostname !== ccu.host ? ` (${ccu.hostname})` : ""}
            </dd>

            {#if ccu.model}
              <dt class="text-slate-500 dark:text-slate-400">{t("fleet.field.model")}</dt>
              <dd class="min-w-0 truncate text-slate-700 dark:text-slate-300">
                {ccu.model}{ccu.version ? ` (${ccu.version})` : ""}
              </dd>
            {:else if ccu.version}
              <dt class="text-slate-500 dark:text-slate-400">{t("fleet.field.version")}</dt>
              <dd class="text-slate-700 dark:text-slate-300">{ccu.version}</dd>
            {/if}

            {#if ccu.serial}
              <dt class="text-slate-500 dark:text-slate-400">{t("fleet.field.serial")}</dt>
              <dd class="min-w-0 truncate font-mono text-xs text-slate-700 dark:text-slate-300">
                {ccu.serial}
              </dd>
            {/if}

            <dt class="text-slate-500 dark:text-slate-400">{t("fleet.field.devices")}</dt>
            <dd class="tabular-nums text-slate-700 dark:text-slate-300">
              {deviceCount(ccu.name)}
            </dd>
          </dl>

          {#if ccu.configured_interfaces.length > 0}
            <div class="flex flex-col gap-1">
              <span class="text-xs text-slate-500 dark:text-slate-400">
                {t("fleet.field.interfaces")}
              </span>
              <div class="flex flex-wrap gap-1">
                {#each ccu.configured_interfaces as iface (iface)}
                  <Badge variant="muted">{iface}</Badge>
                {/each}
              </div>
            </div>
          {/if}

          {#if ccu.url}
            <a
              href={ccu.url}
              target="_blank"
              rel="noopener"
              class="text-xs font-medium underline"
              style="color: var(--ha-primary-color);"
            >
              {t("fleet.open_webui")}
            </a>
          {/if}
        </Card>
      {/each}
    </div>
  {/if}
</section>
