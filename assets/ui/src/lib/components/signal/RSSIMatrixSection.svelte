<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError, friendlyError } from "$lib/api/client";
  import type { RSSIMatrixCentral } from "$lib/api/types";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import ReceiverProposalDialog from "./ReceiverProposalDialog.svelte";
  import { t } from "$lib/i18n";

  // BidCos-RF pairwise reception matrix, one table per central. Each row is
  // one (device, partner) pair with both directions; rows are grouped by
  // device. The matrix is BidCos-only and admin-only on the daemon side, so
  // the whole section stays hidden until at least one central reports
  // either matrix rows or a read error — a pure HmIP installation or a
  // non-admin viewer sees the page exactly as before.

  let { centralFilter = "" }: { centralFilter?: string } = $props();

  type Row = {
    central: string;
    device: string;
    deviceName: string;
    partner: string;
    partnerLabel: string;
    gateway: boolean;
    rx: number | null;
    tx: number | null;
  };

  let centrals = $state<RSSIMatrixCentral[]>([]);
  let loadError = $state<string | null>(null);
  let proposalOpen = $state(false);

  async function load() {
    loadError = null;
    try {
      centrals = await api.rssiMatrix();
    } catch (err) {
      centrals = [];
      // 403 (not an admin) and 404 (daemon without the endpoint) mean the
      // matrix is not available to this viewer: hide, do not alarm.
      if (err instanceof ApiError && (err.status === 403 || err.status === 404)) return;
      loadError = friendlyError(err, t);
    }
  }

  onMount(() => void load());

  const shown = $derived(
    centrals.filter(
      (c) => (!centralFilter || c.central === centralFilter) &&
        (!!c.error || c.devices.length > 0),
    ),
  );
  const visible = $derived(shown.length > 0 || loadError !== null);

  function rowsOf(c: RSSIMatrixCentral): Row[] {
    const ifaces = new Map((c.interfaces ?? []).map((i) => [i.address, i]));
    const names = new Map(c.devices.map((d) => [d.address, d.name || d.address]));
    const out: Row[] = [];
    for (const d of c.devices) {
      for (const p of d.partners) {
        const iface = ifaces.get(p.address);
        out.push({
          central: c.central,
          device: d.address,
          deviceName: d.name || d.address,
          partner: p.address,
          partnerLabel: iface?.description || names.get(p.address) || p.address,
          gateway: !!iface,
          rx: p.rx_dbm ?? null,
          tx: p.tx_dbm ?? null,
        });
      }
    }
    return out;
  }

  // Same dBm buckets as the per-device table above it.
  function rssiVariant(v: number | null): "success" | "warning" | "danger" | "muted" {
    if (v == null) return "muted";
    if (v >= -60) return "success";
    if (v >= -80) return "warning";
    return "danger";
  }

  const columns: DataColumn<Row>[] = $derived([
    { key: "partner", label: t("signal.matrix.col.partner"), sortable: true, title: true, get: (r) => r.partnerLabel },
    { key: "rx", label: t("signal.matrix.col.rx"), sortable: true, align: "right", get: (r) => r.rx },
    { key: "tx", label: t("signal.matrix.col.tx"), sortable: true, align: "right", get: (r) => r.tx },
  ]);
</script>

{#if visible}
  <section class="mt-6" data-testid="rssi-matrix">
    <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
      <h2 class="text-base font-semibold">{t("signal.matrix.title")}</h2>
      <div class="flex flex-wrap items-center gap-2">
        {#if shown.some((c) => c.devices.length > 0)}
          <Button type="button" variant="outline" size="sm" onclick={() => (proposalOpen = true)}>
            {t("signal.proposal.open")}
          </Button>
        {/if}
        <Button type="button" variant="outline" size="sm" onclick={() => void load()}>
          {t("common.reload")}
        </Button>
      </div>
    </div>
    <p class="mb-3 text-sm text-[var(--ha-secondary-text-color)]">{t("signal.matrix.hint")}</p>

    {#if loadError}
      <ErrorState message={loadError} onRetry={() => void load()} />
    {/if}

    <div class="space-y-4">
      {#each shown as c (c.central + "/" + c.interface_id)}
        <Card class="p-3">
          <h3 class="mb-2 text-sm font-semibold">
            {c.central}
            <span class="ml-1 font-mono text-xs text-[var(--ha-secondary-text-color)]">{c.interface_id}</span>
          </h3>
          {#if c.error}
            <ErrorState message={c.error} onRetry={() => void load()} />
          {:else}
            <DataTable
              rows={rowsOf(c)}
              {columns}
              rowKey={(r) => r.device + ">" + r.partner}
              persistKey={"signal-matrix-" + c.central}
              initialSort={{ key: "rx", asc: true }}
              groupBy={(r) => r.deviceName + " · " + r.device}
              emptyMessage={t("signal.matrix.empty")}
              emptyIcon="mdi:signal"
            >
              {#snippet cell(r, col)}
                {#if col.key === "partner"}
                  <span class="font-medium">{r.partnerLabel}</span>
                  {#if r.gateway}
                    <Badge variant="muted" class="ml-1">{t("signal.matrix.gateway")}</Badge>
                  {/if}
                  <span class="block font-mono text-xs text-[var(--ha-secondary-text-color)]">{r.partner}</span>
                {:else if col.key === "rx"}
                  {#if r.rx != null}
                    <Badge variant={rssiVariant(r.rx)}>{r.rx} dBm</Badge>
                  {:else}<span class="text-[var(--ha-secondary-text-color)]">—</span>{/if}
                {:else if col.key === "tx"}
                  {#if r.tx != null}
                    <Badge variant={rssiVariant(r.tx)}>{r.tx} dBm</Badge>
                  {:else}<span class="text-[var(--ha-secondary-text-color)]">—</span>{/if}
                {/if}
              {/snippet}
            </DataTable>
          {/if}
        </Card>
      {/each}
    </div>
  </section>

  <ReceiverProposalDialog
    open={proposalOpen}
    {centralFilter}
    onClose={() => (proposalOpen = false)}
    onApplied={() => void load()}
  />
{/if}
