<!--
  Global direct-links overview (V01). Aggregates every direct link
  (channel-to-channel peering) across all configured centrals into one
  searchable list — the CCU WebUI's cross-device link view. This surface
  is read-only; creating, editing and deleting a link happens on the
  owning device's detail page (its Links section), which each row links
  to.

  When the surface profile hides that editor the listing stays and the
  rows stop linking — see the `opens` relation in
  notes/concepts/ui-surface-profiles.md.
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { Link } from "$lib/api/types";
  import { surfacesStore } from "$lib/stores/surfaces.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import PageShell from "$lib/components/ui/PageShell.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import Select from "$lib/components/ui/Select.svelte";
  import { t } from "$lib/i18n";
  import { loadLS, saveLS } from "$lib/utils";

  type Props = { locale: string };
  let { locale }: Props = $props();

  let links = $state<Link[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let centralFilter = $state(loadLS("links:central"));
  let search = $state("");
  $effect(() => saveLS("links:central", centralFilter));

  async function load() {
    loading = true;
    loadError = null;
    try {
      // Fetch the full cross-central roster once; the central dropdown
      // filters the loaded list client-side so switching is instant.
      links = await api.listAllLinks(undefined, locale);
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  function deviceOf(channelAddress: string): string {
    const i = channelAddress.lastIndexOf(":");
    return i === -1 ? channelAddress : channelAddress.slice(0, i);
  }

  function partyName(link: Link, side: "sender" | "receiver"): string {
    if (side === "sender") {
      return (
        link.sender_device_name ||
        link.sender_channel_name ||
        link.sender_channel_type_label ||
        link.sender_address
      );
    }
    return (
      link.receiver_device_name ||
      link.receiver_channel_name ||
      link.receiver_channel_type_label ||
      link.receiver_address
    );
  }

  const centrals = $derived(
    [...new Set(links.map((l) => l.central_name).filter(Boolean) as string[])].sort(
      (a, b) => a.localeCompare(b, undefined, { sensitivity: "base" }),
    ),
  );

  function matches(link: Link, q: string): boolean {
    if (!q) return true;
    const needle = q.toLowerCase();
    return [
      link.sender_address,
      link.receiver_address,
      link.name,
      link.description,
      link.sender_device_name,
      link.receiver_device_name,
      link.sender_channel_type_label,
      link.receiver_channel_type_label,
      link.central_name,
      link.interface_id,
    ]
      .filter(Boolean)
      .some((v) => (v as string).toLowerCase().includes(needle));
  }

  // Whether the device's own link editor exists in this profile. Hidden
  // means the rows lose their "edit on device" action, not that the
  // cross-device listing loses its value.
  const linkable = $derived(surfacesStore.opensVisible("nav.links"));

  const filtered = $derived(
    links
      .filter((l) => !centralFilter || l.central_name === centralFilter)
      .filter((l) => matches(l, search)),
  );

  // Column set for the shared DataTable. Sender/receiver read through
  // partyName() so search, sort and the on-screen text agree — the operator
  // sorts and filters on exactly what they see. The central column only
  // earns its place with more than one CCU configured, mirroring the badge
  // that used to appear conditionally in the card layout; the actions
  // column likewise only appears while the device-side link editor exists.
  const columns = $derived([
    {
      key: "sender",
      label: t("links.col.sender"),
      sortable: true,
      title: true,
      get: (l: Link) => partyName(l, "sender"),
    },
    {
      key: "receiver",
      label: t("links.col.receiver"),
      sortable: true,
      get: (l: Link) => partyName(l, "receiver"),
    },
    {
      key: "name",
      label: t("links.col.name"),
      sortable: true,
      get: (l: Link) => l.name || "",
    },
    {
      key: "description",
      label: t("links.col.description"),
      sortable: true,
      get: (l: Link) => l.description || "",
    },
    {
      key: "interface",
      label: t("links.col.interface"),
      sortable: true,
      get: (l: Link) => l.interface_id || "",
    },
    ...(centrals.length > 1
      ? [
          {
            key: "central",
            label: t("links.col.central"),
            sortable: true,
            get: (l: Link) => l.central_name || "",
          },
        ]
      : []),
    ...(linkable
      ? [
          {
            key: "actions",
            label: t("links.col.actions"),
            align: "right" as const,
            cellClass: "reflow-actions",
          },
        ]
      : []),
  ] satisfies DataColumn<Link>[]);
</script>

<svelte:head>
  <title>{t("page.title.links")}</title>
</svelte:head>

<PageShell>
  <PageHeader
    title={t("links.title")}
    subtitle={loading ? t("common.loading") : t("links.count", { count: filtered.length })}
  >
    {#snippet actions()}
      {#if centrals.length > 1}
        <Select
          class="w-auto"
          bind:value={centralFilter}
          ariaLabel={t("filter.central_aria")}
          options={[
            { value: "", label: t("common.all_ccus") },
            ...centrals.map((c) => ({ value: c, label: c })),
          ]}
        />
      {/if}
    {/snippet}
  </PageHeader>

  {#if loadError}
    <ErrorState message={loadError} onRetry={load} class="mb-4" />
  {/if}

  {#if !linkable}
    <p
      class="mb-4 flex items-start gap-2 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-3 py-2 text-sm text-[var(--ha-secondary-text-color)]"
    >
      <Icon name="mdi:information-outline" class="mt-0.5 shrink-0" />
      <span>{t("links.editor_hidden")}</span>
    </p>
  {/if}

  {#if loading}
    <LoadingState />
  {:else if links.length === 0}
    <EmptyState
      message={t("links.empty")}
      description={t("links.empty.description")}
      icon="mdi:link"
    />
  {:else}
    <div class="mb-4">
      <Input
        type="search"
        bind:value={search}
        placeholder={t("links.search")}
        aria-label={t("links.search")}
      />
    </div>

    {#if filtered.length === 0}
      <EmptyState message={t("links.no_matches")} icon="mdi:link" />
    {:else}
      <Card class="p-4">
        <DataTable
          rows={filtered}
          {columns}
          rowKey={(l) => l.central_name + "|" + l.sender_address + "->" + l.receiver_address}
          cell={linkCell}
          columnFilters
          persistKey="links"
          initialSort={{ key: "sender", asc: true }}
          emptyMessage={t("links.no_matches")}
        />
      </Card>
    {/if}
  {/if}
</PageShell>

{#snippet linkCell(link: Link, col: DataColumn<Link>)}
  {#if col.key === "sender"}
    <span class="block font-semibold text-[var(--ha-primary-text-color)]">
      {partyName(link, "sender")}
    </span>
    <span class="block font-mono text-xs text-[var(--ha-secondary-text-color)]">
      {link.sender_address}
    </span>
  {:else if col.key === "receiver"}
    <span class="block text-[var(--ha-primary-text-color)]">{partyName(link, "receiver")}</span>
    <span class="block font-mono text-xs text-[var(--ha-secondary-text-color)]">
      {link.receiver_address}
    </span>
  {:else if col.key === "name"}
    {link.name || "—"}
  {:else if col.key === "description"}
    {link.description || "—"}
  {:else if col.key === "interface"}
    {#if link.interface_id}
      <Badge variant="muted">{link.interface_id}</Badge>
    {:else}
      —
    {/if}
  {:else if col.key === "central"}
    {#if link.central_name}
      <Badge variant="muted">{link.central_name}</Badge>
    {:else}
      —
    {/if}
  {:else if col.key === "actions" && linkable}
    <a
      href={`#/devices/${encodeURIComponent(deviceOf(link.sender_address))}?tab=links`}
      class="inline-flex items-center gap-1 text-xs font-medium text-brand-600 hover:underline dark:text-brand-400"
    >
      <Icon name="mdi:pencil" />
      {t("links.edit_on_device")}
    </a>
  {/if}
{/snippet}
