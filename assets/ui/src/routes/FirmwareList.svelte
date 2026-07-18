<script lang="ts">
  import { onMount } from "svelte";
  import { deviceStore } from "$lib/stores/devices.svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { DeviceDetail, DeviceSummary } from "$lib/api/types";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import { t } from "$lib/i18n";
  import { loadLS, saveLS } from "$lib/utils";
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
  let filterMode = $state<"all" | "updatable">(
    loadLS("firmware:mode", "all") === "updatable" ? "updatable" : "all",
  );
  $effect(() => saveLS("firmware:mode", filterMode));
  let centralFilter = $state(loadLS("firmware:central"));
  $effect(() => saveLS("firmware:central", centralFilter));

  // ---- derived ---------------------------------------------------------

  const allDevices = $derived(deviceStore.items);

  const centrals = $derived([
    ...new Set(allDevices.map((d) => d.central).filter(Boolean)),
  ].sort());

  // The CCU reports the all-zero placeholder "0.0.0" as the available
  // firmware of devices it has no OTA image for — e.g. the RPI-RF-MOD
  // gateway module, which is updated through the CCU firmware itself.
  // A placeholder is not an available version: comparing it against
  // the installed version must never yield "update available".
  function availableVersion(fw: DeviceDetail["firmware"] | undefined): string {
    const v = fw?.Available ?? "";
    return /^0+(\.0+)*$/.test(v) ? "" : v;
  }

  // hasUpdate treats a device as having an update when either the
  // gated flag says an install can start now, or the loaded firmware
  // detail shows a newer version the CCU has not delivered to the
  // device yet (HmIP NEW_FIRMWARE_AVAILABLE). The gated flag alone
  // would hide a pending 1.2.2 → 1.4.10 update from the filter and
  // summary while the row visibly lists both versions.
  function hasUpdate(d: DeviceSummary): boolean {
    if (d.update_available) return true;
    const fw = detailMap[d.address]?.firmware;
    const avail = availableVersion(fw);
    return (
      !!fw?.Current &&
      !!avail &&
      avail !== fw.Current &&
      fw.UpdateState !== "UP_TO_DATE" &&
      fw.UpdateState !== "LIVE_UP_TO_DATE"
    );
  }

  const filtered = $derived.by(() => {
    let list = allDevices;
    if (centralFilter) {
      list = list.filter((d) => d.central === centralFilter);
    }
    if (filterMode === "updatable") {
      list = list.filter((d) => hasUpdate(d));
    }
    return list;
  });

  const columns: DataColumn<DeviceSummary>[] = $derived([
    { key: "device", label: t("firmware.col.device"), sortable: true, title: true, get: (d) => d.name || d.address },
    { key: "model", label: t("firmware.col.model"), sortable: true, get: (d) => d.model },
    { key: "current", label: t("firmware.col.current"), sortable: true, get: (d) => detailMap[d.address]?.firmware?.Current ?? "" },
    { key: "available", label: t("firmware.col.available"), sortable: true, get: (d) => availableVersion(detailMap[d.address]?.firmware) },
    { key: "state", label: t("firmware.col.state"), sortable: true, get: (d) => detailMap[d.address]?.firmware?.UpdateState ?? (d.update_available ? "NEW_FIRMWARE_AVAILABLE" : "UP_TO_DATE") },
    { key: "action", label: t("firmware.col.action"), align: "right", cellClass: "reflow-actions" },
  ]);

  const updatableCount = $derived(allDevices.filter((d) => hasUpdate(d)).length);

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

  let reloading = $state(false);

  // Reload = ask the daemon to re-read firmware data from the CCU, then
  // re-fetch the device list AND the per-device firmware details. The
  // detail cache must be dropped explicitly — loadDetail short-circuits
  // on cached entries, so without this the version columns would keep
  // showing the values from the first page load forever.
  async function reloadAll() {
    if (reloading) return;
    reloading = true;
    try {
      try {
        await api.refreshFirmwareData();
      } catch (err) {
        // An old daemon (route missing, 404) or an unreachable CCU must
        // not block the UI-side reload — surface it and continue.
        toastStore.error(
          err instanceof ApiError
            ? `${err.status}: ${err.message}`
            : err instanceof Error
              ? err.message
              : String(err),
        );
      }
      await deviceStore.refresh();
      detailMap = {};
      const updatable = deviceStore.items.filter((d) => d.updatable);
      await Promise.allSettled(updatable.map((d) => loadDetail(d.address)));
    } finally {
      reloading = false;
    }
  }

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
      // Refresh device list so the updatable badge updates, and re-read
      // this device's firmware detail so the lifecycle state column
      // follows the update (loadDetail short-circuits on cached entries).
      await deviceStore.refresh();
      const next = { ...detailMap };
      delete next[addr];
      detailMap = next;
      await loadDetail(addr);
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
  <PageHeader title={t("firmware.title")} subtitle={t("firmware.subtitle")}>
    {#snippet actions()}
      <Button
        type="button"
        variant="outline"
        size="sm"
        onclick={() => void reloadAll()}
        disabled={deviceStore.loading || reloading}
      >
        {t("common.reload")}
      </Button>
    {/snippet}
  </PageHeader>

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
      <Select
        class="h-8 w-auto"
        bind:value={centralFilter}
        options={[
          { value: "", label: t("common.all_ccus") },
          ...centrals.map((c) => ({ value: c ?? "", label: c ?? "" })),
        ]}
      />
    {/if}
  </div>

  <!-- Loading / error / table -->
  {#if deviceStore.loading && allDevices.length === 0}
    <LoadingState />
  {:else if deviceStore.error}
    <ErrorState message={deviceStore.error} onRetry={() => void deviceStore.refresh()} />
  {:else}
    <Card class="p-4">
      <DataTable
        rows={filtered}
        {columns}
        rowKey={(d) => d.interface_id + "/" + d.address}
        search
        searchPlaceholder={t("common.search")}
        persistKey="firmware"
        initialSort={{ key: "device", asc: true }}
        emptyMessage={filterMode === "updatable" ? t("firmware.no_updates") : t("devices.empty")}
        emptyIcon="mdi:upload"
      >
        {#snippet cell(device, col)}
          {@const detail = detailMap[device.address]}
          {@const fw = detail?.firmware}
          {@const busy = updating.has(device.address)}
          {@const loadingFw = loadingDetail.has(device.address)}
          {@const avail = availableVersion(fw)}
          {@const versionsMatch = !!fw?.Current && !!avail && fw.Current === avail}
          {@const newerVersion = !!fw?.Current && !!avail && avail !== fw.Current}
          {@const updateAvailable = detail?.update_available ?? false}
          {#if col.key === "device"}
            <a
              href="#/devices/{encodeURIComponent(device.address)}"
              class="font-medium text-brand-700 hover:underline dark:text-brand-400"
            >{device.name || device.address}</a>
            <div class="text-xs text-slate-500 dark:text-slate-400">
              {device.address}{#if centrals.length > 1 && device.central} · <span class="font-medium">{device.central}</span>{/if}
            </div>
          {:else if col.key === "model"}
            <span>{device.model}</span>
            {#if device.model_label && device.model_label !== device.model}
              <div class="text-xs text-slate-500 dark:text-slate-400">{device.model_label}</div>
            {/if}
          {:else if col.key === "current"}
            <span class="font-mono text-xs">
              {#if loadingFw}<span class="text-slate-400 dark:text-slate-500">…</span>{:else}{fw?.Current || "—"}{/if}
            </span>
          {:else if col.key === "available"}
            <span class="font-mono text-xs">
              {#if loadingFw}
                <span class="text-slate-400 dark:text-slate-500">…</span>
              {:else if newerVersion}
                <span class="font-semibold text-amber-600 dark:text-amber-400">{avail}</span>
              {:else}{avail || "—"}{/if}
            </span>
          {:else if col.key === "state"}
            <!-- The CCU firmware lifecycle state is the truth for this
                 column. The gated update_available flag only says whether
                 an install can start right now (HmIP delivers the image to
                 the device first) — it must never repaint an existing
                 update (NEW_FIRMWARE_AVAILABLE) as "up to date" while the
                 row lists two different versions. -->
            {#if loadingFw}
              <span class="text-xs text-slate-400 dark:text-slate-500">…</span>
            {:else if fw?.UpdateState && fw.UpdateState !== "UNKNOWN"}
              <Badge variant={stateBadgeVariant(fw.UpdateState)}>{stateLabel(fw.UpdateState)}</Badge>
            {:else if versionsMatch}
              <Badge variant="success">{t("firmware.state.UP_TO_DATE")}</Badge>
            {:else if updateAvailable || device.update_available || newerVersion}
              <Badge variant="default">{t("firmware.state.NEW_FIRMWARE_AVAILABLE")}</Badge>
            {:else}
              <Badge variant="muted">{t("firmware.state.UP_TO_DATE")}</Badge>
            {/if}
          {:else if col.key === "action"}
            {#if updateAvailable && !busy && !loadingFw}
              {@const inProgress = isInProgress(fw?.UpdateState)}
              <Button
                type="button"
                variant="default"
                size="sm"
                disabled={inProgress}
                onclick={() => void triggerUpdate(device.address, device.name || device.address)}
              >
                {#if inProgress}{t("firmware.in_progress")}{:else}{t("firmware.update")}{/if}
              </Button>
            {:else if busy}
              <Button type="button" variant="default" size="sm" disabled={true}>
                {t("firmware.triggering")}
              </Button>
            {:else if !loadingFw && detail && !updateAvailable}
              {#if newerVersion && !isInProgress(fw?.UpdateState)}
                <!-- A newer firmware exists but is not installable yet:
                     the CCU still has to deliver the image to the device.
                     Saying "up to date" here contradicts the version
                     columns. -->
                <span class="text-xs text-slate-500 dark:text-slate-400">{t("firmware.awaiting_transfer")}</span>
              {:else if !newerVersion}
                <span class="text-xs text-slate-500 dark:text-slate-400">{t("firmware.up_to_date")}</span>
              {/if}
            {/if}
          {/if}
        {/snippet}
      </DataTable>
    </Card>

    <p class="mt-3 text-xs text-slate-500 dark:text-slate-400">
      {t("firmware.count", { count: filtered.length, total: allDevices.length })}
    </p>
  {/if}
</section>
