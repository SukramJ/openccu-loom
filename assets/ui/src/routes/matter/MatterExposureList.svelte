<script lang="ts">
  import { onMount } from "svelte";
  import { matterStore } from "$lib/stores/matter.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import Button from "$lib/components/ui/Button.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
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

  // Pass the pre-filtered list to DataTable so the external search/kind/class
  // filters are not duplicated by DataTable's own search feature.
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

  function stateIcon(item: MatterExposure): string {
    if (item.mappable === "unmappable") return "⛔";
    if (item.enabled && item.mappable === "mappable") return "●";
    if (item.enabled && item.mappable === "partially_mappable") return "⚠";
    return "◯";
  }

  function stateColorClass(item: MatterExposure): string {
    if (item.mappable === "unmappable") return "text-slate-300 dark:text-slate-600";
    if (item.enabled && item.mappable === "mappable") return "text-green-500 dark:text-green-400";
    if (item.enabled && item.mappable === "partially_mappable") return "text-amber-500 dark:text-amber-400";
    return "text-slate-400 dark:text-slate-500";
  }

  function mappabilityIcon(m: string): string {
    if (m === "mappable") return "●";
    if (m === "partially_mappable") return "⚠";
    return "⛔";
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
    toastStore.info(t("common.modified"));
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

  // DataTable columns — the select and state columns use the cell snippet for
  // custom rendering; name/channel/parameter/kind/class are sortable text fields.
  const columns: DataColumn<MatterExposure>[] = $derived([
    { key: "select", label: t("matter.expose.col_select") },
    { key: "state", label: t("matter.expose.col_state"), get: (r) => (r.enabled ? 1 : 0) },
    {
      key: "name",
      label: t("matter.expose.col_name"),
      sortable: true,
      title: true,
      get: (r) => matterStore.pendingUpdates.get(matterStore.exposureKey(r))?.friendly_name ?? (r.friendly_name || r.display_name),
    },
    { key: "channel", label: t("matter.expose.col_channel"), sortable: true, get: (r) => r.channel_no },
    { key: "parameter", label: t("matter.expose.col_parameter"), sortable: true, get: (r) => r.parameter_label || r.dp_key },
    { key: "kind", label: t("matter.expose.filter_kind"), sortable: true, get: (r) => r.dp_kind },
    { key: "class", label: t("matter.expose.filter_class"), sortable: true, get: (r) => r.device_type_label },
  ]);

  // Row highlight for the selected state via DataTable's rowClass prop.
  function rowClass(item: MatterExposure): string {
    const key = matterStore.exposureKey(item);
    return selectedKeys.has(key) ? "bg-black/5 dark:bg-white/5" : "";
  }
</script>

<div>
  <!-- Toolbar -->
  <div class="flex flex-col gap-2 mb-3">
    <!-- Row 1: search + kind filter -->
    <div class="flex flex-wrap items-center gap-2">
      <Input
        placeholder={t("matter.expose.search_placeholder")}
        bind:value={searchText}
        class="w-full sm:w-64"
      />
      <select
        class="h-10 rounded-md border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 px-2 text-base sm:text-sm sm:h-9"
        bind:value={filterKind}
        aria-label={t("matter.expose.filter_kind")}
      >
        {#each kindOptions as k}
          <option value={k}>
            {k === "all" ? t("matter.expose.filter_kind") : t(`matter.expose.kind.${k}`)}
          </option>
        {/each}
      </select>
    </div>
    <!-- Row 2: bulk actions + save/discard -->
    <div class="flex flex-wrap items-center gap-2">
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
  </div>

  <!-- Class chip filter -->
  {#if classChips.length > 0}
    <div class="flex flex-wrap items-center gap-1.5 mb-4">
      <span class="text-xs font-semibold mr-1 text-slate-500 dark:text-slate-400">
        {t("matter.expose.filter_class")}:
      </span>
      <button
        type="button"
        class="h-7 px-2.5 rounded-full border text-xs transition {selectedClasses.size === 0 ? 'bg-brand-600 border-brand-600 text-white dark:bg-brand-500 dark:border-brand-500' : 'bg-white dark:bg-slate-900 border-slate-300 dark:border-slate-600 text-slate-900 dark:text-slate-100'}"
        onclick={clearClassFilter}
      >
        {t("matter.expose.filter_class_all")}
      </button>
      {#each classChips as chip (chip.value)}
        {@const active = selectedClasses.has(chip.value)}
        <button
          type="button"
          class="h-7 px-2.5 rounded-full border text-xs transition flex items-center gap-1 {active ? 'bg-brand-600 border-brand-600 text-white dark:bg-brand-500 dark:border-brand-500' : 'bg-white dark:bg-slate-900 border-slate-300 dark:border-slate-600 text-slate-900 dark:text-slate-100'}"
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
    <LoadingState message={t("common.loading")} />
  {:else if matterStore.exposuresError}
    <ErrorState message={matterStore.exposuresError} onRetry={() => void matterStore.loadExposures()} />
  {:else}
    <Card class="p-4">
      <DataTable
        rows={filteredItems}
        {columns}
        rowKey={(item) => matterStore.exposureKey(item)}
        {rowClass}
        emptyMessage={t("matter.expose.empty")}
        emptyIcon="mdi:list-checks"
        initialSort={{ key: "name", asc: true }}
      >
        {#snippet cell(item, col)}
          {@const key = matterStore.exposureKey(item)}
          {@const selected = selectedKeys.has(key)}
          {@const bulkable = isBulkable(item)}
          {@const pending = matterStore.pendingUpdates.has(key)}
          {#if col.key === "select"}
            <label class="flex items-center justify-center">
              <input
                type="checkbox"
                checked={selected}
                disabled={!bulkable}
                onclick={(e) => { e.stopPropagation(); toggleSelect(key, bulkable); }}
                class="cursor-pointer h-5 w-5"
                aria-label={t("matter.expose.select_row")}
              />
            </label>
          {:else if col.key === "state"}
            <span class="text-base {stateColorClass(item)}">
              {stateIcon(item)}
            </span>
          {:else if col.key === "name"}
            <button
              type="button"
              class="text-left font-medium text-slate-900 dark:text-slate-100 hover:underline w-full"
              onclick={() => openDrawer(item)}
            >
              {(matterStore.pendingUpdates.get(key)?.friendly_name ?? item.friendly_name) || item.display_name}
              {#if pending}
                <span class="ml-1 text-xs text-brand-600 dark:text-brand-400">{t("common.modified")}</span>
              {/if}
            </button>
          {:else if col.key === "channel"}
            <span class="text-slate-500 dark:text-slate-400">{item.channel_no}</span>
          {:else if col.key === "parameter"}
            {#if item.parameter_label}
              <span class="text-slate-500 dark:text-slate-400">{item.parameter_label}</span>
              <span class="ml-1 font-mono text-[10px] opacity-60">{item.dp_key}</span>
            {:else}
              <span class="font-mono text-xs text-slate-500 dark:text-slate-400">{item.dp_key}</span>
            {/if}
          {:else if col.key === "kind"}
            <span class="text-slate-500 dark:text-slate-400">
              {t(`matter.expose.kind.${item.dp_kind}`) ?? item.dp_kind}
            </span>
          {:else if col.key === "class"}
            <span class="text-slate-500 dark:text-slate-400">
              {item.device_type_label || "—"}
            </span>
          {/if}
        {/snippet}
      </DataTable>
    </Card>
  {/if}
</div>

<!-- Side drawer -->
{#if drawerExposure}
  {@const item = drawerExposure}
  <!-- Backdrop -->
  <div
    class="fixed inset-0 z-40 bg-black/30"
    onclick={closeDrawer}
    onkeydown={(e) => { if (e.key === "Escape") closeDrawer(); }}
    role="presentation"
    tabindex="-1"
    aria-hidden="true"
  ></div>
  <!-- Drawer panel -->
  <aside
    class="fixed right-0 top-0 h-full w-full max-w-sm z-50 flex flex-col border-l border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 overflow-y-auto"
    aria-label={t("matter.expose.drawer_aria")}
  >
    <div class="flex items-center justify-between p-4 border-b border-slate-200 dark:border-slate-700">
      <h2 class="text-base font-semibold text-slate-900 dark:text-slate-100">
        {item.display_name}
      </h2>
      <button
        type="button"
        class="rounded-md p-2 hover:bg-slate-100 dark:hover:bg-slate-800"
        onclick={closeDrawer}
        aria-label={t("common.close")}
      >✕</button>
    </div>
    <div class="flex-1 p-4 space-y-4">
      <!-- Friendly name -->
      <div>
        <label for="drawer-friendly-name" class="block text-xs font-semibold mb-1 text-slate-500 dark:text-slate-400">
          {t("matter.expose.friendly_name")}
        </label>
        <input
          id="drawer-friendly-name"
          type="text"
          bind:value={drawerFriendlyName}
          class="w-full rounded-md border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 px-2 h-10 text-base sm:text-sm"
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
        <label for="drawer-enabled" class="text-sm text-slate-900 dark:text-slate-100">
          {t("matter.status.enabled")}
        </label>
      </div>
      <!-- Conflict hint: non-custom DP, but a custom DP is already enabled on this channel -->
      {#if drawerConflictCustomActive}
        <div class="rounded-md border border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-950/40 px-3 py-2 text-sm space-y-1 text-amber-900 dark:text-amber-200">
          <p class="font-semibold flex items-center gap-1">
            <span aria-hidden="true">⚠</span>
            {t("matter.expose.conflict_hint")}
          </p>
          <p class="text-xs text-amber-800 dark:text-amber-300">
            {t("matter.expose.conflict_hint_custom_active", { profile: drawerConflictCustomDpKey })}
          </p>
        </div>
      {/if}
      <!-- Conflict hint: custom DP, but generic/calculated/combined/measurement DP is already enabled -->
      {#if drawerConflictGenericActive}
        <div class="rounded-md border border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-950/40 px-3 py-2 text-sm space-y-1 text-amber-900 dark:text-amber-200">
          <p class="font-semibold flex items-center gap-1">
            <span aria-hidden="true">⚠</span>
            {t("matter.expose.conflict_hint")}
          </p>
          <p class="text-xs text-amber-800 dark:text-amber-300">
            {t("matter.expose.conflict_hint_generic_active")}
          </p>
        </div>
      {/if}
      <!-- Mappability state -->
      <div>
        <p class="text-xs font-semibold mb-1 text-slate-500 dark:text-slate-400">
          {t("matter.expose.drawer_state")}
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
          <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">{item.reason}</p>
        {/if}
      </div>
      <!-- Source -->
      <div>
        <p class="text-xs font-semibold mb-1 text-slate-500 dark:text-slate-400">{t("matter.expose.drawer_source")}</p>
        {#if item.parameter_label}
          <p class="text-sm text-slate-900 dark:text-slate-100">{item.parameter_label}</p>
          <p class="text-xs font-mono text-slate-500 dark:text-slate-400">{item.dp_kind}: {item.dp_key}</p>
        {:else}
          <p class="text-sm font-mono text-slate-900 dark:text-slate-100">{item.dp_kind}: {item.dp_key}</p>
        {/if}
        <p class="text-xs text-slate-500 dark:text-slate-400">
          {item.device_address} ch.{item.channel_no}
        </p>
      </div>
      <!-- Device type -->
      {#if item.device_type_label || item.device_type}
        <div>
          <p class="text-xs font-semibold mb-1 text-slate-500 dark:text-slate-400">{t("matter.expose.drawer_device_type")}</p>
          <p class="text-sm text-slate-900 dark:text-slate-100">
            {item.device_type_label || `0x${item.device_type.toString(16).toUpperCase().padStart(4, "0")}`}
          </p>
        </div>
      {/if}
      <!-- Clusters -->
      {#if item.clusters.length > 0}
        <div>
          <p class="text-xs font-semibold mb-1 text-slate-500 dark:text-slate-400">{t("matter.expose.drawer_clusters")}</p>
          <p class="text-sm font-mono text-slate-900 dark:text-slate-100">
            {item.clusters.map((c) => `0x${c.toString(16).toUpperCase().padStart(4, "0")}`).join(" · ")}
          </p>
        </div>
      {/if}
    </div>
    <div class="p-4 border-t border-slate-200 dark:border-slate-700 flex gap-2">
      <Button size="sm" onclick={saveDrawer}>{t("common.save")}</Button>
      <Button size="sm" variant="outline" onclick={closeDrawer}>{t("common.cancel")}</Button>
    </div>
  </aside>
{/if}
