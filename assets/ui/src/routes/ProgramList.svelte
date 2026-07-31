<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { ProgramEntry } from "$lib/api/types";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import Switch from "$lib/components/ui/Switch.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import { t } from "$lib/i18n";
  import { favoritesStore } from "$lib/stores/favorites.svelte";
  import { loadLS, saveLS } from "$lib/utils";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { prefs } from "$lib/stores/preferences.svelte";

  function formatDate(iso: string | null | undefined): string {
    if (!iso) return "";
    try {
      return new Date(iso).toLocaleString(prefs.locale === "de" ? "de-DE" : "en-US");
    } catch {
      return iso;
    }
  }

  let programs = $state<ProgramEntry[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let centralFilter = $state(loadLS("programs:central"));
  $effect(() => saveLS("programs:central", centralFilter));
  // showInternal reveals CCU-internal (Tmp_*, prgEnergyCounter_*) programs,
  // mirroring the CCU WebUI's "show system programs" footer toggle. Off by
  // default; the choice is persisted locally like the central filter.
  let showInternal = $state(loadLS("programs:show_internal") === "1");
  $effect(() => saveLS("programs:show_internal", showInternal ? "1" : "0"));
  let runningId = $state<string | null>(null);
  let togglingId = $state<string | null>(null);
  let deletingId = $state<string | null>(null);

  async function load() {
    loading = true;
    loadError = null;
    try {
      programs = await api.listPrograms(showInternal);
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  function toggleShowInternal(next: boolean) {
    showInternal = next;
    void load();
  }

  async function togglePin(id: string, name: string) {
    try {
      const pinned = await favoritesStore.toggle({ type: "program", id, label: name });
      toastStore.success(
        pinned ? t("favorites.added", { label: name }) : t("favorites.removed", { label: name }),
      );
    } catch (err) {
      toastStore.error(err instanceof Error ? err.message : String(err));
    }
  }

  async function execute(id: string, name: string, central?: string) {
    const ok = await confirmStore.ask({
      title: t("programs.confirm_run", { name }),
      confirmLabel: t("programs.run"),
      destructive: false,
      checkbox: { label: t("programs.check_conditions"), checked: false },
    });
    if (!ok) return;
    const checkConditions = confirmStore.checkboxChecked;
    runningId = id;
    try {
      const res = await api.executeProgram(id, central, checkConditions);
      if (checkConditions && res?.executed === false) {
        toastStore.info(t("programs.not_executed", { name }));
      } else {
        toastStore.success(t("programs.executed", { name }));
      }
    } catch (err) {
      toastStore.error(
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err),
      );
    } finally {
      runningId = null;
    }
  }

  async function toggle(id: string, name: string, current: boolean | undefined, central?: string) {
    togglingId = id;
    try {
      const next = !(current === true);
      await api.setProgramEnabled(id, next, central);
      toastStore.success(
        t("programs.toggle_done", {
          name,
          state: next ? t("programs.enabled") : t("programs.disabled"),
        }),
      );
      await load();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err),
      );
    } finally {
      togglingId = null;
    }
  }

  async function remove(id: string, name: string, central?: string) {
    const ok = await confirmStore.ask({
      title: t("programs.confirm_delete", { name }),
      confirmLabel: t("common.remove"),
      destructive: true,
    });
    if (!ok) return;
    deletingId = id;
    try {
      await api.deleteProgram(id, central);
      toastStore.success(t("programs.deleted", { name }));
      await load();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err),
      );
    } finally {
      deletingId = null;
    }
  }

  onMount(load);

  const centrals = $derived.by(() => {
    const set = new Set<string>();
    for (const p of programs) if (p.central) set.add(p.central);
    return Array.from(set).sort((a, b) =>
      a.localeCompare(b, undefined, { sensitivity: "base" }),
    );
  });

  const filtered = $derived(
    centralFilter ? programs.filter((p) => p.central === centralFilter) : programs,
  );

  const columns: DataColumn<ProgramEntry>[] = $derived([
    { key: "name", label: t("programs.col.name"), sortable: true, title: true, get: (p) => p.name },
    { key: "status", label: t("programs.col.status"), sortable: true, get: (p) => (p.active === true ? 1 : p.active === false ? 0 : -1) },
    {
      key: "condition",
      label: t("programs.col.condition"),
      get: (p) => p.condition_summary ?? "",
      headClass: "hide-narrow",
      cellClass: "hide-narrow",
    },
    {
      key: "activity",
      label: t("programs.col.activity"),
      get: (p) => p.activity_summary ?? "",
      headClass: "hide-narrow",
      cellClass: "hide-narrow",
    },
    {
      key: "last_executed",
      label: t("programs.col.last_executed"),
      sortable: true,
      get: (p) => p.last_executed ?? "",
    },
    { key: "actions", label: t("programs.col.actions"), align: "right", cellClass: "reflow-actions" },
  ]);
</script>

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  <PageHeader
    title={t("programs.title")}
    subtitle={loading ? t("common.loading") : t("programs.count", { count: programs.length })}
  >
    {#snippet actions()}
      <label
        for="programs-show-internal"
        class="inline-flex cursor-pointer items-center gap-2 text-sm text-[var(--ha-secondary-text-color)]"
      >
        <Switch
          id="programs-show-internal"
          checked={showInternal}
          disabled={loading}
          onCheckedChange={toggleShowInternal}
        />
        <span>{t("programs.show_internal")}</span>
      </label>
      {#if centrals.length > 1}
        <Select
          class="w-auto"
          bind:value={centralFilter}
          options={[
            { value: "", label: t("common.all_ccus") },
            ...centrals.map((c) => ({ value: c, label: c })),
          ]}
        />
      {/if}
      <Button type="button" variant="outline" size="sm" onclick={() => void load()} disabled={loading}>
        {t("common.reload")}
      </Button>
    {/snippet}
  </PageHeader>

  {#if loadError}
    <ErrorState message={loadError} onRetry={load} class="mb-4" />
  {/if}

  {#if loading}
    <LoadingState />
  {:else}
    <Card class="p-4">
      <DataTable
        rows={filtered}
        {columns}
        rowKey={(p) => (p.central ?? "") + "/" + p.id}
        search
        searchPlaceholder={t("common.search")}
        persistKey="programs"
        initialSort={{ key: "name", asc: true }}
        emptyMessage={t("programs.empty")}
        emptyIcon="mdi:play"
      >
        {#snippet cell(p, col)}
          {#if col.key === "name"}
            <div class:opacity-60={p.active === false}>
              <span class="font-medium">{p.name}</span>
              {#if centrals.length > 1 && p.central}
                <Badge variant="muted">{p.central}</Badge>
              {/if}
              {#if p.description}
                <span class="block text-xs text-slate-500 dark:text-slate-400">{p.description}</span>
              {/if}
              <span class="block font-mono text-[10px] text-slate-500 dark:text-slate-400">{p.id}</span>
            </div>
          {:else if col.key === "status"}
            {#if p.active === true}
              <Badge variant="default">{t("programs.active")}</Badge>
            {:else if p.active === false}
              <Badge variant="muted">{t("programs.inactive")}</Badge>
            {:else}
              <span class="text-[var(--ha-secondary-text-color)]">—</span>
            {/if}
          {:else if col.key === "condition"}
            {#if p.condition_summary}
              <span
                class="block max-w-[22rem] truncate font-mono text-xs text-slate-600 dark:text-slate-300"
                title={p.condition_summary}>{p.condition_summary}</span>
            {:else}
              <span class="text-[var(--ha-secondary-text-color)]">—</span>
            {/if}
          {:else if col.key === "activity"}
            {#if p.activity_summary}
              <span
                class="block max-w-[22rem] truncate font-mono text-xs text-slate-600 dark:text-slate-300"
                title={p.activity_summary}>{p.activity_summary}</span>
            {:else}
              <span class="text-[var(--ha-secondary-text-color)]">—</span>
            {/if}
          {:else if col.key === "last_executed"}
            {#if p.last_executed}
              <span class="text-xs text-slate-600 dark:text-slate-300" title={p.last_executed}>
                {formatDate(p.last_executed)}
              </span>
            {:else}
              <span class="text-[var(--ha-secondary-text-color)]">{t("programs.never_executed")}</span>
            {/if}
          {:else if col.key === "actions"}
            <span class="inline-flex items-center justify-end gap-2">
              <Button
                type="button"
                size="sm"
                variant="outline"
                onclick={() => void togglePin(p.id, p.name)}
                title={favoritesStore.isPinned("program", p.id)
                  ? t("favorites.unpin_program")
                  : t("favorites.pin_program")}
                aria-label={favoritesStore.isPinned("program", p.id)
                  ? t("favorites.unpin_program")
                  : t("favorites.pin_program")}
              >
                <Icon name={favoritesStore.isPinned("program", p.id) ? "mdi:star" : "mdi:star-outline"} />
              </Button>
              {#if p.active !== undefined}
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onclick={() => void toggle(p.id, p.name, p.active, p.central)}
                  disabled={togglingId === p.id}
                  title={t("programs.toggle.tooltip")}
                >
                  {togglingId === p.id ? "…" : p.active ? t("common.disable") : t("common.enable")}
                </Button>
              {/if}
              <Button
                type="button"
                size="sm"
                onclick={() => void execute(p.id, p.name, p.central)}
                disabled={runningId === p.id}
              >
                {runningId === p.id ? t("programs.running") : t("programs.run")}
              </Button>
              <Button
                type="button"
                size="sm"
                variant="destructive"
                onclick={() => void remove(p.id, p.name, p.central)}
                disabled={deletingId === p.id}
                title={t("programs.delete.tooltip")}
              >
                {deletingId === p.id ? "…" : t("common.remove")}
              </Button>
            </span>
          {/if}
        {/snippet}
      </DataTable>
    </Card>
  {/if}
</section>
