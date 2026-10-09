<script lang="ts">
  // The direct-link table, shared by the fleet-wide list and a device's
  // links sub-tab. It follows the CCU WebUI's link list
  // (config/ic_linkpeerlist.cgi, put_tableheader): three captioned column
  // groups Sender | Link | Receiver, each party with its name and serial,
  // and the list grouped by sender or by receiver on demand. A group's
  // header offers to add one more partner to that channel, as the CCU's
  // merged sender / receiver cells do.
  import type { Link } from "$lib/api/types";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import Tabs from "$lib/components/ui/Tabs.svelte";
  import type { DataColumn, DataColumnGroup } from "$lib/components/ui/data-table";
  import { linkHref, newLinkHref, partyLabel, partyModel } from "$lib/links/link-routes";
  import { loadLS, saveLS } from "$lib/utils";
  import { t } from "$lib/i18n";

  type Props = {
    links: Link[];
    /** localStorage scope for sort, filters and grouping. */
    persistKey: string;
    /** Show the CCU column (more than one central configured). */
    showCentral?: boolean;
    /** Edit, delete and add actions; false where the link editor is hidden. */
    editable?: boolean;
    onDelete?: (link: Link) => void;
    emptyMessage: string;
  };

  let {
    links,
    persistKey,
    showCentral = false,
    editable = true,
    onDelete,
    emptyMessage,
  }: Props = $props();

  type Grouping = "none" | "sender" | "receiver";
  function readGrouping(): Grouping {
    const v = loadLS(`${persistKey}:grouping`);
    return v === "sender" || v === "receiver" ? v : "none";
  }
  let grouping = $state<Grouping>(readGrouping());
  $effect(() => saveLS(`${persistKey}:grouping`, grouping));

  const rowKey = (l: Link) => `${l.central_name ?? ""}|${l.sender_address}->${l.receiver_address}`;

  // When the rows are grouped by one party, that party's columns would
  // repeat the group header on every row; they are dropped, as the CCU
  // merges those cells.
  const showSender = $derived(grouping !== "sender");
  const showReceiver = $derived(grouping !== "receiver");

  const columns = $derived([
    ...(showSender
      ? [
          {
            key: "sender",
            label: t("links.col.name"),
            sortable: true,
            title: true,
            filter: "text" as const,
            get: (l: Link) => partyLabel(l, "sender"),
          },
          {
            key: "sender_serial",
            label: t("links.col.serial"),
            sortable: true,
            filter: "text" as const,
            get: (l: Link) => l.sender_address,
            cellClass: "font-mono text-xs",
            headClass: "border-r border-[var(--ha-divider-color)]",
          },
        ]
      : []),
    {
      key: "name",
      label: t("links.col.name"),
      sortable: true,
      filter: "text" as const,
      get: (l: Link) => l.name || "",
      cellClass: "bg-[color-mix(in_srgb,var(--ha-primary-color)_6%,transparent)]",
      headClass: "bg-[color-mix(in_srgb,var(--ha-primary-color)_6%,transparent)]",
    },
    {
      key: "description",
      label: t("links.col.description"),
      sortable: true,
      filter: "text" as const,
      get: (l: Link) => l.description || "",
      cellClass: "bg-[color-mix(in_srgb,var(--ha-primary-color)_6%,transparent)]",
      headClass: "bg-[color-mix(in_srgb,var(--ha-primary-color)_6%,transparent)]",
    },
    {
      key: "actions",
      label: t("links.col.action"),
      cellClass: "reflow-actions bg-[color-mix(in_srgb,var(--ha-primary-color)_6%,transparent)]",
      headClass:
        "border-r border-[var(--ha-divider-color)] bg-[color-mix(in_srgb,var(--ha-primary-color)_6%,transparent)]",
    },
    ...(showReceiver
      ? [
          {
            key: "receiver",
            label: t("links.col.name"),
            sortable: true,
            filter: "text" as const,
            get: (l: Link) => partyLabel(l, "receiver"),
            ...(showSender ? {} : { title: true }),
          },
          {
            key: "receiver_serial",
            label: t("links.col.serial"),
            sortable: true,
            filter: "text" as const,
            get: (l: Link) => l.receiver_address,
            cellClass: "font-mono text-xs",
          },
        ]
      : []),
    ...(showCentral
      ? [
          {
            key: "central",
            label: t("links.col.central"),
            sortable: true,
            get: (l: Link) => l.central_name || "",
          },
        ]
      : []),
  ] satisfies DataColumn<Link>[]);

  const columnGroups = $derived<DataColumnGroup[]>([
    ...(showSender
      ? [{ label: t("links.sender"), span: 2, class: "border-r border-[var(--ha-divider-color)]" }]
      : []),
    {
      label: t("links.editor.link"),
      span: 3,
      class:
        "border-r border-[var(--ha-divider-color)] text-[var(--ha-primary-color)] bg-[color-mix(in_srgb,var(--ha-primary-color)_10%,transparent)]",
    },
    ...(showReceiver ? [{ label: t("links.receiver"), span: 2 }] : []),
    ...(showCentral ? [{ label: "", span: 1 }] : []),
  ]);

  const groupBy = $derived(
    grouping === "sender"
      ? (l: Link) => l.sender_address
      : grouping === "receiver"
        ? (l: Link) => l.receiver_address
        : undefined,
  );
</script>

<div class="mb-3 flex flex-wrap items-center gap-2">
  <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("links.group_by")}</span>
  <Tabs
    variant="segmented"
    active={grouping}
    ariaLabel={t("links.group_by")}
    items={[
      { key: "none", label: t("links.group.none") },
      { key: "sender", label: t("links.group.sender") },
      { key: "receiver", label: t("links.group.receiver") },
    ]}
    onSelect={(key) => (grouping = key as Grouping)}
  />
</div>

<DataTable
  rows={links}
  {columns}
  {columnGroups}
  {rowKey}
  cell={linkCell}
  columnFilters
  {persistKey}
  initialSort={{ key: showSender ? "sender" : "receiver", asc: true }}
  {emptyMessage}
  {groupBy}
  groupHeader={partyGroupHeader}
/>

{#snippet party(link: Link, side: "sender" | "receiver")}
  <span class="block font-medium text-[var(--ha-primary-text-color)]">{partyLabel(link, side)}</span>
  {#if partyModel(link, side)}
    <span class="block text-xs text-[var(--ha-secondary-text-color)]">{partyModel(link, side)}</span>
  {/if}
{/snippet}

{#snippet partyGroupHeader(_key: string, rows: Link[])}
  {@const first = rows[0]}
  {@const side = grouping === "receiver" ? "receiver" : "sender"}
  <div class="flex flex-wrap items-center gap-3">
    <div class="min-w-0 flex-1">
      <span class="text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
        {side === "sender" ? t("links.sender") : t("links.receiver")}
      </span>
      <div class="flex flex-wrap items-baseline gap-x-3">
        <span class="font-semibold text-[var(--ha-primary-text-color)]">{partyLabel(first, side)}</span>
        <span class="font-mono text-xs text-[var(--ha-secondary-text-color)]">
          {side === "sender" ? first.sender_address : first.receiver_address}
        </span>
      </div>
    </div>
    {#if editable}
      <Button
        type="button"
        variant="ghost"
        size="sm"
        onclick={() =>
          (location.hash =
            side === "sender"
              ? newLinkHref({ sender: first.sender_address })
              : newLinkHref({ receiver: first.receiver_address }))}
      >
        <Icon name="mdi:plus" size={16} />
        {side === "sender" ? t("links.add_receiver") : t("links.add_sender")}
      </Button>
    {/if}
  </div>
{/snippet}

{#snippet linkCell(link: Link, col: DataColumn<Link>)}
  {#if col.key === "sender"}
    {@render party(link, "sender")}
  {:else if col.key === "receiver"}
    {@render party(link, "receiver")}
  {:else if col.key === "sender_serial"}
    {link.sender_address}
  {:else if col.key === "receiver_serial"}
    {link.receiver_address}
  {:else if col.key === "name"}
    {link.name || "—"}
  {:else if col.key === "description"}
    {#if link.description}
      {link.description}
    {:else}
      <span class="text-[var(--ha-disabled-text-color)]">{t("links.no_description")}</span>
    {/if}
  {:else if col.key === "central"}
    {#if link.central_name}<Badge variant="muted">{link.central_name}</Badge>{:else}—{/if}
  {:else if col.key === "actions"}
    {#if editable}
      <span class="inline-flex flex-wrap items-center gap-1">
        <a
          href={linkHref(link.sender_address, link.receiver_address)}
          class="inline-flex items-center gap-1 rounded px-1.5 py-1 text-xs font-medium text-brand-600 hover:underline dark:text-brand-400"
        >
          <Icon name="mdi:pencil" />
          {t("links.edit")}
        </a>
        {#if onDelete}
          <button
            type="button"
            class="inline-flex items-center gap-1 rounded px-1.5 py-1 text-xs font-medium text-[var(--ha-error-color)] hover:underline"
            onclick={() => onDelete?.(link)}
          >
            <Icon name="mdi:trash-can" />
            {t("common.delete")}
          </button>
        {/if}
      </span>
    {:else}
      —
    {/if}
  {/if}
{/snippet}
