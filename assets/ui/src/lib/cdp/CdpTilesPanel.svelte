<!--
  Übersicht-Mode panel: one tile per Custom-DP that has a registered
  CDP-side widget; channels not bound to a CDP (or bound to a CDP
  whose widget hasn't shipped yet) fall through to the
  CONTROL-channel-tile panel below as "Sonstige Kanäle". See ADR 0016.
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { api, friendlyError } from "$lib/api/client";
  import type { CustomDPSummary, DeviceDetail } from "$lib/api/types";
  import {
    isOverviewExcluded,
    isStatusOnlyChannelType,
    isVirtualRemoteModel,
  } from "$lib/quickcontrol/domain";
  import ChannelControl from "$lib/control/ChannelControl.svelte";
  import ChannelStatusBadge from "./ChannelStatusBadge.svelte";
  import ChannelTiles from "./ChannelTiles.svelte";
  import VirtualRemoteKeyGrid from "./VirtualRemoteKeyGrid.svelte";
  import { cdpWidgetFor, hasCdpWidget } from "./dispatch";
  import { t } from "$lib/i18n";

  type Props = {
    detail: DeviceDetail;
  };

  let { detail }: Props = $props();

  let cdps = $state<CustomDPSummary[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  // Diagnostic: count seconds since the load started so a stuck
  // request is visible in the UI instead of an indefinite "Lädt…".
  let loadingSince = $state<number>(0);
  let elapsed = $state(0);

  async function load() {
    loading = true;
    error = null;
    loadingSince = Date.now();
    try {
      cdps = await api.listCustomDataPoints(detail.address);
    } catch (err) {
      error = friendlyError(err, t);
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    load();
    const tick = setInterval(() => {
      if (loadingSince > 0) elapsed = Date.now() - loadingSince;
    }, 500);
    return () => clearInterval(tick);
  });

  // CDPs with a registered widget — these render as CDP tiles.
  const renderable = $derived(cdps.filter((c) => hasCdpWidget(c.kind)));

  // CDP channel numbers, used to filter the orphan section. A
  // channel is orphan when (a) it has no CustomDP attached, or (b)
  // its attached CDP has no CDP-side widget shipping yet (so the
  // user still needs a way to reach it).
  const cdpChannelNumbers = $derived(new Set(renderable.map((c) => c.channel_no)));

  let unresolved = $state(new Set<string>());

  function markUnresolved(addr: string) {
    if (unresolved.has(addr)) return;
    unresolved = new Set([...unresolved, addr]);
  }

  // Virtual remotes render as a dedicated key grid instead of 50
  // incoherent per-channel tiles — every KEY channel is excluded from
  // the orphan sections below.
  const isVirtualRemote = $derived(isVirtualRemoteModel(detail.model));

  const orphanChannels = $derived(
    isVirtualRemote
      ? []
      : detail.channels.filter(
          (c) =>
            !isOverviewExcluded(c) &&
            c.address.includes(":") &&
            !cdpChannelNumbers.has(c.number) &&
            (c.data_points_count ?? 0) > 0,
        ),
  );

  // Split orphans into actor channels (full ChannelControl tile) and
  // status-only channels (dense ChannelStatusBadge row). Status channels
  // never accept a setValue write, so rendering them as a control tile
  // shows a disabled clone of the actor UI — far less useful than a
  // compact badge with the last observed value.
  const actorOrphans = $derived(
    orphanChannels.filter((c) => !isStatusOnlyChannelType(c.type)),
  );
  const statusOrphans = $derived(
    orphanChannels.filter((c) => isStatusOnlyChannelType(c.type)),
  );

  // AutoTile coverage (docs/ui/auto-tile-concept.md). Each orphan
  // channel that ships at least one DP renders as its own AutoTile —
  // peers like HmIP-SMO230's four motion channels and HmIP-STE2-PCB's
  // three temperature sensors all deserve equal tiles. The dpc=0
  // filter on `orphanChannels` already drops empty ghost channels
  // (STATE_RESET_RECEIVER, ALARM_COND_SWITCH_TRANSMITTER when wired
  // but unobserved, …) so the tile grid stays clean.
  const autoTileChannels = $derived(orphanChannels);

  // Adaptive column count: a device with one or two tiles (e.g. a wall
  // thermostat or a contact) used only a third of the width on a 3-col
  // grid. Drop to two columns when there is little to show so the tiles
  // fill more of the row.
  const tileGridClass = $derived(
    renderable.length + autoTileChannels.length <= 2
      ? "grid grid-cols-1 gap-3 sm:grid-cols-2"
      : "grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3",
  );

  // --- Sub-device grouping ---
  //
  // When the device declares multiple channel-groups (`has_sub_devices` —
  // strict aiohomematic definition: ≥2 groups, ≥2 of them multi-member),
  // we visually arrange the tiles into per-group sections instead of one
  // flat grid. Singleton-channel groups and ungrouped channels collect in
  // an "Allgemein" section at the top.
  //
  // The data source is `channel.group_no` / `channel.sub_device_name` from
  // the REST channel summary. When `has_sub_devices=false` (most devices),
  // the panel falls back to the legacy flat layout below.
  type GroupBucket = {
    groupNo: number;
    name: string;
    cdps: CustomDPSummary[];
    actorOrphans: typeof orphanChannels;
    statusOrphans: typeof orphanChannels;
  };

  const groups = $derived.by<GroupBucket[]>(() => {
    if (!detail.has_sub_devices) return [];
    const byNo = new Map<number, GroupBucket>();
    for (const ch of detail.channels) {
      const groupNo = ch.group_no ?? 0;
      if (groupNo === 0 || !ch.is_in_multi_group) continue;
      if (!byNo.has(groupNo)) {
        byNo.set(groupNo, {
          groupNo,
          name: ch.sub_device_name || t("cdp.group_n", { n: groupNo }),
          cdps: [],
          actorOrphans: [],
          statusOrphans: [],
        });
      }
    }
    for (const cdp of renderable) {
      const ch = detail.channels.find((c) => c.number === cdp.channel_no);
      const bucket = byNo.get(ch?.group_no ?? 0);
      if (bucket) bucket.cdps.push(cdp);
    }
    for (const ch of actorOrphans) {
      const bucket = byNo.get(ch.group_no ?? 0);
      if (bucket) bucket.actorOrphans.push(ch);
    }
    for (const ch of statusOrphans) {
      const bucket = byNo.get(ch.group_no ?? 0);
      if (bucket) bucket.statusOrphans.push(ch);
    }
    return [...byNo.values()].sort((a, b) => a.groupNo - b.groupNo);
  });

  const groupedChannelKeys = $derived(
    new Set(groups.flatMap((g) => [
      ...g.cdps.map((c) => `cdp:${c.name}:${c.channel_no}`),
      ...g.actorOrphans.map((c) => `orphan:${c.address}`),
      ...g.statusOrphans.map((c) => `orphan:${c.address}`),
    ])),
  );

  // Renderables / orphans that did NOT land in any group — shown in the
  // "Allgemein" section at the top when sub-devices are active, or as the
  // single flat grid when they're not.
  const ungroupedCdps = $derived(
    renderable.filter((c) => !groupedChannelKeys.has(`cdp:${c.name}:${c.channel_no}`)),
  );
  const ungroupedActorOrphans = $derived(
    actorOrphans.filter((c) => !groupedChannelKeys.has(`orphan:${c.address}`)),
  );
  const ungroupedStatusOrphans = $derived(
    statusOrphans.filter((c) => !groupedChannelKeys.has(`orphan:${c.address}`)),
  );

  // Deterministic hue per group — keeps colours stable across reloads and
  // distinct between adjacent groups (golden-angle rotation).
  function groupHue(no: number): number {
    return Math.round((no * 137.508) % 360);
  }
</script>

{#if error}
  <div class="mb-3 rounded-md border border-[var(--ha-error-color)] bg-[var(--ha-card-background-color)] p-2 text-xs text-[var(--ha-error-color)]">
    <div class="flex items-center justify-between gap-3">
      <span>{error}</span>
      <button
        type="button"
        class="min-h-10 rounded-md border border-[var(--ha-divider-color)] px-3 py-2 text-sm"
        onclick={() => load()}
      >
        {t("cdp.retry")}
      </button>
    </div>
  </div>
{:else if loading && elapsed > 5000}
  <div class="mb-3 rounded-md border border-[var(--ha-warning-color)] bg-[var(--ha-card-background-color)] p-2 text-xs text-[var(--ha-secondary-text-color)]">
    <div class="flex items-center justify-between gap-3">
      <span>{t("cdp.panel.loading", { addr: detail.address, n: Math.round(elapsed / 1000) })}</span>
      <button
        type="button"
        class="min-h-10 rounded-md border border-[var(--ha-divider-color)] px-3 py-2 text-sm"
        onclick={() => load()}
      >
        {t("cdp.retry")}
      </button>
    </div>
    {#if elapsed > 8000}
      <p class="mt-2">
        {@html t("cdp.panel.server_unresponsive", { addr: detail.address })}
      </p>
    {/if}
  </div>
{/if}

{#snippet tileGrid(cdps: CustomDPSummary[], actors: typeof orphanChannels)}
  <div class={tileGridClass}>
    {#each cdps as cdp (`${cdp.name}:${cdp.channel_no}`)}
      {@const Widget = cdpWidgetFor(cdp.kind)}
      {@const ch = detail.channels.find((c) => c.number === cdp.channel_no)}
      {@const title = ch?.name || ch?.type_label || `${detail.address}:${cdp.channel_no}`}
      {#if Widget}
        <Widget address={detail.address} {cdp} {title} />
      {/if}
    {/each}
    {#each actors as ch (ch.address)}
      {#if !unresolved.has(ch.address)}
        <ChannelControl
          address={detail.address}
          channel={ch.number}
          title={ch.name ?? ch.type_label ?? ch.address}
          secondary={ch.type_label !== ch.name ? ch.type_label : undefined}
          onUnresolved={() => markUnresolved(ch.address)}
        />
      {/if}
    {/each}
  </div>
{/snippet}

{#snippet statusStripe(channels: typeof orphanChannels)}
  <div class="mt-2 grid grid-cols-1 gap-1 sm:grid-cols-2 xl:grid-cols-3">
    {#each channels as ch (ch.address)}
      <ChannelStatusBadge
        address={detail.address}
        channel={ch.number}
        type={ch.type}
        name={ch.name}
        typeLabel={ch.type_label}
      />
    {/each}
  </div>
{/snippet}

{#if isVirtualRemote}
  <div class="mb-4">
    <VirtualRemoteKeyGrid {detail} />
  </div>
{/if}

{#if detail.has_sub_devices && groups.length > 0}
  <!-- Sub-device layout: per-group sections with a coloured accent + tint.
       Singleton / ungrouped channels collect in an "Allgemein" section
       above so the device root + maintenance tiles stay at the top. -->
  {#if ungroupedCdps.length > 0 || ungroupedActorOrphans.length > 0 || ungroupedStatusOrphans.length > 0}
    <section
      class="mb-4 rounded-lg border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] p-3"
    >
      <h3 class="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
        {t("cdp.panel.general")}
      </h3>
      {#if ungroupedCdps.length > 0 || ungroupedActorOrphans.length > 0}
        {@render tileGrid(ungroupedCdps, ungroupedActorOrphans)}
      {/if}
      {#if ungroupedStatusOrphans.length > 0}
        {@render statusStripe(ungroupedStatusOrphans)}
      {/if}
    </section>
  {/if}

  {#each groups as group (group.groupNo)}
    {@const hue = groupHue(group.groupNo)}
    <section
      class="mb-4 overflow-hidden rounded-lg border bg-[var(--ha-card-background-color)]"
      style:border-left="4px solid hsl({hue} 65% 50%)"
      style:border-top-color="var(--ha-divider-color)"
      style:border-right-color="var(--ha-divider-color)"
      style:border-bottom-color="var(--ha-divider-color)"
    >
      <header
        class="flex items-center gap-2 px-3 py-2 text-xs font-semibold uppercase tracking-wide text-[var(--ha-primary-text-color)]"
        style:background="hsl({hue} 65% 50% / 0.08)"
      >
        <span
          class="inline-block h-2 w-2 rounded-full"
          style:background="hsl({hue} 65% 50%)"
        ></span>
        <span>{group.name}</span>
        <span class="ml-auto text-[var(--ha-secondary-text-color)]">
          {t("cdp.panel.group", { n: group.groupNo })}
        </span>
      </header>
      <div class="p-3">
        {#if group.cdps.length > 0 || group.actorOrphans.length > 0}
          {@render tileGrid(group.cdps, group.actorOrphans)}
        {/if}
        {#if group.statusOrphans.length > 0}
          {@render statusStripe(group.statusOrphans)}
        {/if}
      </div>
    </section>
  {/each}
{:else}
  <!-- Flat layout: every CDP tile + one AutoTile per orphan channel
       that ships at least one DP. Peer channels (HmIP-SMO230's four
       motion channels, HmIP-STE2-PCB's three temperature probes, …)
       all render as siblings in the same grid; the composer's
       gridSpan hint widens readout-heavy tiles to 2 cells. Shared with
       the fleet-wide Overview route via ChannelTiles. -->
  <!-- Pinning is offered here, on the device's own channel list: this is
       where an operator decides a channel is worth quick access. -->
  <ChannelTiles {detail} {cdps} pinnable />
{/if}

{#if !loading && !error && renderable.length === 0 && orphanChannels.length === 0}
  <p class="text-sm text-[var(--ha-secondary-text-color)]">
    {t("cdp.panel.no_controls")}
  </p>
{/if}
