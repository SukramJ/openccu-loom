<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { CentralRow } from "$lib/api/client";
  import type { RoomEntry, FunctionEntry, Area, AreaRoomRef } from "$lib/api/types";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import { t } from "$lib/i18n";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { areasStore } from "$lib/stores/areas.svelte";
  import { deviceStore } from "$lib/stores/devices.svelte";
  import { collectRoomPairs, roomPairKey } from "$lib/areas/roomPairs";

  let rooms = $state<RoomEntry[]>([]);
  let functions = $state<FunctionEntry[]>([]);
  let centrals = $state<CentralRow[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);

  let selectedCentral = $state<string | undefined>(undefined);

  // Rename state for rooms
  let renamingRoom = $state<string | null>(null);
  let renameRoomValue = $state("");

  // Rename state for functions
  let renamingFunction = $state<string | null>(null);
  let renameFunctionValue = $state("");

  // Add form state
  let newRoomName = $state("");
  let newFunctionName = $state("");
  let addingRoom = $state(false);
  let addingFunction = $state(false);

  // --- Areas (operator-defined room groupings above CCU rooms) -----
  // Own loading/error surface (areasStore) so a hiccup here never blanks
  // out the working Rooms/Functions cards above.
  let newAreaName = $state("");
  let addingArea = $state(false);
  let renamingArea = $state<string | null>(null);
  let renameAreaValue = $state("");

  // Room-assignment drawer state.
  let assigningAreaId = $state<string | null>(null);
  let assignDraft = $state<Set<string>>(new Set());
  let assignSearch = $state("");
  let savingRooms = $state(false);

  async function load() {
    loading = true;
    loadError = null;
    try {
      const [r, f, c] = await Promise.all([
        api.listRooms(),
        api.listFunctions(),
        api.listCentralsV2(),
      ]);
      rooms = r;
      functions = f;
      centrals = c;
      if (c.length <= 1) {
        selectedCentral = undefined;
      }
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
    // Independent refreshes: the room-assignment picker needs the live
    // device inventory (central × rooms), and Areas has its own
    // loading/error surface below.
    areasStore.refresh();
    deviceStore.refresh();
  });

  // --- Areas CRUD ----------------------------------------------------
  async function createArea() {
    const name = newAreaName.trim();
    if (!name) return;
    addingArea = true;
    try {
      await api.createArea({ id: "", name });
      newAreaName = "";
      toastStore.success(t("groups.created"));
      await areasStore.refresh();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      addingArea = false;
    }
  }

  function startRenameArea(area: Area) {
    renamingArea = area.id;
    renameAreaValue = area.name;
  }

  async function saveRenameArea(area: Area) {
    const newName = renameAreaValue.trim();
    if (!newName || newName === area.name) {
      renamingArea = null;
      return;
    }
    try {
      await api.putArea(area.id, { id: area.id, name: newName, position: area.position });
      renamingArea = null;
      toastStore.success(t("groups.renamed"));
      await areasStore.refresh();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    }
  }

  async function deleteArea(area: Area) {
    const ok = await confirmStore.ask({
      title: t("areas.delete_confirm"),
      body: `"${area.name}"? ${t("areas.delete_confirm.body")}`,
      confirmLabel: t("common.delete"),
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.deleteArea(area.id);
      toastStore.success(t("groups.deleted"));
      await areasStore.refresh();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    }
  }

  // --- Room-assignment drawer ----------------------------------------
  // Every known (central, room) pair: the union of each device's
  // central × rooms, plus any pair already assigned to an area (so a
  // room whose last device vanished stays visible/manageable).
  const roomPairs = $derived(collectRoomPairs(deviceStore.items, areasStore.areas));
  const roomPairsByKey = $derived(
    new Map(roomPairs.map((p) => [roomPairKey(p.central, p.room), p])),
  );
  const multiCentral = $derived(new Set(roomPairs.map((p) => p.central)).size > 1);
  const filteredRoomPairs = $derived.by(() => {
    const q = assignSearch.trim().toLowerCase();
    if (!q) return roomPairs;
    return roomPairs.filter((p) => `${p.central} ${p.room}`.toLowerCase().includes(q));
  });
  const assigningArea = $derived(
    assigningAreaId ? (areasStore.areas.find((a) => a.id === assigningAreaId) ?? null) : null,
  );

  function openAssign(area: Area) {
    assigningAreaId = area.id;
    assignSearch = "";
    assignDraft = new Set((area.rooms ?? []).map((r) => roomPairKey(r.central, r.room)));
  }
  function closeAssign() {
    assigningAreaId = null;
  }
  function toggleAssign(p: AreaRoomRef) {
    const key = roomPairKey(p.central, p.room);
    const next = new Set(assignDraft);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    assignDraft = next;
  }
  // The area currently holding this room, when it differs from the one
  // being edited — surfaced so checking the box reads as "moves it here"
  // rather than a silent reassignment.
  function currentAreaNameOf(p: AreaRoomRef): string | null {
    const id = areasStore.areaIdOf(p.central, p.room);
    if (!id || id === assigningAreaId) return null;
    return areasStore.areas.find((a) => a.id === id)?.name ?? null;
  }
  async function saveAssign() {
    if (!assigningAreaId) return;
    savingRooms = true;
    try {
      const refs: AreaRoomRef[] = [];
      for (const key of assignDraft) {
        const p = roomPairsByKey.get(key);
        if (p) refs.push(p);
      }
      await api.putAreaRooms(assigningAreaId, refs);
      toastStore.success(t("areas.toast.rooms_saved"));
      await areasStore.refresh();
      assigningAreaId = null;
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      savingRooms = false;
    }
  }

  async function addRoom() {
    const name = newRoomName.trim();
    if (!name) return;
    addingRoom = true;
    try {
      await api.createRoom(name, selectedCentral);
      newRoomName = "";
      toastStore.success(t("groups.created"));
      await load();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      addingRoom = false;
    }
  }

  async function addFunction() {
    const name = newFunctionName.trim();
    if (!name) return;
    addingFunction = true;
    try {
      await api.createFunction(name, selectedCentral);
      newFunctionName = "";
      toastStore.success(t("groups.created"));
      await load();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      addingFunction = false;
    }
  }

  async function saveRenameRoom(oldName: string) {
    const newName = renameRoomValue.trim();
    if (!newName || newName === oldName) {
      renamingRoom = null;
      return;
    }
    try {
      await api.renameRoom(oldName, newName, selectedCentral);
      renamingRoom = null;
      toastStore.success(t("groups.renamed"));
      await load();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    }
  }

  async function saveRenameFunction(oldName: string) {
    const newName = renameFunctionValue.trim();
    if (!newName || newName === oldName) {
      renamingFunction = null;
      return;
    }
    try {
      await api.renameFunction(oldName, newName, selectedCentral);
      renamingFunction = null;
      toastStore.success(t("groups.renamed"));
      await load();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    }
  }

  async function deleteRoom(name: string) {
    const ok = await confirmStore.ask({
      title: t("groups.delete_room_confirm"),
      body: `"${name}"?`,
      confirmLabel: t("common.delete"),
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.deleteRoom(name, selectedCentral);
      toastStore.success(t("groups.deleted"));
      await load();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    }
  }

  async function deleteFunction(name: string) {
    const ok = await confirmStore.ask({
      title: t("groups.delete_function_confirm"),
      body: `"${name}"?`,
      confirmLabel: t("common.delete"),
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.deleteFunction(name, selectedCentral);
      toastStore.success(t("groups.deleted"));
      await load();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    }
  }

  const roomColumns: DataColumn<RoomEntry>[] = $derived([
    { key: "name", label: t("roomsfn.col.name"), sortable: true, title: true, get: (r) => r.name },
    { key: "count", label: t("roomsfn.col.count"), sortable: true, align: "right", get: (r) => r.device_count },
    { key: "actions", label: t("roomsfn.col.actions"), align: "right", cellClass: "reflow-actions" },
  ]);

  const fnColumns: DataColumn<FunctionEntry>[] = $derived([
    { key: "name", label: t("roomsfn.col.name"), sortable: true, title: true, get: (f) => f.name },
    { key: "count", label: t("roomsfn.col.count"), sortable: true, align: "right", get: (f) => f.device_count },
    { key: "actions", label: t("roomsfn.col.actions"), align: "right", cellClass: "reflow-actions" },
  ]);

  const areaColumns: DataColumn<Area>[] = $derived([
    { key: "name", label: t("roomsfn.col.name"), sortable: true, title: true, get: (a) => a.name },
    {
      key: "rooms_count",
      label: t("areas.col.rooms_count"),
      sortable: true,
      align: "right",
      get: (a) => (a.rooms ?? []).length,
    },
    { key: "actions", label: t("roomsfn.col.actions"), align: "right", cellClass: "reflow-actions" },
  ]);
</script>

{#if loading}
  <LoadingState />
{:else if loadError}
  <ErrorState message={loadError} onRetry={load} />
{:else}
  <div class="space-y-4">
    {#if centrals.length > 1}
      <div class="flex items-center gap-2">
        <span class="text-sm font-medium text-[var(--ha-secondary-text-color)]">
          {t("groups.central_label")}
        </span>
        <Select
          class="w-auto"
          value={selectedCentral ?? ""}
          onValueChange={(v) => (selectedCentral = v || undefined)}
          options={[
            { value: "", label: "—" },
            ...centrals.map((c) => ({ value: c.name, label: c.name })),
          ]}
        />
      </div>
    {/if}

    <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
      <!-- Rooms section -->
      <Card class="p-4">
        <h3 class="mb-3 text-sm font-semibold text-[var(--ha-secondary-text-color)] uppercase tracking-wide">
          {t("groups.rooms_title")}
        </h3>

        <div class="mb-3">
          <DataTable
            rows={rooms}
            columns={roomColumns}
            rowKey={(r) => r.name}
            emptyMessage={t("groups.empty_rooms")}
            emptyIcon="mdi:home"
          >
            {#snippet cell(room, col)}
              {#if col.key === "name"}
                {#if renamingRoom === room.name}
                  <input
                    type="text"
                    bind:value={renameRoomValue}
                    class="min-w-0 w-full rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
                    onkeydown={(e) => {
                      if (e.key === "Enter") void saveRenameRoom(room.name);
                      if (e.key === "Escape") renamingRoom = null;
                    }}
                  />
                {:else}
                  <span class="truncate">{room.name}</span>
                {/if}
              {:else if col.key === "count"}
                <span class="text-[var(--ha-secondary-text-color)]">{room.device_count}</span>
              {:else if col.key === "actions"}
                {#if renamingRoom === room.name}
                  <div class="flex justify-end gap-1">
                    <Button type="button" variant="default" size="sm" onclick={() => void saveRenameRoom(room.name)}>
                      {t("common.save")}
                    </Button>
                    <Button type="button" variant="outline" size="sm" onclick={() => (renamingRoom = null)}>
                      {t("common.cancel")}
                    </Button>
                  </div>
                {:else}
                  <div class="flex justify-end gap-1">
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      onclick={() => {
                        renamingRoom = room.name;
                        renameRoomValue = room.name;
                      }}
                    >
                      {t("groups.rename")}
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      class="text-red-600 hover:text-red-700 dark:text-red-400"
                      onclick={() => void deleteRoom(room.name)}
                    >
                      {t("common.delete")}
                    </Button>
                  </div>
                {/if}
              {/if}
            {/snippet}
          </DataTable>
        </div>

        <div class="flex gap-2">
          <input
            type="text"
            bind:value={newRoomName}
            placeholder={t("groups.room_placeholder")}
            class="min-w-0 flex-1 rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
            onkeydown={(e) => { if (e.key === "Enter") void addRoom(); }}
          />
          <Button
            type="button"
            variant="default"
            size="sm"
            disabled={addingRoom || !newRoomName.trim()}
            onclick={() => void addRoom()}
          >
            {t("common.add")}
          </Button>
        </div>
      </Card>

      <!-- Functions section -->
      <Card class="p-4">
        <h3 class="mb-3 text-sm font-semibold text-[var(--ha-secondary-text-color)] uppercase tracking-wide">
          {t("groups.functions_title")}
        </h3>

        <div class="mb-3">
          <DataTable
            rows={functions}
            columns={fnColumns}
            rowKey={(f) => f.name}
            emptyMessage={t("groups.empty_functions")}
            emptyIcon="mdi:format-list-bulleted"
          >
            {#snippet cell(fn, col)}
              {#if col.key === "name"}
                {#if renamingFunction === fn.name}
                  <input
                    type="text"
                    bind:value={renameFunctionValue}
                    class="min-w-0 w-full rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
                    onkeydown={(e) => {
                      if (e.key === "Enter") void saveRenameFunction(fn.name);
                      if (e.key === "Escape") renamingFunction = null;
                    }}
                  />
                {:else}
                  <span class="truncate">{fn.name}</span>
                {/if}
              {:else if col.key === "count"}
                <span class="text-[var(--ha-secondary-text-color)]">{fn.device_count}</span>
              {:else if col.key === "actions"}
                {#if renamingFunction === fn.name}
                  <div class="flex justify-end gap-1">
                    <Button type="button" variant="default" size="sm" onclick={() => void saveRenameFunction(fn.name)}>
                      {t("common.save")}
                    </Button>
                    <Button type="button" variant="outline" size="sm" onclick={() => (renamingFunction = null)}>
                      {t("common.cancel")}
                    </Button>
                  </div>
                {:else}
                  <div class="flex justify-end gap-1">
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      onclick={() => {
                        renamingFunction = fn.name;
                        renameFunctionValue = fn.name;
                      }}
                    >
                      {t("groups.rename")}
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      class="text-red-600 hover:text-red-700 dark:text-red-400"
                      onclick={() => void deleteFunction(fn.name)}
                    >
                      {t("common.delete")}
                    </Button>
                  </div>
                {/if}
              {/if}
            {/snippet}
          </DataTable>
        </div>

        <div class="flex gap-2">
          <input
            type="text"
            bind:value={newFunctionName}
            placeholder={t("groups.function_placeholder")}
            class="min-w-0 flex-1 rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
            onkeydown={(e) => { if (e.key === "Enter") void addFunction(); }}
          />
          <Button
            type="button"
            variant="default"
            size="sm"
            disabled={addingFunction || !newFunctionName.trim()}
            onclick={() => void addFunction()}
          >
            {t("common.add")}
          </Button>
        </div>
      </Card>
    </div>

    <!-- Areas section: operator-defined room groupings ABOVE CCU rooms
         (a floor, a shed, a terrace roof) — distinct from alarm zones. -->
    <Card class="p-4">
      <h3 class="mb-1 text-sm font-semibold text-[var(--ha-secondary-text-color)] uppercase tracking-wide">
        {t("areas.title")}
      </h3>
      <p class="mb-3 text-xs text-[var(--ha-secondary-text-color)]">{t("areas.hint")}</p>

      {#if areasStore.loading && areasStore.areas.length === 0}
        <LoadingState />
      {:else if areasStore.error}
        <ErrorState message={areasStore.error} onRetry={() => areasStore.refresh()} />
      {:else}
        <div class="mb-3">
          <DataTable
            rows={areasStore.areas}
            columns={areaColumns}
            rowKey={(a) => a.id}
            emptyMessage={t("areas.empty")}
            emptyIcon="mdi:home-group"
          >
            {#snippet cell(area, col)}
              {#if col.key === "name"}
                {#if renamingArea === area.id}
                  <input
                    type="text"
                    bind:value={renameAreaValue}
                    class="min-w-0 w-full rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
                    onkeydown={(e) => {
                      if (e.key === "Enter") void saveRenameArea(area);
                      if (e.key === "Escape") renamingArea = null;
                    }}
                  />
                {:else}
                  <span class="truncate">{area.name}</span>
                {/if}
              {:else if col.key === "rooms_count"}
                <span class="text-[var(--ha-secondary-text-color)]">{(area.rooms ?? []).length}</span>
              {:else if col.key === "actions"}
                {#if renamingArea === area.id}
                  <div class="flex justify-end gap-1">
                    <Button type="button" variant="default" size="sm" onclick={() => void saveRenameArea(area)}>
                      {t("common.save")}
                    </Button>
                    <Button type="button" variant="outline" size="sm" onclick={() => (renamingArea = null)}>
                      {t("common.cancel")}
                    </Button>
                  </div>
                {:else}
                  <div class="flex flex-wrap justify-end gap-1">
                    <Button type="button" variant="ghost" size="sm" onclick={() => openAssign(area)}>
                      {t("areas.assign_rooms")}
                    </Button>
                    <Button type="button" variant="ghost" size="sm" onclick={() => startRenameArea(area)}>
                      {t("groups.rename")}
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      class="text-red-600 hover:text-red-700 dark:text-red-400"
                      onclick={() => void deleteArea(area)}
                    >
                      {t("common.delete")}
                    </Button>
                  </div>
                {/if}
              {/if}
            {/snippet}
          </DataTable>
        </div>

        <div class="flex gap-2">
          <input
            type="text"
            bind:value={newAreaName}
            placeholder={t("areas.placeholder")}
            class="min-w-0 flex-1 rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
            onkeydown={(e) => { if (e.key === "Enter") void createArea(); }}
          />
          <Button
            type="button"
            variant="default"
            size="sm"
            disabled={addingArea || !newAreaName.trim()}
            onclick={() => void createArea()}
          >
            {t("common.add")}
          </Button>
        </div>
      {/if}
    </Card>
  </div>

  <!-- Room-assignment drawer: full-set replace (checking a room here
       moves it off any prior area — one area per room, server-enforced). -->
  {#if assigningArea}
    <div
      class="fixed inset-0 z-40 bg-black/40"
      role="presentation"
      onclick={closeAssign}
    ></div>
    <div
      class="fixed right-0 top-0 z-50 flex h-full w-full max-w-md flex-col overflow-y-auto border-l border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] shadow-xl"
      role="dialog"
      aria-modal="true"
      aria-label={t("areas.rooms_dialog.title", { name: assigningArea.name })}
    >
      <header class="flex items-center gap-2 border-b border-[var(--ha-divider-color)] p-4">
        <h2 class="flex-1 truncate font-semibold text-[var(--ha-primary-text-color)]">
          {t("areas.rooms_dialog.title", { name: assigningArea.name })}
        </h2>
        <button
          type="button"
          class="text-[var(--ha-secondary-text-color)] hover:text-[var(--ha-primary-text-color)]"
          aria-label={t("common.close")}
          onclick={closeAssign}
        >
          <Icon name="mdi:close" size={20} />
        </button>
      </header>

      <div class="flex flex-col gap-3 p-4">
        <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("areas.rooms_dialog.hint")}</p>
        <input
          type="search"
          bind:value={assignSearch}
          placeholder={t("areas.rooms_dialog.search_placeholder")}
          class="w-full rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-3 py-2 text-sm text-[var(--ha-primary-text-color)] focus:border-[var(--ha-primary-color)] focus:outline-none"
        />
        {#if filteredRoomPairs.length === 0}
          <p class="p-3 text-center text-xs text-[var(--ha-secondary-text-color)]">
            {t("areas.rooms_dialog.empty")}
          </p>
        {:else}
          <div class="max-h-[60vh] overflow-y-auto rounded-md border border-[var(--ha-divider-color)]">
            {#each filteredRoomPairs as p (roomPairKey(p.central, p.room))}
              {@const key = roomPairKey(p.central, p.room)}
              {@const otherArea = currentAreaNameOf(p)}
              <label class="flex items-center gap-2.5 border-b border-[var(--ha-divider-color)] px-3 py-2 text-sm last:border-0 hover:bg-[var(--ha-secondary-background-color)]">
                <input
                  type="checkbox"
                  class="h-4 w-4 shrink-0 cursor-pointer accent-[var(--ha-primary-color)]"
                  checked={assignDraft.has(key)}
                  onchange={() => toggleAssign(p)}
                />
                <span class="min-w-0 flex-1">
                  <span class="block truncate text-[var(--ha-primary-text-color)]">
                    {multiCentral ? `${p.central} · ${p.room}` : p.room}
                  </span>
                  {#if otherArea}
                    <span class="block truncate text-xs text-[var(--ha-warning-color)]">
                      {t("areas.rooms_dialog.current_area", { name: otherArea })}
                    </span>
                  {/if}
                </span>
              </label>
            {/each}
          </div>
        {/if}
      </div>

      <footer class="mt-auto flex gap-2 border-t border-[var(--ha-divider-color)] p-4">
        <Button variant="outline" size="sm" onclick={closeAssign} disabled={savingRooms}>
          {t("common.cancel")}
        </Button>
        <Button size="sm" class="ml-auto" onclick={() => void saveAssign()} disabled={savingRooms}>
          {savingRooms ? t("common.saving") : t("common.save")}
        </Button>
      </footer>
    </div>
  {/if}
{/if}
