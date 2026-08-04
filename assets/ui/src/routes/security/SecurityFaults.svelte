<script lang="ts">
  import { onMount } from "svelte";
  import { api, friendlyError } from "$lib/api/client";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { prefs } from "$lib/stores/preferences.svelte";
  import { t } from "$lib/i18n";
  import type { SecurityFault } from "$lib/api/types";
  import type { DataColumn } from "$lib/components/ui/data-table";

  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";

  // Standing-fault ledger (docs/security-safety-concept.md §7.8).
  // Acknowledging never clears a fault: the underlying condition is still
  // there, only the "has an operator seen this" flag changes — hence the
  // permanent hint banner rather than a tooltip-only explanation, and the
  // destructive-style confirm (an operator could otherwise silence a
  // standing hazard by reflex-clicking through it).

  let faults = $state<SecurityFault[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let acking = $state<string | null>(null);

  async function load() {
    loading = true;
    loadError = null;
    try {
      faults = await api.listSecurityFaults();
    } catch (err) {
      loadError = friendlyError(err, t);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  function fmtDateTime(iso: string | undefined): string {
    if (!iso) return "";
    try {
      return new Date(iso).toLocaleString(prefs.locale === "de" ? "de-DE" : "en-US");
    } catch {
      return iso;
    }
  }

  // Elapsed time since the fault first appeared, computed at render/reload
  // time — this view has no live ticker, matching the rest of the SPA's
  // non-streamed diagnostic tables.
  function standingFor(sinceIso: string): string {
    const since = new Date(sinceIso).getTime();
    if (Number.isNaN(since)) return "";
    const ms = Math.max(0, Date.now() - since);
    const minutes = Math.floor(ms / 60000);
    const hours = Math.floor(minutes / 60);
    const days = Math.floor(hours / 24);
    if (days > 0) {
      return t("security.faults.duration.days_hours", { days, hours: hours % 24 });
    }
    if (hours > 0) {
      return t("security.faults.duration.hours_minutes", { hours, minutes: minutes % 60 });
    }
    return t("security.faults.duration.minutes", { minutes });
  }

  function sourceLabel(f: SecurityFault): string {
    return f.source?.name || f.source?.channel_address || "";
  }

  async function acknowledge(f: SecurityFault) {
    const ok = await confirmStore.ask({
      title: t("security.faults.acknowledge_confirm.title"),
      body: t("security.faults.acknowledge_confirm.body", {
        reason: t(`security.fault_reason.${f.reason}`),
        source: sourceLabel(f) || t(`security.class.${f.class}`),
      }),
      confirmLabel: t("common.acknowledge"),
      destructive: true,
    });
    if (!ok) return;
    acking = f.id;
    try {
      await api.acknowledgeSecurityFault(f.id);
      toastStore.success(t("security.faults.toast.acknowledged"));
      await load();
    } catch (err) {
      toastStore.error(t("security.faults.toast.acknowledge_failed"), friendlyError(err, t));
    } finally {
      acking = null;
    }
  }

  const columns: DataColumn<SecurityFault>[] = $derived([
    {
      key: "class",
      label: t("security.faults.col.class"),
      sortable: true,
      get: (f) => f.class,
    },
    {
      key: "reason",
      label: t("security.faults.col.reason"),
      sortable: true,
      get: (f) => f.reason,
    },
    {
      key: "source",
      label: t("security.faults.col.source"),
      sortable: true,
      title: true,
      get: (f) => sourceLabel(f),
    },
    {
      key: "since",
      label: t("security.faults.col.standing"),
      sortable: true,
      get: (f) => f.since,
    },
    {
      key: "status",
      label: t("security.faults.col.status"),
      sortable: true,
      get: (f) => (f.acknowledged_at ? 1 : 0),
    },
    {
      key: "actions",
      label: t("security.faults.col.actions"),
      align: "right",
      cellClass: "reflow-actions",
    },
  ]);
</script>

<p
  class="mb-4 rounded-md border p-3 text-sm"
  style="border-color: var(--ha-divider-color); background: var(--ha-secondary-background-color); color: var(--ha-secondary-text-color);"
>
  {t("security.faults.hint")}
</p>

{#if loadError}
  <ErrorState message={loadError} onRetry={() => void load()} class="mb-4" />
{/if}

{#if loading && faults.length === 0}
  <LoadingState />
{:else}
  <Card class="p-4">
    <DataTable
      rows={faults}
      {columns}
      rowKey={(f) => f.id}
      search
      searchPlaceholder={t("common.search")}
      persistKey="security-faults"
      initialSort={{ key: "since", asc: true }}
      emptyMessage={t("security.faults.empty")}
      emptyDescription={t("security.faults.empty.description")}
      emptyIcon="mdi:check-circle"
    >
      {#snippet cell(f, col)}
        {#if col.key === "class"}
          <Badge variant="default">{t(`security.class.${f.class}`)}</Badge>
        {:else if col.key === "reason"}
          {t(`security.fault_reason.${f.reason}`)}
        {:else if col.key === "source"}
          <span class="font-medium">{sourceLabel(f) || "—"}</span>
          {#if f.source?.channel_address}
            <span class="block font-mono text-xs text-[var(--ha-secondary-text-color)]">
              {f.source.channel_address}
            </span>
          {/if}
        {:else if col.key === "since"}
          <span title={fmtDateTime(f.since)}>{standingFor(f.since)}</span>
        {:else if col.key === "status"}
          {#if f.acknowledged_at}
            <Badge variant="muted">
              {f.acknowledged_by
                ? t("security.faults.status.acknowledged_by", {
                    time: fmtDateTime(f.acknowledged_at),
                    who: f.acknowledged_by,
                  })
                : t("security.faults.status.acknowledged", {
                    time: fmtDateTime(f.acknowledged_at),
                  })}
            </Badge>
          {:else}
            <Badge variant="warning">{t("security.faults.status.open")}</Badge>
          {/if}
        {:else if col.key === "actions"}
          {#if !f.acknowledged_at}
            <Button
              size="sm"
              variant="outline"
              disabled={acking === f.id}
              onclick={() => void acknowledge(f)}
            >
              {t("common.acknowledge")}
            </Button>
          {/if}
        {/if}
      {/snippet}
    </DataTable>
  </Card>
{/if}
