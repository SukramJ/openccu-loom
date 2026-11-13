<script lang="ts">
  import { onMount } from "svelte";
  import { api, friendlyError } from "$lib/api/client";
  import type { ChannelSummary, DeviceProjection, ProjectionEntry } from "$lib/api/types";
  import { t } from "$lib/i18n";
  import {
    appears,
    buildMatrix,
    hasReason,
    primaryIdentity,
    type Matrix,
    type MatrixRow,
    type Verdict,
  } from "$lib/projection/matrix";
  import { toastStore } from "$lib/stores/toast.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import Switch from "$lib/components/ui/Switch.svelte";

  // How one device stands on the north-bound planes, as a matrix: a row per
  // subject of a channel, a column per plane. The daemon answers from the
  // decisions its publishers act on; this view only arranges them. It
  // starts with what appears somewhere and leaves the rest — most of it
  // configuration parameters no plane carries — one switch away.
  type Props = {
    address: string;
    // The device's channels, for their names in the group headers.
    channels?: ChannelSummary[];
    // Switches the device detail to another tab; a reason that a setting
    // on one of them governs links there.
    onOpenTab?: (tab: "configure" | "history") => void;
  };
  let { address, channels = [], onOpenTab }: Props = $props();

  let projection = $state<DeviceProjection | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  // Filters. The view opens on what is published or carried somewhere.
  let onlyAppearing = $state(true);
  let showConfig = $state(false);
  let reasonFilter = $state<{ plane: string; reason: string } | null>(null);

  async function load() {
    loading = true;
    error = null;
    try {
      projection = await api.deviceProjection(address);
    } catch (e) {
      error = friendlyError(e, t);
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
  });

  // A catalogue entry, or the daemon's own token when this build does not
  // know it yet: a later daemon may name a plane, a kind or a reason this
  // SPA has no sentence for, and showing the token beats showing nothing.
  function known(key: string, fallback: string): string {
    const text = t(key);
    return text === key ? fallback : text;
  }

  const planeName = (plane: string) => known(`projection.plane.${plane}`, plane);
  const planeShort = (plane: string) => known(`projection.plane_short.${plane}`, planeName(plane));
  const reasonText = (reason: string | undefined) => (reason ? known(`projection.reason.${reason}`, reason) : "");

  const matrix = $derived<Matrix | null>(projection ? buildMatrix(projection) : null);
  const activePlanes = $derived(matrix ? matrix.planes.filter((p) => p.state === "active") : []);
  const inactivePlanes = $derived(matrix ? matrix.planes.filter((p) => p.state !== "active") : []);

  const rows = $derived.by<MatrixRow[]>(() => {
    if (!matrix) return [];
    const f = reasonFilter;
    return matrix.rows.filter((r) => {
      // A reason filter shows exactly the rows behind that reason,
      // whatever the two switches say: it is how the withheld are reached.
      if (f) return hasReason(r, f.plane, f.reason);
      if (!showConfig && r.kind === "master") return false;
      return !onlyAppearing || appears(r);
    });
  });
  const hiddenCount = $derived(matrix ? matrix.rows.length - rows.length : 0);

  function rowLabel(r: MatrixRow): string {
    switch (r.kind) {
      case "parameter":
      case "master":
        return r.key;
      case "group":
        return known(`projection.row.group.${r.key}`, r.key);
      case "device":
        return known(`projection.row.device.${r.key}`, r.key);
      case "schedule_switch":
      case "combined":
      case "companion":
        return t(`projection.row.${r.kind}`, { key: r.key });
      case "other":
        return r.key;
      default:
        return t(`projection.row.${r.kind}`);
    }
  }

  const channelByNumber = $derived(new Map(channels.map((c) => [c.number, c])));

  function groupLabel(channelKey: string): string {
    const no = Number(channelKey);
    if (no < 0) return t("projection.group.device");
    const ch = channelByNumber.get(no);
    const name = ch?.name?.trim() || ch?.type_label || "";
    return name ? t("projection.group.channel_named", { n: no, name }) : t("projection.group.channel", { n: no });
  }

  const columns = $derived<DataColumn<MatrixRow>[]>([
    { key: "subject", label: t("projection.col.subject"), get: rowLabel, title: true },
    ...activePlanes.map((p) => ({
      key: `plane:${p.plane}`,
      label: planeShort(p.plane),
      align: "center" as const,
      get: (r: MatrixRow) => r.cells[p.plane]?.verdict ?? "",
    })),
  ]);

  const verdictVariant: Record<Verdict, "success" | "default" | "muted"> = {
    published: "success",
    folded: "default",
    withheld: "muted",
  };
  const dotClass: Record<Verdict, string> = {
    published: "bg-[var(--ha-success-color)] border-[var(--ha-success-color)]",
    folded:
      "border-[var(--ha-primary-color)] bg-[linear-gradient(90deg,var(--ha-primary-color)_50%,transparent_50%)]",
    withheld: "border-[var(--ha-secondary-text-color)] bg-transparent",
  };

  function otherIdentity(plane: string, e: ProjectionEntry): [string, string][] {
    const primary = primaryIdentity(plane, e)?.key;
    return Object.entries(e.identity ?? {}).filter(([k]) => k !== primary);
  }
  const identityLabel = (k: string) => known(`projection.identity.${k}`, k);

  async function copy(value: string) {
    try {
      // The Clipboard API exists only in a secure context (HTTPS or
      // localhost); over plain http it is undefined.
      if (!navigator.clipboard) throw new Error("clipboard unavailable");
      await navigator.clipboard.writeText(value);
      toastStore.success(t("projection.copied"));
    } catch {
      toastStore.error(t("projection.copy_failed"));
    }
  }

  // Where the rule behind a reason is governed. A tab of this device, or a
  // route of the Config UI; nothing for a rule no setting changes.
  type Target = { label: string; tab?: "configure" | "history"; href?: string };
  function targetFor(plane: string, reason: string | undefined): Target | null {
    switch (reason) {
      case "channel_hidden":
        return onOpenTab ? { label: t("projection.goto.channel"), tab: "configure" } : null;
      case "override_off":
        return onOpenTab ? { label: t("projection.goto.recording"), tab: "history" } : null;
      case "not_on_allow_list":
      case "secondary_channel":
      case "secondary_member":
      case "measurements_not_included":
        return { label: t("projection.goto.matter"), href: "#/matter" };
      case "pattern_excludes":
        return { label: t("projection.goto.measurements"), href: "#/settings?tab=measurements" };
      case "hidden_parameter":
        return { label: t("projection.goto.visibility"), href: "#/settings?tab=visibility" };
      case "plane_disabled":
        if (plane === "matter") return { label: t("projection.goto.plane_settings"), href: "#/settings?tab=matter" };
        if (plane === "measurements")
          return { label: t("projection.goto.plane_settings"), href: "#/settings?tab=measurements" };
        if (plane === "ha_discovery" || plane === "mqtt_raw")
          return { label: t("projection.goto.plane_settings"), href: "#/settings?tab=mqtt" };
        return null;
      default:
        return null;
    }
  }

  function follow(target: Target) {
    if (target.tab) onOpenTab?.(target.tab);
    else if (target.href) location.hash = target.href;
  }
</script>

{#snippet goto(target: Target | null)}
  {#if target}
    <Button variant="ghost" size="sm" onclick={() => follow(target)}>
      <Icon name="mdi:link" class="mr-1 h-3.5 w-3.5" />{target.label}
    </Button>
  {/if}
{/snippet}

{#if loading}
  <LoadingState />
{:else if error}
  <ErrorState message={error} onRetry={load} />
{:else if !matrix || matrix.planes.length === 0}
  <EmptyState icon="mdi:server-network" message={t("projection.empty")} description={t("projection.empty.description")} />
{:else}
  <div class="space-y-4" data-testid="device-projection">
    <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("projection.intro")}</p>

    {#if inactivePlanes.length > 0}
      <ul class="space-y-1 text-sm" data-testid="projection-inactive">
        {#each inactivePlanes as plane (plane.plane)}
          <li class="flex flex-wrap items-center gap-2" data-testid={`projection-plane-${plane.plane}`}>
            <span class="font-medium text-[var(--ha-primary-text-color)]">{planeName(plane.plane)}</span>
            <Badge variant="muted">{t("projection.state.inactive")}</Badge>
            <span class="text-[var(--ha-secondary-text-color)]">{reasonText(plane.reason)}</span>
            {@render goto(targetFor(plane.plane, plane.reason))}
          </li>
        {/each}
      </ul>
    {/if}

    <div class="flex flex-wrap items-center gap-x-6 gap-y-2 text-sm">
      <label class="flex items-center gap-2">
        <Switch bind:checked={onlyAppearing} disabled={reasonFilter !== null} ariaLabel={t("projection.filter.appearing")} />
        <span>{t("projection.filter.appearing")}</span>
      </label>
      <label class="flex items-center gap-2">
        <Switch bind:checked={showConfig} disabled={reasonFilter !== null} ariaLabel={t("projection.filter.config")} />
        <span>{t("projection.filter.config")}</span>
      </label>
      {#if reasonFilter}
        <span class="flex items-center gap-1" data-testid="projection-reason-filter">
          <Badge>{planeName(reasonFilter.plane)}: {reasonText(reasonFilter.reason)}</Badge>
          <Button variant="ghost" size="sm" onclick={() => (reasonFilter = null)}>{t("projection.filter.clear")}</Button>
        </span>
      {:else if hiddenCount > 0}
        <span class="text-[var(--ha-secondary-text-color)]" data-testid="projection-hidden-count">
          {t("projection.filter.hidden", { n: hiddenCount })}
        </span>
      {/if}
    </div>

    <DataTable
      {rows}
      {columns}
      rowKey={(r) => r.id}
      search
      searchPlaceholder={t("common.search")}
      groupBy={(r) => String(r.channel)}
      {groupHeader}
      {expand}
      emptyMessage={t("projection.matrix_empty")}
      emptyDescription={t("projection.matrix_empty.description")}
      emptyIcon="mdi:server-network"
    >
      {#snippet cell(row, col)}
        {#if col.key === "subject"}
          <span class="break-words">{rowLabel(row)}</span>
          {#if row.kind === "master"}
            <Badge variant="muted" class="ml-1">{t("projection.row.master_badge")}</Badge>
          {/if}
        {:else}
          {@const plane = col.key.slice("plane:".length)}
          {@const c = row.cells[plane]}
          {#if c}
            <span
              class={`inline-block h-3 w-3 rounded-full border-2 ${dotClass[c.verdict]}`}
              title={`${planeName(plane)}: ${t(`projection.verdict.${c.verdict}`)}`}
              data-verdict={c.verdict}
            ></span>
            <span class="sr-only">{t(`projection.verdict.${c.verdict}`)}</span>
          {:else}
            <span class="text-[var(--ha-secondary-text-color)]" title={t("projection.cell.none")}>·</span>
          {/if}
        {/if}
      {/snippet}
    </DataTable>

    <p class="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-[var(--ha-secondary-text-color)]">
      {#each ["published", "folded", "withheld"] as const as v (v)}
        <span class="flex items-center gap-1">
          <span class={`inline-block h-3 w-3 rounded-full border-2 ${dotClass[v]}`}></span>{t(`projection.verdict.${v}`)}
        </span>
      {/each}
      <span>· {t("projection.cell.none")}</span>
    </p>

    {#if matrix.withheld.length > 0}
      <Card class="p-4" data-testid="projection-withheld">
        <h3 class="mb-1 text-base font-semibold text-[var(--ha-primary-text-color)]">{t("projection.withheld.title")}</h3>
        <p class="mb-3 text-sm text-[var(--ha-secondary-text-color)]">{t("projection.withheld.description")}</p>
        <ul class="divide-y divide-[var(--ha-divider-color)] text-sm">
          {#each matrix.withheld as w (`${w.plane}|${w.reason}`)}
            <!-- Narrow screens give the sentence a line of its own: between
                 the count and the two actions it would wrap word by word. -->
            <li class="flex flex-wrap items-center gap-x-3 gap-y-1 py-1.5">
              <span class="w-8 text-right font-semibold tabular-nums sm:w-12">{w.count}</span>
              <Badge variant="muted">{planeShort(w.plane)}</Badge>
              <span class="min-w-0 basis-full sm:flex-1 sm:basis-0">{reasonText(w.reason)}</span>
              {@render goto(targetFor(w.plane, w.reason))}
              <Button variant="ghost" size="sm" onclick={() => (reasonFilter = { plane: w.plane, reason: w.reason })}>
                {t("projection.withheld.show")}
              </Button>
            </li>
          {/each}
        </ul>
      </Card>
    {/if}
  </div>
{/if}

{#snippet groupHeader(key: string, groupRows: MatrixRow[])}
  <span class="font-semibold">{groupLabel(key)}</span>
  <span class="ml-2 text-xs font-normal text-[var(--ha-secondary-text-color)]">
    {t("projection.group.count", { n: groupRows.length })}
  </span>
{/snippet}

{#snippet expand(row: MatrixRow)}
  <div class="space-y-3 py-1" data-testid="projection-row-detail">
    {#each activePlanes as plane (plane.plane)}
      {@const c = row.cells[plane.plane]}
      {#if c}
        <div>
          <div class="mb-1 text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
            {planeName(plane.plane)}
          </div>
          <ul class="space-y-1.5">
            {#each c.entries as e, i (i)}
              {@const v = e.verdict as Verdict}
              {@const primary = primaryIdentity(plane.plane, e)}
              <li class="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
                <Badge variant={verdictVariant[v]}>{t(`projection.verdict.${v}`)}</Badge>
                {#if v === "published"}
                  {#if primary}
                    <span class="text-[var(--ha-secondary-text-color)]">{identityLabel(primary.key)}</span>
                    <code class="break-all font-mono text-[var(--ha-primary-text-color)]">{primary.value}</code>
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-label={t("projection.copy_label", { value: primary.value })}
                      onclick={() => copy(primary.value)}
                    >
                      <Icon name="mdi:content-copy" class="h-3.5 w-3.5" />
                    </Button>
                  {/if}
                  {#each otherIdentity(plane.plane, e) as [k, value] (k)}
                    <span class="break-all text-xs text-[var(--ha-secondary-text-color)]">{identityLabel(k)}: {value}</span>
                  {/each}
                {:else if v === "folded"}
                  <span>{t("projection.folded_into", { target: e.folded_into ?? "" })}</span>
                {:else}
                  <span>{reasonText(e.reason)}</span>
                  {@render goto(targetFor(plane.plane, e.reason))}
                {/if}
              </li>
            {/each}
          </ul>
        </div>
      {/if}
    {/each}
  </div>
{/snippet}
