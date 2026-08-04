<script lang="ts">
  import { onMount } from "svelte";
  import { api, friendlyError } from "$lib/api/client";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";
  import { loadLS, saveLS } from "$lib/utils";
  import { SECURITY_CLASSES } from "$lib/security/classes";
  import type {
    SecuritySourceOverride,
    SecuritySourceView,
    SecurityZoneState,
  } from "$lib/api/types";
  import type { DataColumn } from "$lib/components/ui/data-table";

  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import Switch from "$lib/components/ui/Switch.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";

  // Classified data-point inventory (docs/security-safety-concept.md §7.8).
  // The unfiltered list is deliberately available server-side — a source
  // the classifier got wrong is invisible in every aggregate, so listing
  // everything is the only way to find it. This view fetches the whole
  // inventory once and applies every filter client-side (matching
  // Inbox.svelte / SignalQualityList.svelte) so the central/zone option
  // lists never shrink as another filter narrows the visible rows.

  let sources = $state<SecuritySourceView[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);

  // Zone names are not carried on SecuritySourceView (only zone_id) — a
  // best-effort snapshot fetch resolves them for the filter and the table;
  // a failure just falls back to showing the raw id.
  let zones = $state<SecurityZoneState[]>([]);

  let classFilter = $state(loadLS("security-sources:class"));
  $effect(() => saveLS("security-sources:class", classFilter));
  let centralFilter = $state(loadLS("security-sources:central"));
  $effect(() => saveLS("security-sources:central", centralFilter));
  let zoneFilter = $state(loadLS("security-sources:zone"));
  $effect(() => saveLS("security-sources:zone", zoneFilter));
  let relevantOnly = $state(false);
  let activeOnly = $state(false);

  async function load() {
    loading = true;
    loadError = null;
    try {
      sources = await api.listSecuritySources();
    } catch (err) {
      loadError = friendlyError(err, t);
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
    api
      .getSecuritySnapshot()
      .then((snap) => (zones = snap.zones ?? []))
      .catch(() => {
        // Zone names are a convenience label; a failure just falls back
        // to the raw zone id everywhere one would have been shown.
        zones = [];
      });
  });

  function zoneName(id: string | undefined): string {
    if (!id) return "";
    return zones.find((z) => z.id === id)?.name ?? id;
  }

  function displayName(s: SecuritySourceView): string {
    return s.name || s.channel_address;
  }

  const centrals = $derived(
    [...new Set(sources.map((s) => s.central).filter(Boolean))].sort((a, b) =>
      a.localeCompare(b, undefined, { sensitivity: "base" }),
    ),
  );

  const filtered = $derived(
    sources.filter((s) => {
      if (classFilter && s.class !== classFilter) return false;
      if (centralFilter && s.central !== centralFilter) return false;
      if (zoneFilter && s.zone_id !== zoneFilter) return false;
      if (relevantOnly && !s.relevant) return false;
      if (activeOnly && !s.active) return false;
      return true;
    }),
  );

  // --- Override editing ---------------------------------------------
  // Draft defaults to "keep the classifier verdict, included": saving
  // with no changes is a harmless no-op (or reverts a stale override),
  // and picking a concrete class is the only way to actually pin one.
  type Draft = { class: string; included: boolean; note: string };
  function freshDraft(): Draft {
    return { class: "", included: true, note: "" };
  }
  let drafts = $state<Record<string, Draft>>({});
  function draftFor(ref: string): Draft {
    return drafts[ref] ?? freshDraft();
  }
  function setDraft(ref: string, patch: Partial<Draft>) {
    drafts = { ...drafts, [ref]: { ...draftFor(ref), ...patch } };
  }
  function clearDraft(ref: string) {
    const next = { ...drafts };
    delete next[ref];
    drafts = next;
  }

  async function saveOverride(s: SecuritySourceView) {
    const d = draftFor(s.ref);
    try {
      await api.putSecuritySourceOverride(s.ref, {
        class: (d.class || undefined) as SecuritySourceOverride["class"],
        included: d.included,
        note: d.note.trim() || undefined,
      });
      toastStore.success(t("security.sources.toast.saved"));
      clearDraft(s.ref);
      await load();
    } catch (err) {
      toastStore.error(t("security.sources.toast.save_failed"), friendlyError(err, t));
    }
  }

  // The explicit undo for a wrong override: empty class + included:true +
  // no note returns the source to the classifier's own verdict, regardless
  // of whatever is currently sitting in the draft fields.
  async function resetOverride(s: SecuritySourceView) {
    try {
      await api.putSecuritySourceOverride(s.ref, { included: true });
      toastStore.success(t("security.sources.toast.reset"));
      clearDraft(s.ref);
      await load();
    } catch (err) {
      toastStore.error(t("security.sources.toast.reset_failed"), friendlyError(err, t));
    }
  }

  const columns: DataColumn<SecuritySourceView>[] = $derived([
    {
      key: "source",
      label: t("security.sources.col.source"),
      sortable: true,
      title: true,
      get: (s) => displayName(s),
    },
    {
      key: "class",
      label: t("security.sources.col.class"),
      sortable: true,
      get: (s) => s.class,
    },
    {
      key: "central",
      label: t("security.sources.col.central"),
      sortable: true,
      get: (s) => s.central,
      headClass: "hide-narrow",
      cellClass: "hide-narrow",
    },
    {
      key: "zone",
      label: t("security.sources.col.zone"),
      sortable: true,
      get: (s) => zoneName(s.zone_id),
      headClass: "hide-narrow",
      cellClass: "hide-narrow",
    },
    {
      key: "relevant",
      label: t("security.sources.col.relevant"),
      sortable: true,
      align: "center",
      get: (s) => (s.relevant ? 1 : 0),
    },
    {
      key: "active",
      label: t("security.sources.col.active"),
      sortable: true,
      align: "center",
      get: (s) => (s.active ? 1 : 0),
    },
    {
      key: "override",
      label: t("security.sources.col.override"),
      cellClass: "reflow-actions",
    },
  ]);
</script>

<Card class="mb-4 p-4">
  <h2 class="mb-1 text-sm font-semibold">{t("security.sources.intro.title")}</h2>
  <p class="text-sm text-[var(--ha-secondary-text-color)]">
    {t("security.sources.intro.body")}
  </p>
  <p class="mt-2 text-sm text-[var(--ha-secondary-text-color)]">
    {t("security.sources.intro.when")}
  </p>
  <!--
    Once, not per row: this text used to sit inside the override cell,
    which repeated the same three lines for every source in the table.
  -->
  <p class="mt-2 text-sm text-[var(--ha-secondary-text-color)]">
    {t("security.sources.override.help")}
  </p>
  <a
    class="mt-2 inline-block text-sm underline"
    href="https://github.com/SukramJ/openccu-loom/blob/main/docs/alarm-user-guide.md"
    target="_blank"
    rel="noreferrer noopener"
  >
    {t("security.sources.intro.docs")}
  </a>
</Card>

<div class="mb-4 flex flex-wrap items-end gap-3">
  <div class="flex flex-col gap-1.5">
    <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">
      {t("security.sources.filter.class")}
    </span>
    <Select
      class="w-44"
      bind:value={classFilter}
      options={[
        { value: "", label: t("security.sources.filter.all") },
        ...SECURITY_CLASSES.map((c) => ({ value: c, label: t(`security.class.${c}`) })),
      ]}
    />
  </div>

  {#if centrals.length > 1}
    <div class="flex flex-col gap-1.5">
      <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">
        {t("security.sources.filter.central")}
      </span>
      <Select
        class="w-40"
        bind:value={centralFilter}
        options={[
          { value: "", label: t("security.sources.filter.all") },
          ...centrals.map((c) => ({ value: c, label: c })),
        ]}
      />
    </div>
  {/if}

  {#if zones.length > 0}
    <div class="flex flex-col gap-1.5">
      <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">
        {t("security.sources.filter.zone")}
      </span>
      <Select
        class="w-40"
        bind:value={zoneFilter}
        options={[
          { value: "", label: t("security.sources.filter.all") },
          ...zones.map((z) => ({ value: z.id, label: z.name })),
        ]}
      />
    </div>
  {/if}

  <label class="flex items-center gap-2 text-sm">
    <Switch checked={relevantOnly} onCheckedChange={(v) => (relevantOnly = v)} />
    {t("security.sources.filter.relevant")}
  </label>
  <label class="flex items-center gap-2 text-sm">
    <Switch checked={activeOnly} onCheckedChange={(v) => (activeOnly = v)} />
    {t("security.sources.filter.active")}
  </label>

  <Button
    type="button"
    variant="outline"
    size="sm"
    class="ml-auto"
    onclick={() => void load()}
    disabled={loading}
  >
    {t("common.reload")}
  </Button>
</div>

{#if loadError}
  <ErrorState message={loadError} onRetry={() => void load()} class="mb-4" />
{/if}

{#if loading && sources.length === 0}
  <LoadingState />
{:else}
  <Card class="p-4">
    <DataTable
      rows={filtered}
      {columns}
      rowKey={(s) => s.ref}
      search
      searchPlaceholder={t("security.sources.search")}
      persistKey="security-sources"
      initialSort={{ key: "source", asc: true }}
      emptyMessage={t("security.sources.empty")}
      emptyDescription={t("security.sources.empty.description")}
      emptyIcon="mdi:shield-alert"
    >
      {#snippet cell(s, col)}
        {#if col.key === "source"}
          <span class="font-medium">{displayName(s)}</span>
          <span class="block font-mono text-xs text-[var(--ha-secondary-text-color)]">
            {s.channel_address}·{s.parameter}
          </span>
        {:else if col.key === "class"}
          <Badge variant="default">{t(`security.class.${s.class}`)}</Badge>
          {#if s.overridden}
            <Badge variant="muted" class="ml-1">{t("security.sources.badge.overridden")}</Badge>
          {/if}
          {#if s.reason}
            <span class="block text-xs text-[var(--ha-secondary-text-color)]">
              {t(`security.fault_reason.${s.reason}`)}
            </span>
          {/if}
        {:else if col.key === "central"}
          <span class="text-xs">{s.central}</span>
        {:else if col.key === "zone"}
          <span class="text-xs">{zoneName(s.zone_id) || "—"}</span>
        {:else if col.key === "relevant"}
          <Badge variant={s.relevant ? "success" : "muted"}>
            {s.relevant
              ? t("security.sources.badge.relevant")
              : t("security.sources.badge.not_relevant")}
          </Badge>
        {:else if col.key === "active"}
          <Badge variant={s.active ? "danger" : "muted"}>
            {s.active
              ? t("security.sources.badge.active")
              : t("security.sources.badge.inactive")}
          </Badge>
        {:else if col.key === "override"}
          {@const d = draftFor(s.ref)}
          <div class="flex min-w-[260px] flex-col gap-2">
            <div class="flex flex-wrap items-center gap-2">
              <!--
                No fixed width: the placeholder is a sentence ("Keep
                classifier verdict" / "Klassifikator-Urteil beibehalten"),
                and a w-36 box cut it off in both locales. The select sizes
                itself and wraps instead of clipping.
              -->
              <Select
                class="min-w-[14rem] flex-1"
                value={d.class}
                onValueChange={(v) => setDraft(s.ref, { class: v })}
                placeholder={t("security.sources.override.keep")}
                options={SECURITY_CLASSES.map((c) => ({
                  value: c,
                  label: t(`security.class.${c}`),
                }))}
              />
              <label class="flex items-center gap-1.5 text-xs">
                <Switch
                  checked={d.included}
                  onCheckedChange={(v) => setDraft(s.ref, { included: v })}
                />
                {t("security.sources.override.included")}
              </label>
            </div>
            <div class="flex items-center gap-1.5">
              <Input
                class="h-8 text-xs"
                placeholder={t("security.sources.override.note_placeholder")}
                value={d.note}
                oninput={(e) => setDraft(s.ref, { note: e.currentTarget.value })}
              />
              <Button size="sm" onclick={() => void saveOverride(s)}>
                {t("security.sources.override.save")}
              </Button>
            </div>
            {#if s.overridden}
              <Button
                variant="outline"
                size="sm"
                title={t("security.sources.override.reset_title")}
                onclick={() => void resetOverride(s)}
              >
                {t("security.sources.override.reset")}
              </Button>
            {/if}
          </div>
        {/if}
      {/snippet}
    </DataTable>
  </Card>
{/if}
