<!--
  Fleet-wide Overview route (roadmap B8). Renders the existing CDP /
  AutoTile dispatch pipeline (see $lib/cdp/ChannelTiles.svelte) across
  every device instead of just one — grouped by room/function/CCU and
  filterable. The auto-tile engine itself is untouched; this route is
  purely a new lens onto the same per-device tile set CdpTilesPanel
  already renders in DeviceDetail.

  Data flow:
    1. The grouping SKELETON is built instantly from `deviceStore`'s
       already-fetched DeviceSummary list (central/rooms/functions are
       on the summary — no per-device fetch needed for that).
    2. A group's actual tiles (channels + CDPs) are lazy-loaded on
       first expand: one `getDevice` + `listCustomDataPoints` call per
       device in that group, bounded to a small concurrency pool so
       a large fleet never fires hundreds of requests on mount.
-->
<script lang="ts">
  import { onMount, untrack } from "svelte";
  import { deviceStore } from "$lib/stores/devices.svelte";
  import { api } from "$lib/api/client";
  import type { CustomDPSummary, DeviceDetail } from "$lib/api/types";
  import ChannelTiles from "$lib/cdp/ChannelTiles.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { t } from "$lib/i18n";
  import {
    buildOverviewGroups,
    distinctCentrals,
    distinctFunctions,
    distinctRooms,
    type DeviceOverviewGroup,
    type OverviewGroupMode,
  } from "$lib/overview/overview-grouping";
  import { overviewPrefs as saved, persistOverviewPrefs } from "$lib/stores/overviewFilters.svelte";

  // Filter/group-mode state is seeded from the persisted module store
  // and synced back to it on every change (mirrors DeviceList.svelte's
  // `deviceListFilters` pattern).
  let groupMode = $state<OverviewGroupMode>(saved.groupMode);
  let search = $state(saved.filters.search);
  let centralFilter = $state(saved.filters.central);
  let roomFilter = $state(saved.filters.room);
  let functionFilter = $state(saved.filters.function);

  $effect(() => {
    // Depend only on the local filter state (read here). The writes to
    // `saved` + persistOverviewPrefs() run untracked: persist reads the
    // whole `overviewPrefs` $state via JSON.stringify, so tracking it
    // would make this effect re-run on its own writes — an
    // effect_update_depth_exceeded loop.
    const gm = groupMode;
    const c = centralFilter;
    const r = roomFilter;
    const f = functionFilter;
    const s = search;
    untrack(() => {
      saved.groupMode = gm;
      saved.filters = { central: c, room: r, function: f, search: s };
      persistOverviewPrefs();
    });
  });

  onMount(() => {
    deviceStore.refresh();
    deviceStore.ensureStream();
  });

  const centrals = $derived(distinctCentrals(deviceStore.items));
  const rooms = $derived(distinctRooms(deviceStore.items, centralFilter || undefined));
  const functions = $derived(distinctFunctions(deviceStore.items, centralFilter || undefined));

  const groups = $derived(
    buildOverviewGroups(deviceStore.items, groupMode, {
      central: centralFilter,
      room: roomFilter,
      function: functionFilter,
      search,
    }),
  );

  function groupLabel(group: DeviceOverviewGroup): string {
    if (groupMode === "central") {
      return group.groupValue || t("overview.unassigned_central");
    }
    const base =
      group.groupValue ||
      (groupMode === "room" ? t("overview.unassigned_room") : t("overview.unassigned_function"));
    // Only disambiguate with the central label once more than one CCU
    // is actually in play — a single-CCU install stays uncluttered.
    return centrals.length > 1 && group.central ? `${base} · ${group.central}` : base;
  }

  // --- Lazy per-group detail + CDP loading ---
  //
  // `deviceStore` only carries DeviceSummary (channels_count, no full
  // channel/CDP data), so rendering a group's tiles needs one
  // `getDevice` + `listCustomDataPoints` round trip per device. A
  // small worker pool bounds concurrency so a big group does not fire
  // its whole membership at once.
  const CONCURRENCY = 4;

  let expandedKeys = $state<Set<string>>(new Set());
  let loadingKeys = $state<Set<string>>(new Set());
  let groupErrors = $state<Record<string, string>>({});
  let detailsByAddress = $state<Record<string, DeviceDetail>>({});
  let cdpsByAddress = $state<Record<string, CustomDPSummary[]>>({});

  async function loadGroupTiles(group: DeviceOverviewGroup) {
    const pending = group.devices.filter((d) => !(d.address in detailsByAddress));
    if (pending.length === 0) return;

    loadingKeys = new Set([...loadingKeys, group.key]);
    if (group.key in groupErrors) {
      const next = { ...groupErrors };
      delete next[group.key];
      groupErrors = next;
    }

    let firstError: string | null = null;
    let cursor = 0;
    async function worker() {
      while (cursor < pending.length) {
        const device = pending[cursor++];
        try {
          const [detail, cdps] = await Promise.all([
            api.getDevice(device.address),
            api.listCustomDataPoints(device.address),
          ]);
          detailsByAddress = { ...detailsByAddress, [device.address]: detail };
          cdpsByAddress = { ...cdpsByAddress, [device.address]: cdps };
        } catch (err) {
          firstError = err instanceof Error ? err.message : String(err);
        }
      }
    }
    await Promise.all(
      Array.from({ length: Math.min(CONCURRENCY, pending.length) }, () => worker()),
    );

    loadingKeys = new Set([...loadingKeys].filter((k) => k !== group.key));
    if (firstError) {
      groupErrors = { ...groupErrors, [group.key]: firstError };
    }
  }

  function toggleGroup(group: DeviceOverviewGroup) {
    const next = new Set(expandedKeys);
    if (next.has(group.key)) {
      next.delete(group.key);
    } else {
      next.add(group.key);
      void loadGroupTiles(group);
    }
    expandedKeys = next;
  }

  // Auto-expand the first group once the grouped skeleton is available
  // so the route shows live tiles on first paint rather than an
  // all-collapsed wall of headers. Runs once per group-set change
  // (e.g. after switching group mode) via the `groups` identity guard.
  let autoExpandedFor: OverviewGroupMode | null = null;
  $effect(() => {
    if (groups.length === 0) return;
    if (autoExpandedFor === groupMode) return;
    autoExpandedFor = groupMode;
    const first = groups[0];
    expandedKeys = new Set([first.key]);
    // untrack: loadGroupTiles' synchronous prelude reads and mutates
    // loadingKeys/groupErrors/detailsByAddress. Without untrack this
    // effect would register those as dependencies and re-run on its own
    // writes — an effect_update_depth_exceeded loop.
    untrack(() => void loadGroupTiles(first));
  });
</script>

<section class="w-full px-4 py-8 sm:px-6">
  <PageHeader title={t("overview.title")} subtitle={t("overview.subtitle")}>
    {#snippet actions()}
      <div class="flex w-full flex-wrap items-center gap-2 sm:w-auto sm:gap-3">
        <input
          type="search"
          placeholder={t("overview.search_placeholder")}
          bind:value={search}
          class="w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-base shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500 sm:w-64 sm:text-sm dark:border-slate-700 dark:bg-slate-900"
        />
        {#if centrals.length > 1}
          <select
            bind:value={centralFilter}
            class="rounded-md border border-slate-300 bg-white px-2 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900"
            title={t("overview.filter.central_title")}
          >
            <option value="">{t("common.all_ccus")}</option>
            {#each centrals as c (c)}
              <option value={c}>{c}</option>
            {/each}
          </select>
        {/if}
        {#if rooms.length > 0}
          <select
            bind:value={roomFilter}
            class="rounded-md border border-slate-300 bg-white px-2 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900"
            title={t("overview.filter.room_title")}
          >
            <option value="">{t("devicelist.all_rooms")}</option>
            {#each rooms as r (r)}
              <option value={r}>{r}</option>
            {/each}
          </select>
        {/if}
        {#if functions.length > 0}
          <select
            bind:value={functionFilter}
            class="rounded-md border border-slate-300 bg-white px-2 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900"
            title={t("overview.filter.function_title")}
          >
            <option value="">{t("overview.filter.all_functions")}</option>
            {#each functions as f (f)}
              <option value={f}>{f}</option>
            {/each}
          </select>
        {/if}
        <div
          class="ml-auto inline-flex overflow-hidden rounded-md border border-slate-300 dark:border-slate-700"
          role="group"
          aria-label={t("overview.group_by")}
        >
          {#each [
            { key: "room", label: t("overview.group_mode.room") },
            { key: "function", label: t("overview.group_mode.function") },
            ...(centrals.length > 1 ? [{ key: "central", label: t("overview.group_mode.central") }] : []),
          ] as mode (mode.key)}
            <button
              type="button"
              class="px-3 py-2 text-sm transition {groupMode === mode.key
                ? 'bg-brand-50 text-brand-900 dark:bg-brand-900/30 dark:text-brand-100'
                : 'text-slate-600 hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800'}"
              aria-pressed={groupMode === mode.key}
              onclick={() => (groupMode = mode.key as OverviewGroupMode)}
            >
              {mode.label}
            </button>
          {/each}
        </div>
      </div>
    {/snippet}
  </PageHeader>

  {#if deviceStore.error}
    <div class="mb-4">
      <ErrorState
        message={t("overview.load_error", { error: deviceStore.error })}
        onRetry={() => deviceStore.refresh()}
      />
    </div>
  {/if}

  {#if deviceStore.loading && deviceStore.items.length === 0}
    <LoadingState />
  {:else if deviceStore.items.length === 0}
    <EmptyState message={t("overview.empty")} />
  {:else if groups.length === 0}
    <EmptyState message={t("overview.empty_filtered")} />
  {:else}
    {#each groups as group (group.key)}
      {@const isExpanded = expandedKeys.has(group.key)}
      {@const isLoading = loadingKeys.has(group.key)}
      {@const groupError = groupErrors[group.key]}
      <Card class="mb-4 overflow-hidden">
        <button
          type="button"
          class="flex w-full items-center gap-2 px-4 py-3 text-left"
          aria-expanded={isExpanded}
          aria-label={isExpanded ? t("overview.collapse") : t("overview.expand")}
          onclick={() => toggleGroup(group)}
        >
          <Icon name={isExpanded ? "mdi:chevron-down" : "mdi:chevron-right"} size={18} />
          <h2 class="flex-1 text-sm font-semibold text-slate-900 dark:text-white">
            {groupLabel(group)}
          </h2>
          <Badge variant="muted">{t("overview.group.count", { count: group.devices.length })}</Badge>
        </button>

        {#if isExpanded}
          <div class="border-t border-slate-200 p-4 dark:border-slate-800">
            {#if isLoading}
              <LoadingState message={t("overview.group.loading")} />
            {:else if groupError}
              <ErrorState
                message={t("overview.group.error", { error: groupError })}
                onRetry={() => loadGroupTiles(group)}
              />
            {:else}
              {@const details = group.devices
                .map((d) => detailsByAddress[d.address])
                .filter((d): d is DeviceDetail => Boolean(d))}
              {#if details.length === 0}
                <EmptyState message={t("overview.group.empty")} />
              {:else}
                <div class="space-y-4">
                  {#each details as detail (detail.address)}
                    <ChannelTiles {detail} cdps={cdpsByAddress[detail.address] ?? []} />
                  {/each}
                </div>
              {/if}
            {/if}
          </div>
        {/if}
      </Card>
    {/each}
  {/if}
</section>
