<!--
  Read-only heating-groups overview (GR01, roadmap "Operations &
  multi-CCU"). Groups (HmIP / BidCos) are read from the CCU's
  groups.gson via `CCU.getHeatingGroupList` — this view only mirrors
  the current roster. Create/edit/delete runs through the CCU jpages
  proxy (ADR 0055) and is a separate, not-yet-built surface; nothing
  on this page mutates.
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { GroupCentralEntry } from "$lib/api/types";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { t } from "$lib/i18n";
  import { loadLS, saveLS } from "$lib/utils";

  let entries = $state<GroupCentralEntry[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let centralFilter = $state(loadLS("groups:central"));
  $effect(() => saveLS("groups:central", centralFilter));

  async function load() {
    loading = true;
    loadError = null;
    try {
      entries = await api.getGroups();
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  // Deterministic ordering — a central's card grid should not reshuffle
  // between renders just because the REST response happened to change
  // entry order.
  const centrals = $derived(
    [...entries]
      .map((e) => e.central)
      .sort((a, b) => a.localeCompare(b, undefined, { sensitivity: "base" })),
  );

  const filteredEntries = $derived(
    centralFilter ? entries.filter((e) => e.central === centralFilter) : entries,
  );

  const totalGroups = $derived(
    entries.reduce((sum, e) => sum + e.groups.length, 0),
  );
</script>

<svelte:head>
  <title>{t("page.title.groups")}</title>
</svelte:head>

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  <PageHeader
    title={t("groups.title")}
    subtitle={loading ? t("common.loading") : t("groups.count", { count: totalGroups })}
  >
    {#snippet actions()}
      {#if centrals.length > 1}
        <select
          bind:value={centralFilter}
          class="rounded-md border border-slate-300 bg-white px-2 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
          title="CCU"
        >
          <option value="">{t("common.all_ccus")}</option>
          {#each centrals as c (c)}
            <option value={c}>{c}</option>
          {/each}
        </select>
      {/if}
    {/snippet}
  </PageHeader>

  {#if loadError}
    <ErrorState message={loadError} onRetry={load} class="mb-4" />
  {/if}

  {#if loading}
    <LoadingState />
  {:else if totalGroups === 0}
    <EmptyState
      message={t("groups.empty")}
      description={t("groups.empty.description")}
      icon="mdi:home-group"
    />
  {:else}
    <div class="flex flex-col gap-6">
      {#each filteredEntries as entry (entry.central)}
        {#if entry.groups.length > 0}
          <div class="flex flex-col gap-3">
            {#if centrals.length > 1}
              <h2
                class="text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]"
              >
                {entry.central}
              </h2>
            {/if}
            <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
              {#each entry.groups as g (g.id)}
                <Card class="flex flex-col gap-3 p-4">
                  <div class="flex items-start justify-between gap-2">
                    <h3
                      class="min-w-0 truncate text-base font-semibold text-[var(--ha-primary-text-color)]"
                    >
                      {g.name}
                    </h3>
                    {#if g.forbid_single_operation}
                      <Badge variant="muted">{t("groups.operate_only_via_group")}</Badge>
                    {/if}
                  </div>

                  <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm">
                    <dt class="text-[var(--ha-secondary-text-color)]">{t("groups.field.id")}</dt>
                    <dd class="tabular-nums text-[var(--ha-primary-text-color)]">{g.id}</dd>

                    <dt class="text-[var(--ha-secondary-text-color)]">{t("groups.type")}</dt>
                    <dd class="min-w-0 truncate text-[var(--ha-primary-text-color)]">
                      {g.type_label || g.type_id}
                    </dd>

                    {#if g.group_device_name}
                      <dt class="text-[var(--ha-secondary-text-color)]">
                        {t("groups.field.group_device_name")}
                      </dt>
                      <dd class="min-w-0 truncate text-[var(--ha-primary-text-color)]">
                        {g.group_device_name}
                      </dd>
                    {/if}
                  </dl>

                  <div class="flex flex-col gap-1">
                    <span class="text-xs text-[var(--ha-secondary-text-color)]">
                      {t("groups.members", { count: g.members.length })}
                    </span>
                    {#if g.members.length === 0}
                      <span class="text-xs text-[var(--ha-secondary-text-color)]">
                        {t("groups.members.empty")}
                      </span>
                    {:else}
                      <ul class="flex flex-col gap-0.5">
                        {#each g.members as m (m.address)}
                          <li class="font-mono text-xs text-[var(--ha-primary-text-color)]">
                            {m.address}
                            {#if m.type_id}
                              <span class="text-[var(--ha-secondary-text-color)]">
                                ({m.type_id})
                              </span>
                            {/if}
                          </li>
                        {/each}
                      </ul>
                    {/if}
                  </div>
                </Card>
              {/each}
            </div>
          </div>
        {/if}
      {/each}
    </div>
  {/if}
</section>
