<script lang="ts">
  import { onMount } from "svelte";
  import { api, friendlyError } from "$lib/api/client";
  import { alarmPanelStore } from "$lib/stores/alarmPanel.svelte";
  import { prefs } from "$lib/stores/preferences.svelte";
  import { t } from "$lib/i18n";
  import type { AlarmJournalClass, AlarmJournalEntry } from "$lib/api/types";

  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";

  // Journal & health, table half (docs/alarm-concept.md §12.5). The base
  // table is an authoritative server-side query — zone/class/time-range
  // filters all apply there, so results are correct beyond whatever the
  // shared live buffer happens to hold. New entries then live-prepend from
  // alarmPanelStore.journal (see that store's doc comment: "the Journal
  // view re-fetches for the full, filterable history") so an arm/disarm/
  // trigger/silence shows up immediately without a poll. CSV export is a
  // client-side Blob of the currently loaded rows — there is no dedicated
  // journal-download REST endpoint (unlike the audit log's).

  const store = alarmPanelStore;

  const CLASS_ORDER: AlarmJournalClass[] = [
    "arm",
    "disarm",
    "trigger",
    "silence",
    "bypass",
    "fault",
    "test",
    "config",
  ];

  const CLASS_VARIANT: Record<
    AlarmJournalClass,
    "default" | "success" | "warning" | "danger" | "muted"
  > = {
    arm: "success",
    disarm: "muted",
    trigger: "danger",
    silence: "warning",
    bypass: "warning",
    fault: "danger",
    test: "default",
    config: "default",
  };

  let zoneFilter = $state("");
  let classFilter = $state<AlarmJournalClass | "">("");
  let fromLocal = $state("");
  let toLocal = $state("");

  let entries = $state<AlarmJournalEntry[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  // Highest journal id already reflected in `entries`. WS-delivered heads
  // above this are spliced in live instead of triggering a refetch.
  let watermark = $state(0);

  // datetime-local has no timezone; interpret it as local time and emit
  // an RFC3339 instant, mirroring AuditLog.svelte's toRFC3339.
  function toRFC3339(local: string): string | undefined {
    if (!local) return undefined;
    const d = new Date(local);
    return Number.isNaN(d.getTime()) ? undefined : d.toISOString();
  }

  async function load() {
    loading = true;
    error = null;
    try {
      entries = await api.listAlarmJournal({
        zone: zoneFilter || undefined,
        class: classFilter || undefined,
        from: toRFC3339(fromLocal),
        to: toRFC3339(toLocal),
        limit: 200,
      });
      watermark = entries.reduce((m, e) => Math.max(m, e.id), 0);
    } catch (err) {
      error = friendlyError(err, t);
    } finally {
      loading = false;
    }
  }
  onMount(load);

  // Reload whenever a filter changes; the initial run is already covered
  // by onMount(load), so the first pass through this effect is skipped.
  let firstRun = true;
  $effect(() => {
    void [zoneFilter, classFilter, fromLocal, toLocal];
    if (firstRun) {
      firstRun = false;
      return;
    }
    void load();
  });

  function matchesFilters(e: AlarmJournalEntry): boolean {
    if (zoneFilter && e.zone_id !== zoneFilter) return false;
    if (classFilter && e.class !== classFilter) return false;
    const whenMs = new Date(e.when).getTime();
    if (fromLocal) {
      const fromMs = new Date(fromLocal).getTime();
      if (!Number.isNaN(fromMs) && whenMs < fromMs) return false;
    }
    if (toLocal) {
      const toMs = new Date(toLocal).getTime();
      if (!Number.isNaN(toMs) && whenMs > toMs) return false;
    }
    return true;
  }

  // Live prepend: whenever the shared buffer's head advances past the
  // table's watermark, splice the new, filter-matching entries in at the
  // top. The buffer carries no per-filter awareness, so filtering happens
  // here.
  $effect(() => {
    const head = store.journal;
    if (head.length === 0) return;
    const fresh = head.filter((e) => e.id > watermark);
    if (fresh.length === 0) return;
    watermark = Math.max(watermark, ...fresh.map((e) => e.id));
    const matching = fresh.filter(matchesFilters);
    if (matching.length > 0) entries = [...matching, ...entries];
  });

  function zoneName(id: string): string {
    return store.zonesConfig.find((a) => a.id === id)?.name ?? id;
  }

  function fmtTime(iso: string): string {
    try {
      return new Date(iso).toLocaleString(
        prefs.locale === "de" ? "de-DE" : "en-US",
      );
    } catch {
      return iso;
    }
  }

  function csvField(v: string): string {
    return /[",\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v;
  }

  // Client-side export of the currently loaded/filtered rows — the
  // journal has no server-side CSV endpoint, so the table itself is the
  // source of truth for what gets downloaded.
  function exportCsv() {
    const header = [
      t("alarm.journal.col.when"),
      t("alarm.journal.col.zone"),
      t("alarm.journal.col.class"),
      t("alarm.journal.col.event"),
      t("alarm.journal.col.actor"),
      t("alarm.journal.col.source"),
    ];
    const rows = entries.map((e) => [
      fmtTime(e.when),
      zoneName(e.zone_id),
      t(`alarm.journal_class.${e.class}`),
      e.event,
      e.actor ?? "",
      e.source ?? "",
    ]);
    const csv = [header, ...rows]
      .map((r) => r.map(csvField).join(","))
      .join("\r\n");
    const blob = new Blob([csv], { type: "text/csv;charset=utf-8;" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `alarm-journal-${new Date().toISOString().slice(0, 10)}.csv`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  }
</script>

<div>
  <div class="mb-4 flex flex-wrap items-end gap-3">
    <div class="flex flex-col gap-1.5">
      <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">
        {t("alarm.journal.filter.zone")}
      </span>
      <Select
        class="w-40"
        bind:value={zoneFilter}
        options={[
          { value: "", label: t("alarm.journal.filter.all") },
          ...store.zonesConfig.map((a) => ({ value: a.id, label: a.name })),
        ]}
      />
    </div>

    <div class="flex flex-col gap-1.5">
      <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">
        {t("alarm.journal.filter.class")}
      </span>
      <Select
        class="w-40"
        value={classFilter}
        onValueChange={(v) => (classFilter = v as typeof classFilter)}
        options={[
          { value: "", label: t("alarm.journal.filter.all") },
          ...CLASS_ORDER.map((c) => ({
            value: c,
            label: t(`alarm.journal_class.${c}`),
          })),
        ]}
      />
    </div>

    <div class="flex flex-col gap-1.5">
      <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">
        {t("alarm.journal.filter.from")}
      </span>
      <input
        type="datetime-local"
        bind:value={fromLocal}
        class="h-10 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 text-sm text-[var(--ha-primary-text-color)] shadow-sm focus-visible:border-[var(--ha-primary-color)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ha-primary-color)]"
      />
    </div>

    <div class="flex flex-col gap-1.5">
      <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">
        {t("alarm.journal.filter.to")}
      </span>
      <input
        type="datetime-local"
        bind:value={toLocal}
        class="h-10 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 text-sm text-[var(--ha-primary-text-color)] shadow-sm focus-visible:border-[var(--ha-primary-color)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ha-primary-color)]"
      />
    </div>

    <Button
      variant="outline"
      size="sm"
      class="ml-auto"
      onclick={exportCsv}
      disabled={entries.length === 0}
    >
      <Icon name="mdi:download" size={16} />
      {t("alarm.journal.export_csv")}
    </Button>
  </div>

  {#if error}
    <ErrorState message={error} onRetry={load} class="mb-4" />
  {/if}

  {#if loading}
    <LoadingState />
  {:else if entries.length === 0}
    <EmptyState icon="mdi:history" message={t("alarm.journal.empty")} />
  {:else}
    <Card class="overflow-x-auto">
      <table class="w-full border-collapse text-sm">
        <thead class="sticky top-0 z-10 bg-[var(--ha-card-background-color)]">
          <tr class="border-b border-[var(--ha-divider-color)] text-left">
            <th class="p-2 font-medium">{t("alarm.journal.col.when")}</th>
            <th class="p-2 font-medium">{t("alarm.journal.col.zone")}</th>
            <th class="p-2 font-medium">{t("alarm.journal.col.class")}</th>
            <th class="p-2 font-medium">{t("alarm.journal.col.event")}</th>
            <th class="p-2 font-medium">{t("alarm.journal.col.actor")}</th>
            <th class="p-2 font-medium">{t("alarm.journal.col.source")}</th>
          </tr>
        </thead>
        <tbody>
          {#each entries as e (e.id)}
            <tr class="border-b border-[var(--ha-divider-color)] last:border-0">
              <td class="whitespace-nowrap p-2 text-[var(--ha-secondary-text-color)]">
                {fmtTime(e.when)}
              </td>
              <td class="p-2">{zoneName(e.zone_id)}</td>
              <td class="p-2">
                <Badge variant={CLASS_VARIANT[e.class]}>
                  {t(`alarm.journal_class.${e.class}`)}
                </Badge>
              </td>
              <td class="p-2 font-mono text-xs">{e.event}</td>
              <td class="p-2">{e.actor || "—"}</td>
              <td class="p-2 text-xs text-[var(--ha-secondary-text-color)]">
                {e.source ?? "—"}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </Card>
  {/if}
</div>
