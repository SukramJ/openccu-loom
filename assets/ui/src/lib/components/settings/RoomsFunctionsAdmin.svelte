<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { CentralRow } from "$lib/api/client";
  import type { RoomEntry, FunctionEntry } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { t } from "$lib/i18n";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";

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

  onMount(() => void load());

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
</script>

{#if loading}
  <LoadingState />
{:else if loadError}
  <ErrorState message={loadError} onRetry={load} />
{:else}
  <div class="space-y-4">
    {#if centrals.length > 1}
      <div class="flex items-center gap-2">
        <label for="groups-central-select" class="text-sm font-medium text-[var(--ha-secondary-text-color)]">
          {t("groups.central_label")}
        </label>
        <select
          id="groups-central-select"
          bind:value={selectedCentral}
          class="rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
        >
          <option value={undefined}>—</option>
          {#each centrals as c (c.name)}
            <option value={c.name}>{c.name}</option>
          {/each}
        </select>
      </div>
    {/if}

    <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
      <!-- Rooms section -->
      <Card class="p-4">
        <h3 class="mb-3 text-sm font-semibold text-[var(--ha-secondary-text-color)] uppercase tracking-wide">
          {t("groups.rooms_title")}
        </h3>

        {#if rooms.length === 0}
          <EmptyState message={t("groups.empty_rooms")} class="py-6" />
        {:else}
          <ul class="mb-3 divide-y divide-slate-100 dark:divide-slate-800">
            {#each rooms as room (room.name)}
              <li class="py-2">
                {#if renamingRoom === room.name}
                  <div class="flex items-center gap-2">
                    <input
                      type="text"
                      bind:value={renameRoomValue}
                      class="min-w-0 flex-1 rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
                      onkeydown={(e) => {
                        if (e.key === "Enter") void saveRenameRoom(room.name);
                        if (e.key === "Escape") renamingRoom = null;
                      }}
                    />
                    <Button type="button" variant="default" size="sm" onclick={() => void saveRenameRoom(room.name)}>
                      {t("common.save")}
                    </Button>
                    <Button type="button" variant="outline" size="sm" onclick={() => (renamingRoom = null)}>
                      {t("common.cancel")}
                    </Button>
                  </div>
                {:else}
                  <div class="flex items-center justify-between gap-2">
                    <span class="flex min-w-0 items-center gap-2 text-sm">
                      <span class="truncate">{room.name}</span>
                      <Badge variant="muted" class="shrink-0 whitespace-nowrap"
                        >{t("groups.device_count", { count: String(room.device_count) })}</Badge>
                    </span>
                    <div class="flex gap-1">
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
                  </div>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}

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

        {#if functions.length === 0}
          <EmptyState message={t("groups.empty_functions")} class="py-6" />
        {:else}
          <ul class="mb-3 divide-y divide-slate-100 dark:divide-slate-800">
            {#each functions as fn (fn.name)}
              <li class="py-2">
                {#if renamingFunction === fn.name}
                  <div class="flex items-center gap-2">
                    <input
                      type="text"
                      bind:value={renameFunctionValue}
                      class="min-w-0 flex-1 rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
                      onkeydown={(e) => {
                        if (e.key === "Enter") void saveRenameFunction(fn.name);
                        if (e.key === "Escape") renamingFunction = null;
                      }}
                    />
                    <Button type="button" variant="default" size="sm" onclick={() => void saveRenameFunction(fn.name)}>
                      {t("common.save")}
                    </Button>
                    <Button type="button" variant="outline" size="sm" onclick={() => (renamingFunction = null)}>
                      {t("common.cancel")}
                    </Button>
                  </div>
                {:else}
                  <div class="flex items-center justify-between gap-2">
                    <span class="flex min-w-0 items-center gap-2 text-sm">
                      <span class="truncate">{fn.name}</span>
                      <Badge variant="muted" class="shrink-0 whitespace-nowrap"
                        >{t("groups.device_count", { count: String(fn.device_count) })}</Badge>
                    </span>
                    <div class="flex gap-1">
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
                  </div>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}

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
  </div>
{/if}
