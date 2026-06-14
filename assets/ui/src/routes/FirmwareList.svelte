<script lang="ts">
  import { onMount } from "svelte";
  import { deviceStore } from "$lib/stores/devices.svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { DeviceDetail } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import { t } from "$lib/i18n";
  import { confirmStore } from "$lib/stores/confirm.svelte";

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
  let banner = $state<string | null>(null);
  let bannerKind = $state<"ok" | "err">("ok");
  let filterMode = $state<"all" | "updatable">("all");
  let searchText = $state("");
  let centralFilter = $state("");

  // ---- derived ---------------------------------------------------------

  const allDevices = $derived(deviceStore.items);

  // Distinct CCUs present in the data — drives the optional CCU filter
  // (shown only in multi-CCU setups).
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

  // Count devices with an actually-installable update pending (image
  // delivered), NOT merely update-capable devices — see update_available.
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
    banner = null;
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
      banner = t("firmware.triggered", { name });
      bannerKind = "ok";
      // Refresh device list so the updatable badge updates.
      await deviceStore.refresh();
    } catch (err) {
      banner =
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err);
      bannerKind = "err";
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

<section class="mx-auto max-w-6xl px-4 sm:px-6 py-6">
  <!-- Header -->
  <header class="mb-4 flex flex-wrap items-start justify-between gap-3">
    <div>
      <h1 class="text-2xl font-semibold">{t("firmware.title")}</h1>
      <p class="text-sm" style="color: var(--ha-secondary-text-color);">
        {t("firmware.subtitle")}
      </p>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      {#if banner}
        <span
          class="text-xs {bannerKind === 'err' ? 'text-red-600' : ''}"
          style={bannerKind === "ok" ? "color: var(--ha-secondary-text-color);" : ""}
        >
          {banner}
        </span>
      {/if}
      <Button
        type="button"
        variant="outline"
        size="sm"
        onclick={() => void deviceStore.refresh()}
        disabled={deviceStore.loading}
      >
        {t("common.reload")}
      </Button>
    </div>
  </header>

  <!-- Summary bar -->
  {#if updatableCount > 0}
    <div
      class="mb-4 rounded-md border px-4 py-3 text-sm"
      style="border-color: var(--ha-warning-color, #f59e0b); background-color: rgba(245,158,11,0.08);"
    >
      {t("firmware.updates_available", { count: updatableCount })}
    </div>
  {/if}

  <!-- Filters -->
  <div class="mb-4 flex flex-wrap items-center gap-3">
    <div class="flex rounded-md border" style="border-color: var(--ha-divider-color);">
      <button
        type="button"
        class="px-3 py-1.5 text-sm font-medium transition"
        style={filterMode === "all"
          ? "background-color: var(--ha-primary-color); color: #fff; border-radius: 0.375rem 0 0 0.375rem;"
          : "color: var(--ha-primary-text-color); border-radius: 0.375rem 0 0 0.375rem;"}
        onclick={() => (filterMode = "all")}
      >
        {t("firmware.filter.all")}
      </button>
      <button
        type="button"
        class="px-3 py-1.5 text-sm font-medium transition"
        style={filterMode === "updatable"
          ? "background-color: var(--ha-primary-color); color: #fff; border-radius: 0 0.375rem 0.375rem 0;"
          : "color: var(--ha-primary-text-color); border-radius: 0 0.375rem 0.375rem 0;"}
        onclick={() => (filterMode = "updatable")}
      >
        {t("firmware.filter.updatable")}
        {#if updatableCount > 0}
          <span
            class="ml-1 inline-flex h-4 w-4 items-center justify-center rounded-full text-[10px] font-bold"
            style="background-color: var(--ha-warning-color, #f59e0b); color: #fff;"
          >{updatableCount}</span>
        {/if}
      </button>
    </div>

    {#if centrals.length > 1}
      <select
        bind:value={centralFilter}
        class="h-8 rounded-md border px-2 text-sm"
        style="border-color: var(--ha-divider-color); background-color: var(--ha-card-background-color); color: var(--ha-primary-text-color);"
        title="CCU"
      >
        <option value="">Alle CCUs</option>
        {#each centrals as c (c)}
          <option value={c}>{c}</option>
        {/each}
      </select>
    {/if}

    <input
      type="search"
      placeholder={t("common.search")}
      bind:value={searchText}
      class="h-8 rounded-md border px-3 text-sm"
      style="border-color: var(--ha-divider-color); background-color: var(--ha-card-background-color); color: var(--ha-primary-text-color);"
    />
  </div>

  <!-- Loading / error -->
  {#if deviceStore.loading && allDevices.length === 0}
    <p class="py-8 text-center text-sm" style="color: var(--ha-secondary-text-color);">
      {t("common.loading")}
    </p>
  {:else if deviceStore.error}
    <p class="py-8 text-center text-sm text-red-600">{deviceStore.error}</p>
  {:else if filtered.length === 0}
    <p class="py-8 text-center text-sm" style="color: var(--ha-secondary-text-color);">
      {filterMode === "updatable" ? t("firmware.no_updates") : t("devices.empty")}
    </p>
  {:else}
    <!-- Device table -->
    <div class="overflow-hidden rounded-lg border" style="border-color: var(--ha-divider-color);">
      <table class="table-reflow w-full text-sm">
        <thead>
          <tr
            class="border-b text-left text-xs font-semibold uppercase tracking-wide"
            style="border-color: var(--ha-divider-color); background-color: var(--ha-secondary-background-color); color: var(--ha-secondary-text-color);"
          >
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
            <tr
              class="border-b transition-colors last:border-0"
              style="border-color: var(--ha-divider-color);"
            >
              <!-- Device name / address -->
              <td class="reflow-title px-4 py-3">
                <a
                  href="#/devices/{encodeURIComponent(device.address)}"
                  class="font-medium hover:underline"
                  style="color: var(--ha-primary-color);"
                >
                  {device.name || device.address}
                </a>
                <div class="text-xs" style="color: var(--ha-secondary-text-color);">
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
                  <div class="text-xs" style="color: var(--ha-secondary-text-color);">
                    {device.model_label}
                  </div>
                {/if}
              </td>

              <!-- Current version -->
              <td class="px-4 py-3 font-mono text-xs" data-label={t("firmware.col.current")}>
                {#if loadingFw}
                  <span style="color: var(--ha-secondary-text-color);">…</span>
                {:else}
                  {fw?.Current || "—"}
                {/if}
              </td>

              <!-- Available version -->
              <td class="px-4 py-3 font-mono text-xs" data-label={t("firmware.col.available")}>
                {#if loadingFw}
                  <span style="color: var(--ha-secondary-text-color);">…</span>
                {:else if fw?.Available && fw.Available !== fw.Current}
                  <span class="font-semibold" style="color: var(--ha-warning-color, #f59e0b);">
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
                  <span style="color: var(--ha-secondary-text-color);" class="text-xs">…</span>
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
                  <span class="text-xs" style="color: var(--ha-secondary-text-color);">
                    {t("firmware.up_to_date")}
                  </span>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>

    <p class="mt-3 text-xs" style="color: var(--ha-secondary-text-color);">
      {t("firmware.count", { count: filtered.length, total: allDevices.length })}
    </p>
  {/if}
</section>
