<script lang="ts">
  import { onMount } from "svelte";
  import { api, friendlyError } from "$lib/api/client";
  import type { DeviceProjection, ProjectionEntry, ProjectionPlane } from "$lib/api/types";
  import { t } from "$lib/i18n";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";

  // How one device stands on the north-bound planes: per plane what is
  // published and under which identifiers, what another entry carries,
  // and what is left out with the named reason. The daemon answers from
  // the decisions its publishers act on; this view only presents them.
  type Props = { address: string };
  let { address }: Props = $props();

  let projection = $state<DeviceProjection | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

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

  const planeName = (p: ProjectionPlane) => known(`projection.plane.${p.plane}`, p.plane);
  const reasonText = (reason: string | undefined) => (reason ? known(`projection.reason.${reason}`, reason) : "");

  function subject(e: ProjectionEntry): string {
    const kind = e.kind ? known(`projection.kind.${e.kind}`, e.kind) : "";
    if (kind && e.key) return `${kind}: ${e.key}`;
    return kind || e.key || t("projection.subject.channel");
  }

  function channelText(e: ProjectionEntry): string {
    return e.channel < 0 ? t("projection.channel.device") : String(e.channel);
  }

  function detail(e: ProjectionEntry): string {
    if (e.verdict === "withheld") return reasonText(e.reason);
    if (e.verdict === "folded") return t("projection.folded_into", { target: e.folded_into ?? "" });
    return Object.entries(e.identity ?? {})
      .map(([k, v]) => `${known(`projection.identity.${k}`, k)}: ${v}`)
      .join(" · ");
  }

  const verdictVariant = { published: "success", folded: "default", withheld: "muted" } as const;

  const columns = $derived<DataColumn<ProjectionEntry>[]>([
    { key: "channel", label: t("projection.col.channel"), sortable: true, get: (e) => e.channel, numeric: true },
    { key: "subject", label: t("projection.col.subject"), sortable: true, get: subject, title: true, filter: "text" },
    {
      key: "verdict",
      label: t("projection.col.verdict"),
      sortable: true,
      get: (e) => e.verdict,
      filter: "select",
      filterOptions: (["published", "folded", "withheld"] as const).map((v) => ({
        value: v,
        label: t(`projection.verdict.${v}`),
      })),
    },
    { key: "detail", label: t("projection.col.detail"), get: detail, filter: "text" },
  ]);

  const rowKey = (e: ProjectionEntry) => `${e.channel}|${e.kind ?? ""}|${e.key ?? ""}|${e.verdict}`;

  function count(p: ProjectionPlane, verdict: string): number {
    return p.entries.filter((e) => e.verdict === verdict).length;
  }
</script>

{#if loading}
  <LoadingState />
{:else if error}
  <ErrorState message={error} onRetry={load} />
{:else if !projection || projection.planes.length === 0}
  <EmptyState icon="mdi:server-network" message={t("projection.empty")} description={t("projection.empty.description")} />
{:else}
  <div class="space-y-6" data-testid="device-projection">
    <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("projection.intro")}</p>
    {#each projection.planes as plane (plane.plane)}
      <Card class="p-4" data-testid={`projection-plane-${plane.plane}`}>
        <div class="mb-3 flex flex-wrap items-center gap-2">
          <h3 class="text-base font-semibold text-[var(--ha-primary-text-color)]">{planeName(plane)}</h3>
          <Badge variant={plane.state === "active" ? "success" : "muted"}>
            {t(`projection.state.${plane.state}`)}
          </Badge>
          {#if plane.state === "active"}
            <span class="text-xs text-[var(--ha-secondary-text-color)]">
              {t("projection.counts", {
                published: count(plane, "published"),
                folded: count(plane, "folded"),
                withheld: count(plane, "withheld"),
              })}
            </span>
          {/if}
        </div>
        {#if plane.state !== "active"}
          <p class="text-sm text-[var(--ha-secondary-text-color)]">{reasonText(plane.reason)}</p>
        {:else}
          <DataTable
            rows={plane.entries}
            {columns}
            {rowKey}
            columnFilters
            persistKey={`device-projection-${plane.plane}`}
            initialSort={{ key: "channel", asc: true }}
            emptyMessage={t("projection.plane_empty")}
            emptyIcon="mdi:server-network"
          >
            {#snippet cell(entry, col)}
              {#if col.key === "channel"}
                {channelText(entry)}
              {:else if col.key === "subject"}
                {subject(entry)}
              {:else if col.key === "verdict"}
                <Badge variant={verdictVariant[entry.verdict]}>{t(`projection.verdict.${entry.verdict}`)}</Badge>
              {:else}
                <span class="break-words">{detail(entry)}</span>
              {/if}
            {/snippet}
          </DataTable>
        {/if}
      </Card>
    {/each}
  </div>
{/if}
