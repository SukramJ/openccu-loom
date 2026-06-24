<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { AuditEntry } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
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
  let expandedIdx = $state<Set<number>>(new Set());
  let actionFilter = $state<string>("");
  let searchFilter = $state<string>("");
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
  // action/search/central filters do not trigger a refetch.
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

  function toggle(i: number) {
    const next = new Set(expandedIdx);
    if (next.has(i)) next.delete(i);
    else next.add(i);
    expandedIdx = next;
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

  const filteredEntries = $derived.by(() => {
    const q = searchFilter.trim().toLowerCase();
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
      if (!q) return true;
      const haystack = [
        e.action,
        e.user ?? "",
        e.device_address ?? "",
        e.paramset ?? "",
        e.peer ?? "",
        e.parameter ?? "",
        e.note ?? "",
        e.central ?? "",
      ]
        .join(" ")
        .toLowerCase();
      return haystack.includes(q);
    });
  });
</script>

<section
  class={embedded ? "" : "mx-auto max-w-6xl px-4 py-6 sm:px-6"}
>
  {#if !embedded}
    <header class="mb-4 flex flex-wrap items-center justify-between gap-3">
      <div>
        <h1 class="text-2xl font-semibold">{t("audit.title")}</h1>
        <p class="text-sm text-slate-500 dark:text-slate-400">
          {loading
            ? t("common.loading")
            : t("audit.entries", { count: entries.length })}
        </p>
      </div>
      <Button type="button" variant="outline" size="sm" onclick={() => void load()} disabled={loading}>
        {t("common.reload")}
      </Button>
    </header>
  {/if}

  <div class="mb-4 flex flex-wrap items-center gap-2">
    <input
      type="search"
      placeholder={t("common.search")}
      bind:value={searchFilter}
      class="w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100 sm:w-64"
    />
    <select
      bind:value={actionFilter}
      class="rounded-md border border-slate-300 bg-white px-2 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
    >
      <option value="">{t("audit.filter.all")}</option>
      {#each actions as a (a)}
        <option value={a}>{actionLabel(a)}</option>
      {/each}
    </select>
    {#if centrals.length > 0}
      <select
        bind:value={centralFilter}
        class="rounded-md border border-slate-300 bg-white px-2 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
        title="CCU"
      >
        <option value="">{t("common.all_ccus")}</option>
        {#each centrals as c (c)}
          <option value={c}>{c}</option>
        {/each}
        <option value="__global__">{t("audit.filter.global")}</option>
      </select>
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
  {:else if entries.length === 0}
    <EmptyState message={t("audit.empty")} icon="mdi:history" />
  {:else}
    <ul class="space-y-2">
      {#each filteredEntries as entry, idx (idx)}
        {@const expanded = expandedIdx.has(idx)}
        <li>
          <Card class="p-3">
            <button
              type="button"
              class="flex w-full flex-wrap items-baseline gap-2 text-left text-sm"
              onclick={() => toggle(idx)}
            >
              <span class="font-mono text-xs text-slate-500 dark:text-slate-400">{formatTs(entry.timestamp)}</span>
              <Badge variant="default">{actionLabel(entry.action)}</Badge>
              {#if entry.user}
                <Badge variant="muted">{entry.user}</Badge>
              {/if}
              {#if centrals.length > 0 && entry.central}
                <Badge variant="muted">{entry.central}</Badge>
              {/if}
              {#if entry.device_address}
                <span class="font-mono text-xs">
                  {entry.device_address}{#if entry.channel_no}:{entry.channel_no}{/if}
                </span>
              {/if}
              {#if entry.paramset}
                <span class="text-xs text-slate-500 dark:text-slate-400">{entry.paramset}</span>
              {/if}
              {#if entry.peer}
                <span class="text-xs text-slate-500 dark:text-slate-400">→ {entry.peer}</span>
              {/if}
              {#if entry.changes && entry.changes.length > 0}
                <span class="ml-auto text-xs text-slate-500 dark:text-slate-400">
                  {entry.changes.length}
                  {t("audit.changes")}
                </span>
              {/if}
            </button>
            {#if expanded && entry.changes && entry.changes.length > 0}
              <table class="table-reflow mt-2 w-full text-left text-xs">
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
                      <td class="reflow-title py-1 pr-2 font-mono">{change.parameter}</td>
                      <td class="py-1 pr-2 font-mono text-slate-500 dark:text-slate-400" data-label={t("audit.col.before")}>
                        {change.before == null ? "—" : JSON.stringify(change.before)}
                      </td>
                      <td class="py-1 font-mono" data-label={t("audit.col.after")}>
                        {change.after == null ? "—" : JSON.stringify(change.after)}
                      </td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            {/if}
            {#if entry.note}
              <p class="mt-1 text-xs italic text-slate-500 dark:text-slate-400">{entry.note}</p>
            {/if}
          </Card>
        </li>
      {/each}
    </ul>

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
