<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { AuditEntry } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    locale: string;
    /** When set, only entries that match this device address are
     *  displayed. Used by the device-detail "Verlauf" tab to scope
     *  the global change history to one device. */
    deviceFilter?: string;
    /** When true, drops the section header (the device-detail page
     *  already provides one) and tightens the outer padding. */
    embedded?: boolean;
  };
  let { locale, deviceFilter, embedded = false }: Props = $props();

  let entries = $state<AuditEntry[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let expandedIdx = $state<Set<number>>(new Set());
  let actionFilter = $state<string>("");
  let searchFilter = $state<string>("");
  let centralFilter = $state<string>("");

  async function load() {
    loading = true;
    loadError = null;
    try {
      entries = await api.listAudit(500);
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }
  onMount(load);

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
      return new Date(iso).toLocaleString(locale === "de" ? "de-DE" : "en-US");
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

  // Distinct action types present in the current page so the filter
  // dropdown only offers values that actually exist.
  const actions = $derived.by(() => {
    const set = new Set<string>();
    for (const e of entries) set.add(e.action);
    return Array.from(set).sort();
  });

  const centrals = $derived.by(() => {
    const set = new Set<string>();
    for (const e of entries) {
      // Empty/undefined central = global/daemon-wide entry; shown under "—".
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
      if (deviceFilter && e.device_address !== deviceFilter) return false;
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
  class={embedded ? "" : "mx-auto max-w-6xl px-6 py-6"}
>
  {#if !embedded}
    <header class="mb-4 flex flex-wrap items-center justify-between gap-3">
      <div>
        <h1 class="text-2xl font-semibold">{t("audit.title")}</h1>
        <p class="text-sm" style="color: var(--ha-secondary-text-color);">
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
      class="w-64 rounded-md border border-slate-300 bg-white px-3 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900"
    />
    <select
      bind:value={actionFilter}
      class="rounded-md border border-slate-300 bg-white px-2 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900"
    >
      <option value="">{t("audit.filter.all")}</option>
      {#each actions as a (a)}
        <option value={a}>{actionLabel(a)}</option>
      {/each}
    </select>
    {#if centrals.length > 0}
      <select
        bind:value={centralFilter}
        class="rounded-md border border-slate-300 bg-white px-2 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900"
        title="CCU"
      >
        <option value="">Alle CCUs</option>
        {#each centrals as c (c)}
          <option value={c}>{c}</option>
        {/each}
        <option value="__global__">— (global)</option>
      </select>
    {/if}
    <span class="text-xs text-[var(--ha-secondary-text-color)]">
      {filteredEntries.length} / {entries.length}
    </span>
  </div>

  {#if loadError}
    <Card class="mb-4 p-3">
      <p class="text-sm text-red-600 dark:text-red-400">{loadError}</p>
    </Card>
  {/if}

  {#if !loading && entries.length === 0}
    <Card class="p-6 text-center text-sm text-[var(--ha-secondary-text-color)]">
      {t("audit.empty")}
    </Card>
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
              <span class="font-mono text-xs text-[var(--ha-secondary-text-color)]">{formatTs(entry.timestamp)}</span>
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
                <span class="text-xs text-[var(--ha-secondary-text-color)]">{entry.paramset}</span>
              {/if}
              {#if entry.peer}
                <span class="text-xs text-[var(--ha-secondary-text-color)]">→ {entry.peer}</span>
              {/if}
              {#if entry.changes && entry.changes.length > 0}
                <span class="ml-auto text-xs text-[var(--ha-secondary-text-color)]">
                  {entry.changes.length}
                  {t("audit.changes")}
                </span>
              {/if}
            </button>
            {#if expanded && entry.changes && entry.changes.length > 0}
              <table class="mt-2 w-full text-left text-xs">
                <thead class="text-[var(--ha-secondary-text-color)]">
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
                      <td class="py-1 pr-2 font-mono text-[var(--ha-secondary-text-color)]">
                        {change.before == null ? "—" : JSON.stringify(change.before)}
                      </td>
                      <td class="py-1 font-mono">
                        {change.after == null ? "—" : JSON.stringify(change.after)}
                      </td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            {/if}
            {#if entry.note}
              <p class="mt-1 text-xs italic text-[var(--ha-secondary-text-color)]">{entry.note}</p>
            {/if}
          </Card>
        </li>
      {/each}
    </ul>
  {/if}
</section>
