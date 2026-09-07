<script lang="ts">
  import type { ChannelSummary } from "$lib/api/types";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import { t } from "$lib/i18n";
  import {
    roleOf,
    roleLabel,
    isVirtualChannel,
    isWeekProfileChannel,
  } from "$lib/channel/channel-roles";

  // ChannelTable — the device page's channel selector. It replaced a chip
  // strip that could only show a name and a data-point count: a device with
  // thirty channels was a wall of chips with no way to sort, filter, or see
  // which channel can take part in a direct link. Selecting a row opens the
  // channel's editor below the table; the parent owns that selection so a
  // deep link (#/devices/<addr>/channels/<n>) still drives it.
  //
  // `compact` drops the columns a nested listing has no room for — the device
  // list's expandable channels reuse the table read-only in that mode.
  let {
    channels,
    selected,
    linkCounts,
    onSelect,
    compact = false,
  }: {
    channels: ChannelSummary[];
    selected: number | null;
    // Link count per channel address. Loaded lazily by the parent: a missing
    // entry renders "—" rather than a zero, because "not loaded" and "no
    // links" are different answers.
    linkCounts?: Map<string, number>;
    onSelect: (ch: ChannelSummary) => void;
    compact?: boolean;
  } = $props();

  const columns = $derived.by<DataColumn<ChannelSummary>[]>(() => {
    const cols: DataColumn<ChannelSummary>[] = [
      {
        key: "number",
        label: t("device.channels.col.number"),
        sortable: true,
        numeric: true,
        get: (ch) => ch.number,
      },
      {
        key: "name",
        label: t("device.channels.col.name"),
        sortable: true,
        title: true,
        get: (ch) => ch.name ?? "",
      },
      {
        key: "description",
        label: t("device.channels.col.description"),
        sortable: true,
        get: (ch) => ch.type_label || ch.type || "",
      },
    ];
    if (!compact) {
      cols.push({
        key: "type",
        label: t("device.channels.col.type"),
        sortable: true,
        cellClass: "font-mono text-xs",
        get: (ch) => ch.type ?? "",
      });
    }
    cols.push({
      key: "role",
      label: t("device.channels.col.role"),
      sortable: true,
      get: (ch) => roleLabel(roleOf(ch)),
    });
    cols.push({
      key: "datapoints",
      label: t("device.channels.col.datapoints"),
      sortable: true,
      numeric: true,
      get: (ch) => ch.data_points_count,
    });
    if (!compact) {
      cols.push({
        key: "links",
        label: t("device.channels.col.links"),
        sortable: true,
        numeric: true,
        get: (ch) => linkCounts?.get(ch.address) ?? null,
      });
      cols.push({
        key: "status",
        label: t("device.channels.col.status"),
        get: (ch) => statusChips(ch).join(" "),
      });
    }
    return cols;
  });

  // The status column is a set of chips rather than one value: a channel can
  // be hidden AND locked AND part of a group at once.
  function statusChips(ch: ChannelSummary): string[] {
    const chips: string[] = [];
    if (ch.hidden) chips.push(t("device.channel.chip.hidden"));
    if (ch.locked) chips.push(t("device.channel.chip.locked"));
    if (isVirtualChannel(ch.number)) chips.push(t("device.channel.chip.virtual"));
    if (isWeekProfileChannel(ch.type)) chips.push(t("device.channel.chip.week_profile"));
    if (ch.group_no) chips.push(t("device.channel.chip.group", { n: ch.group_no }));
    return chips;
  }

  const selectedAddress = $derived(
    channels.find((ch) => ch.number === selected)?.address ?? null,
  );
</script>

{#snippet channelCell(ch: ChannelSummary, col: DataColumn<ChannelSummary>)}
  {#if col.key === "status"}
    {@const chips = statusChips(ch)}
    {#if chips.length === 0}
      <span class="text-[var(--ha-secondary-text-color)]">—</span>
    {:else}
      <span class="flex flex-wrap gap-1">
        {#each chips as chip (chip)}
          <Badge variant="muted">{chip}</Badge>
        {/each}
      </span>
    {/if}
  {:else if col.key === "links"}
    {@const count = linkCounts?.get(ch.address)}
    <span class:text-[var(--ha-secondary-text-color)]={count === undefined}>
      {count ?? "—"}
    </span>
  {:else if col.key === "name"}
    <span class="font-medium">{ch.name?.trim() || ""}</span>
    <span class="block font-mono text-xs text-[var(--ha-secondary-text-color)]">
      {ch.address}
    </span>
  {:else}
    {col.get?.(ch) || "—"}
  {/if}
{/snippet}

<DataTable
  rows={channels}
  {columns}
  rowKey={(ch) => ch.address}
  search={!compact}
  searchPlaceholder={t("device.channels.search")}
  initialSort={{ key: "number", asc: true }}
  emptyMessage={t("device.no_channels")}
  cell={channelCell}
  onRowClick={onSelect}
  selectedKey={selectedAddress}
/>
