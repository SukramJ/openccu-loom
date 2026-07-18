<script lang="ts">
  import { onMount } from "svelte";
  import { deviceStore } from "$lib/stores/devices.svelte";
  import { centralStore } from "$lib/stores/centrals.svelte";
  import { subscribe } from "$lib/stores/events.svelte";
  import type { DeviceSummary, EventEnvelope } from "$lib/api/types";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import DeviceCard from "$lib/components/DeviceCard.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import { api } from "$lib/api/client";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { t } from "$lib/i18n";
  import { makeTextMatcher } from "$lib/utils";
  import { prefs, setDeviceView } from "$lib/stores/preferences.svelte";
  import {
    deviceListFilters as saved,
    persistDeviceListFilters,
  } from "$lib/stores/deviceListFilters.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import CentralStatusBadge from "$lib/components/ui/CentralStatusBadge.svelte";

  // Filter/sort state is seeded from a module store and synced back to
  // it, so the search term and filters survive opening a device and
  // navigating back. (View mode is the durable preference above.)
  let filter = $state(saved.filter);
  let availability = $state<"all" | "available" | "unavailable">(saved.availability);
  let updateOnly = $state(saved.updateOnly);
  let roomFilter = $state(saved.roomFilter);
  let centralFilter = $state(saved.centralFilter);
  // Sort + interface-grouping mirror the reference config panel's
  // device-list view: clicking a column toggles asc/desc, and devices
  // are clustered under interface headers so a multi-CCU setup stays
  // legible.
  let sortColumn = $state<"name" | "address" | "model">(saved.sortColumn);
  let sortAsc = $state(saved.sortAsc);
  let groupByInterface = $state(saved.groupByInterface);
  $effect(() => {
    saved.filter = filter;
    saved.availability = availability;
    saved.updateOnly = updateOnly;
    saved.roomFilter = roomFilter;
    saved.centralFilter = centralFilter;
    saved.sortColumn = sortColumn;
    saved.sortAsc = sortAsc;
    saved.groupByInterface = groupByInterface;
    persistDeviceListFilters();
  });

  // Layout class for the device containers — a multi-column card grid
  // or a single-column list, per the operator's view preference.
  const listClass = $derived(
    prefs.deviceView === "list"
      ? "flex flex-col gap-2"
      : "grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4",
  );
  let selected = $state<Set<string>>(new Set());
  let bulkBusy = $state(false);
  // Inline "set room" editor — replaces a native prompt() so the bulk
  // flow stays on-brand and works cleanly on touch.
  let roomEditing = $state(false);
  let roomDraft = $state("");

  function setSort(col: "name" | "address" | "model") {
    if (sortColumn === col) {
      sortAsc = !sortAsc;
    } else {
      sortColumn = col;
      sortAsc = true;
    }
  }

  function toggleSelect(addr: string, checked: boolean) {
    const next = new Set(selected);
    if (checked) next.add(addr);
    else next.delete(addr);
    selected = next;
  }

  function selectAll() {
    selected = new Set(filtered.map((d) => d.address));
  }

  function clearSelection() {
    selected = new Set();
  }

  async function bulkUpdateFirmware() {
    if (selected.size === 0) return;
    const list = [...selected].filter((addr) => {
      return deviceStore.items.find(
        (d) => d.address === addr && d.update_available,
      );
    });
    if (list.length === 0) {
      toastStore.info(t("devicelist.bulk_no_updates"));
      return;
    }
    const ok2 = await confirmStore.ask({
      title: t("firmware.title"),
      body: t("devicelist.bulk_firmware_body", { count: list.length }),
      confirmLabel: t("devicelist.bulk_firmware_confirm"),
    });
    if (!ok2) return;
    bulkBusy = true;
    let ok = 0;
    let fail = 0;
    for (const addr of list) {
      try {
        await api.updateFirmware(addr);
        ok++;
      } catch {
        fail++;
      }
    }
    const msg = t("devicelist.bulk_result", { ok, fail });
    if (ok > 0) toastStore.success(msg);
    else toastStore.error(msg);
    bulkBusy = false;
    selected = new Set();
  }

  async function bulkSetRoom() {
    if (selected.size === 0) return;
    const room = roomDraft.trim();
    bulkBusy = true;
    let ok = 0;
    let fail = 0;
    for (const addr of selected) {
      try {
        await api.setDeviceRooms(addr, room ? [room] : []);
        ok++;
      } catch {
        fail++;
      }
    }
    const msg = t("devicelist.bulk_result", { ok, fail });
    if (ok > 0) toastStore.success(msg);
    else toastStore.error(msg);
    bulkBusy = false;
    roomEditing = false;
    roomDraft = "";
    selected = new Set();
    await deviceStore.refresh();
  }

  // Sort by the operator-assigned display name when available. Falls
  // back to the address so un-named devices still render somewhere
  // stable. Uses `localeCompare` with numeric awareness so "Raum 10"
  // appears after "Raum 2", not after "Raum 1".
  function displayName(d: {
    name?: string;
    address: string;
  }): string {
    return (d.name ?? "").trim() || d.address;
  }

  // Distinct list of room names across all devices, sorted, for the
  // dropdown. Computed once per device-list change so the dropdown
  // stays in sync as the store refreshes.
  const rooms = $derived.by(() => {
    const set = new Set<string>();
    for (const d of deviceStore.items) {
      for (const r of d.rooms ?? []) set.add(r);
    }
    return Array.from(set).sort((a, b) =>
      a.localeCompare(b, undefined, { sensitivity: "base" }),
    );
  });

  const centrals = $derived.by(() => {
    const set = new Set<string>();
    for (const d of deviceStore.items) {
      if (d.central) set.add(d.central);
    }
    return Array.from(set).sort((a, b) =>
      a.localeCompare(b, undefined, { sensitivity: "base" }),
    );
  });

  // Readiness lens over the fleet: while a CCU is still in its
  // readiness-gated southbound bring-up its devices simply have not
  // landed yet, so an empty (or short) list is "still initializing",
  // not "offline". Honour an active central filter so a single-CCU
  // drilldown only reasons about the CCU the operator is looking at.
  const relevantCentrals = $derived(
    centralFilter
      ? centralStore.items.filter((c) => c.name === centralFilter)
      : centralStore.items,
  );
  const notReadyCentrals = $derived(
    relevantCentrals.filter((c) => !c.readiness.ready),
  );
  const anyRelevantReady = $derived(
    relevantCentrals.some((c) => c.readiness.ready),
  );

  function sortKey(d: { name?: string; address: string; model: string; model_label?: string }) {
    switch (sortColumn) {
      case "address":
        return d.address;
      case "model":
        return (d.model_label ?? d.model ?? "").trim();
      case "name":
      default:
        return displayName(d);
    }
  }

  const nameMatch = $derived(makeTextMatcher(filter));

  const filtered = $derived(
    deviceStore.items
      .filter((d) => {
        if (filter) {
          const m =
            nameMatch(d.address) ||
            nameMatch(d.name ?? "") ||
            nameMatch(d.model) ||
            nameMatch(d.model_label ?? "");
          if (!m) return false;
        }
        if (availability === "available" && !d.available) return false;
        if (availability === "unavailable" && d.available) return false;
        if (updateOnly && !d.update_available) return false;
        if (roomFilter) {
          const has = (d.rooms ?? []).includes(roomFilter);
          if (!has) return false;
        }
        if (centralFilter && d.central !== centralFilter) return false;
        return true;
      })
      .slice()
      .sort((a, b) => {
        const dir = sortAsc ? 1 : -1;
        return (
          dir *
          sortKey(a).localeCompare(sortKey(b), undefined, {
            numeric: true,
            sensitivity: "base",
          })
        );
      }),
  );

  // Interface-grouped buckets — preserves the sort order within each
  // group. The Map keeps insertion order so the visual output is
  // deterministic.
  const groups = $derived.by(() => {
    if (!groupByInterface) return null;
    const m = new Map<string, typeof filtered>();
    for (const d of filtered) {
      const key = d.interface_id || d.interface || "—";
      const list = m.get(key);
      if (list) list.push(d);
      else m.set(key, [d]);
    }
    return Array.from(m.entries()).map(([iface, items]) => ({
      iface,
      items,
    }));
  });

  // Columns for the table view mode. The select column carries the
  // multi-select checkbox; name/model/interface/status sort on click.
  const columns: DataColumn<DeviceSummary>[] = $derived([
    { key: "select", label: "", get: () => "" },
    { key: "name", label: t("devicelist.col.name"), sortable: true, title: true, get: (d) => d.name || d.address },
    { key: "model", label: t("devicelist.col.model"), sortable: true, get: (d) => d.model_label || d.model },
    { key: "interface", label: t("diagnostics.interfaces"), sortable: true, get: (d) => d.interface_id || d.interface },
    { key: "rooms", label: t("devicelist.col.rooms"), sortable: true, get: (d) => (d.rooms ?? []).join(", ") },
    { key: "status", label: t("devicelist.col.status"), sortable: true, align: "right", get: (d) => (d.available ? 1 : 0) },
  ]);

  onMount(() => {
    deviceStore.refresh();
    deviceStore.ensureStream();
    centralStore.refresh();
    centralStore.ensureStream();
    // When a central finishes bring-up its devices become fetchable, so
    // pull a fresh list the moment one flips to ready — the operator sees
    // the initializing state resolve into real devices without a reload.
    return subscribe((ev: EventEnvelope) => {
      if (ev.type !== "central.readiness_changed") return;
      const p = ev.payload as { ready?: boolean };
      if (p.ready) void deviceStore.refresh();
    });
  });
</script>

<section class="w-full px-4 py-8 sm:px-6">
  <PageHeader
    title={t("devices.title")}
    subtitle={deviceStore.lastLoaded
      ? t("devicelist.last_updated", {
          time: deviceStore.lastLoaded.toLocaleTimeString(),
        })
      : t("common.loading")}
    class="mb-6"
  >
    {#snippet actions()}
      <div class="flex w-full flex-wrap items-center gap-2 sm:w-auto sm:gap-3">
        <input
          type="search"
          placeholder={t("devicelist.search_placeholder")}
          bind:value={filter}
          class="w-full rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-3 py-2 text-base text-[var(--ha-primary-text-color)] shadow-sm focus:border-[var(--ha-primary-color)] focus:outline-none focus:ring-1 focus:ring-[var(--ha-primary-color)] sm:w-72 sm:text-sm"
        />
        <select
          bind:value={availability}
          class="rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 py-2 text-sm text-[var(--ha-primary-text-color)] shadow-sm focus:border-[var(--ha-primary-color)] focus:outline-none"
          title={t("devicelist.availability")}
        >
          <option value="all">{t("devicelist.all")}</option>
          <option value="available">{t("devicelist.available")}</option>
          <option value="unavailable">{t("devicelist.unavailable")}</option>
        </select>
        {#if rooms.length > 0}
          <select
            bind:value={roomFilter}
            class="rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 py-2 text-sm text-[var(--ha-primary-text-color)] shadow-sm focus:border-[var(--ha-primary-color)] focus:outline-none"
            title={t("devicelist.room")}
          >
            <option value="">{t("devicelist.all_rooms")}</option>
            {#each rooms as r (r)}
              <option value={r}>{r}</option>
            {/each}
          </select>
        {/if}
        {#if centrals.length > 1}
          <select
            bind:value={centralFilter}
            class="rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 py-2 text-sm text-[var(--ha-primary-text-color)] shadow-sm focus:border-[var(--ha-primary-color)] focus:outline-none"
            title="CCU"
          >
            <option value="">{t("common.all_ccus")}</option>
            {#each centrals as c (c)}
              <option value={c}>{c}</option>
            {/each}
          </select>
        {/if}
        <label class="flex items-center gap-1.5 text-xs text-[var(--ha-secondary-text-color)]">
          <input type="checkbox" bind:checked={updateOnly} />
          {t("devicelist.update_available")}
        </label>
        <Button
          type="button"
          variant="outline"
          onclick={async () => {
            try {
              await api.refreshDevices();
            } catch {
              // best-effort; the local refresh below picks up regardless
            }
            await deviceStore.refresh();
          }}
          disabled={deviceStore.loading}
          title={t("devicelist.ccu_refresh_title")}
        >
          {t("devicelist.ccu_refresh")}
        </Button>
        <!-- Grid / list layout toggle (persisted preference). -->
        <div
          class="ml-auto inline-flex overflow-hidden rounded-md border border-[var(--ha-divider-color)]"
          role="group"
          aria-label={t("devicelist.view_mode")}
        >
          <button
            type="button"
            class="px-2.5 py-2 transition {prefs.deviceView === 'grid'
              ? 'bg-[color-mix(in_srgb,var(--ha-primary-color)_15%,transparent)] text-[var(--ha-primary-color)]'
              : 'text-[var(--ha-secondary-text-color)] hover:bg-black/5 dark:hover:bg-white/5'}"
            aria-pressed={prefs.deviceView === "grid"}
            title={t("devicelist.view_grid")}
            onclick={() => setDeviceView("grid")}
          >
            <Icon name="mdi:dots-grid" size={18} />
          </button>
          <button
            type="button"
            class="px-2.5 py-2 transition {prefs.deviceView === 'list'
              ? 'bg-[color-mix(in_srgb,var(--ha-primary-color)_15%,transparent)] text-[var(--ha-primary-color)]'
              : 'text-[var(--ha-secondary-text-color)] hover:bg-black/5 dark:hover:bg-white/5'}"
            aria-pressed={prefs.deviceView === "list"}
            title={t("devicelist.view_list")}
            onclick={() => setDeviceView("list")}
          >
            <Icon name="mdi:format-list-bulleted" size={18} />
          </button>
        </div>
      </div>
    {/snippet}
  </PageHeader>

  {#if selected.size > 0}
    <div class="mb-4 flex flex-wrap items-center gap-2 rounded-md border border-[color-mix(in_srgb,var(--ha-primary-color)_40%,transparent)] bg-[color-mix(in_srgb,var(--ha-primary-color)_12%,transparent)] p-2 text-sm">
      <span class="font-medium text-[var(--ha-primary-color)]">
        {t("devicelist.selected", { count: selected.size })}
      </span>
      <Button type="button" variant="outline" size="sm" onclick={selectAll}>{t("devicelist.select_filtered")}</Button>
      <Button type="button" variant="outline" size="sm" onclick={clearSelection}>{t("devicelist.clear_selection")}</Button>
      {#if roomEditing}
        <input
          type="text"
          bind:value={roomDraft}
          placeholder={t("devicelist.room_placeholder")}
          aria-label={t("devicelist.room_aria")}
          class="h-9 w-full rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 text-base text-[var(--ha-primary-text-color)] sm:w-48 sm:text-sm"
          onkeydown={(e) => {
            if (e.key === "Enter") void bulkSetRoom();
            else if (e.key === "Escape") roomEditing = false;
          }}
        />
        <Button type="button" size="sm" onclick={() => void bulkSetRoom()} disabled={bulkBusy}>
          {t("devicelist.apply")}
        </Button>
        <Button type="button" variant="outline" size="sm" onclick={() => (roomEditing = false)} disabled={bulkBusy}>
          {t("common.cancel")}
        </Button>
      {:else}
        <Button type="button" size="sm" onclick={() => { roomDraft = ""; roomEditing = true; }} disabled={bulkBusy}>
          {t("devicelist.set_room")}
        </Button>
      {/if}
      <Button type="button" size="sm" onclick={() => void bulkUpdateFirmware()} disabled={bulkBusy}>
        {t("devicelist.bulk_firmware_label")}
      </Button>
    </div>
  {/if}

  {#if deviceStore.error}
    <div class="mb-4">
      <ErrorState
        message={t("devicelist.load_error", { error: deviceStore.error })}
        onRetry={() => deviceStore.refresh()}
      />
    </div>
  {/if}

  <!-- Mixed-readiness banner: with at least one device already showing,
       flag any central still initializing so its (yet-missing) devices
       are explained rather than read as absent. -->
  {#if notReadyCentrals.length > 0 && filtered.length > 0}
    <div class="mb-4 flex flex-col gap-2">
      {#each notReadyCentrals as c (c.name)}
        <Card class="flex flex-wrap items-center gap-3 p-3 text-sm">
          <Icon name="mdi:refresh" size={18} class="text-[var(--ha-warning-color)]" />
          <span class="min-w-0 flex-1 text-[var(--ha-primary-text-color)]">
            {t("devices.initializing_banner", {
              name: c.name,
              loaded: c.readiness.interfaces_loaded,
              total: c.readiness.interfaces_total,
            })}
          </span>
          <CentralStatusBadge available={c.available} readiness={c.readiness} />
        </Card>
      {/each}
    </div>
  {/if}

  <!-- Sort toolbar: in card (grid) mode the buttons drive the order; in
       table mode the DataTable column headers sort, so only the group-by
       toggle remains. -->
  <div class="mb-3 flex flex-wrap items-center gap-2 text-xs text-[var(--ha-secondary-text-color)]">
    {#if prefs.deviceView !== "list"}
      <span>{t("common.sort")}</span>
      {#each [
        { key: "name", label: t("devicelist.col.name") },
        { key: "address", label: t("devicelist.col.address") },
        { key: "model", label: t("devicelist.col.model") },
      ] as col (col.key)}
        <button
          type="button"
          class="rounded-md border px-2 py-0.5 transition {sortColumn === col.key
            ? 'border-[var(--ha-primary-color)] text-[var(--ha-primary-color)]'
            : 'border-[var(--ha-divider-color)] text-[var(--ha-secondary-text-color)] hover:border-[var(--ha-secondary-text-color)]'}"
          onclick={() => setSort(col.key as "name" | "address" | "model")}
        >
          {col.label}
          {#if sortColumn === col.key}
            <span aria-hidden="true">{sortAsc ? "↑" : "↓"}</span>
          {/if}
        </button>
      {/each}
      <span class="mx-1">·</span>
    {/if}
    <label class="inline-flex items-center gap-1.5 cursor-pointer">
      <input type="checkbox" bind:checked={groupByInterface} />
      {t("devicelist.group_by_interface")}
    </label>
  </div>

  <!-- Per-row cell renderer shared by every device DataTable. -->
  {#snippet deviceCell(device: DeviceSummary, col: DataColumn<DeviceSummary>)}
    {#if col.key === "select"}
      <input
        type="checkbox"
        class="h-4 w-4 cursor-pointer accent-[var(--ha-primary-color)]"
        checked={selected.has(device.address)}
        onchange={(e) => toggleSelect(device.address, e.currentTarget.checked)}
        aria-label={device.name || device.address}
      />
    {:else if col.key === "name"}
      <a
        href="#/devices/{encodeURIComponent(device.address)}"
        class="font-medium text-[var(--ha-primary-color)] hover:underline"
      >{device.name || device.address}</a>
      <span class="block font-mono text-xs text-[var(--ha-secondary-text-color)]">{device.address}</span>
    {:else if col.key === "model"}
      <span>{device.model_label || device.model}</span>
    {:else if col.key === "interface"}
      <span class="font-mono text-xs">{device.interface_id || device.interface}</span>
      {#if centrals.length > 1 && device.central}
        <span class="block text-xs text-[var(--ha-secondary-text-color)]">{device.central}</span>
      {/if}
    {:else if col.key === "rooms"}
      {#if device.rooms && device.rooms.length > 0}
        <span class="text-xs">{device.rooms.join(", ")}</span>
      {:else}
        <span class="text-[var(--ha-secondary-text-color)]">—</span>
      {/if}
    {:else if col.key === "status"}
      <span class="inline-flex flex-wrap items-center justify-end gap-1.5">
        {#if device.available}
          <Badge variant="success">{t("device.list.reachable")}</Badge>
        {:else}
          <Badge variant="danger">{t("device.list.unreachable")}</Badge>
        {/if}
        {#if device.update_available}
          <Badge variant="warning">{t("firmware.update")}</Badge>
        {/if}
      </span>
    {/if}
  {/snippet}

  {#if deviceStore.loading && deviceStore.items.length === 0}
    <LoadingState message={t("devices.loading")} />
  {:else if filtered.length === 0}
    {#if notReadyCentrals.length > 0 && !anyRelevantReady}
      <!-- Nothing loaded yet and every relevant CCU is still in bring-up:
           this is "initializing", not an empty fleet. -->
      <div class="flex flex-col gap-3">
        {#each notReadyCentrals as c (c.name)}
          <Card class="flex flex-wrap items-center gap-3 p-4">
            <LoadingState message={t("devices.initializing", { name: c.name })} />
            <CentralStatusBadge available={c.available} readiness={c.readiness} />
          </Card>
        {/each}
      </div>
    {:else}
      <EmptyState message={t("devices.empty")} />
    {/if}
  {:else if prefs.deviceView === "list"}
    <!-- TABLE MODE -->
    {#if groups}
      {#each groups as g (g.iface)}
        <section class="mb-6">
          <h2 class="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
            {g.iface}
            <span class="text-[var(--ha-disabled-text-color)]">·&nbsp;{g.items.length}</span>
          </h2>
          <Card class="p-4">
            <DataTable
              rows={g.items}
              {columns}
              rowKey={(d) => d.interface_id + "/" + d.address}
              cell={deviceCell}
              initialSort={{ key: "name", asc: true }}
              emptyMessage={t("devices.empty")}
            />
          </Card>
        </section>
      {/each}
    {:else}
      <Card class="p-4">
        <DataTable
          rows={filtered}
          {columns}
          rowKey={(d) => d.interface_id + "/" + d.address}
          cell={deviceCell}
          persistKey="device-list"
          initialSort={{ key: "name", asc: true }}
          emptyMessage={t("devices.empty")}
        />
      </Card>
    {/if}
    <p class="mt-4 text-sm text-[var(--ha-secondary-text-color)]">
      {t("devicelist.count", { filtered: filtered.length, total: deviceStore.items.length })}
    </p>
  {:else if groups}
    <!-- CARD (GRID) MODE, grouped -->
    {#each groups as g (g.iface)}
      <section class="mb-6">
        <h2 class="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
          {g.iface}
          <span class="text-[var(--ha-disabled-text-color)]">·&nbsp;{g.items.length}</span>
        </h2>
        <ul class={listClass}>
          {#each g.items as device (device.interface_id + "/" + device.address)}
            <li>
              <DeviceCard
                {device}
                selected={selected.has(device.address)}
                onToggleSelect={(c) => toggleSelect(device.address, c)}
              />
            </li>
          {/each}
        </ul>
      </section>
    {/each}
    <p class="mt-4 text-sm text-[var(--ha-secondary-text-color)]">
      {t("devicelist.count", { filtered: filtered.length, total: deviceStore.items.length })}
    </p>
  {:else}
    <!-- CARD (GRID) MODE, flat -->
    <ul class={listClass}>
      {#each filtered as device (device.interface_id + "/" + device.address)}
        <li>
          <DeviceCard
            {device}
            selected={selected.has(device.address)}
            onToggleSelect={(c) => toggleSelect(device.address, c)}
          />
        </li>
      {/each}
    </ul>
    <p class="mt-4 text-sm text-[var(--ha-secondary-text-color)]">
      {t("devicelist.count", { filtered: filtered.length, total: deviceStore.items.length })}
    </p>
  {/if}
</section>
