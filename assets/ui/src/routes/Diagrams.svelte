<!--
  Named multi-series diagram definitions (SV03). Operators compose
  diagrams that overlay several measurement-history data points across
  devices/centrals, save them (private or shared), and see them charted
  on one page. The chart data comes from the existing history feature;
  when history is off each series shows its disabled state.
-->
<script lang="ts">
  import { onMount } from "svelte";
  import {
    listDiagrams,
    createDiagram,
    updateDiagram,
    deleteDiagram,
    ApiError,
  } from "$lib/api/client";
  import type { DiagramConfig, DiagramSeries, DiagramDocument } from "$lib/api/types";
  import Card from "$lib/components/ui/Card.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import DiagramSeriesPicker from "$lib/components/DiagramSeriesPicker.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import MultiSeriesChart from "$lib/components/MultiSeriesChart.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { infoStore } from "$lib/stores/info.svelte";
  import { t } from "$lib/i18n";

  // Diagrams chart measurement history; the whole surface is gated on the
  // opt-in history-recording feature (SV03). The nav item is hidden when
  // it is off; this guards direct navigation.
  const historyEnabled = $derived(
    infoStore.info?.capabilities?.includes("history.v1") ?? false,
  );

  let diagrams = $state<DiagramConfig[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);

  // Editor state (null = closed). id === null → creating.
  //
  // A row carries a client-side uid because DiagramSeries has no id and the
  // picker seeds its device / channel / data-point state once, at
  // construction. Keying the editor's {#each} by array index would let a
  // removal shift the surviving pickers onto another row's data — they would
  // keep offering the removed device's channels and parameters while emitting
  // the shifted row's address, so a pick saves a parameter that belongs to a
  // different device (and central) than the series' channel address.
  type DraftRow = { uid: string; series: DiagramSeries };
  type Draft = {
    id: string | null;
    name: string;
    visibility: "private" | "shared";
    rows: DraftRow[];
  };
  let draft = $state<Draft | null>(null);
  let saving = $state(false);
  let uidSeq = 0;

  function row(series: DiagramSeries): DraftRow {
    uidSeq += 1;
    return { uid: `row-${uidSeq}`, series };
  }
  function emptyRow(): DraftRow {
    return row({ central: "", interface_id: "", channel_address: "", parameter: "" });
  }

  function docOf(d: DiagramConfig): DiagramDocument {
    const cfg = (d.config ?? {}) as unknown as DiagramDocument;
    return { series: cfg.series ?? [], default_range_hours: cfg.default_range_hours };
  }

  async function load() {
    loading = true;
    loadError = null;
    try {
      diagrams = await listDiagrams();
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }
  onMount(() => {
    void infoStore.ensure();
    void load();
  });

  function newDraft() {
    draft = {
      id: null,
      name: "",
      visibility: "private",
      rows: [emptyRow()],
    };
  }
  function editDraft(d: DiagramConfig) {
    draft = {
      id: d.id,
      name: d.name,
      visibility: (d.visibility as "private" | "shared") ?? "private",
      rows: docOf(d).series.map((s) => row({ ...s })),
    };
  }
  function addSeries() {
    if (!draft) return;
    draft.rows = [...draft.rows, emptyRow()];
  }
  function removeSeries(uid: string) {
    if (!draft) return;
    draft.rows = draft.rows.filter((r) => r.uid !== uid);
  }
  function updateSeries(uid: string, ns: DiagramSeries) {
    if (!draft) return;
    draft.rows = draft.rows.map((r) => (r.uid === uid ? { ...r, series: ns } : r));
  }

  async function save() {
    if (!draft) return;
    if (!draft.name.trim()) {
      toastStore.error(t("diagrams.error.name_required"));
      return;
    }
    // Keep only fully-picked series (device → channel → value all chosen).
    // The row uid is editor-local and never leaves this component.
    const series = draft.rows
      .map((r) => r.series)
      .filter(
        (s) => (s.central ?? "").trim() && (s.channel_address ?? "").trim() && (s.parameter ?? "").trim(),
      );
    if (series.length === 0) {
      toastStore.error(t("diagrams.error.series_required"));
      return;
    }
    saving = true;
    const body = {
      name: draft.name.trim(),
      visibility: draft.visibility,
      config: { series } as unknown as Record<string, never>,
    };
    try {
      if (draft.id) {
        await updateDiagram(draft.id, body);
      } else {
        await createDiagram(body);
      }
      toastStore.success(t("diagrams.saved"));
      draft = null;
      await load();
    } catch (err) {
      toastStore.error(t("diagrams.error.save"), err instanceof ApiError ? err.message : String(err));
    } finally {
      saving = false;
    }
  }

  async function remove(d: DiagramConfig) {
    const ok = await confirmStore.ask({
      title: t("diagrams.delete.confirm_title", { name: d.name }),
      confirmLabel: t("common.delete"),
      destructive: true,
    });
    if (!ok) return;
    try {
      await deleteDiagram(d.id);
      toastStore.success(t("diagrams.deleted"));
      await load();
    } catch (err) {
      toastStore.error(t("diagrams.error.delete"), err instanceof ApiError ? err.message : String(err));
    }
  }
</script>

<svelte:head>
  <title>{t("page.title.diagrams")}</title>
</svelte:head>

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  <PageHeader title={t("diagrams.title")} subtitle={t("diagrams.subtitle")}>
    {#snippet actions()}
      {#if historyEnabled}
        <Button size="sm" onclick={newDraft}>{t("diagrams.new")}</Button>
      {/if}
    {/snippet}
  </PageHeader>

  {#if !historyEnabled}
    <EmptyState
      message={t("diagrams.history_required")}
      description={t("diagrams.history_required.description")}
      icon="mdi:chart-line-variant"
    />
  {:else}
  {#if loadError}
    <ErrorState message={loadError} onRetry={load} class="mb-4" />
  {/if}

  {#if draft}
    <Card class="mb-6 flex flex-col gap-3 p-4">
      <h2 class="text-base font-semibold text-[var(--ha-primary-text-color)]">
        {draft.id ? t("diagrams.edit") : t("diagrams.new")}
      </h2>
      <div class="flex flex-wrap items-end gap-3">
        <label class="flex flex-col gap-1 text-xs">
          <span class="text-[var(--ha-secondary-text-color)]">{t("diagrams.field.name")}</span>
          <Input bind:value={draft.name} placeholder={t("diagrams.field.name")} />
        </label>
        <label class="flex flex-col gap-1 text-xs">
          <span class="text-[var(--ha-secondary-text-color)]">{t("diagrams.field.visibility")}</span>
          <Select
            value={draft.visibility}
            onValueChange={(v) => draft && (draft.visibility = v as "private" | "shared")}
            options={[
              { value: "private", label: t("diagrams.visibility.private") },
              { value: "shared", label: t("diagrams.visibility.shared") },
            ]}
          />
        </label>
      </div>

      <div class="flex flex-col gap-2">
        <span class="text-xs font-semibold text-[var(--ha-secondary-text-color)]">
          {t("diagrams.field.series")}
        </span>
        {#each draft.rows as r, i (r.uid)}
          <DiagramSeriesPicker
            series={r.series}
            index={i}
            onChange={(ns) => updateSeries(r.uid, ns)}
            onRemove={() => removeSeries(r.uid)}
          />
        {/each}
        <div>
          <Button variant="outline" size="sm" onclick={addSeries}>{t("diagrams.series.add")}</Button>
        </div>
      </div>

      <div class="flex gap-2">
        <Button size="sm" disabled={saving} onclick={save}>{t("common.save")}</Button>
        <Button variant="ghost" size="sm" disabled={saving} onclick={() => (draft = null)}>
          {t("common.cancel")}
        </Button>
      </div>
    </Card>
  {/if}

  {#if loading}
    <LoadingState />
  {:else if diagrams.length === 0 && !draft}
    <EmptyState
      message={t("diagrams.empty")}
      description={t("diagrams.empty.description")}
      icon="mdi:chart-line-variant"
    />
  {:else}
    <div class="flex flex-col gap-6">
      {#each diagrams as d (d.id)}
        <Card class="flex flex-col gap-3 p-4">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div class="flex items-center gap-2">
              <h3 class="text-base font-semibold text-[var(--ha-primary-text-color)]">{d.name}</h3>
              {#if d.visibility === "shared"}
                <Badge variant="muted">{t("diagrams.visibility.shared")}</Badge>
              {/if}
            </div>
            <div class="flex gap-1">
              <Button variant="outline" size="sm" onclick={() => editDraft(d)}>{t("common.edit")}</Button>
              <Button variant="destructive" size="sm" onclick={() => remove(d)}>{t("common.delete")}</Button>
            </div>
          </div>
          <MultiSeriesChart
            series={docOf(d).series}
            rangeHours={docOf(d).default_range_hours ?? 24}
          />
        </Card>
      {/each}
    </div>
  {/if}
  {/if}
</section>
