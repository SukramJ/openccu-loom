<script lang="ts">
  import { onMount } from "svelte";
  import { matterStore } from "$lib/stores/matter.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";
  import Button from "$lib/components/ui/Button.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import type { MatterExposure } from "$lib/api/matter-types";

  onMount(async () => {
    await matterStore.loadExposures();
  });

  // Filters
  let searchText = $state("");
  let filterKind = $state("all");
  // Multi-select class chips. Empty set = no class restriction (= "All").
  // Sentinel "(unmapped)" matches rows whose `device_type_label` is empty.
  let selectedClasses = $state<Set<string>>(new Set());
  const UNMAPPED_LABEL = "__unmapped__";

  // Selected row keys for bulk operations
  let selectedKeys = $state<Set<string>>(new Set());

  // Side drawer
  let drawerExposure = $state<MatterExposure | null>(null);
  let drawerFriendlyName = $state("");
  let drawerEnabled = $state(false);

  const kindOptions = ["all", "custom", "generic", "calculated", "combined", "measurement"] as const;

  // Derive { label, count } per class so chips stay sorted alphabetically and
  // can show how many rows each class contributes. "Unmapped" only appears
  // when at least one row lacks a device_type_label.
  type ClassChip = { value: string; label: string; count: number };
  const classChips = $derived.by<ClassChip[]>(() => {
    const counts = new Map<string, number>();
    for (const item of matterStore.exposures) {
      const key = item.device_type_label ? item.device_type_label : UNMAPPED_LABEL;
      counts.set(key, (counts.get(key) ?? 0) + 1);
    }
    const out: ClassChip[] = [];
    for (const [value, count] of counts) {
      const label = value === UNMAPPED_LABEL ? t("matter.expose.filter_class_unmapped") : value;
      out.push({ value, label, count });
    }
    out.sort((a, b) => {
      // Push the "unmapped" bucket to the end so the spec'd labels read first.
      if (a.value === UNMAPPED_LABEL) return 1;
      if (b.value === UNMAPPED_LABEL) return -1;
      return a.label.localeCompare(b.label);
    });
    return out;
  });

  function toggleClass(value: string) {
    const next = new Set(selectedClasses);
    if (next.has(value)) next.delete(value);
    else next.add(value);
    selectedClasses = next;
  }

  function clearClassFilter() {
    selectedClasses = new Set();
  }

  const filteredItems = $derived.by(() => {
    const q = searchText.trim().toLowerCase();
    const classFilterActive = selectedClasses.size > 0;
    return matterStore.exposures.filter((item) => {
      if (filterKind !== "all" && item.dp_kind !== filterKind) return false;
      if (classFilterActive) {
        const key = item.device_type_label ? item.device_type_label : UNMAPPED_LABEL;
        if (!selectedClasses.has(key)) return false;
      }
      if (q) {
        if (
          !item.display_name.toLowerCase().includes(q) &&
          !item.device_address.toLowerCase().includes(q) &&
          !item.dp_key.toLowerCase().includes(q) &&
          !(item.parameter_label ?? "").toLowerCase().includes(q) &&
          !(item.friendly_name ?? "").toLowerCase().includes(q) &&
          !(item.device_type_label ?? "").toLowerCase().includes(q)
        )
          return false;
      }
      return true;
    });
  });

  function mappabilityIcon(m: string): string {
    if (m === "mappable") return "●";
    if (m === "partially_mappable") return "⚠";
    return "⛔";
  }

  function stateIcon(item: MatterExposure): string {
    if (item.mappable === "unmappable") return "⛔";
    if (item.enabled && item.mappable === "mappable") return "●";
    if (item.enabled && item.mappable === "partially_mappable") return "⚠";
    return "◯";
  }

  function stateColor(item: MatterExposure): string {
    if (item.mappable === "unmappable") return "var(--ha-disabled-text-color)";
    if (item.enabled && item.mappable === "mappable") return "#22c55e"; // green
    if (item.enabled && item.mappable === "partially_mappable") return "#f59e0b"; // amber
    return "var(--ha-secondary-text-color)";
  }

  function isBulkable(item: MatterExposure): boolean {
    return item.mappable !== "unmappable";
  }

  function toggleSelect(key: string, bulkable: boolean) {
    if (!bulkable) return;
    const next = new Set(selectedKeys);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    selectedKeys = next;
  }

  function selectAll() {
    const next = new Set<string>();
    for (const item of filteredItems) {
      if (isBulkable(item)) next.add(matterStore.exposureKey(item));
    }
    selectedKeys = next;
  }

  function clearSelection() {
    selectedKeys = new Set();
  }

  async function bulkSet(enabled: boolean) {
    const items = filteredItems.filter((item) => selectedKeys.has(matterStore.exposureKey(item)));
    for (const item of items) {
      matterStore.markDirty(item, { enabled });
    }
    clearSelection();
  }

  async function saveChanges() {
    try {
      const applied = await matterStore.saveBulk();
      toastStore.success(t("matter.expose.saved_toast", { count: String(applied) }));
    } catch (err) {
      toastStore.error(err instanceof Error ? err.message : String(err));
    }
  }

  function openDrawer(item: MatterExposure) {
    // Check if there's a pending update for this item
    const key = matterStore.exposureKey(item);
    const pending = matterStore.pendingUpdates.get(key);
    drawerExposure = item;
    drawerFriendlyName = pending?.friendly_name ?? item.friendly_name;
    drawerEnabled = pending?.enabled ?? item.enabled;
  }

  function closeDrawer() {
    drawerExposure = null;
  }

  function saveDrawer() {
    if (!drawerExposure) return;
    matterStore.markDirty(drawerExposure, {
      enabled: drawerEnabled,
      friendly_name: drawerFriendlyName,
    });
    closeDrawer();
  }

  // Conflict hint: enabled rows on the same channel with a different dp_kind
  const drawerConflictRows = $derived.by(() => {
    if (!drawerExposure) return [];
    const current = drawerExposure;
    return matterStore.exposures.filter(
      (r) =>
        r.central_name === current.central_name &&
        r.device_address === current.device_address &&
        r.channel_no === current.channel_no &&
        r.dp_kind !== current.dp_kind &&
        r.enabled === true,
    );
  });

  // True when a non-custom DP is open and a custom DP is already enabled on the same channel
  const drawerConflictCustomActive = $derived.by(() => {
    if (!drawerExposure) return false;
    if (drawerExposure.dp_kind === "custom") return false;
    return drawerConflictRows.some((r) => r.dp_kind === "custom");
  });

  // True when the custom DP is open and a generic/calculated/combined/measurement DP is already enabled
  const drawerConflictGenericActive = $derived.by(() => {
    if (!drawerExposure) return false;
    if (drawerExposure.dp_kind !== "custom") return false;
    return drawerConflictRows.some((r) => r.dp_kind !== "custom");
  });

  // dp_key of the first conflicting custom DP (for the "custom active" warning)
  const drawerConflictCustomDpKey = $derived.by(() => {
    const row = drawerConflictRows.find((r) => r.dp_kind === "custom");
    return row?.dp_key ?? "";
  });

</script>

<div>
  <!-- Toolbar -->
  <div class="flex flex-wrap items-center gap-2 mb-3">
    <Input
      placeholder={t("matter.expose.search_placeholder")}
      bind:value={searchText}
      class="w-64"
    />
    <select
      class="h-9 rounded-md border px-2 text-sm"
      style="border-color: var(--ha-divider-color); background-color: var(--ha-card-background-color); color: var(--ha-primary-text-color);"
      bind:value={filterKind}
      aria-label={t("matter.expose.filter_kind")}
    >
      {#each kindOptions as k}
        <option value={k}>
          {k === "all" ? t("matter.expose.filter_kind") : t(`matter.expose.kind.${k}`)}
        </option>
      {/each}
    </select>
    {#if selectedKeys.size > 0}
      <Button size="sm" onclick={() => void bulkSet(true)}>{t("matter.expose.bulk_expose")}</Button>
      <Button size="sm" variant="outline" onclick={() => void bulkSet(false)}>{t("matter.expose.bulk_hide")}</Button>
      <Button size="sm" variant="ghost" onclick={clearSelection}>{t("common.cancel")}</Button>
    {:else}
      <Button size="sm" variant="ghost" onclick={selectAll}>{t("matter.expose.select_all")}</Button>
    {/if}
    {#if matterStore.hasDirty}
      <div class="ml-auto flex gap-2">
        <Button size="sm" onclick={saveChanges}>{t("matter.expose.save")}</Button>
        <Button size="sm" variant="outline" onclick={() => matterStore.discardDirty()}>{t("matter.expose.discard")}</Button>
      </div>
    {/if}
  </div>

  <!-- Class chip filter -->
  {#if classChips.length > 0}
    <div class="flex flex-wrap items-center gap-1.5 mb-4">
      <span class="text-xs font-semibold mr-1" style="color: var(--ha-secondary-text-color);">
        {t("matter.expose.filter_class")}:
      </span>
      <button
        type="button"
        class="h-7 px-2.5 rounded-full border text-xs transition"
        style={
          selectedClasses.size === 0
            ? "background-color: var(--ha-primary-color, #2563eb); border-color: var(--ha-primary-color, #2563eb); color: #fff;"
            : "background-color: var(--ha-card-background-color); border-color: var(--ha-divider-color); color: var(--ha-primary-text-color);"
        }
        onclick={clearClassFilter}
      >
        {t("matter.expose.filter_class_all")}
      </button>
      {#each classChips as chip (chip.value)}
        {@const active = selectedClasses.has(chip.value)}
        <button
          type="button"
          class="h-7 px-2.5 rounded-full border text-xs transition flex items-center gap-1"
          style={
            active
              ? "background-color: var(--ha-primary-color, #2563eb); border-color: var(--ha-primary-color, #2563eb); color: #fff;"
              : "background-color: var(--ha-card-background-color); border-color: var(--ha-divider-color); color: var(--ha-primary-text-color);"
          }
          onclick={() => toggleClass(chip.value)}
          aria-pressed={active}
        >
          <span>{chip.label}</span>
          <span
            class="text-[10px] opacity-75 tabular-nums"
            aria-hidden="true"
          >{chip.count}</span>
        </button>
      {/each}
    </div>
  {/if}

  {#if matterStore.exposuresLoading && matterStore.exposures.length === 0}
    <p class="text-sm" style="color: var(--ha-secondary-text-color);">{t("common.loading")}</p>
  {:else if matterStore.exposuresError}
    <p class="text-sm" style="color: var(--ha-error-color, #ef4444);">{matterStore.exposuresError}</p>
  {:else if filteredItems.length === 0}
    <p class="text-sm" style="color: var(--ha-secondary-text-color);">{t("matter.expose.empty")}</p>
  {:else}
    <div class="rounded-lg border overflow-x-auto" style="border-color: var(--ha-divider-color);">
      <table class="w-full text-sm">
        <thead>
          <tr style="border-bottom: 1px solid var(--ha-divider-color); background-color: var(--ha-secondary-background-color);">
            <th class="px-3 py-2 text-left w-10">
              <span class="sr-only">Select</span>
            </th>
            <th class="px-3 py-2 text-left w-8">
              <span class="sr-only">State</span>
            </th>
            <th class="px-3 py-2 text-left">{t("matter.expose.col_name")}</th>
            <th class="px-3 py-2 text-left hidden md:table-cell">{t("matter.expose.col_channel")}</th>
            <th class="px-3 py-2 text-left hidden md:table-cell">{t("matter.expose.col_parameter")}</th>
            <th class="px-3 py-2 text-left hidden md:table-cell">{t("matter.expose.filter_kind")}</th>
            <th class="px-3 py-2 text-left hidden lg:table-cell">{t("matter.expose.filter_class")}</th>
          </tr>
        </thead>
        <tbody>
          {#each filteredItems as item (matterStore.exposureKey(item))}
            {@const key = matterStore.exposureKey(item)}
            {@const selected = selectedKeys.has(key)}
            {@const bulkable = isBulkable(item)}
            {@const pending = matterStore.pendingUpdates.has(key)}
            <tr
              class="cursor-pointer transition"
              style="border-bottom: 1px solid var(--ha-divider-color); background-color: {selected ? 'rgb(0 0 0 / 0.04)' : 'transparent'};"
              onclick={() => openDrawer(item)}
              onkeydown={(e) => { if (e.key === "Enter") openDrawer(item); }}
              tabindex="0"
            >
              <td class="px-3 py-2">
                <input
                  type="checkbox"
                  checked={selected}
                  disabled={!bulkable}
                  onclick={(e) => { e.stopPropagation(); toggleSelect(key, bulkable); }}
                  class="cursor-pointer"
                  aria-label="Select row"
                />
              </td>
              <td class="px-3 py-2">
                <span style="color: {stateColor(item)}; font-size: 1rem;">
                  {stateIcon(item)}
                </span>
              </td>
              <td class="px-3 py-2 font-medium" style="color: var(--ha-primary-text-color);">
                {(matterStore.pendingUpdates.get(key)?.friendly_name ?? item.friendly_name) || item.display_name}
                {#if pending}
                  <span class="ml-1 text-xs" style="color: var(--ha-primary-color);">{t("common.modified")}</span>
                {/if}
              </td>
              <td class="px-3 py-2 hidden md:table-cell" style="color: var(--ha-secondary-text-color);">
                {item.channel_no}
              </td>
              <td class="px-3 py-2 hidden md:table-cell" style="color: var(--ha-secondary-text-color);">
                {#if item.parameter_label}
                  {item.parameter_label}
                  <span class="ml-1 font-mono text-[10px] opacity-60">{item.dp_key}</span>
                {:else}
                  <span class="font-mono text-xs">{item.dp_key}</span>
                {/if}
              </td>
              <td class="px-3 py-2 hidden md:table-cell" style="color: var(--ha-secondary-text-color);">
                {t(`matter.expose.kind.${item.dp_kind}`) ?? item.dp_kind}
              </td>
              <td class="px-3 py-2 hidden lg:table-cell" style="color: var(--ha-secondary-text-color);">
                {item.device_type_label || "—"}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>

<!-- Side drawer -->
{#if drawerExposure}
  {@const item = drawerExposure}
  <!-- Backdrop -->
  <div
    class="fixed inset-0 z-40"
    style="background-color: rgb(0 0 0 / 0.3);"
    onclick={closeDrawer}
    onkeydown={(e) => { if (e.key === "Escape") closeDrawer(); }}
    role="presentation"
    tabindex="-1"
    aria-hidden="true"
  ></div>
  <!-- Drawer panel -->
  <aside
    class="fixed right-0 top-0 h-full w-full max-w-sm z-50 flex flex-col border-l overflow-y-auto"
    style="background-color: var(--ha-card-background-color); border-color: var(--ha-divider-color);"
    aria-label="Exposure detail"
  >
    <div class="flex items-center justify-between p-4 border-b" style="border-color: var(--ha-divider-color);">
      <h2 class="text-base font-semibold" style="color: var(--ha-primary-text-color);">
        {item.display_name}
      </h2>
      <button
        type="button"
        class="rounded-md p-1 hover:bg-slate-100 dark:hover:bg-slate-800"
        onclick={closeDrawer}
        aria-label={t("common.close")}
      >✕</button>
    </div>
    <div class="flex-1 p-4 space-y-4">
      <!-- Friendly name -->
      <div>
        <label for="drawer-friendly-name" class="block text-xs font-semibold mb-1" style="color: var(--ha-secondary-text-color);">
          Friendly name
        </label>
        <input
          id="drawer-friendly-name"
          type="text"
          bind:value={drawerFriendlyName}
          class="w-full rounded-md border px-2 py-1 text-sm"
          style="border-color: var(--ha-divider-color); background-color: var(--ha-card-background-color); color: var(--ha-primary-text-color);"
          placeholder={item.display_name}
        />
      </div>
      <!-- Expose toggle -->
      <div class="flex items-center gap-3">
        <input
          type="checkbox"
          id="drawer-enabled"
          bind:checked={drawerEnabled}
          disabled={item.mappable === "unmappable"}
          class="cursor-pointer"
        />
        <label for="drawer-enabled" class="text-sm" style="color: var(--ha-primary-text-color);">
          {t("matter.status.enabled")}
        </label>
      </div>
      <!-- Conflict hint: non-custom DP, but a custom DP is already enabled on this channel -->
      {#if drawerConflictCustomActive}
        <div class="rounded-md border px-3 py-2 text-sm space-y-1" style="background-color: rgb(255 251 235); border-color: rgb(253 230 138); color: rgb(120 53 15);">
          <p class="font-semibold flex items-center gap-1">
            <span aria-hidden="true">⚠</span>
            {t("matter.expose.conflict_hint")}
          </p>
          <p class="text-xs" style="color: rgb(146 64 14);">
            {t("matter.expose.conflict_hint_custom_active", { profile: drawerConflictCustomDpKey })}
          </p>
        </div>
      {/if}
      <!-- Conflict hint: custom DP, but generic/calculated DPs are already enabled on this channel -->
      {#if drawerConflictGenericActive}
        <div class="rounded-md border px-3 py-2 text-sm space-y-1" style="background-color: rgb(255 251 235); border-color: rgb(253 230 138); color: rgb(120 53 15);">
          <p class="font-semibold flex items-center gap-1">
            <span aria-hidden="true">⚠</span>
            {t("matter.expose.conflict_hint")}
          </p>
          <p class="text-xs" style="color: rgb(146 64 14);">
            {t("matter.expose.conflict_hint_generic_active")}
          </p>
        </div>
      {/if}
      <!-- Mappability state -->
      <div>
        <p class="text-xs font-semibold mb-1" style="color: var(--ha-secondary-text-color);">
          State
        </p>
        <p class="text-sm">
          {mappabilityIcon(item.mappable)}
          {#if item.mappable === "unmappable"}
            {t("matter.expose.unmappable_hint")}
          {:else if item.mappable === "partially_mappable"}
            {t("matter.expose.partially_mappable_hint")}
          {:else}
            {t("matter.status.enabled")}
          {/if}
        </p>
        {#if item.reason}
          <p class="mt-1 text-xs" style="color: var(--ha-secondary-text-color);">{item.reason}</p>
        {/if}
      </div>
      <!-- Source -->
      <div>
        <p class="text-xs font-semibold mb-1" style="color: var(--ha-secondary-text-color);">Source</p>
        {#if item.parameter_label}
          <p class="text-sm" style="color: var(--ha-primary-text-color);">{item.parameter_label}</p>
          <p class="text-xs font-mono" style="color: var(--ha-secondary-text-color);">{item.dp_kind}: {item.dp_key}</p>
        {:else}
          <p class="text-sm font-mono" style="color: var(--ha-primary-text-color);">{item.dp_kind}: {item.dp_key}</p>
        {/if}
        <p class="text-xs" style="color: var(--ha-secondary-text-color);">
          {item.device_address} ch.{item.channel_no}
        </p>
      </div>
      <!-- Device type -->
      {#if item.device_type_label || item.device_type}
        <div>
          <p class="text-xs font-semibold mb-1" style="color: var(--ha-secondary-text-color);">Matter device type</p>
          <p class="text-sm" style="color: var(--ha-primary-text-color);">
            {item.device_type_label || `0x${item.device_type.toString(16).toUpperCase().padStart(4, "0")}`}
          </p>
        </div>
      {/if}
      <!-- Clusters -->
      {#if item.clusters.length > 0}
        <div>
          <p class="text-xs font-semibold mb-1" style="color: var(--ha-secondary-text-color);">Clusters</p>
          <p class="text-sm font-mono" style="color: var(--ha-primary-text-color);">
            {item.clusters.map((c) => `0x${c.toString(16).toUpperCase().padStart(4, "0")}`).join(" · ")}
          </p>
        </div>
      {/if}
    </div>
    <div class="p-4 border-t flex gap-2" style="border-color: var(--ha-divider-color);">
      <Button size="sm" onclick={saveDrawer}>{t("common.save")}</Button>
      <Button size="sm" variant="outline" onclick={closeDrawer}>{t("common.cancel")}</Button>
    </div>
  </aside>
{/if}
