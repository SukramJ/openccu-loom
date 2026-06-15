<script lang="ts">
  import { onMount } from "svelte";
  import { deviceStore } from "$lib/stores/devices.svelte";
  import DeviceCard from "$lib/components/DeviceCard.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import { api } from "$lib/api/client";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { t } from "$lib/i18n";

  let filter = $state("");
  let availability = $state<"all" | "available" | "unavailable">("all");
  let updateOnly = $state(false);
  let roomFilter = $state("");
  let centralFilter = $state("");
  // Sort + interface-grouping mirror homematicip-local-frontend's
  // device-list view: clicking a column toggles asc/desc, and devices
  // are clustered under interface headers so a multi-CCU setup stays
  // legible.
  let sortColumn = $state<"name" | "address" | "model">("name");
  let sortAsc = $state(true);
  let groupByInterface = $state(true);
  let selected = $state<Set<string>>(new Set());
  let bulkBusy = $state(false);
  let bulkBanner = $state<string | null>(null);
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
      bulkBanner = t("devicelist.bulk_no_updates");
      return;
    }
    const ok2 = await confirmStore.ask({
      title: t("firmware.title"),
      body: t("devicelist.bulk_firmware_body", { count: list.length }),
      confirmLabel: t("devicelist.bulk_firmware_confirm"),
    });
    if (!ok2) return;
    bulkBusy = true;
    bulkBanner = null;
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
    bulkBanner = t("devicelist.bulk_result", { ok, fail });
    bulkBusy = false;
    selected = new Set();
  }

  async function bulkSetRoom() {
    if (selected.size === 0) return;
    const room = roomDraft.trim();
    bulkBusy = true;
    bulkBanner = null;
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
    bulkBanner = t("devicelist.bulk_result", { ok, fail });
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

  const filtered = $derived(
    deviceStore.items
      .filter((d) => {
        if (filter) {
          const q = filter.toLowerCase();
          const m =
            d.address.toLowerCase().includes(q) ||
            (d.name ?? "").toLowerCase().includes(q) ||
            d.model.toLowerCase().includes(q) ||
            (d.model_label ?? "").toLowerCase().includes(q);
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

  onMount(() => {
    deviceStore.refresh();
    deviceStore.ensureStream();
  });
</script>

<section class="mx-auto max-w-6xl px-4 py-8 sm:px-6">
  <header class="mb-6 flex flex-wrap items-center justify-between gap-4">
    <div>
      <h1 class="text-2xl font-semibold tracking-tight">{t("devices.title")}</h1>
      <p class="text-sm text-slate-500 dark:text-slate-400">
        {#if deviceStore.lastLoaded}
          {t("devicelist.last_updated", { time: deviceStore.lastLoaded.toLocaleTimeString() })}
        {:else}
          {t("common.loading")}
        {/if}
      </p>
    </div>
    <div class="flex w-full flex-wrap items-center gap-2 sm:w-auto sm:gap-3">
      <input
        type="search"
        placeholder={t("devicelist.search_placeholder")}
        bind:value={filter}
        class="w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-base shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500 sm:w-72 sm:text-sm dark:border-slate-700 dark:bg-slate-900"
      />
      <select
        bind:value={availability}
        class="rounded-md border border-slate-300 bg-white px-2 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900"
        title={t("devicelist.availability")}
      >
        <option value="all">{t("devicelist.all")}</option>
        <option value="available">{t("devicelist.available")}</option>
        <option value="unavailable">{t("devicelist.unavailable")}</option>
      </select>
      {#if rooms.length > 0}
        <select
          bind:value={roomFilter}
          class="rounded-md border border-slate-300 bg-white px-2 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900"
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
          class="rounded-md border border-slate-300 bg-white px-2 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900"
          title="CCU"
        >
          <option value="">{t("common.all_ccus")}</option>
          {#each centrals as c (c)}
            <option value={c}>{c}</option>
          {/each}
        </select>
      {/if}
      <label class="flex items-center gap-1.5 text-xs text-slate-600 dark:text-slate-400">
        <input type="checkbox" bind:checked={updateOnly} />
        {t("devicelist.update_available")}
      </label>
      <Button
        type="button"
        variant="default"
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
    </div>
  </header>

  {#if selected.size > 0}
    <div class="mb-4 flex flex-wrap items-center gap-2 rounded-md border border-brand-300 bg-brand-50 p-2 text-sm dark:border-brand-800 dark:bg-brand-950/40">
      <span class="font-medium text-brand-900 dark:text-brand-100">
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
          class="h-9 w-full rounded-md border border-slate-300 bg-white px-2 text-base sm:w-48 sm:text-sm dark:border-slate-700 dark:bg-slate-900"
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
      {#if bulkBanner}
        <span class="text-xs text-[var(--ha-primary-text-color)]">{bulkBanner}</span>
      {/if}
    </div>
  {/if}

  {#if deviceStore.error}
    <div class="mb-4 rounded border border-red-300 bg-red-50 p-3 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200">
      {t("devicelist.load_error", { error: deviceStore.error })}
    </div>
  {/if}

  <!-- Sort toolbar: click a column to set the sort direction; clicking
       again flips asc/desc. The "group by interface" toggle clusters
       devices under section headers. -->
  <div class="mb-3 flex flex-wrap items-center gap-2 text-xs" style="color: var(--ha-secondary-text-color);">
    <span>{t("common.sort")}</span>
    {#each [
      { key: "name", label: t("devicelist.col.name") },
      { key: "address", label: t("devicelist.col.address") },
      { key: "model", label: t("devicelist.col.model") },
    ] as col (col.key)}
      <button
        type="button"
        class="rounded-md border px-2 py-0.5 transition"
        style="border-color: {sortColumn === col.key ? 'var(--ha-primary-color)' : 'var(--ha-divider-color)'}; color: {sortColumn === col.key ? 'var(--ha-primary-color)' : 'var(--ha-secondary-text-color)'};"
        onclick={() => setSort(col.key as "name" | "address" | "model")}
      >
        {col.label}
        {#if sortColumn === col.key}
          <span aria-hidden="true">{sortAsc ? "↑" : "↓"}</span>
        {/if}
      </button>
    {/each}
    <span class="mx-1">·</span>
    <label class="inline-flex items-center gap-1.5 cursor-pointer">
      <input type="checkbox" bind:checked={groupByInterface} />
      {t("devicelist.group_by_interface")}
    </label>
  </div>

  {#if deviceStore.loading && deviceStore.items.length === 0}
    <p style="color: var(--ha-secondary-text-color);">{t("devices.loading")}</p>
  {:else if filtered.length === 0}
    <p style="color: var(--ha-secondary-text-color);">{t("devices.empty")}</p>
  {:else if groups}
    {#each groups as g (g.iface)}
      <section class="mb-6">
        <h2
          class="mb-2 text-xs font-semibold uppercase tracking-wide"
          style="color: var(--ha-secondary-text-color);"
        >
          {g.iface}
          <span style="color: var(--ha-disabled-text-color);">·&nbsp;{g.items.length}</span>
        </h2>
        <ul class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
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
    <p class="mt-4 text-sm" style="color: var(--ha-secondary-text-color);">
      {t("devicelist.count", { filtered: filtered.length, total: deviceStore.items.length })}
    </p>
  {:else}
    <ul class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
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
    <p class="mt-4 text-sm" style="color: var(--ha-secondary-text-color);">
      {t("devicelist.count", { filtered: filtered.length, total: deviceStore.items.length })}
    </p>
  {/if}
</section>
