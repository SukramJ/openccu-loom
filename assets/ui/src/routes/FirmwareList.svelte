<script lang="ts">
  import { onMount } from "svelte";
  import { deviceStore } from "$lib/stores/devices.svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { DeviceDetail } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { t } from "$lib/i18n";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";

  // Firmware overview — shows all devices that have firmware metadata,
  // grouped by update availability. Uses:
  //   REST  GET  /api/v1/devices          (device list with updatable flag)
  //   REST  GET  /api/v1/devices/{addr}   (firmware detail: current, available, update_state)
  //   REST  POST /api/v1/devices/{addr}/firmware/update  (trigger OTA update)
  // The WS firmware.info command is wired to a CCU-wide firmware cache;
  // the per-device info already rides in every DeviceDetail response, so
  // we load detail lazily only for devices that are updatable.

  // ---- state ----------------------------------------------------------

  let detailMap = $state<Record<string, DeviceDetail>>({});
  let loadingDetail = $state<Set<string>>(new Set());
  let updating = $state<Set<string>>(new Set());
  let filterMode = $state<"all" | "updatable">("all");
  let searchText = $state("");
  let centralFilter = $state("");

  // ---- derived ---------------------------------------------------------

  const allDevices = $derived(deviceStore.items);

  const centrals = $derived([
    ...new Set(allDevices.map((d) => d.central).filter(Boolean)),
  ].sort());

  const filtered = $derived.by(() => {
    let list = allDevices;
    if (centralFilter) {
      list = list.filter((d) => d.central === centralFilter);
    }
    if (filterMode === "updatable") {
      list = list.filter((d) => d.update_available);
    }
    if (searchText.trim()) {
      const q = searchText.trim().toLowerCase();
      list = list.filter(
        (d) =>
          d.name.toLowerCase().includes(q) ||
          d.address.toLowerCase().includes(q) ||
          d.model.toLowerCase().includes(q),
      );
    }
    return list;
  });

  const updatableCount = $derived(
    allDevices.filter((d) => d.update_available).length,
  );

  // ---- lifecycle -------------------------------------------------------

  onMount(async () => {
    if (deviceStore.items.length === 0) {
      await deviceStore.refresh();
    }
    // Eagerly load detail for updatable devices so firmware version
    // info is immediately visible.
    const updatable = deviceStore.items.filter((d) => d.updatable);
    await Promise.allSettled(updatable.map((d) => loadDetail(d.address)));
  });

  // ---- helpers ---------------------------------------------------------

  async function loadDetail(addr: string) {
    if (detailMap[addr] || loadingDetail.has(addr)) return;
    const next = new Set(loadingDetail);
    next.add(addr);
    loadingDetail = next;
    try {
      const detail = await api.getDevice(addr);
      detailMap = { ...detailMap, [addr]: detail };
    } catch {
      // Silently ignore — the row still renders without version strings.
    } finally {
      const next2 = new Set(loadingDetail);
      next2.delete(addr);
      loadingDetail = next2;
    }
  }

  async function triggerUpdate(addr: string, name: string) {
    const ok = await confirmStore.ask({
      title: t("firmware.confirm_update", { name }),
      body: "",
      confirmLabel: t("firmware.update"),
      destructive: false,
    });
    if (!ok) return;
    const next = new Set(updating);
    next.add(addr);
    updating = next;
    try {
      await api.updateFirmware(addr);
      toastStore.success(t("firmware.triggered", { name }));
      // Refresh device list so the updatable badge updates.
      await deviceStore.refresh();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err),
      );
    } finally {
      const next2 = new Set(updating);
      next2.delete(addr);
      updating = next2;
    }
  }

  function stateLabel(state: string | undefined): string {
    if (!state) return "";
    return t(`firmware.state.${state}`, {}) || state;
  }

  function stateBadgeVariant(
    state: string | undefined,
  ): "default" | "success" | "warning" | "danger" | "muted" {
    if (!state) return "muted";
    if (
      state === "PERFORMING_UPDATE" ||
      state === "DO_UPDATE_PENDING" ||
      state === "DELIVER_FIRMWARE_IMAGE" ||
      state === "LIVE_DELIVER_FIRMWARE_IMAGE"
    )
      return "warning";
    if (state === "NEW_FIRMWARE_AVAILABLE" || state === "LIVE_NEW_FIRMWARE_AVAILABLE")
      return "default";
    if (state === "UP_TO_DATE" || state === "LIVE_UP_TO_DATE") return "success";
    if (state === "READY_FOR_UPDATE") return "warning";
    return "muted";
  }

  function isInProgress(state: string | undefined): boolean {
    return (
      state === "PERFORMING_UPDATE" ||
      state === "DO_UPDATE_PENDING" ||
      state === "DELIVER_FIRMWARE_IMAGE" ||
      state === "LIVE_DELIVER_FIRMWARE_IMAGE"
    );
  }
</script>

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  <!-- Header -->
  <header class="mb-4 flex flex-wrap items-start justify-between gap-3">
    <div>
      <h1 class="text-2xl font-semibold">{t("firmware.title")}</h1>
      <p class="text-sm text-slate-500 dark:text-slate-400">
        {t("firmware.subtitle")}
      </p>
    </div>
    <Button
      type="button"
      variant="outline"
      size="sm"
      onclick={() => void deviceStore.refresh()}
      disabled={deviceStore.loading}
    >
      {t("common.reload")}
    </Button>
  </header>

  <!-- Summary bar -->
  {#if updatableCount > 0}
    <div class="mb-4 rounded-md border border-amber-400 bg-amber-50 px-4 py-3 text-sm text-amber-900 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200">
      {t("firmware.updates_available", { count: updatableCount })}
    </div>
  {/if}

  <!-- Filters -->
  <div class="mb-4 flex flex-wrap items-center gap-3">
    <div class="flex rounded-md border border-slate-300 dark:border-slate-700">
      <Button
        type="button"
        variant={filterMode === "all" ? "default" : "outline"}
        size="sm"
        class="rounded-r-none border-r-0"
        onclick={() => (filterMode = "all")}
      >
        {t("firmware.filter.all")}
      </Button>
      <Button
        type="button"
        variant={filterMode === "updatable" ? "default" : "outline"}
        size="sm"
        class="rounded-l-none"
        onclick={() => (filterMode = "updatable")}
      >
        {t("firmware.filter.updatable")}
        {#if updatableCount > 0}
          <span
            class="ml-1 inline-flex h-4 w-4 items-center justify-center rounded-full bg-amber-500 text-[10px] font-bold text-white dark:bg-amber-600"
          >{updatableCount}</span>
        {/if}
      </Button>
    </div>

    {#if centrals.length > 1}
      <select
        bind:value={centralFilter}
        class="h-8 rounded-md border border-slate-300 bg-white px-2 text-sm dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
        title="CCU"
      >
        <option value="">{t("common.all_ccus")}</option>
        {#each centrals as c (c)}
          <option value={c}>{c}</option>
        {/each}
      </select>
    {/if}

    <Input
      type="search"
      placeholder={t("common.search")}
      bind:value={searchText}
    />
  </div>

  <!-- Loading / error / empty -->
  {#if deviceStore.loading && allDevices.length === 0}
    <LoadingState />
  {:else if deviceStore.error}
    <ErrorState message={deviceStore.error} onRetry={() => void deviceStore.refresh()} />
  {:else if filtered.length === 0}
    <EmptyState
      message={filterMode === "updatable" ? t("firmware.no_updates") : t("devices.empty")}
      icon="mdi:upload"
    />
  {:else}
    <!-- Device table -->
    <Card class="overflow-hidden p-0">
      <table class="table-reflow w-full text-sm">
        <thead class="border-b border-slate-200 bg-slate-50 text-left text-xs font-semibold uppercase tracking-wide text-slate-500 dark:border-slate-800 dark:bg-slate-900 dark:text-slate-400">
          <tr>
            <th class="px-4 py-2">{t("firmware.col.device")}</th>
            <th class="px-4 py-2">{t("firmware.col.model")}</th>
            <th class="px-4 py-2">{t("firmware.col.current")}</th>
            <th class="px-4 py-2">{t("firmware.col.available")}</th>
            <th class="px-4 py-2">{t("firmware.col.state")}</th>
            <th class="px-4 py-2">{t("firmware.col.action")}</th>
          </tr>
        </thead>
        <tbody>
          {#each filtered as device (device.interface_id + "/" + device.address)}
            {@const detail = detailMap[device.address]}
            {@const fw = detail?.firmware}
            {@const busy = updating.has(device.address)}
            {@const loadingFw = loadingDetail.has(device.address)}
            {@const versionsMatch = !!fw?.Current && !!fw?.Available && fw.Current === fw.Available}
            {@const updateAvailable = detail?.update_available ?? false}
            <tr class="border-b border-slate-100 transition-colors last:border-0 dark:border-slate-800">
              <!-- Device name / address -->
              <td class="reflow-title px-4 py-3">
                <a
                  href="#/devices/{encodeURIComponent(device.address)}"
                  class="font-medium text-brand-700 hover:underline dark:text-brand-400"
                >
                  {device.name || device.address}
                </a>
                <div class="text-xs text-slate-500 dark:text-slate-400">
                  {device.address}
                  {#if centrals.length > 1 && device.central}
                    · <span class="font-medium">{device.central}</span>
                  {/if}
                </div>
              </td>

              <!-- Model -->
              <td class="px-4 py-3" data-label={t("firmware.col.model")}>
                <span>{device.model}</span>
                {#if device.model_label && device.model_label !== device.model}
                  <div class="text-xs text-slate-500 dark:text-slate-400">
                    {device.model_label}
                  </div>
                {/if}
              </td>

              <!-- Current version -->
              <td class="px-4 py-3 font-mono text-xs" data-label={t("firmware.col.current")}>
                {#if loadingFw}
                  <span class="text-slate-400 dark:text-slate-500">…</span>
                {:else}
                  {fw?.Current || "—"}
                {/if}
              </td>

              <!-- Available version -->
              <td class="px-4 py-3 font-mono text-xs" data-label={t("firmware.col.available")}>
                {#if loadingFw}
                  <span class="text-slate-400 dark:text-slate-500">…</span>
                {:else if fw?.Available && fw.Available !== fw.Current}
                  <span class="font-semibold text-amber-600 dark:text-amber-400">
                    {fw.Available}
                  </span>
                {:else}
                  {fw?.Available || "—"}
                {/if}
              </td>

              <!-- Update state -->
              <!-- Priority order:
                   1. Both versions known and equal → canonical "Up to date",
                      regardless of CCU-reported state or updatable flag.
                   2. isUpdateAvailable (available > current) → show CCU state
                      if present; fall back to generic "Update available" badge.
                   3. available <= current (versions known, no upgrade) → "Up to date".
                   4. Version info not yet loaded → show CCU state if present,
                      or fall back to device.update_available (the gated pending
                      flag, NOT the updatable capability) for a best-effort hint. -->
              <td class="px-4 py-3" data-label={t("firmware.col.state")}>
                {#if loadingFw}
                  <span class="text-xs text-slate-400 dark:text-slate-500">…</span>
                {:else if versionsMatch || (!updateAvailable && !!fw?.Current && !!fw?.Available)}
                  <Badge variant="success">{t("firmware.state.UP_TO_DATE")}</Badge>
                {:else if updateAvailable}
                  {#if fw?.UpdateState}
                    <Badge variant={stateBadgeVariant(fw.UpdateState)}>
                      {stateLabel(fw.UpdateState)}
                    </Badge>
                  {:else}
                    <Badge variant="default">{t("firmware.state.NEW_FIRMWARE_AVAILABLE")}</Badge>
                  {/if}
                {:else if fw?.UpdateState}
                  <Badge variant={stateBadgeVariant(fw.UpdateState)}>
                    {stateLabel(fw.UpdateState)}
                  </Badge>
                {:else if device.update_available}
                  <Badge variant="default">{t("firmware.state.NEW_FIRMWARE_AVAILABLE")}</Badge>
                {:else}
                  <Badge variant="muted">{t("firmware.state.UP_TO_DATE")}</Badge>
                {/if}
              </td>

              <!-- Action -->
              <!-- Show the update button only when the version comparison
                   confirms available > current. This is the single source of
                   truth; device.updatable (CCU flag) may lag reality after a
                   manual flash or a stale firmware-check cache. -->
              <td class="reflow-actions px-4 py-3">
                {#if updateAvailable && !busy && !loadingFw}
                  {@const inProgress = isInProgress(fw?.UpdateState)}
                  <Button
                    type="button"
                    variant="default"
                    size="sm"
                    disabled={inProgress}
                    onclick={() => void triggerUpdate(device.address, device.name || device.address)}
                  >
                    {#if inProgress}
                      {t("firmware.in_progress")}
                    {:else}
                      {t("firmware.update")}
                    {/if}
                  </Button>
                {:else if busy}
                  <Button
                    type="button"
                    variant="default"
                    size="sm"
                    disabled={true}
                  >
                    {t("firmware.triggering")}
                  </Button>
                {:else if !loadingFw && detail && !updateAvailable}
                  <span class="text-xs text-slate-500 dark:text-slate-400">
                    {t("firmware.up_to_date")}
                  </span>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </Card>

    <p class="mt-3 text-xs text-slate-500 dark:text-slate-400">
      {t("firmware.count", { count: filtered.length, total: allDevices.length })}
    </p>
  {/if}
</section>
