<script lang="ts">
  // Create / edit a heating group (GR02+). Type picker → grouped member picker
  // → name + "operate only via group" toggle. Backed by the CCU jpages proxy
  // through the REST API; the create path is fire-and-poll on the daemon side,
  // so a save can take a moment.
  //
  // The member picker groups candidates by device, offers search + room/only-
  // selected filters, a tri-state per-device checkbox, and a live selection
  // tray — a flat address list does not scale to the hundreds of channels a
  // real installation exposes. Candidate identification (device/channel name,
  // model, room, function) is enriched by the daemon from the live model.
  import { onMount, untrack } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type {
    GroupEntry,
    GroupTypeEntry,
    SuitableMemberEntry,
  } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Switch from "$lib/components/ui/Switch.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { areasStore } from "$lib/stores/areas.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    central: string;
    group?: GroupEntry;
    onClose: () => void;
    onSaved: () => void;
  };
  let { central, group, onClose, onSaved }: Props = $props();

  const isEdit = $derived(!!group);

  let types = $state<GroupTypeEntry[]>([]);
  let typeId = $state(untrack(() => group?.type_id ?? ""));
  let name = $state(untrack(() => group?.name ?? ""));
  let forbidSingle = $state(untrack(() => group?.forbid_single_operation ?? false));
  let selected = $state<Set<string>>(
    untrack(() => new Set((group?.members ?? []).map((m) => m.address))),
  );
  // A candidate plus whether it can currently be assigned. Non-selectable
  // candidates (e.g. a device still config-pending) are shown with a hint
  // rather than hidden.
  type PickerMember = SuitableMemberEntry & { selectable: boolean };
  let candidates = $state<PickerMember[]>([]);

  let loading = $state(true);
  let loadingMembers = $state(false);
  let saving = $state(false);
  let error = $state<string | null>(null);

  // Picker UI state.
  let query = $state("");
  let roomFilter = $state<string | null>(null);
  // Area = an operator-defined grouping ABOVE CCU rooms (settings/
  // RoomsFunctionsAdmin.svelte), single-select like roomFilter. Hidden
  // entirely when no areas are defined.
  let areaFilter = $state<string | null>(null);
  let selectedOnly = $state(false);
  let openDevices = $state<Set<string>>(new Set());

  onMount(async () => {
    areasStore.ensureLoaded();
    try {
      if (!isEdit) {
        types = await api.groupTypes(central);
        if (!typeId && types.length > 0) typeId = types[0].id;
      }
      if (typeId) await loadMembers(typeId);
    } catch (err) {
      error = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  });

  async function loadMembers(type: string) {
    loadingMembers = true;
    try {
      const res = await api.groupSuitableMembers(type, central);
      const byAddr = new Map<string, PickerMember>();
      // Assignable candidates are selectable — unless the device is still
      // config-pending, in which case it is shown but not selectable.
      for (const m of res.assignable)
        byAddr.set(m.address, { ...m, selectable: !m.config_pending });
      // Surface config-pending leftovers as non-selectable candidates (with a
      // hint) instead of hiding them. The rest of the leftover list (devices of
      // the wrong type) stays hidden — it would only be noise.
      for (const m of res.leftover ?? []) {
        if (m.config_pending && !byAddr.has(m.address))
          byAddr.set(m.address, { ...m, selectable: false });
      }
      // The group's current members stay present AND selectable so they can be
      // kept or removed even when the type's suitable list no longer surfaces
      // them (or reports them config-pending).
      for (const m of group?.members ?? []) {
        const existing = byAddr.get(m.address);
        if (existing) existing.selectable = true;
        else
          // Not in the type's suitable list — carry the daemon-resolved
          // identification (device/channel name, model, rooms) the member row
          // already provides so the picker and tray show its name, not the raw
          // address.
          byAddr.set(m.address, {
            address: m.address,
            type: m.type_id,
            device_address: m.address.split(":")[0],
            device_name: m.device_name,
            device_model: m.device_model,
            channel_name: m.channel_name,
            rooms: m.rooms,
            selectable: true,
          });
      }
      candidates = [...byAddr.values()];
    } finally {
      loadingMembers = false;
    }
  }

  async function onTypeChange(next: string) {
    typeId = next;
    selected = new Set();
    roomFilter = null;
    areaFilter = null;
    query = "";
    selectedOnly = false;
    await loadMembers(next);
  }

  // ── grouping / filtering ────────────────────────────────────────────────
  type Channel = { address: string; name: string; no: number; selectable: boolean };
  type DeviceGroup = {
    deviceAddress: string;
    deviceName: string;
    deviceModel: string;
    memberType: string;
    rooms: string[];
    functions: string[];
    channels: Channel[];
    selectable: boolean;
    configPending: boolean;
  };

  function channelLabel(m: SuitableMemberEntry): string {
    if (m.channel_name) return m.channel_name;
    const no = m.channel_no ?? Number(m.address.split(":")[1] ?? 0);
    return t("groups.editor.channel_fallback", { no });
  }

  const devices = $derived.by<DeviceGroup[]>(() => {
    const byDev = new Map<string, DeviceGroup>();
    for (const m of candidates) {
      const devAddr = m.device_address || m.address.split(":")[0];
      let g = byDev.get(devAddr);
      if (!g) {
        g = {
          deviceAddress: devAddr,
          deviceName: m.device_name || devAddr,
          deviceModel: m.device_model || "",
          memberType: m.type || "",
          rooms: [...(m.rooms ?? [])],
          functions: [...(m.functions ?? [])],
          channels: [],
          selectable: false,
          configPending: false,
        };
        byDev.set(devAddr, g);
      }
      for (const r of m.rooms ?? []) if (!g.rooms.includes(r)) g.rooms.push(r);
      for (const f of m.functions ?? [])
        if (!g.functions.includes(f)) g.functions.push(f);
      g.channels.push({
        address: m.address,
        name: channelLabel(m),
        no: m.channel_no ?? Number(m.address.split(":")[1] ?? 0),
        selectable: m.selectable,
      });
      if (m.selectable) g.selectable = true;
      if (m.config_pending) g.configPending = true;
    }
    const list = [...byDev.values()];
    for (const g of list) g.channels.sort((a, b) => a.no - b.no);
    list.sort((a, b) => a.deviceName.localeCompare(b.deviceName));
    return list;
  });

  const roomOptions = $derived([
    ...new Set(devices.flatMap((g) => g.rooms).filter(Boolean)),
  ].sort((a, b) => a.localeCompare(b)));

  const filtered = $derived.by<DeviceGroup[]>(() => {
    const q = query.trim().toLowerCase();
    return devices.filter((g) => {
      if (roomFilter && !g.rooms.includes(roomFilter)) return false;
      if (areaFilter && !g.rooms.some((r) => areasStore.areaIdOf(central, r) === areaFilter)) {
        return false;
      }
      if (selectedOnly && !g.channels.some((c) => selected.has(c.address)))
        return false;
      if (q) {
        const hay = [
          g.deviceName,
          g.deviceModel,
          g.memberType,
          g.deviceAddress,
          ...g.rooms,
          ...g.functions,
          ...g.channels.map((c) => `${c.name} ${c.address}`),
        ]
          .join(" ")
          .toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  });

  const selectedDeviceCount = $derived(
    new Set([...selected].map((a) => a.split(":")[0])).size,
  );
  const selectedChips = $derived.by(() => {
    const out: { address: string; device: string; room: string; suffix: string }[] = [];
    for (const g of devices) {
      for (const c of g.channels) {
        if (selected.has(c.address)) {
          out.push({
            address: c.address,
            device: g.deviceName,
            room: g.rooms[0] ?? "",
            suffix: g.channels.length > 1 ? `·${c.no}` : "",
          });
        }
      }
    }
    return out;
  });

  function deviceState(g: DeviceGroup): "none" | "some" | "all" {
    const on = g.channels.filter((c) => selected.has(c.address)).length;
    if (on === 0) return "none";
    return on === g.channels.length ? "all" : "some";
  }

  function selectedInDevice(g: DeviceGroup): number {
    return g.channels.filter((c) => selected.has(c.address)).length;
  }

  const selectableAddrs = $derived(
    new Set(candidates.filter((m) => m.selectable).map((m) => m.address)),
  );

  function toggle(addr: string) {
    const s = new Set(selected);
    if (s.has(addr)) s.delete(addr);
    // A non-selectable channel (config-pending, not a current member) cannot be
    // added; an already-selected one can always be removed.
    else if (selectableAddrs.has(addr)) s.add(addr);
    else return;
    selected = s;
  }

  function toggleDevice(g: DeviceGroup) {
    if (!g.selectable) return;
    const s = new Set(selected);
    if (deviceState(g) === "all") g.channels.forEach((c) => s.delete(c.address));
    else g.channels.forEach((c) => c.selectable && s.add(c.address));
    selected = s;
  }

  function toggleOpen(addr: string) {
    const s = new Set(openDevices);
    if (s.has(addr)) s.delete(addr);
    else s.add(addr);
    openDevices = s;
  }

  function selectVisible() {
    const s = new Set(selected);
    for (const g of filtered)
      for (const c of g.channels) if (c.selectable) s.add(c.address);
    selected = s;
  }

  function clearAll() {
    selected = new Set();
  }

  const canSave = $derived(
    name.trim().length > 0 && typeId.length > 0 && !saving,
  );

  async function save() {
    if (!canSave) return;
    saving = true;
    try {
      const members = [...selected];
      if (isEdit && group) {
        await api.updateGroup(
          group.id,
          { name: name.trim(), forbid_single_operation: forbidSingle, members },
          central,
        );
        toastStore.success(t("groups.editor.updated"));
      } else {
        await api.createGroup(
          {
            type_id: typeId,
            name: name.trim(),
            forbid_single_operation: forbidSingle,
            members,
          },
          central,
        );
        toastStore.success(t("groups.editor.created"));
      }
      onSaved();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      saving = false;
    }
  }

  function typeLabel(ty: GroupTypeEntry): string {
    return ty.id;
  }
</script>

<div
  class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
  role="dialog"
  aria-modal="true"
  aria-label={isEdit ? t("groups.editor.edit_title") : t("groups.editor.create_title")}
  onkeydown={(e) => {
    if (e.key === "Escape" && !saving) onClose();
  }}
  tabindex="-1"
>
  <div
    class="flex max-h-[90vh] w-full max-w-2xl flex-col overflow-hidden rounded-lg shadow-xl"
    style="background-color: var(--ha-card-background-color); color: var(--ha-primary-text-color);"
  >
    <header class="border-b border-[var(--ha-divider-color)] px-5 py-3">
      <h2 class="text-lg font-semibold">
        {isEdit ? t("groups.editor.edit_title") : t("groups.editor.create_title")}
      </h2>
      {#if central}
        <p class="text-xs text-[var(--ha-secondary-text-color)]">{central}</p>
      {/if}
    </header>

    <div class="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-5 py-4">
      {#if loading}
        <LoadingState />
      {:else if error}
        <p class="text-sm text-red-600 dark:text-red-400">
          {t("common.error")} {error}
        </p>
      {:else}
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="mb-1 block text-sm font-medium" for="group-name">
              {t("groups.editor.name")}
            </label>
            <Input id="group-name" bind:value={name} placeholder={t("groups.editor.name")} disabled={saving} />
          </div>

          {#if !isEdit}
            <div>
              <span class="mb-1 block text-sm font-medium">{t("groups.type")}</span>
              {#if types.length <= 1}
                <p class="flex h-10 items-center text-sm text-[var(--ha-secondary-text-color)]">
                  {types[0] ? typeLabel(types[0]) : typeId || t("groups.editor.no_types")}
                </p>
              {:else}
                <select
                  class="h-10 w-full rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-3 text-sm text-[var(--ha-primary-text-color)]"
                  value={typeId}
                  disabled={saving}
                  onchange={(e) => void onTypeChange((e.currentTarget as HTMLSelectElement).value)}
                >
                  {#each types as ty (ty.id)}
                    <option value={ty.id}>{typeLabel(ty)}</option>
                  {/each}
                </select>
              {/if}
            </div>
          {/if}
        </div>

        <label class="flex items-center justify-between gap-3">
          <span class="flex min-w-0 flex-col">
            <span class="text-sm font-medium">{t("groups.operate_only_via_group")}</span>
            <span class="text-xs text-[var(--ha-secondary-text-color)]">
              {t("groups.operate_only_via_group.help")}
            </span>
          </span>
          <Switch bind:checked={forbidSingle} disabled={saving} />
        </label>

        <!-- member picker -->
        <div class="flex min-h-0 flex-col">
          <div class="mb-2 flex items-baseline justify-between">
            <span class="text-sm font-medium">{t("groups.editor.members")}</span>
            <span class="text-xs text-[var(--ha-secondary-text-color)]">
              {t("groups.editor.selection_summary", {
                channels: selected.size,
                devices: selectedDeviceCount,
              })}
            </span>
          </div>

          {#if loadingMembers}
            <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
          {:else if candidates.length === 0}
            <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("groups.editor.no_members")}</p>
          {:else}
            <!-- search + facets -->
            <div class="mb-2 space-y-2">
              <input
                type="search"
                class="h-10 w-full rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-secondary-background-color)] px-3 text-sm text-[var(--ha-primary-text-color)]"
                placeholder={t("groups.editor.search_placeholder")}
                aria-label={t("groups.editor.search_placeholder")}
                bind:value={query}
                disabled={saving}
              />
              <div class="flex flex-wrap items-center gap-1.5">
                {#each roomOptions as room (room)}
                  <button
                    type="button"
                    class="rounded-full border px-2.5 py-1 text-xs font-medium transition-colors {roomFilter === room
                      ? 'border-[var(--ha-primary-color)] bg-[var(--ha-primary-color)]/10 text-[var(--ha-primary-color)]'
                      : 'border-[var(--ha-divider-color)] text-[var(--ha-secondary-text-color)] hover:bg-[var(--ha-secondary-background-color)]'}"
                    aria-pressed={roomFilter === room}
                    onclick={() => (roomFilter = roomFilter === room ? null : room)}
                  >
                    {room}
                  </button>
                {/each}
                {#each areasStore.areas as a (a.id)}
                  <button
                    type="button"
                    class="rounded-full border px-2.5 py-1 text-xs font-medium transition-colors {areaFilter === a.id
                      ? 'border-[var(--ha-primary-color)] bg-[var(--ha-primary-color)]/10 text-[var(--ha-primary-color)]'
                      : 'border-[var(--ha-divider-color)] text-[var(--ha-secondary-text-color)] hover:bg-[var(--ha-secondary-background-color)]'}"
                    aria-pressed={areaFilter === a.id}
                    title={t("devicelist.area")}
                    onclick={() => (areaFilter = areaFilter === a.id ? null : a.id)}
                  >
                    {a.name}
                  </button>
                {/each}
                <button
                  type="button"
                  class="rounded-full border px-2.5 py-1 text-xs font-medium transition-colors {selectedOnly
                    ? 'border-[var(--ha-primary-color)] bg-[var(--ha-primary-color)]/10 text-[var(--ha-primary-color)]'
                    : 'border-[var(--ha-divider-color)] text-[var(--ha-secondary-text-color)] hover:bg-[var(--ha-secondary-background-color)]'}"
                  aria-pressed={selectedOnly}
                  onclick={() => (selectedOnly = !selectedOnly)}
                >
                  ★ {t("groups.editor.only_selected")}
                </button>
                <span class="flex-1"></span>
                <button
                  type="button"
                  class="text-xs font-semibold text-[var(--ha-primary-color)] hover:underline disabled:opacity-50"
                  onclick={selectVisible}
                  disabled={filtered.length === 0}
                >
                  {t("groups.editor.select_visible")}
                </button>
              </div>
            </div>

            <!-- grouped device list -->
            <div class="max-h-64 space-y-1.5 overflow-y-auto rounded-md border border-[var(--ha-divider-color)] p-1.5">
              {#if filtered.length === 0}
                <p class="px-2 py-6 text-center text-sm text-[var(--ha-secondary-text-color)]">
                  {t("groups.editor.no_matches")}
                </p>
              {/if}
              {#each filtered as g (g.deviceAddress)}
                {@const st = deviceState(g)}
                {@const multi = g.channels.length > 1}
                {@const locked = !g.selectable}
                <div
                  class="overflow-hidden rounded-md border {st === 'some'
                    ? 'border-[var(--ha-primary-color)]/60'
                    : 'border-[var(--ha-divider-color)]'} {locked ? 'opacity-60' : ''}"
                >
                  <div class="flex items-center gap-2.5 px-2 py-1.5">
                    <button
                      type="button"
                      class="flex h-5 w-5 flex-none items-center justify-center rounded border text-[11px] font-bold text-white {st ===
                      'none'
                        ? 'border-[var(--ha-divider-color)] bg-transparent'
                        : 'border-[var(--ha-primary-color)] bg-[var(--ha-primary-color)]'} disabled:cursor-not-allowed"
                      role="checkbox"
                      aria-checked={st === "all" ? "true" : st === "some" ? "mixed" : "false"}
                      aria-label={g.deviceName}
                      disabled={saving || locked}
                      onclick={() => toggleDevice(g)}
                    >
                      {#if st === "all"}✓{:else if st === "some"}–{/if}
                    </button>

                    <button
                      type="button"
                      class="flex min-w-0 flex-1 items-center gap-2 text-left disabled:cursor-not-allowed"
                      disabled={saving || (!multi && locked)}
                      onclick={() => (multi ? toggleOpen(g.deviceAddress) : toggle(g.channels[0].address))}
                    >
                      <span class="min-w-0 flex-1">
                        <span class="block truncate text-sm font-medium">{g.deviceName}</span>
                        <span class="mt-0.5 flex flex-wrap items-center gap-1.5 text-xs text-[var(--ha-secondary-text-color)]">
                          {#each g.rooms.slice(0, 1) as room (room)}
                            <span class="rounded-full bg-blue-100 px-1.5 py-0.5 font-medium text-blue-700 dark:bg-blue-900/40 dark:text-blue-300">{room}</span>
                          {/each}
                          {#each g.functions.slice(0, 1) as fn (fn)}
                            <span class="rounded-full bg-amber-100 px-1.5 py-0.5 font-medium text-amber-700 dark:bg-amber-900/40 dark:text-amber-300">{fn}</span>
                          {/each}
                          {#if g.deviceModel}
                            <span class="font-mono text-[11px]">{g.deviceModel}</span>
                          {/if}
                          <span class="font-mono text-[11px] opacity-70">{g.deviceAddress}</span>
                          {#if locked}
                            <span class="rounded-full bg-[var(--ha-divider-color)] px-1.5 py-0.5 font-medium text-[var(--ha-secondary-text-color)]">
                              {g.configPending ? t("groups.editor.config_pending") : t("groups.editor.not_selectable")}
                            </span>
                          {/if}
                        </span>
                      </span>
                      {#if multi}
                        <span class="flex-none font-mono text-xs text-[var(--ha-secondary-text-color)]">
                          {selectedInDevice(g)}/{g.channels.length}
                        </span>
                        <span
                          class="flex-none text-[var(--ha-secondary-text-color)] transition-transform"
                          style={openDevices.has(g.deviceAddress) ? "transform: rotate(90deg)" : ""}
                        >▸</span>
                      {/if}
                    </button>
                  </div>

                  {#if multi && openDevices.has(g.deviceAddress)}
                    <div class="border-t border-[var(--ha-divider-color)] bg-[var(--ha-secondary-background-color)]">
                      {#each g.channels as c (c.address)}
                        <label
                          class="ml-2 flex items-center gap-2.5 border-l-2 px-2.5 py-1.5 text-sm {selected.has(
                            c.address,
                          )
                            ? 'border-[var(--ha-primary-color)] bg-[var(--ha-primary-color)]/5'
                            : 'border-transparent hover:bg-[var(--ha-card-background-color)]'} {c.selectable ||
                          selected.has(c.address)
                            ? 'cursor-pointer'
                            : 'cursor-not-allowed opacity-60'}"
                        >
                          <input
                            type="checkbox"
                            class="h-4 w-4"
                            checked={selected.has(c.address)}
                            disabled={saving || (!c.selectable && !selected.has(c.address))}
                            onchange={() => toggle(c.address)}
                          />
                          <span class="flex-1">{c.name}</span>
                          <span class="font-mono text-[11px] text-[var(--ha-secondary-text-color)]">{c.address}</span>
                        </label>
                      {/each}
                    </div>
                  {/if}
                </div>
              {/each}
            </div>

            <!-- selection tray -->
            <div class="mt-2 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-secondary-background-color)] p-2">
              <div class="mb-1.5 flex items-center gap-2">
                <span class="text-sm font-medium">{t("groups.editor.selected")}</span>
                <span class="flex-1"></span>
                {#if selected.size > 0}
                  <button
                    type="button"
                    class="text-xs font-semibold text-[var(--ha-primary-color)] hover:underline"
                    onclick={clearAll}
                  >
                    {t("groups.editor.clear_all")}
                  </button>
                {/if}
              </div>
              {#if selectedChips.length === 0}
                <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("groups.editor.no_selection")}</p>
              {:else}
                <div class="flex max-h-20 flex-wrap gap-1.5 overflow-y-auto">
                  {#each selectedChips as chip (chip.address)}
                    <span class="inline-flex items-center gap-1.5 rounded-full border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] py-0.5 pl-2 pr-0.5 text-xs">
                      {#if chip.room}<span class="text-blue-600 dark:text-blue-300">{chip.room}</span>{/if}
                      <span>{chip.device}{chip.suffix}</span>
                      <button
                        type="button"
                        class="flex h-4 w-4 items-center justify-center rounded-full bg-[var(--ha-secondary-background-color)] text-[var(--ha-secondary-text-color)] hover:text-[var(--ha-primary-text-color)]"
                        aria-label={t("common.remove")}
                        onclick={() => toggle(chip.address)}
                      >✕</button>
                    </span>
                  {/each}
                </div>
              {/if}
            </div>
          {/if}
        </div>
      {/if}
    </div>

    <footer class="flex justify-end gap-2 border-t border-[var(--ha-divider-color)] px-5 py-3">
      <Button variant="outline" onclick={onClose} disabled={saving}>
        {t("common.cancel")}
      </Button>
      <Button onclick={() => void save()} disabled={!canSave}>
        {saving ? t("common.saving") : t("common.save")}
      </Button>
    </footer>
  </div>
</div>
