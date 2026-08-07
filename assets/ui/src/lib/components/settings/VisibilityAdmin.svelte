<script lang="ts">
  import { onMount } from "svelte";
  import { visibilityStore } from "$lib/stores/visibility.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import Select from "$lib/components/ui/Select.svelte";

  let selectedCentral = $state<string>("");
  let customPattern = $state("");
  let savingCentral = $state<string | null>(null);
  let includeMaster = $state(false);

  onMount(async () => {
    await Promise.all([
      visibilityStore.loadCentrals(),
      visibilityStore.loadCandidates(includeMaster),
    ]);
    if (!selectedCentral && visibilityStore.centrals.length > 0) {
      selectedCentral = visibilityStore.centrals[0].central_name;
    }
  });

  function toggleIncludeMaster() {
    includeMaster = !includeMaster;
    void visibilityStore.loadCandidates(includeMaster);
  }

  const centralOptions = $derived(
    visibilityStore.centrals.map((c) => c.central_name),
  );

  const effective = $derived.by<string[]>(() => {
    if (!selectedCentral) return [];
    return visibilityStore.effectivePatterns(selectedCentral);
  });

  const effectiveSet = $derived(new Set(effective));

  // Full pattern list without client-side text filter; DataTable's built-in
  // search replaces the previous searchText Input.
  const pickerItems = $derived.by<string[]>(() => {
    const set = new Set<string>(visibilityStore.candidates);
    for (const p of effective) set.add(p);
    return Array.from(set).sort();
  });

  const hasPending = $derived(
    selectedCentral ? visibilityStore.hasPending(selectedCentral) : false,
  );

  function toggle(pattern: string) {
    if (!selectedCentral) return;
    visibilityStore.togglePattern(selectedCentral, pattern);
  }

  function addCustom() {
    if (!selectedCentral) return;
    if (!customPattern.trim()) return;
    visibilityStore.addPattern(selectedCentral, customPattern.trim());
    customPattern = "";
  }

  async function save() {
    if (!selectedCentral) return;
    savingCentral = selectedCentral;
    try {
      const resp = await visibilityStore.save(selectedCentral);
      if (resp && resp.parse_errors && resp.parse_errors.length > 0) {
        toastStore.error(
          t("unignore.saved_with_errors", {
            count: String(resp.parse_errors.length),
          }),
        );
      } else if (resp) {
        toastStore.success(
          t("unignore.saved", { count: String(resp.applied_count) }),
        );
      }
    } catch (e) {
      toastStore.error(
        t("unignore.save_failed", {
          err: e instanceof Error ? e.message : String(e),
        }),
      );
    } finally {
      savingCentral = null;
    }
  }

  function discard() {
    if (!selectedCentral) return;
    visibilityStore.discardPending(selectedCentral);
  }

  async function reloadCentrals() {
    await Promise.all([
      visibilityStore.loadCentrals(),
      visibilityStore.loadCandidates(includeMaster),
    ]);
  }

  const columns: DataColumn<string>[] = $derived([
    {
      key: "pattern",
      label: t("unignore.col.pattern"),
      sortable: true,
      title: true,
      get: (p) => p,
    },
    {
      key: "match",
      label: t("unignore.col.match"),
      sortable: true,
      get: (p) =>
        visibilityStore.candidates.includes(p) ? "match" : "no match",
    },
    {
      key: "enabled",
      label: t("unignore.col.enabled"),
      sortable: true,
      align: "center",
      get: (p) => (effectiveSet.has(p) ? 1 : 0),
    },
  ]);
</script>

<div class="space-y-4">
  <div class="flex items-center justify-between gap-2">
    <h3 class="text-sm font-semibold tracking-wide text-[var(--ha-secondary-text-color)] uppercase">
      {t("settings.tab.visibility")}
    </h3>
    <Button
      type="button"
      variant="outline"
      size="sm"
      onclick={() => void reloadCentrals()}
      disabled={visibilityStore.centralsLoading || visibilityStore.candidatesLoading}
    >
      {t("common.reload")}
    </Button>
  </div>

  <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("unignore.subtitle")}</p>
  <p class="text-sm text-amber-600 dark:text-amber-400">
    ⚠ {t("unignore.warning")}
  </p>

  {#if visibilityStore.centralsLoading || visibilityStore.candidatesLoading}
    <LoadingState />
  {:else if visibilityStore.centralsError}
    <ErrorState message={visibilityStore.centralsError} onRetry={reloadCentrals} />
  {:else if centralOptions.length === 0}
    <EmptyState message={t("unignore.no_centrals")} icon="mdi:server" />
  {:else}
    <Card>
      <!-- Central selector + include-master toggle -->
      <div class="flex flex-wrap items-center gap-3 p-4">
        <span class="flex items-center gap-2 text-sm font-medium">
          {t("unignore.central_label")}
          <Select
            class="w-auto"
            bind:value={selectedCentral}
            options={centralOptions.map((name) => ({ value: name, label: name }))}
          />
        </span>

        <label class="flex items-center gap-2 text-sm sm:ml-auto">
          <input
            type="checkbox"
            checked={includeMaster}
            onchange={toggleIncludeMaster}
          />
          {t("unignore.include_master")}
        </label>
      </div>

      <!-- Pattern list as DataTable -->
      <div class="border-t border-slate-200 p-4 dark:border-slate-800">
        <DataTable
          rows={pickerItems}
          {columns}
          rowKey={(p) => p}
          search
          searchPlaceholder={t("unignore.search_placeholder")}
          persistKey="unignore"
          initialSort={{ key: "pattern", asc: true }}
          emptyMessage={t("unignore.no_candidates")}
          emptyIcon="mdi:hidden"
        >
          {#snippet cell(p, col)}
            {#if col.key === "pattern"}
              <code class="font-mono text-sm">{p}</code>
            {:else if col.key === "match"}
              {#if !visibilityStore.candidates.includes(p)}
                <span class="text-xs text-slate-500 dark:text-slate-400">
                  {t("unignore.no_match")}
                </span>
              {:else}
                <span class="text-xs text-slate-500 dark:text-slate-400">—</span>
              {/if}
            {:else if col.key === "enabled"}
              <input
                type="checkbox"
                checked={effectiveSet.has(p)}
                onchange={() => toggle(p)}
              />
            {/if}
          {/snippet}
        </DataTable>
      </div>

      <!-- Custom pattern add -->
      <div class="flex flex-wrap items-center gap-2 border-t border-slate-200 p-4 dark:border-slate-800">
        <Input
          bind:value={customPattern}
          placeholder={t("unignore.add_pattern_placeholder")}
          aria-label={t("unignore.add_pattern_placeholder")}
        />
        <Button onclick={addCustom} disabled={!customPattern.trim()}>
          {t("unignore.add_pattern")}
        </Button>
      </div>

      <!-- Save / discard -->
      <div class="flex flex-wrap items-center gap-2 border-t border-slate-200 p-4 dark:border-slate-800">
        {#if hasPending}
          <span class="text-sm text-amber-600 dark:text-amber-400">
            {t("unignore.unsaved_changes")}
          </span>
        {/if}
        <div class="ml-auto flex gap-2">
          <Button
            variant="outline"
            onclick={discard}
            disabled={!hasPending || savingCentral !== null}
          >
            {t("unignore.discard")}
          </Button>
          <Button
            onclick={save}
            disabled={!hasPending || savingCentral !== null}
          >
            {savingCentral === selectedCentral
              ? t("common.saving")
              : t("unignore.save")}
          </Button>
        </div>
      </div>

      {#if visibilityStore.lastSave && visibilityStore.lastSave.parse_errors && visibilityStore.lastSave.parse_errors.length > 0}
        <div class="border-t border-slate-200 p-4 text-sm dark:border-slate-800">
          <p class="font-semibold text-red-600 dark:text-red-400">{t("unignore.parse_errors_title")}</p>
          <ul class="mt-2 list-disc pl-5 text-red-600 dark:text-red-400">
            {#each visibilityStore.lastSave.parse_errors as err (err)}
              <li>{err}</li>
            {/each}
          </ul>
        </div>
      {/if}
    </Card>
  {/if}
</div>
