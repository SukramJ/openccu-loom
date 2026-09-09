<!--
  Fleet-wide schedule overview. Answers "which devices have a week
  schedule at all" — the question the device detail can only answer one
  device at a time, and the counterpart to the direct-links list.

  Read-only by design: a row opens the device's own schedule editor,
  which is where a program is changed. The daemon derives this list from
  channel types alone, so opening it costs no CCU traffic even on a
  large fleet.

  When the surface profile hides that editor the catalogue stays and the
  rows stop linking — see the `opens` relation in
  notes/concepts/ui-surface-profiles.md.
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { ScheduleDeviceSummary } from "$lib/api/types";
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
  import { t } from "$lib/i18n";

  let items = $state<ScheduleDeviceSummary[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let search = $state("");

  async function load() {
    loading = true;
    loadError = null;
    try {
      const resp = await api.listSchedules();
      items = resp.items ?? [];
    } catch (e) {
      loadError = e instanceof ApiError ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  const centrals = $derived(
    [...new Set(items.map((i) => i.central).filter(Boolean))].sort((a, b) =>
      a.localeCompare(b, undefined, { sensitivity: "base" }),
    ),
  );

  function matches(item: ScheduleDeviceSummary, q: string): boolean {
    if (!q) return true;
    const needle = q.toLowerCase();
    return [item.name, item.address, item.model, item.central, item.channel?.address]
      .filter(Boolean)
      .some((v) => (v as string).toLowerCase().includes(needle));
  }

  const filtered = $derived(items.filter((i) => matches(i, search)));

  /** The device's schedule tab — where the program is actually edited. */
  function href(item: ScheduleDeviceSummary): string {
    return `#/devices/${encodeURIComponent(item.address)}?tab=schedule`;
  }

  // Whether that tab exists in this profile at all. Hidden means the
  // rows lose their link, not that the list loses its rows.
  const linkable = $derived(surfacesStore.opensVisible("nav.schedules"));

  function openRow(item: ScheduleDeviceSummary) {
    location.hash = href(item);
  }

  function kindLabel(kind: string): string {
    return kind === "climate" ? t("schedules.kind.climate") : t("schedules.kind.week_profile");
  }

  const columns = $derived<DataColumn<ScheduleDeviceSummary>[]>([
    {
      key: "name",
      label: t("schedules.col.name"),
      sortable: true,
      title: true,
      get: (item) => item.name || item.address,
    },
    {
      key: "channel",
      label: t("schedules.col.channel"),
      sortable: true,
      get: (item) => item.channel?.address ?? item.address,
      cellClass: "font-mono text-xs",
    },
    {
      key: "model",
      label: t("schedules.col.model"),
      sortable: true,
      get: (item) => item.model ?? null,
    },
    {
      key: "kind",
      label: t("schedules.col.kind"),
      sortable: true,
      get: (item) => item.kind,
      filter: "select",
      filterOptions: [
        { value: "climate", label: t("schedules.kind.climate") },
        { value: "week_profile", label: t("schedules.kind.week_profile") },
      ],
    },
    ...(centrals.length > 1
      ? [
          {
            key: "central",
            label: t("schedules.col.central"),
            sortable: true,
            get: (item: ScheduleDeviceSummary) => item.central ?? null,
          } satisfies DataColumn<ScheduleDeviceSummary>,
        ]
      : []),
  ]);
</script>

{#snippet scheduleCell(item: ScheduleDeviceSummary, col: DataColumn<ScheduleDeviceSummary>)}
  {#if col.key === "name"}
    <span class="flex min-w-0 items-center gap-2">
      <Icon
        name="mdi:calendar-clock"
        class="shrink-0 text-[var(--ha-secondary-text-color)]"
      />
      {#if linkable}
        <a href={href(item)} class="min-w-0 truncate font-semibold text-[var(--ha-primary-text-color)] no-underline hover:underline">
          {item.name || item.address}
        </a>
      {:else}
        <span class="min-w-0 truncate font-semibold text-[var(--ha-primary-text-color)]">
          {item.name || item.address}
        </span>
      {/if}
    </span>
  {:else if col.key === "channel"}
    {item.channel?.address ?? item.address}
  {:else if col.key === "model"}
    {#if item.model}
      <Badge variant="muted">{item.model}</Badge>
    {/if}
  {:else if col.key === "kind"}
    <Badge variant={item.kind === "climate" ? "default" : "muted"}>
      {kindLabel(item.kind)}
    </Badge>
  {:else if col.key === "central"}
    {#if item.central}
      <Badge variant="muted">{item.central}</Badge>
    {/if}
  {/if}
{/snippet}

<PageShell>
  <PageHeader title={t("schedules.title")} subtitle={t("schedules.subtitle")} />

  {#if loadError}
    <ErrorState message={loadError} onRetry={load} class="mb-4" />
  {/if}

  {#if !linkable}
    <p
      class="mb-4 flex items-start gap-2 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-3 py-2 text-sm text-[var(--ha-secondary-text-color)]"
    >
      <Icon name="mdi:information-outline" class="mt-0.5 shrink-0" />
      <span>{t("schedules.editor_hidden")}</span>
    </p>
  {/if}

  {#if loading}
    <LoadingState />
  {:else if items.length === 0}
    <EmptyState
      message={t("schedules.empty")}
      description={t("schedules.empty.description")}
      icon="mdi:calendar-clock"
    />
  {:else}
    <div class="mb-4">
      <Input
        type="search"
        bind:value={search}
        placeholder={t("schedules.search")}
        aria-label={t("schedules.search")}
      />
    </div>

    <Card class="p-4">
      <DataTable
        rows={filtered}
        {columns}
        rowKey={(item) => item.central + "|" + item.address}
        cell={scheduleCell}
        columnFilters
        persistKey="schedules"
        initialSort={{ key: "name", asc: true }}
        onRowClick={linkable ? openRow : undefined}
        emptyMessage={t("schedules.no_matches")}
        emptyIcon="mdi:calendar-clock"
      />
    </Card>
  {/if}
</PageShell>
