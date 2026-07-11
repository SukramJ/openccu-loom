<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { AuditEntry } from "$lib/api/types";
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
  import { prefs } from "$lib/stores/preferences.svelte";

  type Props = {
    /** When set, only entries that match this device address are
     *  displayed. Used by the device-detail "Verlauf" tab to scope
     *  the global change history to one device. */
    deviceFilter?: string;
    /** When true, drops the section header (the device-detail page
     *  already provides one) and tightens the outer padding. */
    embedded?: boolean;
  };
  let { deviceFilter, embedded = false }: Props = $props();

  let entries = $state<AuditEntry[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  // Expanded rows keyed by a stable composite string.
  let expandedRows = $state<Set<string>>(new Set());
  let actionFilter = $state<string>("");
  let centralFilter = $state<string>("");

  // Server-side filters (durable audit path): date range + pagination.
  // `since`/`until` are bound to datetime-local inputs and converted to
  // RFC3339 for the API; offset = page * pageSize walks the full history.
  const PAGE_SIZE = 200;
  let sinceLocal = $state<string>("");
  let untilLocal = $state<string>("");
  let page = $state(0);

  // datetime-local has no timezone; interpret it as local time and emit
  // an ISO/RFC3339 instant. Empty input → undefined (no bound).
  function toRFC3339(local: string): string | undefined {
    if (!local) return undefined;
    const d = new Date(local);
    return isNaN(d.getTime()) ? undefined : d.toISOString();
  }

  async function load() {
    loading = true;
    loadError = null;
    try {
      entries = await api.listAudit({
        limit: PAGE_SIZE,
        offset: page * PAGE_SIZE,
        device: deviceFilter,
        since: toRFC3339(sinceLocal),
        until: toRFC3339(untilLocal),
      });
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }
  onMount(load);

  // Reload whenever the server-side filters or page change. The date
  // inputs and pagination buttons mutate these; client-side
  // action/central filters do not trigger a refetch.
  let firstRun = true;
  $effect(() => {
    // Track the server-side inputs.
    void [sinceLocal, untilLocal, page];
    if (firstRun) {
      firstRun = false;
      return;
    }
    void load();
  });

  function applyDateFilter() {
    page = 0;
    // $effect picks up the page reset / date change and reloads.
  }
  function nextPage() {
    if (entries.length === PAGE_SIZE) page += 1;
  }
  function prevPage() {
    if (page > 0) page -= 1;
  }

  const downloadUrl = $derived(
    api.auditDownloadUrl({
      device: deviceFilter,
      since: toRFC3339(sinceLocal),
      until: toRFC3339(untilLocal),
    }),
  );

  function actionLabel(action: string): string {
    const known = new Set([
      "paramset_write",
      "link_paramset_write",
      "link_add",
      "link_remove",
      "schedule_write",
      "active_profile",
      "data_point_write",
    ]);
    return known.has(action) ? t(`audit.action.${action}`) : action;
  }

  function formatTs(iso: string): string {
    try {
      return new Date(iso).toLocaleString(prefs.locale === "de" ? "de-DE" : "en-US");
    } catch {
      return iso;
    }
  }

  function rowKey(e: AuditEntry): string {
    return e.timestamp + "|" + e.action + "|" + (e.device_address ?? "") + "|" + (e.user ?? "");
  }

  function toggleExpand(e: AuditEntry) {
    const key = rowKey(e);
    const next = new Set(expandedRows);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    expandedRows = next;
  }

  const actions = $derived.by(() => {
    const set = new Set<string>();
    for (const e of entries) set.add(e.action);
    return Array.from(set).sort();
  });

  const centrals = $derived.by(() => {
    const set = new Set<string>();
    for (const e of entries) {
      if (e.central) set.add(e.central);
    }
    return Array.from(set).sort((a, b) =>
      a.localeCompare(b, undefined, { sensitivity: "base" }),
    );
  });

  // Client-side action/central filter applied before DataTable search.
  const filteredEntries = $derived.by(() => {
    return entries.filter((e) => {
      if (actionFilter && e.action !== actionFilter) return false;
      // Device scoping is applied server-side (prefix match) via the
      // `device` query param, so no client-side device filter here.
      // centralFilter="" means show all (including global entries).
      // centralFilter="__global__" shows only global (empty-central) entries.
      // Otherwise filter to the selected central (non-empty entries only).
      if (centralFilter === "__global__") {
        if (e.central) return false;
      } else if (centralFilter && e.central !== centralFilter) {
        return false;
      }
      return true;
    });
  });

  // Columns for the outer DataTable. The `get()` functions provide both
  // sort values and search text used by DataTable's built-in search.
  // Reuse existing audit.col.* keys for the inner changes sub-table.
  const columns: DataColumn<AuditEntry>[] = $derived([
    {
      key: "time",
      label: t("audit.col.time"),
      sortable: true,
      title: true,
      get: (e) => e.timestamp,
    },
    {
      key: "action",
      label: t("audit.col.action"),
      sortable: true,
      get: (e) => actionLabel(e.action),
    },
    {
      key: "user",
      label: t("audit.col.user"),
      sortable: true,
      get: (e) => e.user ?? "",
    },
    {
      key: "target",
      label: t("audit.col.target"),
      sortable: true,
      get: (e) =>
        [e.device_address, e.paramset, e.peer, e.parameter]
          .filter(Boolean)
          .join(" "),
    },
    {
      key: "changes",
      label: t("audit.col.changes"),
      sortable: true,
      align: "right",
      get: (e) => e.changes?.length ?? 0,
    },
  ]);
</script>

<section
  class={embedded ? "" : "mx-auto max-w-6xl px-4 py-6 sm:px-6"}
>
  {#if !embedded}
    <PageHeader
      title={t("audit.title")}
      subtitle={loading ? t("common.loading") : t("audit.entries", { count: entries.length })}
    >
      {#snippet actions()}
        <Button type="button" variant="outline" size="sm" onclick={() => void load()} disabled={loading}>
          {t("common.reload")}
        </Button>
      {/snippet}
    </PageHeader>
  {/if}

  <!-- External filters: action, central, date range, export. DataTable
       provides the free-text search box on top of these filters. -->
  <div class="mb-4 flex flex-wrap items-center gap-2">
    <Select
      class="w-auto"
      bind:value={actionFilter}
      options={[
        { value: "", label: t("audit.filter.all") },
        ...actions.map((a) => ({ value: a, label: actionLabel(a) })),
      ]}
    />
    {#if centrals.length > 0}
      <Select
        class="w-auto"
        bind:value={centralFilter}
        options={[
          { value: "", label: t("common.all_ccus") },
          ...centrals.map((c) => ({ value: c, label: c })),
          { value: "__global__", label: t("audit.filter.global") },
        ]}
      />
    {/if}
    <label class="flex items-center gap-1 text-xs text-slate-500 dark:text-slate-400">
      {t("audit.from")}
      <input
        type="datetime-local"
        bind:value={sinceLocal}
        onchange={applyDateFilter}
        class="rounded-md border border-slate-300 bg-white px-2 py-1.5 text-xs shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
      />
    </label>
    <label class="flex items-center gap-1 text-xs text-slate-500 dark:text-slate-400">
      {t("audit.to")}
      <input
        type="datetime-local"
        bind:value={untilLocal}
        onchange={applyDateFilter}
        class="rounded-md border border-slate-300 bg-white px-2 py-1.5 text-xs shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
      />
    </label>
    <a
      href={downloadUrl}
      class="rounded-md border border-slate-300 px-3 py-1.5 text-xs shadow-sm hover:bg-slate-50 dark:border-slate-700 dark:hover:bg-slate-800"
      download="audit-log.csv"
    >
      {t("audit.export_csv")}
    </a>
    <span class="text-xs text-slate-500 dark:text-slate-400">
      {filteredEntries.length} / {entries.length}
    </span>
  </div>

  {#if loadError}
    <ErrorState message={loadError} onRetry={load} class="mb-4" />
  {/if}

  {#if loading}
    <LoadingState />
  {:else}
    <Card class="p-4">
      <DataTable
        rows={filteredEntries}
        {columns}
        rowKey={(e) => rowKey(e)}
        search
        searchPlaceholder={t("common.search")}
        persistKey="audit-log"
        initialSort={{ key: "time", asc: false }}
        emptyMessage={t("audit.empty")}
        emptyDescription={t("audit.empty.description")}
        emptyIcon="mdi:history"
      >
        {#snippet cell(entry, col)}
          {#if col.key === "time"}
            <button
              type="button"
              class="flex items-center gap-1 text-left text-xs text-slate-500 dark:text-slate-400 hover:text-[var(--ha-primary-text-color)]"
              onclick={() => toggleExpand(entry)}
              title={entry.changes && entry.changes.length > 0 ? t("audit.changes") : undefined}
            >
              <span class="w-3 text-[10px]" aria-hidden="true">
                {#if entry.changes && entry.changes.length > 0}
                  {expandedRows.has(rowKey(entry)) ? "▼" : "▶"}
                {:else}
                  &nbsp;
                {/if}
              </span>
              {formatTs(entry.timestamp)}
            </button>
          {:else if col.key === "action"}
            <Badge variant="default">{actionLabel(entry.action)}</Badge>
            {#if entry.note}
              <span class="block text-xs italic text-slate-500 dark:text-slate-400">{entry.note}</span>
            {/if}
          {:else if col.key === "user"}
            {#if entry.user}
              <Badge variant="muted">{entry.user}</Badge>
            {/if}
            {#if centrals.length > 0 && entry.central}
              <Badge variant="muted">{entry.central}</Badge>
            {/if}
          {:else if col.key === "target"}
            {#if entry.device_address}
              <span class="font-mono text-xs">
                {entry.device_address}{#if entry.channel_no}:{entry.channel_no}{/if}
              </span>
            {/if}
            {#if entry.paramset}
              <span class="block text-xs text-slate-500 dark:text-slate-400">{entry.paramset}</span>
            {/if}
            {#if entry.peer}
              <span class="block text-xs text-slate-500 dark:text-slate-400">→ {entry.peer}</span>
            {/if}
          {:else if col.key === "changes"}
            {#if entry.changes && entry.changes.length > 0}
              <span class="text-xs text-slate-500 dark:text-slate-400">
                {entry.changes.length}
              </span>
              {#if expandedRows.has(rowKey(entry))}
                <div class="mt-2">
                  <table class="w-full text-left text-xs">
                    <thead class="text-slate-500 dark:text-slate-400">
                      <tr>
                        <th class="py-1 pr-2">{t("audit.col.parameter")}</th>
                        <th class="py-1 pr-2">{t("audit.col.before")}</th>
                        <th class="py-1">{t("audit.col.after")}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {#each entry.changes as change (change.parameter)}
                        <tr class="border-t border-slate-100 dark:border-slate-800">
                          <td class="py-1 pr-2 font-mono">{change.parameter}</td>
                          <td class="py-1 pr-2 font-mono text-slate-500 dark:text-slate-400">
                            {change.before == null ? "—" : JSON.stringify(change.before)}
                          </td>
                          <td class="py-1 font-mono">
                            {change.after == null ? "—" : JSON.stringify(change.after)}
                          </td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                </div>
              {/if}
            {:else}
              <span class="text-slate-400 dark:text-slate-500">—</span>
            {/if}
          {/if}
        {/snippet}
      </DataTable>
    </Card>

    <div class="mt-4 flex items-center justify-center gap-3">
      <Button
        type="button"
        variant="outline"
        size="sm"
        onclick={prevPage}
        disabled={page === 0 || loading}
      >
        ← {t("audit.prev")}
      </Button>
      <span class="text-xs text-slate-500 dark:text-slate-400">
        {t("audit.page", { page: page + 1 })}
      </span>
      <Button
        type="button"
        variant="outline"
        size="sm"
        onclick={nextPage}
        disabled={entries.length < PAGE_SIZE || loading}
      >
        {t("audit.next")} →
      </Button>
    </div>
  {/if}
</section>
