<script lang="ts">
  import { untrack } from "svelte";
  import { api, friendlyError } from "$lib/api/client";
  import type { ReceiverProposal, ReceiverVerdict } from "$lib/api/types";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import DialogFrame from "$lib/components/ui/DialogFrame.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";

  // Best-receiver proposal for BidCos-RF devices. The daemon computes a
  // verdict per device from the pairwise matrix and writes nothing; applying
  // is one interface assignment per confirmed device. Only `switch` rows can
  // be applied, and they start ticked — the proposal already cleared the
  // margin for them. Every other verdict is informational.
  let {
    open,
    centralFilter = "",
    onClose,
    onApplied,
  }: {
    open: boolean;
    centralFilter?: string;
    onClose: () => void;
    onApplied?: () => void;
  } = $props();

  const DEFAULT_MARGIN_DB = 6;

  let margin = $state<number | string>(DEFAULT_MARGIN_DB);
  let proposals = $state<ReceiverProposal[]>([]);
  let loading = $state(false);
  let loadError = $state<string | null>(null);
  let ticked = $state<Set<string>>(new Set());
  let applying = $state(false);
  // Per-row result of the apply step: "ok" or the error text.
  let rowResult = $state<Record<string, { ok: boolean; message?: string }>>({});

  const shown = $derived(
    centralFilter ? proposals.filter((p) => p.central === centralFilter) : proposals,
  );
  const tickedRows = $derived(
    shown.filter((p) => p.verdict === "switch" && ticked.has(p.address) && !rowResult[p.address]?.ok),
  );

  function marginValue(): number {
    const n = Number(margin);
    if (!Number.isFinite(n)) return DEFAULT_MARGIN_DB;
    return Math.min(30, Math.max(0, Math.round(n)));
  }

  async function load() {
    loading = true;
    loadError = null;
    rowResult = {};
    try {
      const items = await api.receiverProposal(marginValue());
      proposals = items;
      ticked = new Set(items.filter((p) => p.verdict === "switch").map((p) => p.address));
    } catch (err) {
      proposals = [];
      ticked = new Set();
      loadError = friendlyError(err, t);
    } finally {
      loading = false;
    }
  }

  // Each open starts at the default margin with a fresh proposal. The load
  // is untracked: it reads the margin, and editing the margin must not
  // refetch on every keystroke — that is what the Recalculate button is for.
  $effect(() => {
    if (!open) return;
    untrack(() => {
      margin = DEFAULT_MARGIN_DB;
      void load();
    });
  });

  function toggle(address: string, on: boolean) {
    const next = new Set(ticked);
    if (on) next.add(address);
    else next.delete(address);
    ticked = next;
  }

  async function apply() {
    const rows = tickedRows;
    if (rows.length === 0) return;
    applying = true;
    let ok = 0;
    let failed = 0;
    for (const p of rows) {
      if (!p.best_interface) continue;
      try {
        await api.assignRFInterface(p.address, p.best_interface, false);
        rowResult = { ...rowResult, [p.address]: { ok: true } };
        ok++;
      } catch (err) {
        rowResult = {
          ...rowResult,
          [p.address]: { ok: false, message: friendlyError(err, t) },
        };
        failed++;
      }
    }
    applying = false;
    if (failed > 0) {
      toastStore.warn(t("signal.proposal.partial", { failed, total: ok + failed }));
    } else {
      toastStore.success(t("signal.proposal.done", { count: ok }));
    }
    if (ok > 0) onApplied?.();
  }

  function verdictVariant(v: ReceiverVerdict): "success" | "default" | "warning" | "danger" | "muted" {
    switch (v) {
      case "switch":
        return "default";
      case "keep":
        return "success";
      case "marginal":
        return "warning";
      case "unheard":
        return "danger";
      case "unmeasured":
      case "roaming":
        return "muted";
    }
  }

  function dbm(v: number | null | undefined): string {
    return v == null ? "—" : `${v} dBm`;
  }

  const columns: DataColumn<ReceiverProposal>[] = $derived([
    { key: "apply", label: t("signal.proposal.col.apply") },
    { key: "device", label: t("signal.proposal.col.device"), sortable: true, title: true, get: (p) => p.name || p.address },
    { key: "current", label: t("signal.proposal.col.current"), sortable: true, get: (p) => p.current_rx_dbm ?? null },
    { key: "best", label: t("signal.proposal.col.best"), sortable: true, get: (p) => p.best_rx_dbm ?? null },
    { key: "verdict", label: t("signal.proposal.col.verdict"), sortable: true, get: (p) => p.verdict },
  ]);
</script>

<DialogFrame {open} title={t("signal.proposal.title")} {onClose} closeDisabled={applying} size="xl">
  <p class="mb-3 text-sm text-[var(--ha-secondary-text-color)]">{t("signal.proposal.hint")}</p>

  <form
    class="mb-3 flex flex-wrap items-end gap-2"
    onsubmit={(e) => {
      e.preventDefault();
      void load();
    }}
  >
    <label class="flex flex-col gap-1 text-xs font-semibold text-[var(--ha-secondary-text-color)]">
      {t("signal.proposal.margin")}
      <Input type="number" min="0" max="30" step="1" class="w-28" bind:value={margin} disabled={loading || applying} />
    </label>
    <Button type="submit" variant="outline" size="sm" disabled={loading || applying}>
      {t("signal.proposal.recalculate")}
    </Button>
  </form>

  {#if loading}
    <LoadingState />
  {:else if loadError}
    <ErrorState message={loadError} onRetry={() => void load()} />
  {:else}
    <DataTable
      rows={shown}
      {columns}
      rowKey={(p) => (p.central ?? "") + "/" + p.address}
      initialSort={{ key: "verdict", asc: false }}
      emptyMessage={t("signal.proposal.empty")}
      emptyIcon="mdi:signal"
    >
      {#snippet cell(p, col)}
        {#if col.key === "apply"}
          {#if p.verdict === "switch"}
            <input
              type="checkbox"
              class="h-4 w-4 rounded border-[var(--ha-divider-color)]"
              checked={ticked.has(p.address)}
              disabled={applying || !!rowResult[p.address]?.ok}
              aria-label={t("signal.proposal.select", { name: p.name || p.address })}
              data-testid="proposal-tick"
              onchange={(e) => toggle(p.address, (e.currentTarget as HTMLInputElement).checked)}
            />
          {/if}
        {:else if col.key === "device"}
          <span class="font-medium">{p.name || p.address}</span>
          <span class="block font-mono text-xs text-[var(--ha-secondary-text-color)]">{p.address}</span>
          {#if rowResult[p.address]}
            {@const r = rowResult[p.address]}
            <span
              class={r.ok
                ? "block text-xs text-[var(--ha-success-color)]"
                : "block text-xs text-[var(--ha-error-color)]"}
              data-testid="proposal-result"
            >
              {r.ok ? t("signal.proposal.assigned") : r.message}
            </span>
          {/if}
        {:else if col.key === "current"}
          <span class="font-mono text-xs">{p.current_interface ?? "—"}</span>
          <span class="block text-xs text-[var(--ha-secondary-text-color)]">{dbm(p.current_rx_dbm)}</span>
        {:else if col.key === "best"}
          <span class="font-mono text-xs">{p.best_interface ?? "—"}</span>
          <span class="block text-xs text-[var(--ha-secondary-text-color)]">{dbm(p.best_rx_dbm)}</span>
        {:else if col.key === "verdict"}
          <Badge variant={verdictVariant(p.verdict)} title={t(`signal.proposal.verdict.${p.verdict}.hint`)}>
            {t(`signal.proposal.verdict.${p.verdict}`)}
          </Badge>
        {/if}
      {/snippet}
    </DataTable>
  {/if}

  {#snippet footer()}
    <Button type="button" variant="outline" size="md" class="w-full sm:w-auto" disabled={applying} onclick={onClose}>
      {t("common.close")}
    </Button>
    <Button
      type="button"
      size="md"
      class="w-full sm:w-auto"
      disabled={loading || applying || tickedRows.length === 0}
      onclick={() => void apply()}
    >
      {applying ? t("signal.proposal.applying") : t("signal.proposal.apply", { count: tickedRows.length })}
    </Button>
  {/snippet}
</DialogFrame>
